// Package profileset reads the profile file: named groups of packages,
// each pinned to a version and a manifest digest, versioned in a
// repository alongside the code it belongs to.
//
// The file is the source of truth. Local state never has authority over
// it, which is what makes `git pull` the propagation mechanism: pulling
// a changed file is what turns a check red, with no cache, no central
// definition and no network involved anywhere.
package profileset

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// File is a whole profile file.
type File struct {
	Profiles map[string]Profile `json:"profiles"`
}

// Profile is one named group.
type Profile struct {
	// Packages are what the profile declares directly.
	Packages map[string]Pin `json:"packages"`

	// Resolved will hold transitive dependencies once goop computes
	// them. Absent means empty, deliberately: files written today stay
	// valid when that arrives, and nothing has to be regenerated.
	Resolved map[string]Pin `json:"resolved,omitempty"`
}

// Pin is what a profile requires of one package.
type Pin struct {
	Version string `json:"version"`

	// Hash is the manifest digest (manifest.Digest), not the artifact
	// hash. It covers what installing will *do* -- scripts included --
	// rather than only the bytes downloaded. Optional: a pin with no
	// hash still checks the version.
	Hash string `json:"hash,omitempty"`
}

// UnmarshalJSON accepts a bare version string as shorthand for a pin
// with no digest, so a profile that only cares about versions stays
// readable.
func (p *Pin) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		p.Version, p.Hash = s, ""
		return nil
	}
	type raw Pin
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	*p = Pin(r)
	return nil
}

// Load reads a profile file.
func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read profile file %s: %w", path, err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("parse profile file %s: %w", path, err)
	}
	if f.Profiles == nil {
		f.Profiles = map[string]Profile{}
	}
	return f, nil
}

// Save writes a profile file.
func Save(path string, f File) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Names returns every profile in the file, sorted.
func (f File) Names() []string {
	out := make([]string, 0, len(f.Profiles))
	for n := range f.Profiles {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Select resolves the profiles a command was asked for: the named ones,
// or all of them when none is named.
//
// A name absent from the file is an error, never a silent skip. A check
// that reported conformance because it had nothing to verify would be
// worse than one that failed.
func (f File) Select(names []string) ([]string, error) {
	if len(names) == 0 {
		if len(f.Profiles) == 0 {
			return nil, fmt.Errorf("the file declares no profiles")
		}
		return f.Names(), nil
	}
	for _, n := range names {
		if _, ok := f.Profiles[n]; !ok {
			return nil, fmt.Errorf("no profile %q in this file (it has: %v)", n, f.Names())
		}
	}
	return names, nil
}

// All returns a profile's packages, declared and resolved together, since
// every consumer wants both.
func (p Profile) All() map[string]Pin {
	out := make(map[string]Pin, len(p.Packages)+len(p.Resolved))
	for k, v := range p.Resolved {
		out[k] = v
	}
	for k, v := range p.Packages { // declared wins over resolved
		out[k] = v
	}
	return out
}

// SortedNames returns a profile's package names, sorted.
func (p Profile) SortedNames() []string {
	all := p.All()
	out := make([]string, 0, len(all))
	for n := range all {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
