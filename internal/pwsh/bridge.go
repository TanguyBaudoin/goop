// Package pwsh delegates manifest PowerShell (pre_install, post_install,
// installer.script, uninstaller.script) to a real pwsh/powershell
// process, never reinterpreting it (CPT-04). It also provides
// goop-flavored polyfills of the Scoop helper functions manifest scripts
// commonly call directly (Invoke-ExternalCommand, Expand-7zipArchive,
// Expand-MsiArchive, shim, ...), since those scripts assume Scoop's own
// library is already in scope.
package pwsh

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// scriptTimeout bounds how long a single delegated script may run.
// Generous, since some manifest scripts genuinely do their own slow
// downloads/extraction, but a script (or something it shells out to,
// like a stalled network call) must not be able to hang goop forever.
const scriptTimeout = 15 * time.Minute

//go:embed prelude.ps1
var prelude string

// Vars is the Scoop-compatible variable context bound before a script
// runs, mirroring the names real manifest scripts reference.
type Vars struct {
	Dir             string // $dir -- app directory the script operates on (the staging dir during install)
	Version         string
	Architecture    string
	PersistDir      string
	Fname           string // $fname -- primary downloaded file's name
	Cmd             string // $cmd -- "install" or "uninstall"
	Bucket          string
	BucketsDir      string
	ShimsDir        string // used by the shim/unshim polyfills, not a real Scoop variable name
	ShimMaster      string
	AppsDir         string // used by the appdir/versiondir/currentdir polyfills
	CacheDir        string // used by the cache_path polyfill
	ShimLogPath     string // if set, the shim polyfill appends every name it creates here (see prelude.ps1)
	ShortcutLogPath string // same, for startmenu_shortcut
}

// runMu serializes every delegated script system-wide within this
// process. Real Scoop only ever installs one package at a time, so this
// never came up upstream; goop parallelizes installs (A1), and several
// manifest scripts (directly, or via the Expand-MsiArchive/
// Expand-DarkArchive polyfills) shell out to msiexec.exe, which holds a
// single machine-wide installer lock -- two concurrent invocations
// don't queue, one just fails with "Another program is being
// installed." Serializing script execution here trades away some
// parallelism only for the minority of packages that run scripts at
// all; plain downloads (the bulk of install time for most packages)
// never touch this lock.
var runMu sync.Mutex

// Run executes script with vars bound and goop's compat library
// available. Output is returned regardless of error, for logging.
func Run(script string, vars Vars) (output string, err error) {
	runMu.Lock()
	defer runMu.Unlock()
	return runLocked(script, vars)
}

func runLocked(script string, vars Vars) (output string, err error) {
	if strings.TrimSpace(script) == "" {
		return "", nil
	}

	pwshPath, err := resolvePwsh()
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp("", "goop-script-*.ps1")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	// Deliberately no `$ErrorActionPreference = 'Stop'` here: real Scoop
	// runs hook scripts under the default 'Continue', so a native
	// command's stderr chatter (e.g. reg.exe's own success message) is
	// non-terminating and doesn't abort the script. The try/catch below
	// still turns a genuinely terminating error (an explicit `throw`, an
	// uncaught .NET exception, an `abort` call) into a failing exit code.
	// [Console]::OutputEncoding governs how PowerShell encodes text
	// written to a redirected/piped stdout (our case, always -- Go's
	// CombinedOutput captures via a pipe, never a real console) --
	// independent of the UTF-8 BOM below, which only affects how the
	// script *file* is read. Without this, Windows PowerShell 5.1 writes
	// redirected output using the OEM console code page regardless of
	// the BOM, silently mangling any non-ASCII text a script echoes
	// (confirmed: "é" came back as the single CP437 byte 0x82, not
	// UTF-8; setting this fixed it). $OutputEncoding (the pipeline-to-
	// native-exe preference variable) is a different, unrelated setting
	// and doesn't affect Write-Host at all -- confirmed by testing it
	// alone first, which changed nothing. Wrapped in try/catch even
	// though it didn't throw in real testing here, since it's a Win32
	// console API call that could plausibly fail in some other
	// environment (headless CI, a service context, ...) where failing
	// to *set* the encoding shouldn't block the script from running.
	encodingPreamble := "try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch {}\n"
	full := encodingPreamble + header(vars) + "\n" + prelude + "\n" +
		"try {\n" + script + "\n} catch {\n  Write-Host \"script error: $_\" -ForegroundColor Red\n  exit 1\n}\n"
	// Windows PowerShell 5.1 reads a BOM-less script file using the
	// system's active code page, not UTF-8 -- without this, any
	// non-ASCII text (accented shortcut names, non-English descriptions
	// scripts might echo, ...) gets silently mangled.
	if _, err := tmp.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString(full); err != nil {
		tmp.Close()
		return "", err
	}
	tmp.Close()

	ctx, cancel := context.WithTimeout(context.Background(), scriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, pwshPath, "-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", tmpPath)
	// Scripts often shell out to another tool by its bare name (e.g.
	// aws-sam-cli's pre_install calls "lessmsi"), expecting it resolvable
	// the way it would be on a real Scoop install where the shims dir is
	// on PATH. Any tool the user already has goop-installed should be
	// just as reachable here.
	//
	// $dir goes on PATH too, so a script can invoke the app it is
	// installing by bare name -- extras/firefox.json's post_install runs
	// `firefox -CreateProfile ...`, which on a real Scoop install
	// resolves through the shim created before post_install runs. goop
	// deliberately runs hooks earlier, against the staging directory and
	// before the commit point, so that a failing hook rolls the whole
	// install back (TR-04) -- which means no shim exists yet. Putting
	// the app's own directory on PATH gets those scripts working without
	// giving up that atomicity.
	//
	// $dir comes FIRST, ahead of the shims dir: a shim for this very app
	// may already exist and point at <app>\current\<exe>, which during a
	// pre-commit hook is either the *previous* version or, after an
	// uninstall that left the shim orphaned, nothing at all. Seen for
	// real -- zen-browser's post_install calls `zen`, hit a stale shim,
	// and failed with "The system cannot find the path specified". The
	// staging directory is what the hook is actually installing, so it
	// wins.
	cmd.Env = prependToPath(os.Environ(), vars.Dir, vars.ShimsDir)
	out, runErr := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("script timed out after %s (it or something it called stopped responding)\n%s", scriptTimeout, out)
	}
	if runErr != nil {
		return string(out), fmt.Errorf("script failed: %w\n%s", runErr, out)
	}
	return string(out), nil
}

// prependToPath returns env with each non-empty dir prepended to PATH,
// first argument ending up first (case-insensitive match, since Windows
// env var names are case-insensitive).
func prependToPath(env []string, dirs ...string) []string {
	var prefix []string
	for _, d := range dirs {
		if d != "" {
			prefix = append(prefix, d)
		}
	}
	if len(prefix) == 0 {
		return env
	}
	joined := strings.Join(prefix, string(os.PathListSeparator))

	out := make([]string, 0, len(env)+1)
	found := false
	for _, e := range env {
		if len(e) >= 5 && strings.EqualFold(e[:5], "PATH=") {
			out = append(out, "PATH="+joined+string(os.PathListSeparator)+e[5:])
			found = true
		} else {
			out = append(out, e)
		}
	}
	if !found {
		out = append(out, "PATH="+joined)
	}
	return out
}

func resolvePwsh() (string, error) {
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("powershell.exe"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("neither pwsh.exe nor powershell.exe found on PATH; required to run manifest scripts (CPT-04)")
}

// RunningProcessesUnder reports the executable paths of any currently
// running process whose image lives under dir (or a subdirectory) --
// mirrors real Scoop's own test_running_process (lib/install.ps1),
// which Uninstall/Update use to refuse cleanly before touching
// anything, rather than failing deep into a partial removal with a
// low-level "Access is denied" (confirmed against a real GUI app --
// its own version directory still open while goop had already removed
// its shims/shortcuts/current link, leaving it worse off than a clean
// refusal would have). A query failure returns an error, not a false
// "nothing running" -- callers should treat that as "couldn't check",
// not as a green light.
func RunningProcessesUnder(dir string) ([]string, error) {
	pwshPath, err := resolvePwsh()
	if err != nil {
		return nil, err
	}
	script := fmt.Sprintf(
		"Get-Process | Where-Object { $_.Path -like %s } | Select-Object -ExpandProperty Path",
		psQuote(strings.TrimRight(dir, `\`)+`\*`),
	)
	cmd := exec.Command(pwshPath, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("check running processes under %s: %w", dir, err)
	}
	var running []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			running = append(running, line)
		}
	}
	return running, nil
}

func header(v Vars) string {
	var b strings.Builder
	fmt.Fprintf(&b, "$dir = %s\n", psQuote(v.Dir))
	// Real Scoop's own $original_dir is just "$dir = $original_dir" set
	// once before any later redirection (lib/install.ps1:49) -- goop has
	// no such redirection to begin with, so the two are always the same
	// value here too. A handful of real manifest scripts reference
	// $original_dir directly (confirmed: extras/hwinfo.json's
	// installer.script), so it needs to actually exist as a script
	// variable, not just the Go-side substitution `notes` display
	// already had for its own separate purpose.
	fmt.Fprintf(&b, "$original_dir = %s\n", psQuote(v.Dir))
	fmt.Fprintf(&b, "$version = %s\n", psQuote(v.Version))
	fmt.Fprintf(&b, "$architecture = %s\n", psQuote(v.Architecture))
	fmt.Fprintf(&b, "$persist_dir = %s\n", psQuote(v.PersistDir))
	fmt.Fprintf(&b, "$fname = %s\n", psQuote(v.Fname))
	fmt.Fprintf(&b, "$cmd = %s\n", psQuote(v.Cmd))
	fmt.Fprintf(&b, "$global = $false\n") // NR-01: goop never installs system-wide
	fmt.Fprintf(&b, "$bucket = %s\n", psQuote(v.Bucket))
	fmt.Fprintf(&b, "$bucketsdir = %s\n", psQuote(v.BucketsDir))
	fmt.Fprintf(&b, "$goop_shims_dir = %s\n", psQuote(v.ShimsDir))
	fmt.Fprintf(&b, "$goop_shim_master = %s\n", psQuote(v.ShimMaster))
	fmt.Fprintf(&b, "$goop_apps_dir = %s\n", psQuote(v.AppsDir))
	fmt.Fprintf(&b, "$goop_cache_dir = %s\n", psQuote(v.CacheDir))
	fmt.Fprintf(&b, "$goop_shim_log = %s\n", psQuote(v.ShimLogPath))
	fmt.Fprintf(&b, "$goop_shortcut_log = %s\n", psQuote(v.ShortcutLogPath))
	return b.String()
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
