package installer

import (
	"fmt"
	"os"
	"path/filepath"

	"goop/internal/paths"
)

// StaleVersion is one installed version that `current` doesn't point at.
type StaleVersion struct {
	App     string
	Version string
	Size    int64
	Path    string
}

// StaleVersions lists every installed version other than the one
// `current` points at. goop keeps old versions on purpose (NR-03: an
// update never destroys what it replaced, so rolling back is possible),
// but nothing ever removed them either -- on a real machine they had
// grown to 6.3 GB. Passing an app name limits the scan to that app.
func StaleVersions(only string) ([]StaleVersion, error) {
	apps, err := os.ReadDir(paths.Apps())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []StaleVersion
	for _, a := range apps {
		if !a.IsDir() || (only != "" && a.Name() != only) {
			continue
		}
		appName := a.Name()

		// Whatever `current` resolves to is the live version and must
		// survive. If it can't be resolved at all, skip the app rather
		// than guess -- deleting the wrong directory here is not
		// recoverable.
		target, err := os.Readlink(paths.AppCurrent(appName))
		if err != nil {
			continue
		}
		live := filepath.Base(target)

		versions, err := os.ReadDir(paths.App(appName))
		if err != nil {
			continue
		}
		for _, v := range versions {
			if !v.IsDir() || v.Name() == "current" || v.Name() == live {
				continue
			}
			// A ".partial" directory is a failed install's staging area,
			// not a version worth reporting as one -- but it is dead
			// weight, so it still gets cleaned.
			p := filepath.Join(paths.App(appName), v.Name())
			out = append(out, StaleVersion{appName, v.Name(), dirSize(p), p})
		}
	}
	return out, nil
}

// Cleanup removes the versions StaleVersions reports, returning how
// many bytes were freed. An app that is currently running keeps its
// files locked, so a failure to remove one version is reported and the
// rest still proceed.
func Cleanup(only string) (freed int64, removed int, err error) {
	stale, err := StaleVersions(only)
	if err != nil {
		return 0, 0, err
	}
	for _, s := range stale {
		if err := os.RemoveAll(s.Path); err != nil {
			Logf("%s: couldn't remove %s: %v", s.App, s.Version, err)
			continue
		}
		Logf("%s: removed old version %s (%s)", s.App, s.Version, humanBytes(s.Size))
		freed += s.Size
		removed++
	}
	return freed, removed, nil
}

func humanBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0fMB", float64(b)/float64(1<<20))
	default:
		return fmt.Sprintf("%.0fKB", float64(b)/float64(1<<10))
	}
}
