// Package profile groups installed apps into user-curated, named
// profiles ("core", "projectA", "projectB", ...) -- a membership label,
// not an isolated environment: installation itself stays global/shared
// exactly as always (one apps/<name>/ tree regardless of how many
// profiles reference it). Reuses internal/lockfile's existing File/Entry
// shape unchanged (Load/Save were already parameterized by path, not
// hardcoded to the root lockfile) -- a profile file is structurally
// identical to goop.lock.json, just stored under a name.
package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/TanguyBaudoin/goop/internal/lockfile"
	"github.com/TanguyBaudoin/goop/internal/paths"
)

// Default is the profile installs land in when no other profile has
// been activated via Use. It maps to the classic root-level
// goop.lock.json (Path), so existing lockfile-based workflows are
// unaffected by profiles existing at all.
const Default = "default"

// Path returns where profile's membership file lives. Default maps to
// lockfile.Path() ("<root>/goop.lock.json") for zero migration pain;
// named profiles live at "<root>/profiles/<name>.json".
//
// A name that is really a file path ("./chipA.lock.json",
// "D:\src\proj\goop.lock.json") is used verbatim instead. A lockfile
// pins a *project's* toolchain, so like package-lock.json or Cargo.lock
// it belongs in that project's repo, versioned by the repo's own
// history -- not buried in goop's install root where it can't be
// committed alongside the code it pins. IsFilePath's rules make this
// unambiguous against real profile names, which are bare identifiers.
func Path(name string) string {
	if name == "" || name == Default {
		return lockfile.Path()
	}
	if IsFilePath(name) {
		return name
	}
	return filepath.Join(paths.Root(), "profiles", name+".json")
}

// IsFilePath reports whether name should be treated as an explicit
// lockfile path rather than a profile name: profile names are bare
// identifiers ("default", "projectA"), so anything carrying a
// separator, a drive letter, or a .json suffix is a path.
func IsFilePath(name string) bool {
	return strings.ContainsAny(name, `/\`) ||
		filepath.IsAbs(name) ||
		strings.HasSuffix(strings.ToLower(name), ".json")
}

func profilesDir() string {
	return filepath.Join(paths.Root(), "profiles")
}

// List returns every profile name with a file on disk, Default always
// included even if goop.lock.json doesn't exist yet.
func List() ([]string, error) {
	names := map[string]bool{Default: true}

	entries, err := os.ReadDir(profilesDir())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "active.json" || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		names[strings.TrimSuffix(e.Name(), ".json")] = true
	}

	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// profileMu guards read-modify-write of any profile file against
// concurrent installs (A1) registering into the same active profile at
// once.
var profileMu sync.Mutex

// Add registers appName as a member of profileName, idempotent (a no-op
// if already present). Creates the profile file if it doesn't exist yet.
func Add(profileName, appName string) error {
	profileMu.Lock()
	defer profileMu.Unlock()

	path := Path(profileName)
	f, err := lockfile.Load(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if _, ok := f.Find(appName); ok {
		return nil
	}
	f.Entries = append(f.Entries, lockfile.Entry{Name: appName})
	return lockfile.Save(path, f)
}

// Remove removes appName from profileName's membership, idempotent (a
// no-op if the profile or the entry doesn't exist).
func Remove(profileName, appName string) error {
	profileMu.Lock()
	defer profileMu.Unlock()

	path := Path(profileName)
	f, err := lockfile.Load(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	idx := -1
	for i, e := range f.Entries {
		if e.Name == appName {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil
	}
	f.Entries = append(f.Entries[:idx], f.Entries[idx+1:]...)
	return lockfile.Save(path, f)
}

// ContainingProfiles returns every profile that currently lists appName
// as a member -- the "why" query.
func ContainingProfiles(appName string) ([]string, error) {
	names, err := List()
	if err != nil {
		return nil, err
	}
	var containing []string
	for _, name := range names {
		f, err := lockfile.Load(Path(name))
		if err != nil {
			continue // no file yet (or unreadable) -- treat as an empty profile
		}
		if _, ok := f.Find(appName); ok {
			containing = append(containing, name)
		}
	}
	return containing, nil
}

func activeFilePath() string {
	return filepath.Join(profilesDir(), "active.json")
}

type activeState struct {
	Active string `json:"active,omitempty"`
}

// Active returns the currently active profile name (Default if Use has
// never been called).
func Active() string {
	data, err := os.ReadFile(activeFilePath())
	if err != nil {
		return Default
	}
	var s activeState
	if json.Unmarshal(data, &s) != nil || s.Active == "" {
		return Default
	}
	return s.Active
}

// Use persists name as the active profile -- subsequent
// `goop install`/`goop uninstall` calls register/unregister against it
// until changed again (conda-activate-style).
func Use(name string) error {
	if name == "" {
		name = Default
	}
	data, err := json.MarshalIndent(activeState{Active: name}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(profilesDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(activeFilePath(), data, 0o644)
}
