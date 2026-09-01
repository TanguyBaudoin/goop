package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/envvars"
	"github.com/TanguyBaudoin/goop/internal/paths"
)

// StalePathEntry is a PATH entry pointing into goop's app tree at
// something that is no longer the installed version.
type StalePathEntry struct {
	Path    string
	App     string
	Version string
	Reason  string
}

// AppsDir is where goop keeps installed versions, for a caller that
// wants to say which tree it is about to touch.
func AppsDir() string { return paths.Apps() }

// StalePathEntries finds PATH entries goop left behind.
//
// Until this release an update added its versioned env_add_path entry
// and never took back the one it replaced, so PATH accumulated one entry
// per update, forever. That is fixed going forward, but a machine that
// has been running goop for months already carries the backlog -- 17
// entries under apps/ on the one this was found on, 8 superseded and 5
// pointing at directories that no longer existed.
//
// Only entries under <root>/apps are considered, and that is what makes
// this safe to offer. That tree is goop's: it creates the version
// directories, it deletes them on cleanup, and nothing else writes
// there. An entry naming a version goop no longer has installed is
// stale whoever added it -- the directory it names is gone or about to
// be. <root>/bin and <root>/shims are deliberately out of scope: those
// are set up once by the installer and must survive everything.
func StalePathEntries() ([]StalePathEntry, error) {
	entries, err := envvars.PathEntries()
	if err != nil {
		return nil, err
	}
	appsRoot := paths.Apps()

	var out []StalePathEntry
	for _, entry := range entries {
		app, version, ok := appVersionUnder(appsRoot, entry)
		if !ok {
			continue
		}
		e := StalePathEntry{Path: entry, App: app, Version: version}

		if _, err := os.Stat(paths.AppVersion(app, version)); os.IsNotExist(err) {
			e.Reason = "that version is gone"
			out = append(out, e)
			continue
		}
		current, ok := readCurrentRecord(app)
		if !ok {
			e.Reason = app + " is not installed"
			out = append(out, e)
			continue
		}
		if current.Version != version {
			e.Reason = "superseded by " + current.Version
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].App != out[j].App {
			return out[i].App < out[j].App
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// appVersionUnder pulls "<app>/<version>" out of a PATH entry that sits
// under goop's apps directory, or reports that it does not.
//
// `current` is deliberately not matched: an entry through the junction
// follows whatever is installed and never goes stale, so it is not this
// function's business.
func appVersionUnder(appsRoot, entry string) (app, version string, ok bool) {
	rel, err := filepath.Rel(appsRoot, filepath.Clean(entry))
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", "", false
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	if parts[1] == "current" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// PrunePathEntries removes the given entries from the persisted PATH.
// Reports how many went, and the first failure if any did.
func PrunePathEntries(entries []StalePathEntry) (int, error) {
	removed := 0
	var firstErr error
	for _, e := range entries {
		if err := envvars.RemoveFromPath(e.Path); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", e.Path, err)
			}
			continue
		}
		Logf("removing %s from your PATH (%s)", e.Path, e.Reason)
		removed++
	}
	return removed, firstErr
}
