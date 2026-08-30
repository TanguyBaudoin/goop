// Package installer orchestrates install/uninstall/list (FR-01, FR-02,
// FR-04): resolving a manifest, downloading and verifying its assets,
// extracting them, running any delegated PowerShell (CPT-04), wiring up
// the version junction, and creating shims/shortcuts.
package installer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TanguyBaudoin/goop/internal/archive"
	"github.com/TanguyBaudoin/goop/internal/bucket"
	"github.com/TanguyBaudoin/goop/internal/downloader"
	"github.com/TanguyBaudoin/goop/internal/envvars"
	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/paths"
	"github.com/TanguyBaudoin/goop/internal/profile"
	"github.com/TanguyBaudoin/goop/internal/pwsh"
	goopshim "github.com/TanguyBaudoin/goop/internal/shim"
	"github.com/TanguyBaudoin/goop/internal/shimbin"
	"github.com/TanguyBaudoin/goop/internal/vercmp"
)

// Logf, if set, receives human-readable progress messages.
var Logf = func(format string, args ...any) {}

// Record is what goop writes into a version directory after a successful
// install, and reads back for `list`, `uninstall`, and `lock` (FR-10:
// name/version/bucket/resolved-URL/hash/architecture, plus what goop
// itself additionally needs to reproduce a *working* install from the
// lockfile alone -- Bin/ExtractDirs/ExtractTos -- without FR-11's sync
// consulting the bucket at all).
type Record struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Bucket       string `json:"bucket"`
	Architecture string `json:"architecture"`
	// Description/Homepage/LicenseIdentifier/LicenseURL are purely
	// informational, captured at install time (like URLs/Hashes below)
	// so `goop info` can show them without needing the bucket manifest
	// to still exist or be unchanged -- mirrors real Scoop's own
	// `scoop info` (libexec/scoop-info.ps1).
	Description       string                   `json:"description,omitempty"`
	Homepage          string                   `json:"homepage,omitempty"`
	LicenseIdentifier string                   `json:"license_identifier,omitempty"`
	LicenseURL        string                   `json:"license_url,omitempty"`
	URLs              []string                 `json:"urls"`
	Hashes            []string                 `json:"hashes"`
	Bin               []manifest.BinEntry      `json:"bin"`
	ExtractDirs       []string                 `json:"extract_dirs,omitempty"`
	ExtractTos        []string                 `json:"extract_tos,omitempty"`
	Shortcuts         []manifest.ShortcutEntry `json:"shortcuts,omitempty"`
	Uninstaller       manifest.InstallHook     `json:"uninstaller,omitempty"`
	// PreUninstall/PostUninstall are resolved script content (like
	// PreInstall/PostInstall on the install side), captured at install
	// time so Uninstall can run them even if the bucket manifest has
	// since changed or been removed.
	PreUninstall  string `json:"pre_uninstall,omitempty"`
	PostUninstall string `json:"post_uninstall,omitempty"`
	// ExtraShims are shims a manifest's own pre_install/installer/
	// post_install script created directly (calling the `shim` compat
	// function itself, rather than declaring them in the manifest's
	// `bin` field -- real manifests do this, e.g. looping over every
	// .exe an MSI installed and shimming each one individually).
	// installResolved detects these by diffing paths.Shims() before and
	// after running the hooks, purely so Uninstall knows their names too
	// -- without this they'd be orphaned (left behind, pointing at
	// nothing) on removal, since they're invisible to the normal Bin
	// bookkeeping.
	ExtraShims []ExtraShim `json:"extra_shims,omitempty"`
	// ExtraShortcuts: same idea, for startmenu_shortcut.
	ExtraShortcuts []ExtraShortcut `json:"extra_shortcuts,omitempty"`
	// EnvSet/EnvAddedPaths are the already-expanded ($dir/$version/
	// $persist_dir substituted) values actually applied to
	// HKCU\Environment, so uninstall can reverse exactly what was set
	// without needing $dir to still exist.
	EnvSet        map[string]string `json:"env_set,omitempty"`
	EnvAddedPaths []string          `json:"env_added_paths,omitempty"`
	// PSModuleName is a manifest's `psmodule.name`, if set -- recorded
	// so Uninstall knows to remove the module junction (real Scoop's
	// own uninstall_psmodule, lib/psmodules.ps1) without needing the
	// bucket manifest to still exist or be unchanged.
	PSModuleName string `json:"psmodule_name,omitempty"`
	// Suggest is a manifest's `suggest` field, carried through to the
	// record so cmdInstall (CLI layer) can show it once at the end of a
	// batch, matching real Scoop's own show_suggestions -- see its
	// field comment on manifest.Manifest.
	Suggest map[string]manifest.StringList `json:"suggest,omitempty"`
	// Depends is the manifest's declared `depends` (never the implicit
	// helper tools -- see manifest.Resolved.Depends), recorded so
	// Uninstall can find which installed apps depend on the one being
	// removed and take them down with it.
	Depends []string `json:"depends,omitempty"`
	// Hold pins the app at its current version: `goop update` skips it
	// until `goop unhold`. Stored in the record like real Scoop's own
	// `hold` (libexec/scoop-hold.ps1 writes it into install.json), so it
	// survives goop upgrades and is visible in `goop info`.
	Hold        bool      `json:"hold,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
	// ManifestDigest fingerprints the manifest this was installed from,
	// so a profile check can compare against it without re-reading the
	// bucket -- and would notice a manifest edited since.
	ManifestDigest string `json:"manifest_digest,omitempty"`
	// State is "pending" between the commit rename and the moment shims,
	// shortcuts and environment entries are in place; "ready" after.
	// Empty means ready, for records written before this field existed.
	State string `json:"state,omitempty"`
}

// Record states. An empty state means ready: records written before this
// existed are valid installs, and must not all become suspect.
const (
	recordPending = "pending"
	recordReady   = "ready"
)

// Ready reports whether the install this record describes actually
// finished. The record is committed by the rename that makes a version
// visible, but shims, shortcuts and environment entries are created
// after that -- so a failure in between used to leave a record claiming
// an install that has no working commands. Worse, the next attempt saw
// the record, reported "already installed", and did nothing.
func (r Record) Ready() bool {
	return r.State == "" || r.State == recordReady
}

// ExtraShim is one shim a manifest script created itself. Name is what
// Uninstall removes; Path is the executable it points at, recorded so
// Reset can rebuild the shim without re-running the script (which is
// not generally safe to repeat -- real post_install scripts create
// profiles and import registry keys).
//
// Serializes as an object, but decodes a bare string too: records
// written before Path existed stored just the name, and they must keep
// loading.
type ExtraShim struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

func (e *ExtraShim) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		e.Name, e.Path = s, ""
		return nil
	}
	type raw ExtraShim
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	*e = ExtraShim(r)
	return nil
}

// ExtraShortcut is a Start Menu shortcut a manifest script created via
// startmenu_shortcut, rather than through the manifest's `shortcuts`
// field. Tracked for the same reason as ExtraShim: without it uninstall
// leaves the .lnk behind pointing at a deleted directory (NR-02, seen
// for real with extras/freecad.json), and reset has nothing to rebuild
// from.
type ExtraShortcut struct {
	Name   string `json:"name"`
	Target string `json:"target,omitempty"`
	Args   string `json:"args,omitempty"`
	Icon   string `json:"icon,omitempty"`
}

const recordFileName = "goop-install.json"

// unsupportedManifest reports why a manifest can't be installed yet,
// distinct from an ordinary error so callers (e.g. a batch harness) can
// tally "not yet supported" separately from "broke unexpectedly".
type unsupportedManifest struct {
	reason string
}

func (e *unsupportedManifest) Error() string { return e.reason }

// IsUnsupported reports whether err is because the manifest needs
// functionality goop doesn't implement yet, rather than an actual
// failure.
func IsUnsupported(err error) bool {
	_, ok := err.(*unsupportedManifest)
	return ok
}

// installLocks serializes concurrent installs of the *same* app --
// necessary because installs already run concurrently across different
// apps (A1), and now a dependency can trigger installing an app that's
// also a direct target of the same batch, or a shared dependency of two
// different targets, at the same time.
var installLocks sync.Map // appName -> *sync.Mutex

func lockInstall(appName string) func() {
	v, _ := installLocks.LoadOrStore(appName, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// Install resolves spec ("[bucket/]name[@constraint]", FR-06/A4)
// against configured buckets, recursively ensuring every `depends`
// entry is installed first, and installs it for the host architecture.
// Installing an already-installed version is a no-op success (idempotent,
// so re-running a batch is cheap).
//
// The top-level app named by spec is registered as a member of the
// currently active profile (profile.Active/`goop profile use`) --
// deliberately only the top-level target, not any dependency pulled in
// along the way (installDependencies calls installSpec directly, never
// this function), so a profile's membership reflects what was actually
// asked for, not everything transitively required to satisfy it.
func Install(spec string) (Record, error) {
	rec, err := installSpec(spec, nil, false)
	if err != nil {
		return Record{}, err
	}
	if err := profile.Add(profile.Active(), rec.Name); err != nil {
		Logf("%s: profile registration: %v", rec.Name, err) // non-fatal: same tier as shortcuts, the app is installed and usable either way
	}
	return rec, nil
}

// installSpec resolves and installs spec. quiet suppresses the
// "already installed" line installResolved would otherwise log when
// the target is already at the current version -- Update passes true
// for its own top-level target, since its caller (cmdUpdate) already
// prints a clean "up to date" summary for exactly that case afterward
// and the two together were pure noise (confirmed against a real
// `goop update` run: every already-current app logged twice, once
// here and once in that summary). A dependency pulled in along the way
// (installDependencies' own recursive call) always stays quiet=false
// regardless -- that's genuine new work happening mid-update, not a
// redundant restatement of something the caller already reports.
func installSpec(spec string, stack []string, quiet bool) (Record, error) {
	if strings.HasPrefix(spec, "maven:") {
		return installMaven(spec)
	}

	parsed := manifest.ParseSpec(spec)
	appName := parsed.Name

	for _, s := range stack {
		if s == appName {
			return Record{}, fmt.Errorf("dependency cycle detected: %s -> %s", strings.Join(stack, " -> "), appName)
		}
	}

	unlock := lockInstall(appName)
	defer unlock()

	if err := paths.EnsureLayout(); err != nil {
		return Record{}, err
	}

	bucketName, m, err := bucket.Resolve(parsed)
	if err != nil {
		return Record{}, err
	}

	if parsed.Constraint != "" {
		ok, err := vercmp.Satisfies(m.Version, parsed.Constraint)
		if err != nil {
			return Record{}, fmt.Errorf("%s: %w", spec, err)
		}
		if !ok {
			return Record{}, fmt.Errorf(
				"conflict: %s requires %s, but bucket %q currently offers %s (installing a specific historical version isn't supported -- only the bucket's current version can be checked against a constraint)",
				appName, spec, bucketName, m.Version)
		}
	}

	// Resolved before dependencies rather than after: implicitHelpers
	// needs the architecture-merged url/scripts to tell which helper
	// tools this install will actually shell out to. Resolve is pure,
	// so hoisting it above the install step below changes nothing else.
	archKey, err := manifest.HostArchKey()
	if err != nil {
		return Record{}, err
	}
	resolved, err := m.Resolve(appName, archKey)
	if err != nil {
		return Record{}, err
	}

	// Declared `depends` plus the helper tools extraction needs (7zip,
	// innounp, dark) -- same combination real Scoop's Get-Dependency
	// feeds its installer, so a manifest never fails at extraction time
	// for a helper goop could have installed itself. installDependencies
	// already skips anything already installed and detects cycles.
	deps := append(append([]string{}, m.Depends...), implicitHelpers(resolved)...)
	if err := installDependencies(appName, deps, append(append([]string{}, stack...), appName)); err != nil {
		return Record{}, err
	}

	return installResolved(appName, bucketName, archKey, resolved, quiet)
}

// installDependencies ensures every entry in depends is installed
// before parent itself is, recursing through each dependency's own
// depends first (stack carries the chain of apps currently being
// resolved, for installSpec's cycle check). An already-installed
// dependency is left alone unless its own constraint rejects the
// installed version, which is reported as a conflict (FR-06) rather
// than silently reinstalling or upgrading something already in place.
func installDependencies(parent string, depends []string, stack []string) error {
	for _, raw := range depends {
		dep := manifest.ParseSpec(raw)

		if rec, ok := readCurrentRecord(dep.Name); ok {
			if dep.Constraint == "" {
				continue
			}
			satisfies, err := vercmp.Satisfies(rec.Version, dep.Constraint)
			if err != nil {
				return fmt.Errorf("%s depends on %s: %w", parent, raw, err)
			}
			if !satisfies {
				return fmt.Errorf(
					"conflict: %s requires %s, but %s is already installed at %s (won't silently change an existing install's version)",
					parent, raw, dep.Name, rec.Version)
			}
			continue
		}

		Logf("%s: installing dependency %s", parent, dep.Name)
		if _, err := installSpec(raw, stack, false); err != nil {
			return fmt.Errorf("%s: dependency %s: %w", parent, dep.Name, err)
		}
	}
	return nil
}

// installResolved runs the actual install pipeline (download, verify,
// extract, hooks, persist, commit, shims, shortcuts, env) against an
// already-resolved manifest. Install uses this after resolving appName
// against a bucket; Sync uses it directly against a lockfile entry's
// frozen fields, with no bucket involved at all (FR-11).
func installResolved(appName, bucketName, archKey string, resolved manifest.Resolved, quiet bool) (Record, error) {
	if err := paths.EnsureLayout(); err != nil {
		return Record{}, err
	}

	// Before the already-installed shortcut below, not after: refreshing
	// the shared shim binary is about *goop* having been upgraded, not
	// about this app needing work. Gating it on a real install meant a
	// machine whose apps are all current never picked up a shim fix.
	if err := ensureShimMaster(); err != nil {
		Logf("%s: shim binary: %v", appName, err) // non-fatal, createShims reports it properly later
	}

	versionDir := paths.AppVersion(appName, resolved.Version)
	if rec, ok := readRecord(versionDir); ok && rec.Ready() {
		if !quiet {
			Logf("%s %s already installed", appName, resolved.Version)
		}
		if err := relinkCurrent(appName, versionDir); err != nil {
			return Record{}, err
		}
		return rec, nil
	} else if ok {
		// A record that is not ready describes an install that committed
		// and then failed before its shims existed. Clear it out so the
		// retry below can commit over the same version -- os.Rename will
		// not replace an existing directory, so leaving it there would
		// make every retry fail for a reason unrelated to the original
		// problem.
		Logf("%s %s: previous install did not finish, redoing it", appName, resolved.Version)
		if err := os.RemoveAll(versionDir); err != nil {
			return Record{}, fmt.Errorf("clear the unfinished install of %s: %w", appName, err)
		}
	}

	staging := paths.AppVersionStaging(appName, resolved.Version)
	os.RemoveAll(staging) // leftover from a previous failed attempt
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return Record{}, err
	}
	// TR-04: only the final rename below makes the install visible; any
	// failure before that leaves no trace beyond the staging dir, which
	// we clean up.
	succeeded := false
	defer func() {
		if !succeeded {
			os.RemoveAll(staging)
			// A failed *first* install would otherwise leave an empty
			// apps/<name>/ behind, which List reports as "broken: no
			// readable current record" -- a ghost entry for something
			// that was never installed (seen for real after a firefox
			// post_install failure). os.Remove only succeeds on an
			// empty directory, so an upgrade failure, where earlier
			// versions are still in there, is left untouched.
			os.Remove(paths.App(appName))
		}
	}()

	var primaryFname string
	for i, rawEntryURL := range resolved.URLs {
		if i >= len(resolved.Hashes) {
			return Record{}, &unsupportedManifest{fmt.Sprintf(
				"%s: url[%d] has no hash; refusing to install without verification (FR-40)", appName, i)}
		}
		fname, err := placeAsset(appName, rawEntryURL, resolved.Hashes[i], resolved.ExtractDirFor(i), resolved.ExtractToFor(i), staging, resolved.InnoSetup)
		if err != nil {
			return Record{}, err
		}
		if i == 0 {
			primaryFname = fname
		}
	}

	// Some real manifests call the `shim` compat function directly from
	// a hook script (looping over every .exe an MSI installed, e.g.)
	// instead of declaring them in the manifest's `bin` field -- the
	// shim polyfill appends each name it creates to this scratch file
	// (via $goop_shim_log) so Uninstall knows about them too (ExtraShims)
	// even though they never went through resolved.Bin. Deliberately a
	// dedicated per-install file rather than a before/after diff of
	// paths.Shims() itself: installs run concurrently (A1) against that
	// same shared directory, so a diff would (and, confirmed against a
	// real concurrent 11-package install, did) misattribute other apps'
	// own declarative shims -- created outside any pwsh.Run call, so not
	// serialized by pwsh's runMu -- to whichever app's hook happened to
	// be running at the same moment.
	shimLog, err := os.CreateTemp("", "goop-shimlog-*.txt")
	if err != nil {
		return Record{}, err
	}
	shimLogPath := shimLog.Name()
	shortcutLog, err := os.CreateTemp("", "goop-lnklog-*.txt")
	if err != nil {
		return Record{}, err
	}
	shortcutLogPath := shortcutLog.Name()
	shortcutLog.Close()
	defer os.Remove(shortcutLogPath)
	shimLog.Close()
	defer os.Remove(shimLogPath)

	vars := pwsh.Vars{
		Dir:             staging,
		Version:         resolved.Version,
		Architecture:    archKey,
		PersistDir:      paths.Persist(appName),
		Fname:           primaryFname,
		Cmd:             "install",
		Bucket:          bucketName,
		BucketsDir:      paths.Buckets(),
		ShimsDir:        paths.Shims(),
		ShimMaster:      paths.ShimMaster(),
		AppsDir:         paths.Apps(),
		CacheDir:        paths.Cache(),
		ShimLogPath:     shimLogPath,
		ShortcutLogPath: shortcutLogPath,
	}

	if resolved.PreInstall != "" {
		Logf("%s: running pre_install", appName)
		if out, err := pwsh.Run(resolved.PreInstall, vars); err != nil {
			return Record{}, fmt.Errorf("%s: pre_install: %w", appName, err)
		} else if strings.TrimSpace(out) != "" {
			Logf("%s", strings.TrimRight(out, "\n"))
		}
	}

	if !resolved.Installer.IsZero() {
		if err := runInstallHook(appName, "installer", resolved.Installer, staging, vars); err != nil {
			return Record{}, err
		}
	}

	if err := linkPersist(appName, resolved.Persist, staging); err != nil {
		return Record{}, err
	}

	// post_install runs here, still against the staging dir -- same as
	// pre_install -- and BEFORE the commit point below. TR-04 requires
	// atomic installs (full success or no visible change); a post_install
	// failure must roll back like any other step, not leave a working
	// install on disk while still being reported as a failure.
	if resolved.PostInstall != "" {
		Logf("%s: running post_install", appName)
		if out, err := pwsh.Run(resolved.PostInstall, vars); err != nil {
			return Record{}, fmt.Errorf("%s: post_install: %w", appName, err)
		} else if strings.TrimSpace(out) != "" {
			Logf("%s", strings.TrimRight(out, "\n"))
		}
	}

	// $dir here means the app's final, stable path -- versionDir, not
	// staging, even though the rename hasn't happened yet -- so an
	// env_set value like ERLANG_HOME=$dir survives the rename intact.
	envSet := expandEnvSet(resolved.EnvSet, versionDir, resolved.Version, paths.Persist(appName))
	envAddedPaths := expandEnvAddPath(resolved.EnvAddPath, versionDir)

	extraShims := readShimLog(shimLogPath)
	extraShortcuts := readShortcutLog(shortcutLogPath)
	if len(extraShortcuts) > 0 {
		Logf("%s: %d shortcut(s) created by the manifest's own script, tracked for uninstall", appName, len(extraShortcuts))
	}
	if len(extraShims) > 0 {
		Logf("%s: %d shim(s) created by the manifest's own script, tracked for uninstall", appName, len(extraShims))
	}

	rec := Record{
		Name:              appName,
		Version:           resolved.Version,
		Bucket:            bucketName,
		Architecture:      archKey,
		Description:       resolved.Description,
		Homepage:          resolved.Homepage,
		LicenseIdentifier: resolved.LicenseIdentifier,
		LicenseURL:        resolved.LicenseURL,
		URLs:              resolved.URLs,
		Hashes:            resolved.Hashes,
		Bin:               resolved.Bin,
		ExtractDirs:       resolved.ExtractDirs,
		ExtractTos:        resolved.ExtractTos,
		Shortcuts:         resolved.Shortcuts,
		Uninstaller:       resolved.Uninstaller,
		PreUninstall:      resolved.PreUninstall,
		PostUninstall:     resolved.PostUninstall,
		ExtraShims:        extraShims,
		ExtraShortcuts:    extraShortcuts,
		EnvSet:            envSet,
		EnvAddedPaths:     envAddedPaths,
		PSModuleName:      resolved.PSModuleName,
		Suggest:           resolved.Suggest,
		Depends:           functionalDepends(resolved.Depends, resolved),
		ManifestDigest:    resolved.Digest,
		InstalledAt:       time.Now().UTC(),
	}
	// Written pending: the rename below makes this version visible, but
	// the install is not finished until shims, shortcuts and env entries
	// exist. Anything that fails in between leaves a record that says so.
	rec.State = recordPending
	if err := writeRecord(staging, rec); err != nil {
		return Record{}, err
	}

	// Commit point: everything above must succeed before anything below
	// becomes visible.
	if err := os.Rename(staging, versionDir); err != nil {
		return Record{}, fmt.Errorf("finalize install of %s: %w", appName, err)
	}
	succeeded = true

	// Backstop for staging paths a manifest script persisted -- runs
	// before shims/shortcuts are created below, so anything goop itself
	// writes is already correct and only third-party leftovers get
	// touched.
	repairStagingPaths(appName, staging, versionDir)

	if err := relinkCurrent(appName, versionDir); err != nil {
		return Record{}, err
	}
	if err := createShims(appName, resolved.Bin); err != nil {
		return Record{}, err
	}
	if err := createShortcuts(appName, resolved.Shortcuts); err != nil {
		Logf("%s: shortcuts: %v", appName, err) // non-fatal: app is installed and usable either way
	}
	if resolved.PSModuleName != "" {
		if err := linkPSModule(appName, resolved.PSModuleName, versionDir); err != nil {
			Logf("%s: psmodule: %v", appName, err) // non-fatal, same tier as shortcuts
		}
	}
	applyEnv(appName, envSet, envAddedPaths)

	// Everything the install promises now exists, so the record can stop
	// hedging. Until this line a retry re-does the work; after it, the
	// "already installed" shortcut is telling the truth.
	rec.State = recordReady
	if err := writeRecord(versionDir, rec); err != nil {
		return Record{}, fmt.Errorf("finalize record for %s: %w", appName, err)
	}

	Logf("installed %s %s", appName, resolved.Version)
	showNotes(resolved.Notes, versionDir, paths.Persist(appName))
	return rec, nil
}

// showNotes prints a manifest's `notes` field after a successful
// install, mirroring real Scoop's own show_notes (lib/install.ps1) --
// often the only place a manifest documents a manual step it can't
// safely automate itself (e.g. extras/vscode.json's contTR-menu/
// file-association `reg import` commands, which touch the registry and
// so are left opt-in rather than run automatically; silently dropping
// this field, as goop did before, meant that guidance never reached
// the user at all). $original_dir has no goop-side distinction from
// $dir (real Scoop's own "app dir before any persist-related move"),
// so both substitute to the same value here.
func showNotes(notes []string, dir, persistDir string) {
	if len(notes) == 0 {
		return
	}
	Logf("Notes")
	Logf("-----")
	replacer := strings.NewReplacer("$dir", dir, "$original_dir", dir, "$persist_dir", persistDir)
	for _, line := range notes {
		Logf("%s", replacer.Replace(line))
	}
}

// runInstallHook executes a manifest installer/uninstaller entry: either
// a script (via the pwsh bridge) or a File to run directly with Args.
func runInstallHook(appName, label string, hook manifest.InstallHook, dir string, vars pwsh.Vars) error {
	if hook.Script != "" {
		Logf("%s: running %s.script", appName, label)
		out, err := pwsh.Run(hook.Script, vars)
		if strings.TrimSpace(out) != "" {
			Logf("%s", strings.TrimRight(out, "\n"))
		}
		if err != nil {
			return fmt.Errorf("%s: %s.script: %w", appName, label, err)
		}
		return nil
	}
	if hook.File != "" {
		target := filepath.Join(dir, filepath.FromSlash(hook.File))
		Logf("%s: running %s %v", appName, hook.File, hook.Args)
		cmdLine := goopshim.QuoteArg(target)
		for _, a := range hook.Args {
			cmdLine += " " + goopshim.QuoteArg(a)
		}
		exitCode, err := goopshim.Run(target, cmdLine)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", appName, label, err)
		}
		if exitCode != 0 {
			return fmt.Errorf("%s: %s %s exited with code %d", appName, label, hook.File, exitCode)
		}
	}
	return nil
}

// placeAsset downloads+verifies one manifest URL and gets its content
// onto disk under staging, either by extracting it (zip/7z/msi/tar) or,
// for anything else, copying the raw file in as fname -- always leaving
// fname reachable there, since pre_install/installer scripts frequently
// reference "$dir\$fname" themselves (e.g. to run a bundled installer or
// self-extract it their own way).
func placeAsset(appName, rawURL, expectedHash, extractDir, extractTo, staging string, innosetup bool) (fname string, err error) {
	assetURL, fragName := manifest.SplitURLFragment(rawURL)
	if fragName == "" {
		fragName = basenameWithoutQuery(assetURL)
	}

	Logf("%s: downloading %s", appName, assetURL)
	local, err := downloader.Get(paths.Cache(), assetURL, fragName, expectedHash)
	if err != nil {
		return "", err
	}

	destDir := staging
	if extractTo != "" {
		destDir = filepath.Join(staging, filepath.FromSlash(extractTo))
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return "", err
		}
	}

	ext := archiveExt(fragName)
	switch {
	case ext == ".zip":
		err = archive.ExtractZip(local, destDir, extractDir)
		if err != nil && archive.IsUnsupportedCompression(err) {
			// A minority of real-world zips use a method Go's stdlib
			// zip reader doesn't implement (e.g. Deflate64); 7z.exe
			// handles them, so fall back to it rather than failing.
			Logf("%s: zip uses a compression method Go can't read natively, retrying via 7z", appName)
			err = extractVia7z(appName, local, destDir, extractDir)
		}
	case ext == ".7z":
		err = extractVia7z(appName, local, destDir, extractDir)
	case ext == ".msi":
		err = extractViaMsi(appName, local, destDir, extractDir)
	case ext == ".tar.gz" || ext == ".tgz":
		err = archive.ExtractTarGz(local, destDir, extractDir)
	case ext == ".tar":
		err = archive.ExtractTar(local, destDir, extractDir)
	case ext == ".exe" && innosetup:
		// Real Scoop only ever routes a downloaded .exe through Inno
		// Setup extraction when the manifest sets "innosetup": true --
		// same gate here. Without it, a .exe is left as-is (an
		// installer.script may run it directly, or it's not
		// Inno-based at all).
		err = extractViaInno(appName, local, destDir, extractDir)
	case sevenZipExts[ext]:
		// Formats goop has no dedicated native/msi path for, but 7z.exe
		// natively supports -- confirmed real usage includes .tar.xz
		// (main/curl.json, main/msys2.json, main/haskell.json, ...) and
		// .rar (extras/garbro.json, extras/dxwnd.json, ...), which
		// previously fell to the raw-copy default below and silently
		// produced a broken, unextracted install.
		err = extractVia7z(appName, local, destDir, extractDir)
	default:
		err = copyFile(local, filepath.Join(destDir, fragName))
	}
	if err != nil {
		return "", err
	}
	return fragName, nil
}

// basenameWithoutQuery derives a filename from a URL that has no
// manifest-provided #/rename fragment, using the URL's path -- not
// filepath.Base(rawURL) directly, which (treating the whole URL as a
// filesystem path) would include a trailing "?query=string" as part of
// the "filename" for a URL like ".../download?channel=release", and "?"
// is illegal in a Windows filename.
func basenameWithoutQuery(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Path != "" {
		return filepath.Base(u.Path)
	}
	return filepath.Base(rawURL)
}

// archiveExt returns the extension used to pick an extraction strategy,
// treating compound extensions like ".tar.gz" as one unit rather than
// filepath.Ext's ".gz".
func archiveExt(name string) string {
	lower := strings.ToLower(name)
	for _, compound := range []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst"} {
		if strings.HasSuffix(lower, compound) {
			return compound
		}
	}
	return filepath.Ext(lower)
}

// sevenZipExts are archive formats goop has no dedicated native/msi
// extraction case for, but 7z.exe natively supports -- the same set
// real Scoop's own Test-7zipRequirement recognizes (lib/depends.ps1's
// regex: `\.(001|7z|bz(ip)?2?|gz|img|iso|lzma|lzh|nupkg|rar|tar|
// t[abgpx]z2?|t?zst|xz)(\.[^\d.]+)?$`), minus .7z/.tar/.tar.gz/.tgz,
// which already have their own cases above.
var sevenZipExts = map[string]bool{
	".001": true, ".bz": true, ".bzip": true, ".bz2": true, ".bzip2": true,
	".gz": true, ".img": true, ".iso": true, ".lzma": true, ".lzh": true,
	".nupkg": true, ".rar": true, ".xz": true, ".zst": true, ".tzst": true,
	".taz": true, ".tbz": true, ".tbz2": true, ".tpz": true, ".tpz2": true,
	".txz": true, ".txz2": true, ".tgz2": true,
	".tar.bz2": true, ".tar.xz": true, ".tar.zst": true,
}

// extractShimVars is the minimal Vars every extract* helper below needs:
// ShimsDir so a helper tool goop itself installed (7zip, innounp, ...)
// resolves via `Get-Command`/PATH inside the delegated script regardless
// of whether the user's own system PATH happens to include goop's shims
// dir -- confirmed necessary on a real machine where it didn't.
func extractShimVars(destDir string) pwsh.Vars {
	return pwsh.Vars{Dir: destDir, ShimsDir: paths.Shims()}
}

func extractVia7z(appName, src, destDir, extractDir string) error {
	Logf("%s: extracting (7z) %s", appName, filepath.Base(src))
	script := fmt.Sprintf("Expand-7zipArchive -Path %s -DestinationPath %s", psArg(src), psArg(destDir))
	if extractDir != "" {
		script += " -ExtractDir " + psArg(extractDir)
	}
	out, err := pwsh.Run(script, extractShimVars(destDir))
	if err != nil {
		return fmt.Errorf("%s: %w", appName, err)
	}
	_ = out
	return nil
}

func extractViaInno(appName, src, destDir, extractDir string) error {
	Logf("%s: extracting (Inno Setup) %s", appName, filepath.Base(src))
	script := fmt.Sprintf("Expand-InnoArchive -Path %s -DestinationPath %s", psArg(src), psArg(destDir))
	if extractDir != "" {
		script += " -ExtractDir " + psArg(extractDir)
	}
	_, err := pwsh.Run(script, extractShimVars(destDir))
	if err != nil {
		return fmt.Errorf("%s: %w", appName, err)
	}
	return nil
}

func extractViaMsi(appName, src, destDir, extractDir string) error {
	Logf("%s: extracting (msiexec /a) %s", appName, filepath.Base(src))
	script := fmt.Sprintf("Expand-MsiArchive -Path %s -DestinationPath %s", psArg(src), psArg(destDir))
	if extractDir != "" {
		script += " -ExtractDir " + psArg(extractDir)
	}
	_, err := pwsh.Run(script, extractShimVars(destDir))
	if err != nil {
		return fmt.Errorf("%s: %w", appName, err)
	}
	return nil
}

func psArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.ReadFrom(in); err != nil {
		return err
	}
	return nil
}

func writeRecord(versionDir string, rec Record) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(versionDir, recordFileName), data, 0o644)
}

func readRecord(versionDir string) (Record, bool) {
	data, err := os.ReadFile(filepath.Join(versionDir, recordFileName))
	if err != nil {
		return Record{}, false
	}
	var rec Record
	if json.Unmarshal(data, &rec) != nil {
		return Record{}, false
	}
	return rec, true
}

// relinkCurrent repoints <app>/current at versionDir (TR-30: a versioned
// directory plus a junction, so an in-use binary is never overwritten in
// place).
func relinkCurrent(appName, versionDir string) error {
	link := paths.AppCurrent(appName)
	if _, err := os.Lstat(link); err == nil {
		out, err := exec.Command("cmd", "/c", "rmdir", link).CombinedOutput()
		if err != nil {
			return fmt.Errorf("remove existing junction %s: %w\n%s", link, err, out)
		}
	}
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, versionDir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("create junction %s -> %s: %w\n%s", link, versionDir, err, out)
	}
	return nil
}

// createShims materializes one shim per bin entry, hard-linked from a
// single embedded shim binary (TR-24), pointed through the `current`
// junction so an upgrade never needs to touch existing shims.
func createShims(appName string, bins []manifest.BinEntry) error {
	if len(bins) == 0 {
		return nil
	}
	if err := ensureShimMaster(); err != nil {
		return err
	}
	for _, b := range bins {
		// A manifest `bin` entry is relative to the app directory, but a
		// rebuilt script-created shim (Reset) carries the absolute target
		// it was recorded with -- joining that onto AppCurrent would
		// produce nonsense like "C:\apps\x\current\C:\apps\x\bin\y.exe".
		targetPath := filepath.FromSlash(b.Exe)
		if !filepath.IsAbs(targetPath) {
			targetPath = filepath.Join(paths.AppCurrent(appName), targetPath)
		}

		// Checked before anything is written. A shim whose target does not
		// exist is worse than no shim: it looks installed, `goop status`
		// reports no drift, and the failure only surfaces when someone runs
		// the command. Every manifest hook has run by this point, so a file
		// that is not there never will be -- usually a `bin` that does not
		// match what the archive actually contains, or an extract_dir that
		// put things somewhere else.
		if _, err := os.Stat(targetPath); err != nil {
			return fmt.Errorf("shim %s would point at a missing target %s -- the manifest's bin entry %q does not match what was installed", b.Name, targetPath, b.Exe)
		}

		shimExe := filepath.Join(paths.Shims(), b.Name+".exe")
		os.Remove(shimExe) // fine if it doesn't exist; reinstall/upgrade case
		if err := os.Link(paths.ShimMaster(), shimExe); err != nil {
			return fmt.Errorf("create shim %s: %w", b.Name, err)
		}

		content := fmt.Sprintf("path = %q\n", targetPath)
		if b.Args != "" {
			content += fmt.Sprintf("args = %q\n", b.Args)
		}
		sidecar := filepath.Join(paths.Shims(), b.Name+".shim")
		if err := os.WriteFile(sidecar, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write sidecar for %s: %w", b.Name, err)
		}
	}
	return nil
}

// readShimLog returns the deduplicated, sorted list of shim names the
// `shim` compat function appended to path (one per line) while a
// script ran with it as $goop_shim_log -- see the ShimLogPath field
// comment on pwsh.Vars.
func readShimLog(path string) []ExtraShim {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	// Windows PowerShell 5.1's `Add-Content -Encoding utf8` (used by the
	// shim polyfill) always writes a UTF-8 BOM on the first write to a
	// new/empty file -- confirmed against a real run, where it mangled
	// the first recorded shim name with three stray leading bytes.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	seen := map[string]bool{}
	var out []ExtraShim
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// "<name>\t<target>"; a tab-less line is a pre-target record.
		name, target, _ := strings.Cut(line, "\t")
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, ExtraShim{Name: name, Path: strings.TrimSpace(target)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// expandEnvSet substitutes the manifest-script-style placeholders real
// manifests use in env_set values ($dir, $version, $persist_dir) with
// their concrete resolved values.
func expandEnvSet(m map[string]string, dir, version, persistDir string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	replacer := strings.NewReplacer("$dir", dir, "$version", version, "$persist_dir", persistDir)
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = replacer.Replace(v)
	}
	return out
}

// expandEnvAddPath resolves env_add_path entries (paths relative to the
// app dir, e.g. ".", "bin") to absolute paths under dir.
func expandEnvAddPath(entries []string, dir string) []string {
	if len(entries) == 0 {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, filepath.Clean(filepath.Join(dir, filepath.FromSlash(e))))
	}
	return out
}

// applyEnv sets env_set variables and env_add_path entries in
// HKCU\Environment. Non-fatal on failure -- the app is already fully
// installed and usable via its shims either way, same tier as shortcuts.
func applyEnv(appName string, envSet map[string]string, envAddedPaths []string) {
	for name, val := range envSet {
		if err := envvars.Set(name, val); err != nil {
			Logf("%s: env_set %s: %v", appName, name, err)
		}
	}
	for _, p := range envAddedPaths {
		if err := envvars.AddToPath(p); err != nil {
			Logf("%s: env_add_path %s: %v", appName, p, err)
		}
	}
	if len(envSet) > 0 || len(envAddedPaths) > 0 {
		Logf("%s: environment updated (already-open shells need restarting to see it)", appName)
	}
}

// revertEnv unsets/removes exactly what applyEnv recorded having set, so
// uninstall leaves no environment residue (NR-02).
func revertEnv(appName string, envSet map[string]string, envAddedPaths []string) {
	for name := range envSet {
		if err := envvars.Unset(name); err != nil {
			Logf("%s: revert env_set %s: %v", appName, name, err)
			continue
		}
		Logf("%s: removing %s from your environment", appName, name)
	}
	for _, p := range envAddedPaths {
		if err := envvars.RemoveFromPath(p); err != nil {
			Logf("%s: revert env_add_path %s: %v", appName, p, err)
			continue
		}
		Logf("%s: removing %s from your path", appName, friendlyPath(p))
	}
}

// shimMasterMu guards ensureShimMaster against concurrent installs (A1
// parallelizes installs across apps) racing to write the same shared
// master shim file at once.
var shimMasterMu sync.Mutex

func ensureShimMaster() error {
	shimMasterMu.Lock()
	defer shimMasterMu.Unlock()

	if len(shimbin.Bytes) == 0 {
		return fmt.Errorf("embedded shim binary is empty; run scripts/build.ps1 to build cmd/shim first")
	}
	master := paths.ShimMaster()
	// Compare content, not length: a rebuilt shim binary routinely lands
	// on the same size, and a length-only check then left every machine
	// running the old code forever -- a real fix to the sidecar parser
	// shipped in goop.exe but never reached the shims themselves.
	stale := true
	if data, err := os.ReadFile(master); err == nil {
		stale = !bytes.Equal(data, shimbin.Bytes)
	}
	if !stale {
		return nil
	}
	masterExisted := false
	if _, err := os.Stat(master); err == nil {
		masterExisted = true
	}
	if err := os.MkdirAll(paths.Shims(), 0o755); err != nil {
		return err
	}
	// Write via a temp file + atomic rename so a concurrent os.Link
	// (from createShims, running for another app right now) never sees
	// a partially-written master file.
	tmp := master + ".tmp"
	if err := os.WriteFile(tmp, shimbin.Bytes, 0o755); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, master); err != nil {
		os.Remove(tmp)
		return err
	}
	// Every <name>.exe in shims/ is a hardlink to the master. The
	// rename above replaced the file rather than its contents, so those
	// links still resolve to the *old* inode and would keep running the
	// old binary. Re-link them onto the new master, or a shim fix never
	// reaches an existing install.
	if masterExisted {
		relinkShims(master)
	}
	return nil
}

// relinkShims points every existing shim executable at the freshly
// written master. Best-effort per entry: a shim currently running is
// locked and simply stays on the old binary until next time, which is
// no worse than before.
func relinkShims(master string) {
	entries, err := os.ReadDir(paths.Shims())
	if err != nil {
		return
	}
	relinked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".exe" || name == filepath.Base(master) {
			continue
		}
		p := filepath.Join(paths.Shims(), name)
		if _, err := os.Stat(strings.TrimSuffix(p, ".exe") + ".shim"); err != nil {
			continue // not one of ours: no sidecar beside it
		}
		if err := os.Remove(p); err != nil {
			continue
		}
		if err := os.Link(master, p); err != nil {
			continue
		}
		relinked++
	}
	if relinked > 0 {
		Logf("refreshed %d shim(s) onto the updated shim binary", relinked)
	}
}

// readShortcutLog parses the shortcut journal startmenu_shortcut writes
// ("<name>\t<target>\t<args>\t<icon>" per line), same shape and BOM
// handling as readShimLog.
func readShortcutLog(path string) []ExtraShortcut {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	seen := map[string]bool{}
	var out []ExtraShortcut
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		name := strings.TrimSpace(parts[0])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		es := ExtraShortcut{Name: name}
		if len(parts) > 1 {
			es.Target = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			es.Args = strings.TrimSpace(parts[2])
		}
		if len(parts) > 3 {
			es.Icon = strings.TrimSpace(parts[3])
		}
		out = append(out, es)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
