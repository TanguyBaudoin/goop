package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/paths"
	"github.com/TanguyBaudoin/goop/internal/pwsh"
	"github.com/TanguyBaudoin/goop/internal/vercmp"
)

// InstalledVersion is one version of an app present on disk.
type InstalledVersion struct {
	Version string
	Current bool
}

// InstalledVersions lists the versions of appName that are installed,
// newest first, saying which one `current` points at.
//
// A version directory without a receipt is left out: `goop use` would
// have nothing to rewire the shims and environment from, so offering it
// would be offering a switch that cannot be completed. `goop doctor`
// reports those separately.
func InstalledVersions(appName string) ([]InstalledVersion, error) {
	appDir := paths.App(appName)
	entries, err := os.ReadDir(appDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s is %w", appName, ErrNotInstalled)
		}
		return nil, err
	}

	activeDir, _ := currentRecordDir(appName)
	var out []InstalledVersion
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "current" || strings.HasSuffix(e.Name(), ".partial") {
			continue
		}
		dir := filepath.Join(appDir, e.Name())
		if isReparsePoint(dir) {
			continue // `current` under another name, not a version of its own
		}
		if _, err := os.Stat(filepath.Join(dir, recordFileName)); err != nil {
			continue
		}
		out = append(out, InstalledVersion{
			Version: e.Name(),
			Current: activeDir != "" && sameDir(dir, activeDir),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return vercmp.Compare(out[i].Version, out[j].Version) > 0
	})
	return out, nil
}

// Use points an app at a version that is already installed.
//
// REQUIREMENTS NR-03 claims "multiple versions side by side, version
// switching" as Met, and the first half was true: an install keeps every
// version in its own directory with its own receipt, and `current` is a
// junction. The second half had no command behind it. The only way to
// move `current` was to install the version again -- which needs a
// resolvable manifest, and a bucket carries only its current one. So
// having 1.7.1 and 1.8.2 both sitting on disk, switching between them
// could still fail because upstream had moved on.
//
// Nothing is downloaded and no files are touched: this moves the
// junction and rebuilds the wiring around it from the target version's
// own receipt.
func Use(appName, version string) error {
	versions, err := InstalledVersions(appName)
	if err != nil {
		return err
	}
	var target *InstalledVersion
	for i := range versions {
		if strings.EqualFold(versions[i].Version, version) {
			target = &versions[i]
		}
	}
	if target == nil {
		have := make([]string, len(versions))
		for i, v := range versions {
			have[i] = v.Version
		}
		if len(have) == 0 {
			return fmt.Errorf("%s has no installed version with a usable receipt -- `goop reinstall %s` rewrites one", appName, appName)
		}
		return fmt.Errorf("%s %s is not installed; installed: %s", appName, version, strings.Join(have, ", "))
	}
	if target.Current {
		Logf("%s is already on %s", appName, target.Version)
		return nil
	}

	// goop reaches its own binary through the bin junction, not through
	// apps\goop\current, so moving the latter would change nothing and
	// moving the former mid-switch is self-update's job. Refuse rather
	// than half-do it.
	if strings.EqualFold(appName, SelfPackageName) {
		return fmt.Errorf("switching goop's own version is `goop self-update`'s job, not this command's")
	}

	// Checked before anything is reverted: rewiring under a running app
	// fails partway through on locked files, and the environment would
	// already have been taken back by then.
	if running, err := pwsh.RunningProcessesUnder(paths.App(appName)); err == nil && len(running) > 0 {
		return fmt.Errorf("%s is still running -- close it and try again:\n  %s",
			appName, strings.Join(running, "\n  "))
	}

	// Take back the outgoing version's environment before relinking.
	//
	// Reset reverts and re-applies the record `current` points at, so on
	// its own it would take back the *incoming* version's entries and
	// leave the outgoing version's behind. env_add_path records an
	// already-expanded, versioned path -- apps\gcc\15.2.0\bin -- so that
	// stale entry would sit on PATH alongside the new one. That is
	// exactly how 80 of 98 per-user PATH entries came to be dead on the
	// development machine.
	if outgoing, ok := readCurrentRecord(appName); ok {
		revertEnv(appName, outgoing.EnvSet, outgoing.EnvAddedPaths)
	}

	if err := relinkCurrent(appName, paths.AppVersion(appName, target.Version)); err != nil {
		return err
	}
	// Reset now reads the incoming version's receipt and rebuilds shims,
	// shortcuts, the module link and the environment from it. Shims point
	// at `current` rather than at a version directory, so they follow the
	// junction -- but the rest does not, which is why this runs at all.
	if err := Reset(appName); err != nil {
		return err
	}
	Logf("%s is now on %s", appName, target.Version)
	return nil
}
