package installer

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/paths"
	"github.com/TanguyBaudoin/goop/internal/profile"
	"github.com/TanguyBaudoin/goop/internal/pwsh"
)

// friendlyPath replaces the user's home directory prefix with "~\",
// matching Scoop's own friendly_path display convention -- used so
// uninstall output showing real paths (shortcuts, the current link)
// reads the same way Scoop's own does.
func friendlyPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if len(p) > len(home) && strings.EqualFold(p[:len(home)], home) {
		return "~" + p[len(home):]
	}
	return p
}

// Uninstall removes every version of appName, its shims, shortcuts, and
// its `current` junction, leaving no residue outside goop's own
// directories (NR-02). Its persist store is intentionally left in place
// (NR-04's data survives an ordinary uninstall, matching Scoop).
// uninstaller hooks are run best-effort: a failure there is logged, not
// fatal, since uninstall must always be able to get a user unstuck.
//
// Safety net: if appName is still referenced by a profile other than
// the currently active one, Uninstall refuses unless force is true --
// removing it from the profile you're actually working in is fine (that
// IS what you're asking for), but silently breaking some other profile
// that still needs it isn't. On success, appName's membership is
// removed from every profile that referenced it (there's nothing left
// installed for them to reference anymore).
// dependentsOf returns the names of every other installed app whose
// own recorded Depends includes appName. Matching is on the bare name
// via manifest.ParseSpec, since real `depends` values are frequently
// bucket-qualified ("extras/keepass") or constrained ("foo@1.2.3") --
// the same normalization installDependencies already applies.
// containsString guards the cascade against recursing forever. Install
// -time cycle detection already makes a genuine `depends` cycle among
// installed apps impossible, so this is purely defensive -- cheap
// insurance against a hand-edited goop-install.json.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func dependentsOf(appName string) ([]string, error) {
	records, err := List()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rec := range records {
		if rec.Name == appName {
			continue
		}
		for _, d := range rec.Depends {
			if manifest.ParseSpec(d).Name == appName {
				out = append(out, rec.Name)
				break
			}
		}
	}
	return out, nil
}

// ErrNotInstalled reports that an app is not present. Callers removing
// several apps at once -- the cascade below, and UninstallAll -- treat it
// as success rather than failure: a package that another removal already
// took away has still ended up in the desired state. Only a direct,
// single-app `goop uninstall foo` surfaces it as an error, where naming
// a package that was never installed really is a mistake worth reporting.
var ErrNotInstalled = errors.New("not installed")

func Uninstall(appName string, force bool) error {
	return uninstallRec(appName, force, nil)
}

func uninstallRec(appName string, force bool, cascadeStack []string) error {
	appDir := paths.App(appName)
	if _, err := os.Stat(appDir); err != nil {
		return fmt.Errorf("%s is %w", appName, ErrNotInstalled)
	}

	if !force {
		// More than one claimant is the case worth stopping for: the
		// README always described it as "if cmake belongs to another
		// profile too". It used to be implemented as "any profile other
		// than the active one", which made the warning depend on a
		// setting made days earlier -- and warned about a package with a
		// single owner whenever you happened to be somewhere else.
		if containing, err := profile.ContainingProfiles(appName); err == nil {
			var others []string
			if len(containing) > 1 {
				others = containing
			}
			if len(others) > 0 {
				return fmt.Errorf(
					"%s is still referenced by profile(s) %s -- pass --force to remove it anyway, or `goop profile remove <profile> %s` first",
					appName, strings.Join(others, ", "), appName)
			}
		}
	}

	if rec, ok := readCurrentRecord(appName); ok {
		Logf("uninstalling %s (%s)", appName, rec.Version)
	} else {
		Logf("uninstalling %s", appName)
	}

	// Cascade: an app that declares `depends` on appName is useless once
	// appName is gone (a KeePass plugin without KeePass, ack without
	// perl), so it comes out too. No real-Scoop equivalent -- its own
	// uninstall never looks at `depends` at all -- this is goop's own
	// behavior. Only manifest-declared depends are followed, never the
	// implicit extraction helpers (Record.Depends excludes those): 7zip
	// unpacked curl once, it isn't needed to keep curl working.
	//
	// Placed after appName's own preconditions (installed, profile
	// membership) so a blocked target touches nothing, and each
	// dependent goes through this same function recursively -- its own
	// profile check, running-process check, hooks and profile cleanup --
	// so a protected dependent aborts the whole cascade cleanly instead
	// of leaving a half-removed state. Transitive chains fall out of the
	// recursion: for A->B->C, removing C finds B, which finds A.
	if !containsString(cascadeStack, appName) {
		dependents, err := dependentsOf(appName)
		if err == nil && len(dependents) > 0 {
			Logf("%s: also removing dependent package(s): %s", appName, strings.Join(dependents, ", "))
			for _, dep := range dependents {
				err := uninstallRec(dep, force, append(append([]string{}, cascadeStack...), appName))
				// Already gone is not a failure: under UninstallAll the
				// dependent may be removed by its own concurrent goroutine
				// between dependentsOf listing it and this call. Treating
				// that as an error aborted the parent removal and left the
				// dependency itself installed -- reproduced 5/5 on a tree of
				// four packages sharing one dependency.
				if err != nil && !errors.Is(err, ErrNotInstalled) {
					return fmt.Errorf("%s: removing dependent %s: %w", appName, dep, err)
				}
			}
		}
	}

	// Mirrors real Scoop's own test_running_process (lib/install.ps1):
	// refuse before touching anything if the app is still running,
	// rather than failing deep into a partial removal -- confirmed
	// against a real GUI app (Electron-based) where, without this
	// check, goop had already deleted its shims/shortcuts/current link
	// by the time os.RemoveAll hit "Access is denied" on a file the
	// running process still had open, leaving it worse off (no shim, no
	// shortcut, but still-running and undeletable) than a clean refusal
	// would have. A query failure (e.g. no pwsh/powershell on PATH)
	// only warns and proceeds -- this is a defensive improvement, not a
	// hard requirement, and blocking every uninstall over an
	// unrelated environment problem would be worse than the gap it fixes.
	if running, err := pwsh.RunningProcessesUnder(appDir); err != nil {
		Logf("%s: couldn't check for running processes, proceeding anyway: %v", appName, err)
	} else if len(running) > 0 {
		if !force {
			msg := fmt.Sprintf("%s is still running -- close it and try again (or pass --force):", appName)
			for _, p := range running {
				msg += "\n  " + p
			}
			return fmt.Errorf("%s", msg)
		}
		Logf("%s: still running, but --force was passed; removing anyway:", appName)
		for _, p := range running {
			Logf("  %s", p)
		}
	}

	// Normally one entry per installed version, each holding its own
	// record. A self-updating app can leave the record only inside
	// `current` (see readCurrentRecord) -- without the fallback below,
	// no record is found at all, the loop body never runs, and the
	// app's shims/shortcuts/env survive the uninstall as orphans. That
	// really happened: a stale zen.shim left pointing at a `current`
	// that no longer existed then broke the *next* install's
	// post_install, which invoked `zen` by name.
	versionDirs := []string{}
	entries, _ := os.ReadDir(appDir)
	for _, e := range entries {
		if e.IsDir() && e.Name() != "current" {
			versionDirs = append(versionDirs, filepath.Join(appDir, e.Name()))
		}
	}
	anyRecord := false
	for _, d := range versionDirs {
		if _, ok := readRecord(d); ok {
			anyRecord = true
			break
		}
	}
	if !anyRecord {
		if _, ok := readRecord(paths.AppCurrent(appName)); ok {
			versionDirs = append(versionDirs, paths.AppCurrent(appName))
		}
	}

	seenBin := map[string]bool{}
	seenShortcut := map[string]bool{}
	for _, versionDir := range versionDirs {
		rec, ok := readRecord(versionDir)
		if !ok {
			continue
		}

		vars := pwsh.Vars{
			Dir:        versionDir,
			Version:    rec.Version,
			PersistDir: paths.Persist(appName),
			Cmd:        "uninstall",
			ShimsDir:   paths.Shims(),
			ShimMaster: paths.ShimMaster(),
			AppsDir:    paths.Apps(),
			CacheDir:   paths.Cache(),
		}

		// Real Scoop runs pre_uninstall before touching anything else --
		// same tolerance as the uninstaller hook below: a failure here
		// mustn't block getting the user unstuck.
		if rec.PreUninstall != "" {
			Logf("%s: running pre_uninstall", appName)
			if out, err := pwsh.Run(rec.PreUninstall, vars); err != nil {
				Logf("%s: pre_uninstall failed, continuing removal anyway: %v", appName, err)
			} else if strings.TrimSpace(out) != "" {
				Logf("%s", strings.TrimRight(out, "\n"))
			}
		}

		if !rec.Uninstaller.IsZero() {
			if err := runInstallHook(appName, "uninstaller", rec.Uninstaller, versionDir, vars); err != nil {
				Logf("%s: uninstaller hook failed, continuing removal anyway: %v", appName, err)
			}
		}

		shimNames := make([]string, 0, len(rec.Bin)+len(rec.ExtraShims))
		for _, b := range rec.Bin {
			shimNames = append(shimNames, b.Name)
		}
		// ExtraShims: names a hook script created by calling the `shim`
		// compat function directly, bypassing the manifest's `bin` field
		// entirely -- installResolved records them precisely (via the
		// script's own $goop_shim_log writes) so they don't end up
		// orphaned here.
		for _, es := range rec.ExtraShims {
			shimNames = append(shimNames, es.Name)
		}

		for _, name := range shimNames {
			if seenBin[name] {
				continue
			}
			seenBin[name] = true
			Logf("%s: removing shim %s.shim", appName, name)
			os.Remove(filepath.Join(paths.Shims(), name+".shim"))
			Logf("%s: removing shim %s.exe", appName, name)
			os.Remove(filepath.Join(paths.Shims(), name+".exe"))
		}

		for _, s := range rec.Shortcuts {
			if seenShortcut[s.Name] {
				continue
			}
			seenShortcut[s.Name] = true
			p := shortcutPath(s.Name)
			Logf("%s: removing shortcut %s", appName, friendlyPath(p))
			os.Remove(p)
		}

		// Shortcuts a manifest script created itself (startmenu_shortcut).
		// Invisible to rec.Shortcuts, so without this they outlive the
		// app as .lnk files pointing at a deleted directory -- exactly
		// what NR-02 forbids, and what freecad left behind.
		for _, es := range rec.ExtraShortcuts {
			if seenShortcut[es.Name] {
				continue
			}
			seenShortcut[es.Name] = true
			p := shortcutPath(es.Name)
			Logf("%s: removing shortcut %s", appName, friendlyPath(p))
			os.Remove(p)
		}

		if rec.PSModuleName != "" {
			unlinkPSModule(appName, rec.PSModuleName)
		}

		revertEnv(appName, rec.EnvSet, rec.EnvAddedPaths)

		// Runs last, while versionDir still exists (still before the
		// directory removal below, which happens once for every version
		// after this loop) -- same reasoning as the pre_uninstall/
		// uninstaller tolerance: logged, not fatal.
		if rec.PostUninstall != "" {
			Logf("%s: running post_uninstall", appName)
			if out, err := pwsh.Run(rec.PostUninstall, vars); err != nil {
				Logf("%s: post_uninstall failed, continuing removal anyway: %v", appName, err)
			} else if strings.TrimSpace(out) != "" {
				Logf("%s", strings.TrimRight(out, "\n"))
			}
		}
	}

	link := paths.AppCurrent(appName)
	if info, err := os.Lstat(link); err == nil {
		// `current` is normally a junction, but a self-updating app can
		// replace it with a real directory holding the whole app: Zen
		// Browser's own updater does exactly this (confirmed on a real
		// install -- current/ held 57 entries including the app and
		// goop-install.json, while the version dir was left with just
		// updater.exe). `rmdir` without /S then fails with "The
		// directory is not empty" (exit 145) and the app becomes
		// impossible to remove through goop at all, so fall back to a
		// recursive delete when it isn't actually a link.
		if info.Mode()&os.ModeSymlink != 0 {
			Logf("%s: unlinking %s", appName, friendlyPath(link))
			if out, err := exec.Command("cmd", "/c", "rmdir", link).CombinedOutput(); err != nil {
				return fmt.Errorf("remove junction %s: %w\n%s", link, err, out)
			}
		} else {
			Logf("%s: removing %s (a real directory, not a link -- app replaced it itself)", appName, friendlyPath(link))
			if err := os.RemoveAll(link); err != nil {
				return fmt.Errorf("remove %s: %w", link, err)
			}
		}
	}

	Logf("%s: removing files", appName)
	if err := os.RemoveAll(appDir); err != nil {
		return fmt.Errorf("remove %s: %w", appDir, err)
	}

	if containing, err := profile.ContainingProfiles(appName); err == nil {
		for _, p := range containing {
			if err := profile.Remove(p, appName); err != nil {
				Logf("%s: profile cleanup (%s): %v", appName, p, err)
			}
		}
	}

	Logf("uninstalled %s", appName)
	return nil
}

// List returns every installed app's current record (FR-04). An app
// whose `current` junction is missing or unreadable is still reported,
// with Version set to a marker string, so `list` surfaces breakage
// instead of silently omitting it.
func List() ([]Record, error) {
	entries, err := os.ReadDir(paths.Apps())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []Record
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if rec, ok := readCurrentRecord(e.Name()); ok {
			out = append(out, rec)
			continue
		}
		out = append(out, Record{Name: e.Name(), Version: "(broken: no readable current record)"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// readCurrentRecord resolves <app>/current to find which version is
// active, then reads that version's record from goop's own local
// bookkeeping directory -- never by reading through the junction
// itself. A normal install's `current` junctions at goop's own version
// directory (which has the record either way), but an imported app's
// junctions straight at Scoop's real directory, which never gets a
// goop-install.json written into it (import.go's whole point is to
// never write into the Scoop tree).
// Info returns appName's full install record -- its resolved URLs,
// hashes, bucket, architecture, and install time, i.e. everything
// needed to answer "where did this actually come from" (FR-42
// provenance traceability).
func Info(appName string) (Record, error) {
	rec, ok := readCurrentRecord(appName)
	if !ok {
		return Record{}, fmt.Errorf("%s is not installed (or its current record is unreadable)", appName)
	}
	return rec, nil
}

func readCurrentRecord(appName string) (Record, bool) {
	current := paths.AppCurrent(appName)
	if target, err := os.Readlink(current); err == nil {
		return readRecord(paths.AppVersion(appName, filepath.Base(target)))
	}
	// Readlink fails when `current` isn't a link at all -- a
	// self-updating app (Zen Browser, confirmed on a real install) can
	// replace the junction with a real directory and move the app,
	// record included, into it. Reporting that as "broken: no readable
	// current record" hid a perfectly readable record, so read it
	// straight out of the directory before giving up.
	return readRecord(current)
}

// UninstallPlan is everything `goop uninstall <name>` would remove.
//
// Uninstall cascades to the packages that declare the target as a
// dependency, which is the part that surprises people: asking to remove
// one package can remove three. Working it out first makes that
// visible before anything is deleted.
type UninstallPlan struct {
	Requested []string
	Cascaded  []string // pulled in because they depend on something requested
	Missing   []string // asked for but not installed
}

// Total is how many packages would actually be removed.
func (p UninstallPlan) Total() int { return len(p.Requested) + len(p.Cascaded) }

// PlanUninstall works out what removing names would take with it,
// touching nothing.
func PlanUninstall(names []string) (UninstallPlan, error) {
	var p UninstallPlan

	// Everything asked for first, so a package that is both named and
	// reachable as a dependent is labelled "asked for" rather than
	// "depends on one of them" -- which was wrong, and confusing in the
	// one place the label has to be trusted.
	requested := map[string]bool{}
	for _, name := range names {
		if requested[name] {
			continue
		}
		if _, ok := readCurrentRecord(name); !ok {
			p.Missing = append(p.Missing, name)
			continue
		}
		requested[name] = true
		p.Requested = append(p.Requested, name)
	}

	seen := map[string]bool{}
	var walk func(string) error
	walk = func(name string) error {
		if seen[name] {
			return nil
		}
		seen[name] = true
		deps, err := dependentsOf(name)
		if err != nil {
			return err
		}
		for _, d := range deps {
			if !requested[d] && !seen[d] {
				p.Cascaded = append(p.Cascaded, d)
			}
			if err := walk(d); err != nil {
				return err
			}
		}
		return nil
	}
	for _, name := range p.Requested {
		if err := walk(name); err != nil {
			return UninstallPlan{}, err
		}
	}

	sort.Strings(p.Requested)
	sort.Strings(p.Cascaded)
	sort.Strings(p.Missing)
	return p, nil
}

// IsInstalled reports whether appName has a finished install.
//
// "Finished" matters: a receipt committed by the rename with shims that
// never got created describes a package that does not work, and treating
// it as present is what let a broken install hide.
func IsInstalled(appName string) bool {
	rec, ok := readCurrentRecord(appName)
	return ok && rec.Ready()
}
