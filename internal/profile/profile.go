// Package profile groups packages into named, user-curated sets
// ("core", "baseline.tool", "ide", ...).
//
// A profile is a membership label and nothing more: a list of names, no
// versions, no hashes, no payload. That is the whole point of the
// separation from lockfiles. A profile is allowed to drift -- the team
// adds a tool, everyone picks it up -- while reproducibility is
// guaranteed by the lockfile alone, and by snapshots taken from it.
//
// Profiles used to be stored *as* lockfiles, with the default profile
// literally being goop.lock.json. That made the two indistinguishable on
// disk and let a soft grouping masquerade as a pinned, auditable
// artifact. `goop migrate` converts the old shape.
//
// Installation itself stays global and shared: one apps/<name>/ tree
// regardless of how many profiles reference it.
package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/TanguyBaudoin/goop/internal/index"
	"github.com/TanguyBaudoin/goop/internal/paths"
)

// Default is the profile installs land in when none has been activated.
const Default = "default"

// Definition is a profile as stored: a name and the packages it groups.
// Apps are kept sorted so two machines that added the same tools produce
// the same file, and a diff shows only what changed.
type Definition struct {
	Name string   `json:"name"`
	Apps []string `json:"apps"`

	// Source is where this definition came from: "local" for a file on
	// this machine, "index" for one published by the team. Not stored --
	// it is a property of the lookup, not of the file.
	Source string `json:"-"`
}

// Path is where profile's definition lives. Every profile, default
// included, is a file under profiles/ -- unlike before, where default
// aliased the root lockfile.
func Path(name string) string {
	if name == "" {
		name = Default
	}
	return filepath.Join(profilesDir(), name+".json")
}

func profilesDir() string {
	return filepath.Join(paths.Root(), "profiles")
}

// Load reads a profile. A profile with no file yet is an empty one, not
// an error: membership is created by the first `add` or install.
//
// Legacy shapes are read, never rewritten. Profiles used to be stored as
// lockfiles, so an old file carries `entries` instead of `apps`; and the
// default profile used to be the root lockfile itself. Both are accepted
// here and converted on the next Save, so upgrading goop does not lose
// anyone's membership and does not silently rewrite files either.
func Load(name string) (Definition, error) {
	if name == "" {
		name = Default
	}
	data, err := os.ReadFile(Path(name))
	if os.IsNotExist(err) {
		// No local file: fall back to the published index. A local
		// definition always wins, so a machine can diverge on purpose
		// without the index overwriting that choice.
		if apps, ok := index.Apps(name); ok {
			return Definition{Name: name, Apps: apps, Source: "index"}, nil
		}
		if name == Default {
			return loadLegacyDefault()
		}
		return Definition{Name: name, Source: "local"}, nil
	}
	if err != nil {
		return Definition{}, fmt.Errorf("read profile %q: %w", name, err)
	}
	return decodeDefinition(name, data)
}

// legacyFile is the old on-disk shape: a lockfile, whose entries carried
// versions and hashes a profile has no business holding.
type legacyFile struct {
	Entries []struct {
		Name string `json:"name"`
	} `json:"entries"`
}

func decodeDefinition(name string, data []byte) (Definition, error) {
	var d Definition
	if err := json.Unmarshal(data, &d); err != nil {
		return Definition{}, fmt.Errorf("parse profile %q (%s): %w", name, Path(name), err)
	}
	if len(d.Apps) == 0 {
		var legacy legacyFile
		if err := json.Unmarshal(data, &legacy); err == nil && len(legacy.Entries) > 0 {
			for _, e := range legacy.Entries {
				d.Apps = append(d.Apps, e.Name)
			}
		}
	}
	d.Name = name // the filename is authoritative
	d.Source = "local"
	return d, nil
}

// loadLegacyDefault recovers default membership from the root lockfile,
// which is where it lived before profiles and lockfiles were separated.
// The lockfile is left alone: it is still a perfectly good lockfile, it
// just no longer doubles as a profile.
func loadLegacyDefault() (Definition, error) {
	data, err := os.ReadFile(filepath.Join(paths.Root(), "goop.lock.json"))
	if err != nil {
		return Definition{Name: Default}, nil
	}
	return decodeDefinition(Default, data)
}

// Save writes a profile, deduplicated, in the order entries were added.
//
// Not sorted: a profile of alternatives ranks its members, and the first
// is the default. Insertion order is stable anyway, so diffs stay clean.
func Save(d Definition) error {
	if d.Name == "" {
		d.Name = Default
	}
	seen := make(map[string]bool, len(d.Apps))
	apps := make([]string, 0, len(d.Apps))
	for _, a := range d.Apps {
		if a = strings.TrimSpace(a); a != "" && !seen[a] {
			seen[a] = true
			apps = append(apps, a)
		}
	}
	d.Apps = apps

	if err := os.MkdirAll(profilesDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(d.Name), append(data, '\n'), 0o644)
}

// List returns every profile with a file on disk, Default always
// included even before anything has been added to it.
func List() ([]string, error) {
	names := map[string]bool{Default: true}

	// Index-defined profiles are real profiles even with no local file;
	// leaving them out would make the team's baseline invisible until
	// someone happened to install from it.
	for _, name := range index.Names() {
		names[name] = true
	}

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

// profileMu guards read-modify-write of a profile against concurrent
// installs (A1) registering into the same active profile at once.
var profileMu sync.Mutex

// Add registers appName as a member of profileName. Idempotent.
func Add(profileName, appName string) error {
	profileMu.Lock()
	defer profileMu.Unlock()

	d, err := Load(profileName)
	if err != nil {
		return err
	}
	for _, a := range d.Apps {
		if a == appName {
			return nil
		}
	}
	d.Apps = append(d.Apps, appName)
	return Save(d)
}

// Remove drops appName from profileName. Idempotent.
func Remove(profileName, appName string) error {
	profileMu.Lock()
	defer profileMu.Unlock()

	d, err := Load(profileName)
	if err != nil {
		return err
	}
	out := d.Apps[:0]
	for _, a := range d.Apps {
		if a != appName {
			out = append(out, a)
		}
	}
	if len(out) == len(d.Apps) {
		return nil
	}
	d.Apps = out
	return Save(d)
}

// ContainingProfiles returns every profile listing appName -- the "why"
// query.
func ContainingProfiles(appName string) ([]string, error) {
	names, err := List()
	if err != nil {
		return nil, err
	}
	var containing []string
	for _, name := range names {
		d, err := Load(name)
		if err != nil {
			continue // unreadable -- treat as empty rather than failing the query
		}
		for _, a := range d.Apps {
			if a == appName {
				containing = append(containing, name)
				break
			}
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

// Reset merges every profile's members into Default, deletes the named
// profiles, and makes Default active again.
func Reset() error {
	profileMu.Lock()
	defer profileMu.Unlock()

	names, err := List()
	if err != nil {
		return err
	}
	def, err := Load(Default)
	if err != nil {
		return err
	}
	for _, name := range names {
		if name == Default {
			continue
		}
		d, err := Load(name)
		if err != nil {
			return err
		}
		def.Apps = append(def.Apps, d.Apps...) // Save dedupes and sorts
		if err := os.Remove(Path(name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := Save(def); err != nil {
		return err
	}
	return Use(Default)
}

// Active returns the currently active profile name.
func Active() string {
	data, err := os.ReadFile(activeFilePath())
	if err != nil {
		return Default
	}
	var s activeState
	if err := json.Unmarshal(data, &s); err != nil || s.Active == "" {
		return Default
	}
	return s.Active
}

// Use makes name the active profile.
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

// RemoveLocal drops appName from profileName only when that profile is
// defined on this machine. A profile that comes from the index is left
// alone.
//
// This is what uninstall uses. Editing an index-defined profile forks it
// locally, and an uninstall must not do that as a side effect: removing
// one package would silently detach the machine from the team's whole
// baseline, and the profile would then be an empty local file shadowing
// it. Deliberately editing such a profile is still possible through
// `goop profile remove`, which says what it is doing.
func RemoveLocal(profileName, appName string) error {
	if _, err := os.Stat(Path(profileName)); err != nil {
		return nil // index-defined or absent: nothing local to edit
	}
	return Remove(profileName, appName)
}
