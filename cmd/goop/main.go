// Command goop is the CLI. See REQUIREMENTS.md for the full
// design and ARCHITECTURE.md for how the pieces fit together.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TanguyBaudoin/goop/internal/auth"
	"github.com/TanguyBaudoin/goop/internal/bucket"
	"github.com/TanguyBaudoin/goop/internal/credstore"
	"github.com/TanguyBaudoin/goop/internal/downloader"
	"github.com/TanguyBaudoin/goop/internal/installer"
	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/mavenrepo"
	"github.com/TanguyBaudoin/goop/internal/minisign"
	"github.com/TanguyBaudoin/goop/internal/paths"
	"github.com/TanguyBaudoin/goop/internal/profile"
	"github.com/TanguyBaudoin/goop/internal/profileset"
	"github.com/TanguyBaudoin/goop/internal/setup"
	"github.com/TanguyBaudoin/goop/internal/ui"
)

func main() {
	// One shared render target for both log lines and live download
	// bars (A1: installs/syncs run concurrently across apps) -- Progress
	// serializes all of it internally so bars and log lines never tear
	// or overwrite each other.
	prog := ui.NewProgress()
	installer.Logf = func(format string, args ...any) {
		prog.Println(styleLogLine(fmt.Sprintf(format, args...)))
	}
	wireProgressBars(prog)
	os.Exit(run(os.Args[1:]))
}

// wireProgressBars hooks downloader's progress callbacks to render one
// live bar per active download via prog. downloader reports raw
// (id, downloaded, total) on a fixed timer already throttled to avoid
// adding latency to the transfer itself; this only turns that into a
// smoothed transfer rate and a rendered line -- cheap, and off the
// download's hot path either way.
func wireProgressBars(prog *ui.Progress) {
	type state struct {
		mu        sync.Mutex
		label     string
		lastBytes int64
		lastTime  time.Time
		rate      float64
	}
	var active sync.Map // id -> *state

	downloader.OnDownloadStart = func(id, label string) {
		active.Store(id, &state{label: label, lastTime: time.Now()})
		prog.Update(id, ui.RenderBar(label, 0, -1, 0))
	}
	downloader.OnDownloadProgress = func(id string, downloaded, total int64) {
		v, ok := active.Load(id)
		if !ok {
			return
		}
		st := v.(*state)
		st.mu.Lock()
		now := time.Now()
		if dt := now.Sub(st.lastTime).Seconds(); dt > 0 {
			inst := float64(downloaded-st.lastBytes) / dt
			if st.rate == 0 {
				st.rate = inst
			} else {
				st.rate = st.rate*0.7 + inst*0.3 // smoothed so the number doesn't jitter every tick
			}
		}
		st.lastBytes, st.lastTime = downloaded, now
		label, rate := st.label, st.rate
		st.mu.Unlock()
		prog.Update(id, ui.RenderBar(label, downloaded, total, rate))
	}
	downloader.OnDownloadDone = func(id string) {
		active.Delete(id)
		prog.Done(id)
	}
}

// styleLogLine colors one line of installer.Logf output by what kind of
// event it reports. installer.Logf itself stays a plain
// format-string logger (business logic has no idea the CLI exists);
// this is purely presentation, matched against the actual message
// shapes the installer package produces -- see ARCHITECTURE.md's
// concurrency section for why that boundary is kept this way.
var (
	reDone    = regexp.MustCompile(`^(installed|imported|uninstalled) `)
	reAlready = regexp.MustCompile(`already (installed|imported|in sync)`)
	reWarn    = regexp.MustCompile(`: (shortcuts|env_set|env_add_path|revert env_set|revert env_add_path|zip uses|uninstaller hook failed|skipped)\b`)
	reStep    = regexp.MustCompile(`: (downloading|extracting|running|installing dependency|environment updated|removing|reverting|unlinking)`)
)

func styleLogLine(line string) string {
	switch {
	case reDone.MatchString(line):
		return ui.Green(ui.CheckMark) + " " + line
	case reAlready.MatchString(line):
		return ui.Gray(ui.CheckMark + " " + line)
	case reWarn.MatchString(line):
		return ui.Yellow(ui.Bang) + " " + line
	case reStep.MatchString(line):
		return ui.Cyan(ui.Arrow) + " " + line
	default:
		// Raw passthrough (a delegated script's own stdout/stderr):
		// dim rather than uncolored, so it visually recedes behind the
		// events above without looking like an error.
		return ui.Dim(line)
	}
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}

	// Keep topLevelCommands (completion.go) in sync with the cases below --
	// it's what `goop <tab>` offers.
	switch args[0] {
	case "install":
		return cmdInstall(args[1:])
	case "uninstall":
		return cmdUninstall(args[1:])
	case "update":
		return cmdUpdate(args[1:])
	case "list":
		return cmdList(args[1:])
	case "info":
		return cmdInfo(args[1:])
	case "search":
		return cmdSearch(args[1:])
	case "depends":
		return cmdDepends(args[1:])
	case "cleanup":
		return cmdCleanup(args[1:])
	case "reset":
		return cmdReset(args[1:])
	case "hold":
		return cmdHold(args[1:], true)
	case "unhold":
		return cmdHold(args[1:], false)
	case "cache":
		return cmdCache(args[1:])
	case "download":
		return cmdDownload(args[1:])
	case "bucket":
		return cmdBucket(args[1:])
	case "maven-repo":
		return cmdMavenRepo(args[1:])
	case "import":
		return cmdImportSetup(args[1:])
	case "adopt":
		return cmdImport(args[1:])
	case "export":
		return cmdExport(args[1:])
	case "audit":
		return cmdAudit(args[1:])
	case "digest":
		return cmdDigest(args[1:])
	case "check":
		return cmdCheck(args[1:])
	case "sync":
		return cmdSyncProfiles(args[1:])
	case "profile":
		return cmdProfile(args[1:])
	case "why":
		return cmdWhy(args[1:])
	case "auth":
		return cmdAuth(args[1:])
	case "verify":
		return cmdVerify(args[1:])
	case "config":
		return cmdConfig(args[1:])
	case "migrate":
		return cmdMigrate(args[1:])
	case "completion":
		return cmdCompletion(args[1:])
	case "self-update":
		return cmdSelfUpdate(args[1:])
	case "version", "--version":
		return cmdVersion(args[1:])
	case "-h", "--help", "help":
		printUsage()
		return 0
	default:
		ui.Fail("unknown command %q", args[0])
		printUsage()
		return 2
	}
}

func printUsage() {
	section := func(title string) { fmt.Fprintln(os.Stderr, ui.Bold(title)) }
	cmd := func(usage, desc string) {
		if desc == "" {
			fmt.Fprintf(os.Stderr, "  %s\n", usage)
			return
		}
		if len(usage) >= 46 {
			fmt.Fprintf(os.Stderr, "  %s\n      %s\n", usage, ui.Dim(desc))
			return
		}
		fmt.Fprintf(os.Stderr, "  %-46s %s\n", usage, ui.Dim(desc))
	}

	section("install & remove")
	cmd("goop install <spec>... [--no-update]", "spec: [bucket/]name[@constraint], e.g. jq, extras/mpv, jq@1.8.2")
	cmd("", "refreshes buckets first if older than 3h (--no-update skips), same as Scoop")
	cmd("", "depends resolve recursively, with cycle + conflict detection (A4)")
	cmd("", "or maven:[reponame/]groupId:artifactId:version:classifier:packaging -- needs `goop maven-repo add` first")
	cmd("goop uninstall <name>... [--force]", "refuses if still referenced by another profile unless --force (see `goop why`)")
	cmd("goop uninstall --all [--force]", "remove every installed app; asks you to type a word to confirm")
	cmd("", "there is no unattended form: it refuses when stdin is not a terminal")
	cmd("goop update [name]... [--dry-run] [-y] [-v]", "shows what would change and asks before doing it; all installed if none given (FR-05)")
	cmd("", "refreshes buckets first if stale (--no-update skips) -- without that it would report 'up to date' against old data")
	cmd("", "--dry-run plans only, -y skips the prompt, -v lists the packages already current")
	cmd("", "a non-interactive run (CI, a pipe) proceeds without asking")
	fmt.Fprintln(os.Stderr)

	section("inspect")
	cmd("goop list [--tree]", "installed apps; --tree groups them by profile, nesting dependencies")
	cmd("goop info <name>", "full provenance: URL(s), hash(es), bucket, install time (FR-42)")
	cmd("goop depends <spec>", "full dependency closure in install order, the app itself last")
	cmd("", "includes the extraction helpers (7zip/innounp/dark) an install would pull in")
	cmd("goop download <spec>...", "fetch + hash-verify into the cache without installing (prime an offline sync)")
	cmd("goop hold <name>...", "pin at the installed version; `goop update` skips it")
	cmd("goop unhold <name>...", "let it update again")
	cmd("goop cache show", "cached installers, biggest first, with the total")
	cmd("goop cache rm [pattern...]", "delete cached files matching any pattern; no pattern empties the cache")
	cmd("", "patterns match anywhere in the filename, so `goop cache rm firefox` works")
	cmd("goop reset <name>... | --all", "rebuild shims/shortcuts/env from the install record; files untouched")
	cmd("goop cleanup [name] [--dry-run]", "remove versions an update superseded; `current` is never touched")
	cmd("goop search <query> [--bin]", "query is a case-insensitive regex matched against manifest names")
	cmd("", "--bin also matches each manifest's `bin` field (e.g. `rg` -> ripgrep), decoding every manifest so slower")
	fmt.Fprintln(os.Stderr)

	section("buckets")
	cmd("goop bucket add <name> <url> [git|archive]", "archive needs no Git (FR-21); auto-detected from the URL")
	cmd("goop bucket remove <name>", "drops the config entry and deletes the local clone; already-installed apps are unaffected")
	cmd("goop bucket priority <name> <n>", "n=1 is searched first, so it wins when several buckets carry the same app")
	cmd("goop bucket list", "")
	cmd("goop bucket update [name]", "")
	cmd("goop migrate [--dry-run]", "copy every Scoop bucket + app into goop, independent of Scoop (safe to uninstall it after)")
	fmt.Fprintln(os.Stderr)

	section("maven repos")
	cmd("goop maven-repo add <name> <url>", "e.g. https://repo1.maven.org/maven2, or a private Artifactory Maven repo")
	cmd("goop maven-repo list", "")
	cmd("goop maven-repo remove <name>", "")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("Priority order like buckets: an unqualified maven: spec searches all configured repos in order."))
	fmt.Fprintln(os.Stderr)

	section("profiles")
	cmd("goop profile use <name>", "switch the active profile (like conda activate); installs register into it")
	cmd("goop profile list", "* marks the active one; MEMBERS is how many apps each profile references")
	cmd("goop profile show <name>", "what it contains, and which of its members are installed")
	cmd("goop profile add <name> <app>...", "declare app(s) as members without installing them")
	cmd("goop profile remove <name> <app>...", "un-declare, without uninstalling")
	cmd("goop profile reset", "merge all profiles into default, delete named profiles, reset active")
	cmd("goop why <name>", "which profile(s) reference name")

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("A profile is a named group of app names, not an isolated environment -- installs stay global/shared."))
	fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("Every `goop install` registers into whichever profile is active (default: \"default\")."))
	fmt.Fprintln(os.Stderr)

	section("profiles -- what a repository needs")
	cmd("goop check <file> [profile...]", "compare this machine to the profiles a file declares; exit 3 on deviation")
	cmd("", "reads receipts only -- no bucket, no network. A package outside the profile is never a deviation")
	cmd("goop sync <file> [profile...]", "install what the file requires and is missing; idempotent, no prior state needed")
	cmd("goop profile export --out <file> --profile <name>...", "maintainer: pin local profiles to a file, versions and manifest digests from receipts")
	cmd("goop profile clone <file> <name>", "take a profile from a file as a local, editable one")
	fmt.Fprintln(os.Stderr)

	section("whole machine")
	cmd("goop export [--out <file>]", "capture this machine: its buckets and every installed package")
	cmd("goop import <file>", "replay a capture: configure its buckets, then install its packages")
	cmd("goop audit <file>", "compare this machine to a capture; exit 3 on any difference, either way")
	cmd("goop adopt [name]...", "adopt apps installed by a real Scoop, without touching Scoop's own files")
	cmd("goop digest <name>... | --all [--recheck]", "record a manifest digest for installs that have none (older goop, or adopted)")
	cmd("", "only when the bucket still offers that exact version and every recorded field matches")
	cmd("", "--recheck also reports versions a bucket has republished since you installed them")
	fmt.Fprintln(os.Stderr)

	section("auth")
	cmd("goop auth add <host> bearer", "")
	cmd("goop auth add <host> basic <user>", "")
	cmd("goop auth remove <host>", "")
	cmd("goop auth list", "hosts + type only, secrets are never shown (FR-34)")
	cmd("", "the token/password is prompted for, never passed as an argument -- pipe it in for CI")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("Credentials resolve env var -> Credential Manager -> anonymous (FR-33)."))
	fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("Env var for a host: GOOP_AUTH_<HOST>, set to \"bearer:<token>\" or \"basic:<user>:<password>\"."))
	fmt.Fprintln(os.Stderr)

	section("provenance & signatures")
	cmd("goop verify <file> <sigfile> <pubkey>", "verify a minisign signature (FR-41/A5)")
	fmt.Fprintln(os.Stderr)

	section("config")
	cmd("goop config get-root", "the install root goop is currently using, and where that came from")
	cmd("goop config set-root <path>", "persist path as the install root (survives reboots, no env var needed)")
	cmd("goop config unset-root", "forget the persisted root; fall back to GOOP_HOME or the default")
	cmd("goop config get-proxy", "the proxy goop is currently using, and where that came from")
	cmd("goop config set-proxy <url>", "persist a proxy for both http and https (downloads + git bucket clones/pulls)")
	cmd("goop config unset-proxy", "forget the persisted proxy")
	cmd("goop config set-no-proxy <hosts>", "comma-separated hosts/.domains (or \"*\") that bypass the persisted proxy")
	cmd("goop config unset-no-proxy", "clear the no-proxy list")
	cmd("goop config get-cache-limit", "the download-cache ceiling, plus what the cache currently holds")
	cmd("goop config set-cache-limit <size>", "e.g. 5GB, 500MB, 0 (keep nothing), unlimited; evicts oldest-first when over")
	cmd("goop config unset-cache-limit", "back to unlimited (nothing is ever evicted)")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("Cache holds verified downloads, so clearing it only costs a re-download."))
	fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("Inspect/clear it with `goop cache`; the limit above evicts oldest-first once exceeded."))
	cmd("goop config get-bucket-ttl", "how long buckets stay fresh before install refreshes them")
	cmd("goop config set-bucket-ttl <dur>", "e.g. 24h to refresh less often, 0 to never auto-refresh (default 3h)")
	cmd("goop config unset-bucket-ttl", "back to the 3h default")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("Resolution order: $GOOP_HOME (if set) -> persisted root -> <user home>\\goop."))
	fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("Same pattern for proxy: HTTP_PROXY/HTTPS_PROXY env vars (if set) -> persisted proxy -> none."))
	fmt.Fprintln(os.Stderr)

	section("shell completion")
	cmd("goop completion <powershell|bash> --install", "register it in your shell profile automatically (idempotent, appends only)")
	cmd("goop completion powershell", "or print it; load with: goop completion powershell | Out-String | Invoke-Expression")
	cmd("goop completion bash", "or print it (e.g. Git Bash); load with: eval \"$(goop completion bash)\"")
	fmt.Fprintln(os.Stderr)

	section("about")
	cmd("goop version", "goop's own version, commit and build date -- quote it in bug reports")
	cmd("goop self-update [--force]", "replace goop itself with the current release; never automatic (D7)")
	cmd("", "refuses to go backwards unless --force -- a local build is not an older release")
	fmt.Fprintln(os.Stderr)

	fmt.Fprintf(os.Stderr, "%s 0 ok, 1 error, 2 usage, 3 deviation detected ('goop check', 'goop audit')\n", ui.Bold("exit codes:"))
}

func cmdVerify(args []string) int {
	if len(args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: goop verify <file> <sigfile> <pubkey-file-or-key>")
		return 2
	}
	filePath, sigPath, pubArg := args[0], args[1], args[2]

	fileData, err := os.ReadFile(filePath)
	if err != nil {
		ui.Fail("verify: %v", err)
		return 1
	}
	sigData, err := os.ReadFile(sigPath)
	if err != nil {
		ui.Fail("verify: %v", err)
		return 1
	}

	pubText := pubArg
	if data, err := os.ReadFile(pubArg); err == nil {
		pubText = string(data)
	}

	pub, err := minisign.ParsePublicKey(pubText)
	if err != nil {
		ui.Fail("verify: %v", err)
		return 1
	}
	sig, err := minisign.ParseSignature(string(sigData))
	if err != nil {
		ui.Fail("verify: %v", err)
		return 1
	}
	if err := minisign.VerifyFile(pub, fileData, sig); err != nil {
		ui.Fail("verify: %v", err)
		return 1
	}

	ui.Ok("%s is validly signed", filePath)
	return 0
}

// appKnown reports whether name is something goop could actually
// install or already has: installed apps count (their bucket may since
// have been removed), otherwise any configured bucket must carry it.
func appKnown(name string) bool {
	if _, ok := installer.Info(name); ok == nil {
		return true
	}
	_, _, err := bucket.Resolve(manifest.ParseSpec(name))
	return err == nil
}

func cmdProfile(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: goop profile <use|list|show|add|remove|reset|export|clone> ...")
		return 2
	}
	switch args[0] {
	case "use":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: goop profile use <name>")
			return 2
		}
		if err := profile.Use(args[1]); err != nil {
			ui.Fail("profile use: %v", err)
			return 1
		}
		ui.Ok("active profile set to %s", args[1])
		return 0
	case "list":
		names, err := profile.List()
		if err != nil {
			ui.Fail("profile list: %v", err)
			return 1
		}
		active := profile.Active()
		rows := make([][]string, len(names))
		for i, name := range names {
			d, _ := profile.Load(name)
			marker := ""
			if name == active {
				marker = ui.Green("*")
			}
			rows[i] = []string{marker, name, fmt.Sprintf("%d", len(d.Apps))}
		}
		fmt.Print(ui.Table([]string{"", "NAME", "MEMBERS"}, rows))
		return 0
	case "add":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: goop profile add <name> <app>...")
			return 2
		}
		exit := 0
		for _, app := range args[2:] {
			// A profile may list an app that isn't installed yet (that's
			// the point of declarative membership), but a name no bucket
			// carries is a typo -- silently accepting it means `goop sync`
			// fails much later, far from the mistake.
			if !appKnown(app) {
				ui.Fail("profile add: %q isn't installed and no configured bucket has it (typo? try `goop search %s`)", app, app)
				exit = 1
				continue
			}
			if err := profile.Add(args[1], app); err != nil {
				ui.Fail("profile add: %v", err)
				exit = 1
				continue
			}
			ui.Ok("added %s to profile %s", app, args[1])
		}
		return exit
	case "remove":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: goop profile remove <name> <app>...")
			return 2
		}
		exit := 0
		for _, app := range args[2:] {
			// Removal is idempotent underneath, which made a typo look
			// like a success ("removed gooo from profile default").
			// Check membership first so the message reflects reality.
			// Two different mistakes deserve two different messages: a
			// name that exists nowhere is a typo, while a real app that
			// simply isn't in this profile is a wrong-profile mistake.
			// Reporting both as "not a member" sent you looking at the
			// profile when the problem was the spelling.
			if !appKnown(app) {
				ui.Fail("profile remove: %q isn't installed and no configured bucket has it (typo? try `goop search %s`)", app, app)
				exit = 1
				continue
			}
			member := false
			if profiles, err := profile.ContainingProfiles(app); err == nil {
				for _, p := range profiles {
					if p == args[1] {
						member = true
						break
					}
				}
			}
			if !member {
				ui.Fail("profile remove: %q is not a member of profile %s", app, args[1])
				exit = 1
				continue
			}
			if err := profile.Remove(args[1], app); err != nil {
				ui.Fail("profile remove: %v", err)
				exit = 1
				continue
			}
			ui.Ok("removed %s from profile %s", app, args[1])
		}
		return exit
	case "export":
		out, wanted := "", []string{}
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--out":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, "profile export: --out needs a file")
					return 2
				}
				out = rest[i+1]
				i++
			case "--profile":
				for i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "--") {
					wanted = append(wanted, rest[i+1])
					i++
				}
			default:
				fmt.Fprintln(os.Stderr, "usage: goop profile export --out <file> --profile <name>...")
				return 2
			}
		}
		if out == "" || len(wanted) == 0 {
			fmt.Fprintln(os.Stderr, "usage: goop profile export --out <file> --profile <name>...")
			return 2
		}
		rep, err := installer.ExportProfiles(wanted, func(n string) ([]string, error) {
			d, err := profile.Load(n)
			return d.Apps, err
		})
		if err != nil {
			ui.Fail("profile export: %v", err)
			return 1
		}
		if len(rep.Missing) > 0 {
			// Guessing a version from the bucket would publish a pin
			// nobody has ever run.
			ui.Fail("profile export: not installed here, so there is nothing to pin: %s", strings.Join(rep.Missing, ", "))
			return 1
		}
		if err := profileset.Save(out, rep.File); err != nil {
			ui.Fail("profile export: %v", err)
			return 1
		}
		ui.Ok("exported %d profile(s), %d package(s) to %s", len(rep.File.Profiles), rep.Pinned, out)
		// A pin with no digest is a version number and nothing more. The
		// maintainer is about to commit this file, so the weakness has to
		// be visible here rather than discovered by whoever trusts it.
		if len(rep.Undigested) > 0 {
			ui.Warn("%d of %d pin(s) carry no manifest digest: %s",
				len(rep.Undigested), rep.Pinned, strings.Join(rep.Undigested, ", "))
			fmt.Println(ui.Dim("  those were installed before goop recorded digests (or adopted from Scoop)."))
			fmt.Println(ui.Dim("  they pin a version only, so a manifest republished under the same"))
			fmt.Println(ui.Dim("  version number will not be detected. `goop update <name>` re-installs"))
			fmt.Println(ui.Dim("  and records one; re-export afterwards."))
		}
		fmt.Println(ui.Dim("  commit it with the code; `goop check " + out + "` should be green here"))
		return 0
	case "clone":
		if len(args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: goop profile clone <file> <profile>")
			return 2
		}
		f, err := profileset.Load(args[1])
		if err != nil {
			ui.Fail("profile clone: %v", err)
			return 1
		}
		src, ok := f.Profiles[args[2]]
		if !ok {
			ui.Fail("profile clone: no profile %q in %s (it has: %v)", args[2], args[1], f.Names())
			return 1
		}
		d := profile.Definition{Name: args[2], Apps: src.SortedNames()}
		if err := profile.Save(d); err != nil {
			ui.Fail("profile clone: %v", err)
			return 1
		}
		ui.Ok("cloned %s (%d package(s)) as a local profile", args[2], len(d.Apps))
		fmt.Println(ui.Dim("  edit it with `goop profile add/remove`, then `goop profile export`"))
		return 0
	case "show":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: goop profile show <name>")
			return 2
		}
		// A profile that does not exist must say so. Printing it with
		// "no members" reads as an empty profile, which is the same
		// silence `goop check` refuses for a profile a file does not
		// declare.
		known, err := profile.List()
		if err != nil {
			ui.Fail("profile show: %v", err)
			return 1
		}
		if !slices.Contains(known, args[1]) {
			ui.Fail("profile show: no profile %q (there is: %v)", args[1], known)
			return 1
		}
		d, err := profile.Load(args[1])
		if err != nil {
			ui.Fail("profile show: %v", err)
			return 1
		}
		fmt.Println(ui.Bold(d.Name))
		if len(d.Apps) == 0 {
			fmt.Println(ui.Dim("  no members"))
			return 0
		}
		installed := make(map[string]bool)
		if recs, err := installer.List(); err == nil {
			for _, r := range recs {
				installed[r.Name] = true
			}
		}
		for _, a := range d.Apps {
			mark := ui.Dim("not installed")
			if installed[a] {
				mark = ui.Green(ui.CheckMark)
			}
			fmt.Printf("  %-28s %s\n", a, mark)
		}
		return 0
	case "reset":
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "usage: goop profile reset")
			return 2
		}
		if err := profile.Reset(); err != nil {
			ui.Fail("profile reset: %v", err)
			return 1
		}
		ui.Ok("profile reset to default (all members merged into default, named profiles removed)")
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: goop profile <use|list|show|add|remove|reset|export|clone> ...")
		return 2
	}
}

func cmdWhy(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: goop why <name>")
		return 2
	}
	containing, err := profile.ContainingProfiles(args[0])
	if err != nil {
		ui.Fail("why: %v", err)
		return 1
	}
	if len(containing) == 0 {
		fmt.Println(ui.Dim(fmt.Sprintf("%s is not referenced by any profile", args[0])))
		return 0
	}
	fmt.Printf("%s referenced by profile(s): %s\n", args[0], strings.Join(containing, ", "))
	return 0
}

func cmdImport(names []string) int {
	scoopRoot, ok := installer.DetectScoopRoot()
	if !ok {
		ui.Fail("import: no Scoop installation found (checked $SCOOP and <home>\\scoop)")
		return 1
	}

	if len(names) == 0 {
		found, err := installer.ImportableApps(scoopRoot)
		if err != nil {
			ui.Fail("import: %v", err)
			return 1
		}
		if len(found) == 0 {
			fmt.Println(ui.Dim("no importable apps found under " + scoopRoot))
			return 0
		}
		names = found
	}

	exit := 0
	for _, name := range names {
		if _, err := installer.Import(scoopRoot, name); err != nil {
			ui.Fail("import %s: %v", name, err)
			exit = 1
		}
	}
	return exit
}

// cmdMigrate implements `goop migrate`: unlike `goop import` (which
// junctions straight at Scoop's own directories, so it keeps depending
// on Scoop staying installed), this copies every detected bucket and app
// into goop's own tree so the result is fully independent of Scoop --
// safe to uninstall Scoop afterward. Always shows the full plan (buckets,
// then apps) before doing anything, and --dry-run stops there.
func cmdMigrate(args []string) int {
	dryRun := false
	for _, a := range args {
		if a != "--dry-run" && a != "-n" {
			fmt.Fprintln(os.Stderr, "usage: goop migrate [--dry-run]")
			return 2
		}
		dryRun = true
	}

	plan, err := installer.PlanMigration()
	if err != nil {
		ui.Fail("migrate: %v", err)
		return 1
	}

	fmt.Printf("%s %s\n\n", ui.Bold("Scoop installation:"), plan.ScoopRoot)
	printBucketPlan(plan.Buckets)
	fmt.Println()
	printAppPlan(plan.Apps)

	pendingBuckets, pendingApps := 0, 0
	for _, b := range plan.Buckets {
		if !b.AlreadyInGoop {
			pendingBuckets++
		}
	}
	for _, a := range plan.Apps {
		if !a.AlreadyInGoop {
			pendingApps++
		}
	}
	if pendingBuckets == 0 && pendingApps == 0 {
		fmt.Println()
		fmt.Println(ui.Dim("nothing to migrate -- everything above is already in goop"))
		return 0
	}
	if dryRun {
		fmt.Println()
		fmt.Println(ui.Dim("dry run: nothing changed"))
		return 0
	}

	fmt.Println()
	fmt.Println(ui.Bold("Migrating..."))
	bucketErrs := map[string]error{}
	for _, b := range plan.Buckets {
		if err := installer.MigrateBucket(plan.ScoopRoot, b); err != nil {
			bucketErrs[b.Name] = err
			ui.Fail("bucket %s: %v", b.Name, err)
		}
	}
	appErrs := installer.MigrateAllApps(plan.ScoopRoot, plan.Apps)

	return printMigrationReport(plan, bucketErrs, appErrs)
}

func printBucketPlan(buckets []installer.MigrationBucket) {
	fmt.Println(ui.Bold(fmt.Sprintf("Buckets detected (%d)", len(buckets))))
	if len(buckets) == 0 {
		fmt.Println(ui.Dim("  none"))
		return
	}
	rows := make([][]string, len(buckets))
	for i, b := range buckets {
		url := b.URL
		if url == "" {
			url = ui.Dim("(url unknown)")
		}
		status := ui.Cyan("will migrate")
		if b.AlreadyInGoop {
			status = ui.Gray("already in goop")
		}
		rows[i] = []string{b.Name, url, status}
	}
	fmt.Print(ui.Table([]string{"NAME", "URL", "STATUS"}, rows))
}

func printAppPlan(apps []installer.MigrationApp) {
	fmt.Println(ui.Bold(fmt.Sprintf("Apps detected (%d)", len(apps))))
	if len(apps) == 0 {
		fmt.Println(ui.Dim("  none"))
		return
	}
	rows := make([][]string, len(apps))
	var totalSize int64
	for i, a := range apps {
		bucketName := a.Bucket
		if bucketName == "" {
			bucketName = ui.Dim("(unknown)")
		}
		status := ui.Cyan("will migrate")
		if a.AlreadyInGoop {
			status = ui.Gray("already in goop")
		} else {
			totalSize += a.SizeBytes
		}
		rows[i] = []string{a.Name, a.Version, bucketName, ui.HumanBytes(a.SizeBytes), status}
	}
	fmt.Print(ui.Table([]string{"NAME", "VERSION", "BUCKET", "SIZE", "STATUS"}, rows))
	fmt.Println(ui.Dim(fmt.Sprintf("  %s to copy", ui.HumanBytes(totalSize))))
}

func printMigrationReport(plan installer.MigrationPlan, bucketErrs map[string]error, appErrs map[string]error) int {
	fmt.Println()
	fmt.Println(ui.Bold("Report"))

	bucketsOK, bucketsFailed := 0, 0
	for _, b := range plan.Buckets {
		if b.AlreadyInGoop {
			continue
		}
		if err, failed := bucketErrs[b.Name]; failed {
			ui.Fail("bucket %-20s %v", b.Name, err)
			bucketsFailed++
		} else {
			fmt.Printf("%s bucket %-20s migrated\n", ui.Green(ui.CheckMark), b.Name)
			bucketsOK++
		}
	}

	appsOK, appsFailed := 0, 0
	for _, a := range plan.Apps {
		if a.AlreadyInGoop {
			continue
		}
		// appErrs (from MigrateAllApps/runConcurrent) has an entry for
		// every name it attempted, success or not -- nil on success --
		// so failure is err != nil, not "key present in the map".
		if err := appErrs[a.Name]; err != nil {
			ui.Fail("app    %-20s %v", a.Name, err)
			appsFailed++
		} else {
			fmt.Printf("%s app    %-20s %s migrated\n", ui.Green(ui.CheckMark), a.Name, ui.Dim(a.Version))
			appsOK++
		}
	}

	fmt.Println()
	fmt.Printf("%s buckets migrated: %d ok, %d failed\n", ui.Bold("summary:"), bucketsOK, bucketsFailed)
	fmt.Printf("         apps migrated:    %d ok, %d failed\n", appsOK, appsFailed)
	if bucketsFailed == 0 && appsFailed == 0 {
		fmt.Println()
		fmt.Println(ui.Dim("everything above is now independent of Scoop; `scoop uninstall scoop` (or removing its folder) is safe."))
		return 0
	}
	return 1
}

func cmdInstall(args []string) int {
	var names []string
	noUpdate := false
	for _, a := range args {
		if a == "--no-update" {
			noUpdate = true
			continue
		}
		names = append(names, a)
	}
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "usage: goop install <spec>... [--no-update]")
		return 2
	}
	// Refresh stale buckets first, the way real Scoop's own install does
	// (libexec/scoop-install.ps1: `if (is_scoop_outdated) { scoop-update }`,
	// 3h threshold). Without it a week-old bucket keeps handing out a
	// stale hash, and any app served from a rolling URL -- Spotify's
	// installer, for one -- fails verification against a file that is
	// simply newer than the manifest, which reads as corruption rather
	// than as "your bucket is out of date".
	if !noUpdate && paths.BucketsStale() {
		fmt.Println(ui.Dim("buckets are out of date, refreshing (skip with --no-update)"))
		if err := bucket.UpdateAll(); err != nil {
			ui.Fail("bucket update: %v", err) // non-fatal: an offline install from cache should still work
		} else if err := paths.MarkBucketsUpdated(); err != nil {
			ui.Fail("bucket update: %v", err)
		}
	}

	errs := installer.InstallAll(names)
	exit := 0
	for _, name := range sortedKeys(errs) {
		if err := errs[name]; err != nil {
			ui.Fail("install %s: %v", name, err)
			exit = 1
		}
	}
	showSuggestions(names, errs)
	return exit
}

// showSuggestions prints, once at the end of the batch, any `suggest`
// entry from a just-installed app that isn't already satisfied by
// something installed -- mirrors real Scoop's own show_suggestions
// (lib/install.ps1), confirmed firing for real against real Scoop
// during this session's own benchmark runs (e.g. "'ripgrep' suggests
// installing 'extras/vcredist2022'."). Runs once per batch, after every
// install in it finished, rather than per-app as each one completes --
// same reasoning as real Scoop: an earlier app's suggestion might be
// fulfilled by a later one in the same batch, and checking too early
// would flag it as unfulfilled anyway.
func showSuggestions(names []string, errs map[string]error) {
	installed, err := installer.List()
	if err != nil {
		return
	}
	have := make(map[string]bool, len(installed))
	for _, rec := range installed {
		have[rec.Name] = true
	}

	for _, name := range names {
		if errs[name] != nil {
			continue
		}
		rec, err := installer.Info(name)
		if err != nil || len(rec.Suggest) == 0 {
			continue
		}
		for _, feature := range sortedStringKeys(rec.Suggest) {
			alternatives := rec.Suggest[feature]
			fulfilled := false
			for _, alt := range alternatives {
				if have[manifest.ParseSpec(alt).Name] {
					fulfilled = true
					break
				}
			}
			if !fulfilled {
				fmt.Printf("'%s' suggests installing '%s'.\n", name, strings.Join(alternatives, "' or '"))
			}
		}
	}
}

func sortedStringKeys(m map[string]manifest.StringList) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeys(m map[string]error) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func cmdUninstall(args []string) int {
	var names []string
	force, all := false, false
	for _, a := range args {
		switch a {
		case "--force":
			force = true
		case "--all":
			all = true
		default:
			names = append(names, a)
		}
	}
	if all {
		if !confirmUninstallAll() {
			return 2
		}
		errs := installer.UninstallAll(force)
		if errs == nil {
			fmt.Println(ui.Dim("no apps installed"))
			return 0
		}
		exit := 0
		for _, name := range sortedKeys(errs) {
			if err := errs[name]; err != nil {
				ui.Fail("uninstall %s: %v", name, err)
				exit = 1
			}
		}
		return exit
	}
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "usage: goop uninstall <name>... [--force] | --all [--force]")
		return 2
	}
	exit := 0
	for _, name := range names {
		if err := installer.Uninstall(name, force); err != nil {
			ui.Fail("uninstall %s: %v", name, err)
			exit = 1
		}
	}
	return exit
}

// uninstallAllToken is what has to be typed to confirm `uninstall --all`.
// Deliberately not "y": removing every installed package is irreversible
// and should cost a moment's attention rather than a reflex keystroke.
const uninstallAllToken = "uninstall-all"

// confirmUninstallAll gates the removal of every installed package.
//
// Two protections against two different mistakes. The typed word stops a
// person confirming out of muscle memory. The terminal check stops the
// command running unattended at all: a piped or scripted stdin cannot
// consent, so goop refuses rather than assuming it.
//
// There is deliberately no override flag. One existed briefly and was
// removed: a --yes that scripts may pass is a --yes an automated caller
// may pass, which hands back exactly the case the check exists to stop.
// Removing every installed package is therefore something only a person
// at a terminal can ask for. Callers that genuinely need it unattended
// can uninstall by name, which is bounded by what they name.
func confirmUninstallAll() bool {
	records, err := installer.List()
	if err != nil {
		ui.Fail("uninstall --all: %v", err)
		return false
	}
	if len(records) == 0 {
		return true // nothing to lose; the caller reports "no apps installed"
	}
	if !ui.IsTerminal(os.Stdin) {
		ui.Fail("uninstall --all needs an interactive terminal to confirm")
		fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("Nothing was removed. Run it from a terminal, or remove packages by name."))
		return false
	}

	names := make([]string, len(records))
	for i, r := range records {
		names[i] = r.Name
	}
	sort.Strings(names)

	fmt.Fprintf(os.Stderr, "%s %s\n", ui.Yellow(ui.Bang),
		ui.Bold(fmt.Sprintf("This removes all %d installed package(s) from %s:", len(names), paths.Root())))
	fmt.Fprintf(os.Stderr, "  %s\n", strings.Join(names, ", "))
	fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("Data persisted by those packages goes with them. The download cache is kept."))
	fmt.Fprintf(os.Stderr, "Type %s to confirm: ", ui.Bold(uninstallAllToken))

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != uninstallAllToken {
		ui.Fail("cancelled -- nothing was removed")
		return false
	}
	return true
}

// cmdHold pins or unpins apps against `goop update`.
func cmdHold(names []string, hold bool) int {
	verb := "hold"
	if !hold {
		verb = "unhold"
	}
	if len(names) == 0 {
		fmt.Fprintf(os.Stderr, "usage: goop %s <name>...\n", verb)
		return 2
	}
	exit := 0
	for _, n := range names {
		if err := installer.SetHold(n, hold); err != nil {
			ui.Fail("%s: %v", verb, err)
			exit = 1
			continue
		}
		if hold {
			rec, _ := installer.Info(n)
			ui.Ok("%s held at %s -- `goop update` will skip it", n, rec.Version)
		} else {
			ui.Ok("%s is no longer held", n)
		}
	}
	return exit
}

// cmdCache shows or clears the download cache.
func cmdCache(args []string) int {
	sub := "show"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "show", "list":
		total, entries, err := installer.CacheUsage()
		if err != nil {
			ui.Fail("cache: %v", err)
			return 1
		}
		if len(entries) == 0 {
			fmt.Println(ui.Dim("cache is empty"))
			return 0
		}
		// Biggest first: the point of looking is usually to find what's
		// worth reclaiming.
		sort.Slice(entries, func(i, j int) bool { return entries[i].Size > entries[j].Size })
		rows := make([][]string, len(entries))
		for i, e := range entries {
			rows[i] = []string{e.Name, formatSize(e.Size)}
		}
		fmt.Print(ui.Table([]string{"FILE", "SIZE"}, rows))
		fmt.Printf("%s %s across %d file(s)\n", ui.Bold("total:"), formatSize(total), len(entries))
		return 0
	case "rm", "clear":
		freed, removed, err := installer.ClearCache(args[1:])
		if err != nil {
			ui.Fail("cache rm: %v", err)
			return 1
		}
		if removed == 0 {
			fmt.Println(ui.Dim("nothing matched"))
			return 0
		}
		ui.Ok("removed %d cached file(s), freed %s", removed, formatSize(freed))
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: goop cache <show|rm [pattern...]>")
		return 2
	}
}

// cmdReset rebuilds an app's shims/shortcuts/env without reinstalling.
func cmdReset(names []string) int {
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "usage: goop reset <name>... | --all")
		return 2
	}
	if len(names) == 1 && names[0] == "--all" {
		errs := installer.ResetAll()
		for _, n := range sortedKeys(errs) {
			ui.Fail("reset %s: %v", n, errs[n])
		}
		if len(errs) > 0 {
			return 1
		}
		ui.Ok("reset every installed app")
		return 0
	}
	exit := 0
	for _, n := range names {
		if err := installer.Reset(n); err != nil {
			ui.Fail("reset %s: %v", n, err)
			exit = 1
		}
	}
	return exit
}

// cmdDownload fetches assets into the cache without installing.
func cmdDownload(names []string) int {
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "usage: goop download <spec>...")
		return 2
	}
	exit := 0
	for _, n := range names {
		res, err := installer.Download(n)
		if err != nil {
			ui.Fail("download %s: %v", n, err)
			exit = 1
			continue
		}
		ui.Ok("downloaded %s %s (%d file(s), verified)", res.Name, res.Version, res.Files)
	}
	return exit
}

// cmdCleanup removes versions superseded by an update. --dry-run is
// the default-safe way to look first, since the removal is permanent.
func cmdCleanup(args []string) int {
	dry := false
	only := ""
	for _, a := range args {
		if a == "--dry-run" {
			dry = true
			continue
		}
		only = a
	}
	stale, err := installer.StaleVersions(only)
	if err != nil {
		ui.Fail("cleanup: %v", err)
		return 1
	}
	if len(stale) == 0 {
		fmt.Println(ui.Dim("nothing to clean up"))
		return 0
	}
	if dry {
		var total int64
		rows := make([][]string, len(stale))
		for i, s := range stale {
			rows[i] = []string{s.App, s.Version, formatSize(s.Size)}
			total += s.Size
		}
		fmt.Print(ui.Table([]string{"APP", "OLD VERSION", "SIZE"}, rows))
		fmt.Printf("%s %s in %d version(s) would be freed\n", ui.Bold("total:"), formatSize(total), len(stale))
		return 0
	}
	freed, removed, err := installer.Cleanup(only)
	if err != nil {
		ui.Fail("cleanup: %v", err)
		return 1
	}
	ui.Ok("removed %d old version(s), freed %s", removed, formatSize(freed))
	return 0
}

func cmdDepends(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: goop depends <spec>")
		return 2
	}
	deps, err := installer.ResolveDependencies(args[0])
	if err != nil {
		ui.Fail("depends: %v", err)
		return 1
	}
	rows := make([][]string, len(deps))
	for i, d := range deps {
		rows[i] = []string{d.Name, d.Bucket}
	}
	fmt.Print(ui.Table([]string{"NAME", "BUCKET"}, rows))
	return 0
}

func cmdSearch(args []string) int {
	var query string
	includeBin := false
	for _, a := range args {
		if a == "--bin" {
			includeBin = true
			continue
		}
		query = a
	}
	if query == "" {
		fmt.Fprintln(os.Stderr, "usage: goop search <query> [--bin]")
		return 2
	}

	results, err := bucket.Search(query, includeBin)
	if err != nil {
		ui.Fail("search: %v", err)
		return 1
	}
	if len(results) == 0 {
		fmt.Println(ui.Dim("no matches found"))
		return 1
	}
	rows := make([][]string, len(results))
	for i, r := range results {
		rows[i] = []string{r.Name, r.Version, r.Bucket, r.Binaries}
	}
	fmt.Print(ui.Table([]string{"NAME", "VERSION", "BUCKET", "BINARIES"}, rows))
	return 0
}

func cmdList(args []string) int {
	tree := false
	for _, a := range args {
		if a != "--tree" {
			fmt.Fprintln(os.Stderr, "usage: goop list [--tree]")
			return 2
		}
		tree = true
	}

	records, err := installer.List()
	if err != nil {
		ui.Fail("list: %v", err)
		return 1
	}
	if len(records) == 0 {
		fmt.Println(ui.Dim("no apps installed"))
		return 0
	}
	if tree {
		return listTree(records)
	}
	rows := make([][]string, len(records))
	for i, r := range records {
		// An app can be a member of several profiles at once (that's the
		// whole point of `goop why`), so join rather than picking one.
		profiles, err := profile.ContainingProfiles(r.Name)
		if err != nil {
			profiles = nil
		}
		rows[i] = []string{r.Name, r.Version, r.Bucket, strings.Join(profiles, ", ")}
	}
	fmt.Print(ui.Table([]string{"NAME", "VERSION", "BUCKET", "PROFILE"}, rows))
	return 0
}

// listTree renders every profile as a root with its member apps under
// it, and each app's own recorded `depends` nested beneath it. Only
// apps explicitly asked for become profile members (installDependencies
// never registers what it pulls in), so the nesting is exactly the
// "I asked for this" vs "this came along" distinction. Apps belonging
// to no profile at all are grouped last so nothing is ever hidden.
func listTree(records []installer.Record) int {
	byName := make(map[string]installer.Record, len(records))
	for _, r := range records {
		byName[r.Name] = r
	}

	profiles, err := profile.List()
	if err != nil {
		ui.Fail("list: %v", err)
		return 1
	}
	active := profile.Active()

	shown := map[string]bool{}
	for _, p := range profiles {
		members := profileMembers(p, byName)
		marker := ""
		if p == active {
			marker = ui.Green(" *")
		}
		fmt.Printf("%s%s  %s\n", ui.Bold(p), marker, ui.Dim(fmt.Sprintf("(%d app(s))", len(members))))
		top := roots(members, byName)
		for i, name := range top {
			printAppTree(name, byName, "", i == len(top)-1, shown, map[string]bool{})
		}
		fmt.Println()
	}

	// Anything installed that no profile claims and nothing above
	// already showed as a dependency -- e.g. imported apps, or an app
	// whose profile entry was removed. Never silently dropped.
	var orphans []string
	for _, r := range records {
		if !shown[r.Name] {
			orphans = append(orphans, r.Name)
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		fmt.Printf("%s  %s\n", ui.Bold("(no profile)"), ui.Dim(fmt.Sprintf("(%d app(s))", len(orphans))))
		for i, name := range orphans {
			printAppTree(name, byName, "", i == len(orphans)-1, shown, map[string]bool{})
		}
	}
	return 0
}

// roots drops members that some other member already pulls in as a
// dependency, so each app is rendered once, at its most informative
// place: nested under what needs it rather than repeated at top level.
func roots(members []string, byName map[string]installer.Record) []string {
	member := make(map[string]bool, len(members))
	for _, m := range members {
		member[m] = true
	}

	pulledIn := map[string]bool{}
	var walk func(name string, stack map[string]bool)
	walk = func(name string, stack map[string]bool) {
		if stack[name] {
			return // defensive: a hand-edited record could loop
		}
		stack[name] = true
		defer delete(stack, name)
		rec, ok := byName[name]
		if !ok {
			return
		}
		for _, d := range recordDepends(rec) {
			dep := manifest.ParseSpec(d).Name
			if _, installed := byName[dep]; !installed {
				continue
			}
			pulledIn[dep] = true
			walk(dep, stack)
		}
	}
	for _, m := range members {
		walk(m, map[string]bool{})
	}

	var out []string
	for _, m := range members {
		if !pulledIn[m] {
			out = append(out, m)
		}
	}
	// A dependency cycle among members would leave nothing to print;
	// showing the flat member list beats showing an empty profile.
	if len(out) == 0 {
		return members
	}
	return out
}

// profileMembers returns p's installed member apps, sorted.
func profileMembers(p string, byName map[string]installer.Record) []string {
	d, err := profile.Load(p)
	if err != nil {
		return nil
	}
	var out []string
	for _, name := range d.Apps {
		if _, ok := byName[name]; ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// recordDepends returns rec's dependencies, falling back to its bucket
// manifest when the record itself has none. Records only started
// carrying `depends` recently, so every app installed before that would
// otherwise render as a flat, dependency-less tree until reinstalled --
// a poor trade for a display-only command. The record stays
// authoritative when present; the manifest is consulted only to fill a
// gap, and a missing bucket or manifest just yields nothing.
func recordDepends(rec installer.Record) []string {
	if len(rec.Depends) > 0 {
		return rec.Depends
	}
	spec := rec.Name
	if rec.Bucket != "" {
		spec = rec.Bucket + "/" + rec.Name
	}
	_, m, err := bucket.Resolve(manifest.ParseSpec(spec))
	if err != nil {
		return nil
	}
	return m.Depends
}

func printAppTree(name string, byName map[string]installer.Record, prefix string, last bool, shown, stack map[string]bool) {
	rec, ok := byName[name]
	if !ok {
		return // declared as a member or a dependency but not installed
	}
	branch, childPrefix := "├─ ", prefix+"│  "
	if last {
		branch, childPrefix = "└─ ", prefix+"   "
	}
	// Pad the name to a fixed *total* width including the tree prefix,
	// so version/bucket stay in one column however deep the nesting is.
	const nameCol = 32
	label := prefix + branch + name
	if pad := nameCol - len([]rune(label)); pad > 0 {
		label += strings.Repeat(" ", pad)
	}
	fmt.Printf("%s %-22s %s\n", label, rec.Version, ui.Dim(rec.Bucket))
	shown[name] = true

	// stack guards against a hand-edited record forming a depends
	// cycle; install-time detection already prevents real ones.
	if stack[name] {
		return
	}
	stack[name] = true
	defer delete(stack, name)

	var deps []string
	for _, d := range recordDepends(rec) {
		dep := manifest.ParseSpec(d).Name
		if _, ok := byName[dep]; ok {
			deps = append(deps, dep)
		}
	}
	sort.Strings(deps)
	for i, d := range deps {
		printAppTree(d, byName, childPrefix, i == len(deps)-1, shown, stack)
	}
}

func cmdInfo(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: goop info <name>")
		return 2
	}
	rec, err := installer.Info(args[0])
	if err != nil {
		ui.Fail("info: %v", err)
		return 1
	}

	field := func(label, value string) {
		fmt.Printf("%s  %s\n", ui.Bold(padLabel(label, 13)), value)
	}
	field("name", rec.Name)
	if rec.Description != "" {
		field("description", rec.Description)
	}
	field("version", rec.Version)
	field("bucket", rec.Bucket)
	field("architecture", rec.Architecture)
	if rec.Homepage != "" {
		field("website", strings.TrimRight(rec.Homepage, "/"))
	}
	if rec.LicenseIdentifier != "" {
		license := rec.LicenseIdentifier
		if rec.LicenseURL != "" {
			license = fmt.Sprintf("%s (%s)", license, rec.LicenseURL)
		}
		field("license", license)
	}
	field("installed", rec.InstalledAt.Format("2006-01-02 15:04:05 MST"))

	if len(rec.URLs) > 0 {
		fmt.Println()
		fmt.Println(ui.Bold("source:"))
		for i, u := range rec.URLs {
			fmt.Printf("  %s\n", u)
			if i < len(rec.Hashes) {
				fmt.Printf("  %s\n", ui.Dim(rec.Hashes[i]))
			}
		}
	}
	if len(rec.Bin) > 0 {
		fmt.Println()
		fmt.Println(ui.Bold("bin:"))
		for _, b := range rec.Bin {
			fmt.Printf("  %s %s %s.exe\n", b.Exe, ui.Dim("->"), b.Name)
		}
	}
	if len(rec.ExtraShims) > 0 {
		fmt.Println()
		fmt.Println(ui.Bold("extra shims:"))
		fmt.Println(ui.Dim("  created by the manifest's own script, not the bin field:"))
		for _, es := range rec.ExtraShims {
			if es.Path != "" {
				fmt.Printf("  %s %s %s\n", es.Name, ui.Dim("->"), es.Path)
			} else {
				fmt.Printf("  %s\n", es.Name)
			}
		}
	}
	return 0
}

func padLabel(s string, w int) string {
	s += ":"
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

func cmdAuth(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: goop auth <add|remove|list> ...")
		return 2
	}
	switch args[0] {
	case "add":
		return cmdAuthAdd(args[1:])
	case "remove":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: goop auth remove <host>")
			return 2
		}
		if err := credstore.Delete(args[1]); err != nil {
			ui.Fail("auth remove: %v", err)
			return 1
		}
		ui.Ok("removed credential for %s", args[1])
		return 0
	case "list":
		entries, err := credstore.List()
		if err != nil {
			ui.Fail("auth list: %v", err)
			return 1
		}
		if len(entries) == 0 {
			fmt.Println(ui.Dim("no hosts configured"))
			return 0
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Host < entries[j].Host })
		rows := make([][]string, len(entries))
		for i, e := range entries {
			rows[i] = []string{e.Host, e.Type}
		}
		fmt.Print(ui.Table([]string{"HOST", "TYPE"}, rows))
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: goop auth <add|remove|list> ...")
		return 2
	}
}

func cmdAuthAdd(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: goop auth add <host> bearer")
		fmt.Fprintln(os.Stderr, "       goop auth add <host> basic <user>")
		return 2
	}
	host, kind := args[0], args[1]
	var username, prompt string
	switch kind {
	case "bearer":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: goop auth add <host> bearer")
			fmt.Fprintln(os.Stderr, ui.Dim("  the token is asked for, not passed as an argument -- an argument would"))
			fmt.Fprintln(os.Stderr, ui.Dim("  land in your shell history and in the process list. Pipe it in for CI:"))
			fmt.Fprintln(os.Stderr, ui.Dim("  echo $TOKEN | goop auth add "+host+" bearer"))
			return 2
		}
		prompt = fmt.Sprintf("Token for %s: ", host)
	case "basic":
		if len(args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: goop auth add <host> basic <user>")
			fmt.Fprintln(os.Stderr, ui.Dim("  the password is asked for, not passed as an argument -- an argument"))
			fmt.Fprintln(os.Stderr, ui.Dim("  would land in your shell history and in the process list."))
			return 2
		}
		username = args[2]
		prompt = fmt.Sprintf("Password for %s@%s: ", username, host)
	default:
		ui.Fail("auth add: unknown type %q, want bearer or basic", kind)
		return 2
	}

	secret, err := ui.ReadSecret(prompt)
	if err != nil {
		ui.Fail("auth add: %v", err)
		return 1
	}
	if secret == "" {
		ui.Fail("auth add: empty secret, nothing stored")
		return 2
	}

	if err := credstore.Set(host, kind, username, secret); err != nil {
		ui.Fail("auth add: %v", err)
		return 1
	}
	ui.Ok("stored %s credential for %s", kind, host)
	fmt.Println(ui.Dim(fmt.Sprintf("(or set %s=%s:... in the environment instead, e.g. in CI)", auth.EnvVarName(host), kind)))
	return 0
}

func cmdBucket(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: goop bucket <add|list|remove|priority|update> ...")
		return 2
	}
	switch args[0] {
	case "add":
		if len(args) < 3 || len(args) > 4 {
			fmt.Fprintln(os.Stderr, "usage: goop bucket add <name> <url> [git|archive]")
			return 2
		}
		var kind bucket.Kind
		if len(args) == 4 {
			kind = bucket.Kind(args[3])
			if kind != bucket.KindGit && kind != bucket.KindArchive {
				ui.Fail("bucket add: kind must be \"git\" or \"archive\", got %q", args[3])
				return 2
			}
		}
		if err := bucket.Add(args[1], args[2], kind); err != nil {
			ui.Fail("bucket add: %v", err)
			return 1
		}
		ui.Ok("added bucket %s", args[1])
		return 0
	case "remove", "rm":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: goop bucket remove <name>")
			return 2
		}
		if err := bucket.Remove(args[1]); err != nil {
			ui.Fail("bucket remove: %v", err)
			return 1
		}
		ui.Ok("removed bucket %s", args[1])
		return 0
	case "priority":
		if len(args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: goop bucket priority <name> <position>   (1 = searched first)")
			return 2
		}
		pos, err := strconv.Atoi(args[2])
		if err != nil {
			ui.Fail("bucket priority: position must be a number, got %q", args[2])
			return 2
		}
		if err := bucket.SetPriority(args[1], pos); err != nil {
			ui.Fail("bucket priority: %v", err)
			return 1
		}
		ui.Ok("bucket %s moved to priority %d", args[1], pos)
		return cmdBucket([]string{"list"})
	case "list":
		entries, err := bucket.List()
		if err != nil {
			ui.Fail("bucket list: %v", err)
			return 1
		}
		if len(entries) == 0 {
			fmt.Println(ui.Dim("no buckets configured"))
			return 0
		}
		rows := make([][]string, len(entries))
		for i, e := range entries {
			kind := e.Kind
			if kind == "" {
				kind = bucket.KindGit
			}
			// Position is the search order, so show it: without a number
			// in front, "which bucket wins" is invisible in this table.
			rows[i] = []string{strconv.Itoa(i + 1), e.Name, string(kind), e.URL}
		}
		fmt.Print(ui.Table([]string{"#", "NAME", "KIND", "URL"}, rows))
		return 0
	case "update":
		if len(args) == 2 {
			if err := bucket.Update(args[1]); err != nil {
				ui.Fail("bucket update: %v", err)
				return 1
			}
			ui.Ok("updated bucket %s", args[1])
			return 0
		}
		if err := bucket.UpdateAll(); err != nil {
			ui.Fail("bucket update: %v", err)
			return 1
		}
		ui.Ok("updated all buckets")
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: goop bucket <add|list|update> ...")
		return 2
	}
}

func cmdMavenRepo(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: goop maven-repo <add|list|remove> ...")
		return 2
	}
	switch args[0] {
	case "add":
		if len(args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: goop maven-repo add <name> <url>")
			return 2
		}
		if err := mavenrepo.Add(args[1], args[2]); err != nil {
			ui.Fail("maven-repo add: %v", err)
			return 1
		}
		ui.Ok("added Maven repo %s", args[1])
		return 0
	case "list":
		entries, err := mavenrepo.List()
		if err != nil {
			ui.Fail("maven-repo list: %v", err)
			return 1
		}
		if len(entries) == 0 {
			fmt.Println(ui.Dim("no Maven repos configured"))
			return 0
		}
		rows := make([][]string, len(entries))
		for i, e := range entries {
			rows[i] = []string{e.Name, e.URL}
		}
		fmt.Print(ui.Table([]string{"NAME", "URL"}, rows))
		return 0
	case "remove":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: goop maven-repo remove <name>")
			return 2
		}
		if err := mavenrepo.Remove(args[1]); err != nil {
			ui.Fail("maven-repo remove: %v", err)
			return 1
		}
		ui.Ok("removed Maven repo %s", args[1])
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: goop maven-repo <add|list|remove> ...")
		return 2
	}
}

const configUsage = "usage: goop config <get-root|set-root|unset-root|get-proxy|set-proxy|unset-proxy|set-no-proxy|unset-no-proxy|get-cache-limit|set-cache-limit|unset-cache-limit|get-bucket-ttl|set-bucket-ttl|unset-bucket-ttl> ..."

// parseSize reads a human-written size ("0", "500MB", "5GB", "2g",
// "1024") into bytes. Bare numbers are bytes.
func parseSize(s string) (int64, error) {
	t := strings.TrimSpace(strings.ToUpper(s))
	t = strings.TrimSuffix(t, "B") // "5GB" -> "5G", "1024B" -> "1024"
	mult := int64(1)
	switch {
	case strings.HasSuffix(t, "K"):
		mult, t = 1<<10, strings.TrimSuffix(t, "K")
	case strings.HasSuffix(t, "M"):
		mult, t = 1<<20, strings.TrimSuffix(t, "M")
	case strings.HasSuffix(t, "G"):
		mult, t = 1<<30, strings.TrimSuffix(t, "G")
	case strings.HasSuffix(t, "T"):
		mult, t = 1<<40, strings.TrimSuffix(t, "T")
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q (try 0, 500MB, 5GB, or `unlimited`)", s)
	}
	return int64(n * float64(mult)), nil
}

// formatSize renders bytes the way the user would write them back.
func formatSize(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0fMB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.0fKB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

func cmdConfig(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, configUsage)
		return 2
	}
	switch args[0] {
	case "get-bucket-ttl":
		ttl := paths.ConfiguredBucketTTL()
		if ttl == 0 {
			fmt.Println("0 (automatic refresh disabled)")
		} else {
			fmt.Println(ttl)
		}
		src := "default (matches Scoop's own 3h)"
		if paths.ConfiguredBucketTTL() != 3*time.Hour {
			src = "goop config (" + paths.ConfigFilePath() + ")"
		}
		fmt.Println(ui.Dim("source: " + src))
		fmt.Println(ui.Dim("install refreshes buckets older than this; --no-update skips it for one run"))
		return 0
	case "set-bucket-ttl":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: goop config set-bucket-ttl <duration>   e.g. 3h, 24h, 30m, 0 to disable")
			return 2
		}
		d, err := time.ParseDuration(args[1])
		if err != nil {
			ui.Fail("config set-bucket-ttl: invalid duration %q (try 3h, 24h, 30m, or 0)", args[1])
			return 2
		}
		if err := paths.SetConfiguredBucketTTL(d); err != nil {
			ui.Fail("config set-bucket-ttl: %v", err)
			return 1
		}
		if d == 0 {
			ui.Ok("automatic bucket refresh disabled (run `goop bucket update` yourself)")
		} else {
			ui.Ok("buckets considered fresh for %s", d)
		}
		return 0
	case "unset-bucket-ttl":
		if err := paths.UnsetConfiguredBucketTTL(); err != nil {
			ui.Fail("config unset-bucket-ttl: %v", err)
			return 1
		}
		ui.Ok("bucket TTL back to the default (3h)")
		return 0
	case "get-cache-limit":
		limit := paths.ConfiguredCacheLimit()
		used, entries, err := installer.CacheUsage()
		if err != nil {
			ui.Fail("config get-cache-limit: %v", err)
			return 1
		}
		if limit == paths.CacheUnlimited {
			fmt.Println("unlimited")
			fmt.Println(ui.Dim("source: default (nothing is ever evicted)"))
		} else if limit == 0 {
			fmt.Println("0")
			fmt.Println(ui.Dim("source: goop config (" + paths.ConfigFilePath() + ") -- cache emptied after each command"))
		} else {
			fmt.Println(formatSize(limit))
			fmt.Println(ui.Dim("source: goop config (" + paths.ConfigFilePath() + ")"))
		}
		fmt.Printf("%s %s across %d file(s) in %s\n", ui.Bold("in use:"), formatSize(used), len(entries), paths.Cache())
		return 0
	case "set-cache-limit":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: goop config set-cache-limit <size|0|unlimited>   e.g. 5GB, 500MB, 0")
			return 2
		}
		if strings.EqualFold(args[1], "unlimited") {
			if err := paths.UnsetConfiguredCacheLimit(); err != nil {
				ui.Fail("config set-cache-limit: %v", err)
				return 1
			}
			ui.Ok("cache limit set to unlimited")
			return 0
		}
		n, err := parseSize(args[1])
		if err != nil {
			ui.Fail("config set-cache-limit: %v", err)
			return 2
		}
		if err := paths.SetConfiguredCacheLimit(n); err != nil {
			ui.Fail("config set-cache-limit: %v", err)
			return 1
		}
		ui.Ok("cache limit set to %s", formatSize(n))
		// Apply it now rather than only on the next download, so the
		// number the user just set is true immediately.
		if freed, err := installer.PruneCache(); err == nil && freed > 0 {
			ui.Ok("freed %s from the cache", formatSize(freed))
		}
		return 0
	case "unset-cache-limit":
		if err := paths.UnsetConfiguredCacheLimit(); err != nil {
			ui.Fail("config unset-cache-limit: %v", err)
			return 1
		}
		ui.Ok("cache limit unset (unlimited)")
		return 0
	case "get-root":
		root := paths.Root()
		source := "default"
		if os.Getenv("GOOP_HOME") != "" {
			source = "$GOOP_HOME"
		} else if _, ok := paths.ConfiguredRoot(); ok {
			source = "goop config (" + paths.ConfigFilePath() + ")"
		}
		fmt.Printf("%s\n", root)
		fmt.Println(ui.Dim("source: " + source))
		return 0
	case "set-root":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: goop config set-root <path>")
			return 2
		}
		newRoot, err := filepath.Abs(args[1])
		if err != nil {
			ui.Fail("config set-root: %v", err)
			return 1
		}

		oldRoot := paths.Root()
		if oldRoot != newRoot {
			if entries, err := os.ReadDir(paths.Apps()); err == nil && len(entries) > 0 {
				fmt.Println(ui.Yellow(ui.Bang) + fmt.Sprintf(" %d app(s) currently installed under %s -- they won't be visible to goop after this change (nothing is moved or deleted).", len(entries), oldRoot))
			}
		}

		if err := paths.SetConfiguredRoot(newRoot); err != nil {
			ui.Fail("config set-root: %v", err)
			return 1
		}
		if os.Getenv("GOOP_HOME") != "" {
			fmt.Println(ui.Yellow(ui.Bang) + " $GOOP_HOME is set in this environment and will keep overriding this until it's unset.")
		}
		ui.Ok("install root set to %s", newRoot)
		return 0
	case "unset-root":
		if err := paths.UnsetConfiguredRoot(); err != nil {
			ui.Fail("config unset-root: %v", err)
			return 1
		}
		ui.Ok("root reverted to %s", paths.Root())
		return 0
	case "get-proxy":
		envSet := paths.EnvProxyConfigured()
		proxy, configured := paths.ConfiguredProxy()
		switch {
		case envSet:
			fmt.Println(ui.Dim("(controlled by the HTTP_PROXY/HTTPS_PROXY environment variables, not goop's own config)"))
		case configured:
			fmt.Println(proxy)
		default:
			fmt.Println(ui.Dim("(none configured)"))
		}
		source := "none"
		if envSet {
			source = "environment (HTTP_PROXY/HTTPS_PROXY)"
		} else if configured {
			source = "goop config (" + paths.ConfigFilePath() + ")"
		}
		fmt.Println(ui.Dim("source: " + source))
		if noProxy := paths.ConfiguredNoProxy(); len(noProxy) > 0 {
			fmt.Println(ui.Dim("no-proxy: " + strings.Join(noProxy, ", ")))
		}
		return 0
	case "set-proxy":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: goop config set-proxy <url>")
			return 2
		}
		if err := paths.SetConfiguredProxy(args[1]); err != nil {
			ui.Fail("config set-proxy: %v", err)
			return 1
		}
		if paths.EnvProxyConfigured() {
			fmt.Println(ui.Yellow(ui.Bang) + " HTTP_PROXY/HTTPS_PROXY is set in this environment and will keep overriding this until it's unset.")
		}
		ui.Ok("proxy set to %s (used for both http and https, and for git bucket clones/pulls)", args[1])
		return 0
	case "unset-proxy":
		if err := paths.UnsetConfiguredProxy(); err != nil {
			ui.Fail("config unset-proxy: %v", err)
			return 1
		}
		ui.Ok("proxy unset")
		return 0
	case "set-no-proxy":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: goop config set-no-proxy <host1,host2,...>")
			return 2
		}
		hosts := strings.Split(args[1], ",")
		for i := range hosts {
			hosts[i] = strings.TrimSpace(hosts[i])
		}
		if err := paths.SetConfiguredNoProxy(hosts); err != nil {
			ui.Fail("config set-no-proxy: %v", err)
			return 1
		}
		ui.Ok("no-proxy set to %s", strings.Join(hosts, ", "))
		return 0
	case "unset-no-proxy":
		if err := paths.UnsetConfiguredNoProxy(); err != nil {
			ui.Fail("config unset-no-proxy: %v", err)
			return 1
		}
		ui.Ok("no-proxy list cleared")
		return 0
	default:
		fmt.Fprintln(os.Stderr, configUsage)
		return 2
	}
}

// cmdCheck compares this machine against the profiles a file declares.
// Reads receipts only -- no bucket, no network -- so it is instant and
// answers the same on a disconnected machine.
func cmdCheck(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: goop check <file> [profile...]")
		return 2
	}
	f, err := profileset.Load(args[0])
	if err != nil {
		ui.Fail("check: %v", err)
		return 1
	}
	deviations, err := installer.Check(f, args[1:])
	if err != nil {
		ui.Fail("check: %v", err)
		return 1
	}
	if len(deviations) == 0 {
		names := args[1:]
		if len(names) == 0 {
			names = f.Names()
		}
		ui.Ok("conformant: %s", strings.Join(names, ", "))
		return 0
	}
	rows := make([][]string, len(deviations))
	for i, d := range deviations {
		got := ui.Yellow(d.Got)
		if d.Got == "" {
			got = ui.Red("-")
		}
		rows[i] = []string{d.Profile, d.Package, d.Want, got, ui.Red(d.Reason)}
	}
	fmt.Print(ui.Table([]string{"PROFILE", "PACKAGE", "REQUIRED", "INSTALLED", "WHY"}, rows))
	return 3
}

// cmdSyncProfiles applies a profile file: install what is required and
// missing, leave everything else alone.
func cmdSyncProfiles(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: goop sync <file> [profile...]")
		return 2
	}
	f, err := profileset.Load(args[0])
	if err != nil {
		ui.Fail("sync: %v", err)
		return 1
	}
	fixed, errs, err := installer.SyncProfiles(f, args[1:])
	if err != nil {
		ui.Fail("sync: %v", err)
		return 1
	}
	for _, d := range fixed {
		if strings.HasPrefix(d.Reason, "not filed under") {
			ui.Ok("%s: %s filed under %s (was %s)", d.Profile, d.Package, d.Profile, whereItWas(d.Reason))
			continue
		}
		ui.Ok("%s: %s %s", d.Profile, d.Package, d.Want)
	}
	for _, name := range sortedKeys(errs) {
		ui.Fail("sync %s: %v", name, errs[name])
	}
	if len(errs) > 0 {
		return 1
	}

	// A package can install cleanly and still not be what the profile
	// pinned: the bucket may now carry different instructions under the
	// same version number. Saying so is the difference between applying a
	// file and merely running installs.
	drifted, err := installer.VerifyPins(f, args[1:])
	if err == nil && len(drifted) > 0 {
		for _, d := range drifted {
			ui.Warn("%s: %s %s installed, but its manifest differs from the one pinned", d.Profile, d.Package, d.Want)
		}
		fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("The bucket has changed what this version does. Re-pin with `goop profile export`, or restore the manifest."))
		return 3
	}
	// "nothing to do" said nothing about whether anything had been
	// checked -- an empty file and a fully conformant machine printed the
	// same line. Report what was actually verified.
	sum, err := installer.Summarize(f, args[1:])
	if err != nil {
		ui.Fail("sync: %v", err)
		return 1
	}
	if len(fixed) == 0 {
		ui.Ok("already conformant: %d package(s) across %s",
			sum.Packages, strings.Join(sum.Profiles, ", "))
	} else {
		ui.Ok("conformant: %d package(s) across %s (%d changed)",
			sum.Packages, strings.Join(sum.Profiles, ", "), len(fixed))
	}
	return 0
}

// whereItWas pulls the profile list out of a "not filed under X (it is
// in Y, Z)" reason, for a message that says what changed.
func whereItWas(reason string) string {
	_, rest, ok := strings.Cut(reason, "(it is in ")
	if !ok {
		return "no profile"
	}
	return strings.TrimSuffix(rest, ")")
}

// cmdExport captures this machine to a file: its buckets and everything
// installed on it.
func cmdExport(args []string) int {
	out := "goop-setup.json"
	if len(args) == 2 && args[0] == "--out" {
		out = args[1]
	} else if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: goop export [--out <file>]")
		return 2
	}
	f, err := installer.ExportSetup()
	if err != nil {
		ui.Fail("export: %v", err)
		return 1
	}
	if err := setup.Save(out, f); err != nil {
		ui.Fail("export: %v", err)
		return 1
	}
	ui.Ok("captured %d package(s) and %d bucket(s) to %s", len(f.Apps), len(f.Buckets), out)
	fmt.Println(ui.Dim("  replay it on another machine with `goop import " + out + "`"))
	return 0
}

// cmdImportSetup replays a captured machine.
func cmdImportSetup(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: goop import <file>")
		fmt.Fprintln(os.Stderr, ui.Dim("  to adopt apps installed by a real Scoop instead, use `goop adopt`"))
		return 2
	}
	f, err := setup.Load(args[0])
	if err != nil {
		ui.Fail("import: %v", err)
		return 1
	}
	installed, errs, err := installer.ImportSetup(f)
	if err != nil {
		ui.Fail("import: %v", err)
		return 1
	}
	for _, n := range installed {
		ui.Ok("installed %s", n)
	}
	for _, n := range sortedKeys(errs) {
		ui.Fail("import %s: %v", n, errs[n])
	}
	if len(errs) > 0 {
		return 1
	}
	if len(installed) == 0 {
		fmt.Println(ui.Dim("nothing to do"))
	}
	return 0
}

// cmdAudit compares this machine against a capture.
func cmdDigest(args []string) int {
	var names []string
	all, recheck := false, false
	for _, a := range args {
		switch a {
		case "--all":
			all = true
		case "--recheck":
			recheck = true
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintln(os.Stderr, "usage: goop digest <name>... | --all [--recheck]")
				return 2
			}
			names = append(names, a)
		}
	}
	if len(names) == 0 && !all {
		fmt.Fprintln(os.Stderr, "usage: goop digest <name>... | --all [--recheck]")
		return 2
	}

	results, err := installer.BackfillDigests(names, recheck)
	if err != nil {
		ui.Fail("digest: %v", err)
		return 1
	}
	if len(results) == 0 {
		fmt.Println(ui.Dim("nothing to do"))
		return 0
	}

	var rows [][]string
	recorded, blocked, moved := 0, 0, 0
	for _, r := range results {
		// Without --recheck, saying "already had one" for every healthy
		// package buries the handful that need attention.
		if r.Outcome == installer.DigestAlreadyHad && !recheck {
			continue
		}
		var outcome string
		switch r.Outcome {
		case installer.DigestRecorded:
			outcome = ui.Green(string(r.Outcome))
			recorded++
		case installer.DigestAlreadyHad:
			outcome = ui.Dim(string(r.Outcome))
		case installer.DigestMoved:
			outcome = ui.Red(string(r.Outcome))
			moved++
		default:
			outcome = ui.Red(string(r.Outcome))
			blocked++
		}
		detail := r.Detail
		if detail == "" {
			detail = ui.Dim("-")
		}
		rows = append(rows, []string{r.Package, r.Version, outcome, detail})
	}
	if len(rows) > 0 {
		fmt.Print(ui.Table([]string{"PACKAGE", "VERSION", "RESULT", "DETAIL"}, rows))
		fmt.Println()
	}

	if recorded > 0 {
		ui.Ok("recorded %d digest(s)", recorded)
		// The receipt never captured pre_install/post_install, so a
		// manifest republished with an edited install script and
		// everything else unchanged would have passed the corroboration
		// above. Claiming these pins are as strong as one written by an
		// install would be the overstatement this whole feature exists to
		// avoid.
		fmt.Println(ui.Dim("  each was corroborated against the bucket: same version, and every field"))
		fmt.Println(ui.Dim("  the receipt kept -- urls, hashes, bin, shortcuts, uninstaller -- matches."))
		fmt.Println(ui.Dim("  install scripts could not be checked: goop never recorded them, so these"))
		fmt.Println(ui.Dim("  adopt the current manifest rather than recover what actually ran."))
		fmt.Println(ui.Dim("  reinstall (`goop update <name>`) if you want a digest of the real thing."))
	}
	if moved > 0 {
		// These already have a digest and it is doing its job. Nothing is
		// rewritten: overwriting would erase the very evidence that the
		// bucket republished the version.
		ui.Warn("%d package(s) no longer match the bucket's manifest for the version you have", moved)
		fmt.Println(ui.Dim("  the bucket republished that version with different instructions. Your"))
		fmt.Println(ui.Dim("  recorded digest is left alone -- it is the evidence. Compare with"))
		fmt.Println(ui.Dim("  `goop info <name>`, and `goop update <name>` to take the new manifest."))
	}
	if blocked > 0 {
		ui.Warn("%d package(s) could not be given one", blocked)
		fmt.Println(ui.Dim("  `unavailable` means the bucket has moved on: goop cannot fetch a"))
		fmt.Println(ui.Dim("  historical manifest, so there is nothing honest to record. Updating"))
		fmt.Println(ui.Dim("  the package records a digest as a side effect."))
	}
	if blocked > 0 || moved > 0 {
		return 1
	}
	if recorded == 0 && len(rows) == 0 {
		fmt.Println(ui.Dim("every install already has a manifest digest"))
	}
	return 0
}

func cmdAudit(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: goop audit <file>")
		return 2
	}
	f, err := setup.Load(args[0])
	if err != nil {
		ui.Fail("audit: %v", err)
		return 1
	}
	deviations, err := installer.AuditSetup(f)
	if err != nil {
		ui.Fail("audit: %v", err)
		return 1
	}
	if len(deviations) == 0 {
		ui.Ok("this machine matches %s", args[0])
		return 0
	}
	rows := make([][]string, len(deviations))
	for i, d := range deviations {
		want, got := d.Want, d.Got
		if want == "" {
			want = ui.Dim("-")
		}
		if got == "" {
			got = ui.Red("-")
		}
		rows[i] = []string{d.Package, want, got, ui.Red(d.Reason)}
	}
	fmt.Print(ui.Table([]string{"PACKAGE", "CAPTURED", "HERE", "WHY"}, rows))
	return 3
}
