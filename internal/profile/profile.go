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
	"slices"
	"sort"
	"strings"
	"sync"

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
		if name == Default {
			return loadLegacyDefault()
		}
		return Definition{Name: name}, nil
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
//
// Adding to a named profile drops the app from `default`. `default` is
// where things land when nobody has said otherwise, so once someone does
// say otherwise it has no claim left -- and leaving it there made
// `goop why` report two owners for a package with one, and left the app
// behind in `default` after it had been deliberately filed elsewhere.
//
// Removing from `default` is not the same as removing from any other
// profile: a package genuinely shared by two named profiles stays in
// both, which is what makes the uninstall safety net meaningful.
func Add(profileName, appName string) error {
	if err := addOne(profileName, appName); err != nil {
		return err
	}
	if profileName != Default {
		return Remove(Default, appName)
	}
	return nil
}

func addOne(profileName, appName string) error {
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

// Delete removes a named profile.
//
// Nothing is uninstalled: a profile is a grouping, not an installation.
// Members left in no profile at all fall back to Default -- that is what
// Default is for, and an orphaned package would otherwise be invisible to
// the uninstall safety net.
//
// Default itself cannot be deleted: it is the fallback, so removing it
// would leave nowhere for anything to fall back to.
func Delete(name string) error {
	if name == "" || name == Default {
		return fmt.Errorf("the default profile cannot be deleted (use `goop profile remove default <app>...` to un-declare members)")
	}
	names, err := List()
	if err != nil {
		return err
	}
	if !slices.Contains(names, name) {
		return fmt.Errorf("no profile %q (there is: %v)", name, names)
	}

	d, err := Load(name)
	if err != nil {
		return err
	}

	profileMu.Lock()
	if err := os.Remove(Path(name)); err != nil && !os.IsNotExist(err) {
		profileMu.Unlock()
		return err
	}
	profileMu.Unlock()

	// After the file is gone, so ContainingProfiles no longer counts it.
	for _, app := range d.Apps {
		in, err := ContainingProfiles(app)
		if err != nil {
			return err
		}
		if len(in) == 0 {
			if err := Add(Default, app); err != nil {
				return err
			}
		}
	}
	return nil
}

// Reset merges every profile's members into Default and deletes the
// named profiles. Nothing is uninstalled.
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
	return Save(def)
}
