package installer

import (
	"fmt"
	"strings"

	"goop/internal/manifest"
	"goop/internal/paths"
	"goop/internal/pwsh"
)

// Reset re-applies everything an install wires up *around* an app's
// files -- the `current` junction, shims, Start Menu shortcuts, the
// PowerShell module link and environment variables -- without
// downloading or extracting anything. Mirrors real Scoop's own
// `scoop reset` (libexec/scoop-reset.ps1).
//
// It repairs the states this session kept running into by hand: a shim
// left orphaned or pointing at a staging path, a shortcut deleted or
// overwritten by another app, a `current` junction a self-updating app
// replaced with a real directory. It also refreshes the shared shim
// binary (ensureShimMaster), so a goop upgrade reaches existing shims
// even on a machine where nothing needs installing.
//
// The app's own files are never touched: reset rebuilds the wiring, it
// does not reinstall.
func Reset(appName string) error {
	rec, ok := readCurrentRecord(appName)
	if !ok {
		return fmt.Errorf("%s is not installed (or its current record is unreadable)", appName)
	}

	// Same guard Scoop applies: rewiring shims under a running app
	// would fail on locked files partway through.
	appDir := paths.App(appName)
	if running, err := pwsh.RunningProcessesUnder(appDir); err == nil && len(running) > 0 {
		return fmt.Errorf("%s is still running -- close it and try again:\n  %s",
			appName, strings.Join(running, "\n  "))
	}

	versionDir := paths.AppVersion(appName, rec.Version)
	Logf("resetting %s (%s)", appName, rec.Version)

	if err := ensureShimMaster(); err != nil {
		Logf("%s: shim binary: %v", appName, err)
	}
	if err := relinkCurrent(appName, versionDir); err != nil {
		return err
	}
	if err := createShims(appName, rec.Bin); err != nil {
		return err
	}
	if err := createShortcuts(appName, rec.Shortcuts); err != nil {
		Logf("%s: shortcuts: %v", appName, err) // non-fatal, same tier as install
	}
	// Shims a manifest script created itself: rebuilt from the recorded
	// target rather than by re-running the script, which is not safe to
	// repeat (real post_install scripts create browser profiles and
	// import registry keys). Records written before targets were
	// captured have only a name -- nothing to rebuild from, so they are
	// reported instead of silently skipped.
	var unrecoverable []string
	for _, es := range rec.ExtraShims {
		if es.Path == "" {
			unrecoverable = append(unrecoverable, es.Name)
			continue
		}
		if err := createShims(appName, []manifest.BinEntry{{Exe: es.Path, Name: es.Name}}); err != nil {
			Logf("%s: shim %s: %v", appName, es.Name, err)
		}
	}
	if len(unrecoverable) > 0 {
		Logf("%s: %d script-created shim(s) predate target tracking and can't be rebuilt (%s) -- reinstall to restore them",
			appName, len(unrecoverable), strings.Join(unrecoverable, ", "))
	}

	for _, es := range rec.ExtraShortcuts {
		if es.Target == "" {
			continue // predates target tracking; nothing to rebuild from
		}
		sc := manifest.ShortcutEntry{Exe: es.Target, Name: es.Name, Args: es.Args, Icon: es.Icon}
		if err := createShortcuts(appName, []manifest.ShortcutEntry{sc}); err != nil {
			Logf("%s: shortcut %s: %v", appName, es.Name, err)
		}
	}

	if rec.PSModuleName != "" {
		if err := linkPSModule(appName, rec.PSModuleName, versionDir); err != nil {
			Logf("%s: psmodule: %v", appName, err)
		}
	}

	// Reverse first, then re-apply: env_add_path entries are recorded
	// as already-expanded values, so a stale one from an older version
	// would otherwise linger alongside the current one.
	revertEnv(appName, rec.EnvSet, rec.EnvAddedPaths)
	applyEnv(appName, rec.EnvSet, rec.EnvAddedPaths)

	Logf("reset %s", appName)
	return nil
}

// ResetAll resets every installed app, reporting per-app errors.
func ResetAll() map[string]error {
	errs := map[string]error{}
	records, err := List()
	if err != nil {
		return map[string]error{"": err}
	}
	for _, r := range records {
		if strings.HasPrefix(r.Version, "(broken") {
			continue
		}
		if err := Reset(r.Name); err != nil {
			errs[r.Name] = err
		}
	}
	return errs
}
