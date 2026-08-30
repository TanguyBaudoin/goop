// Package setup captures and replays a whole machine: its buckets and
// every package installed on it.
//
// This is the plane that has nothing to do with any repository. A
// profile file answers "does this project have what it needs"; a setup
// file answers "what is on this machine", for moving to a new one or for
// diagnosing an old one. Keeping them apart is what stopped the two from
// being confused for each other.
//
// Shaped after `scoop export`, which also records buckets alongside apps
// -- without them a fresh machine has the list but no way to install it.
package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// File is a captured machine.
type File struct {
	Buckets []Bucket `json:"buckets"`
	Apps    []App    `json:"apps"`
}

// Bucket is one configured catalogue.
type Bucket struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Kind string `json:"kind,omitempty"`
}

// App is one installed package, with what it takes to put it back.
type App struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Bucket  string `json:"bucket,omitempty"`
	// ManifestDigest fingerprints the instructions this was installed
	// from, so an audit can tell "same version, different manifest" from
	// a genuine match.
	ManifestDigest string `json:"manifest_digest,omitempty"`
}

// Load reads a setup file.
func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read setup file %s: %w", path, err)
	}
	// Probe for the key before decoding into File. A profile file is
	// valid JSON with no `apps`, so it decodes into an empty capture and
	// `audit` then reports every installed package as "not in the
	// capture" -- a confident, wrong answer to a question nobody asked.
	// `check` already refuses a capture for the mirror reason; this is
	// the other half of that guard.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return File{}, fmt.Errorf("parse setup file %s: %w", path, err)
	}
	if _, ok := probe["apps"]; !ok {
		if _, isProfiles := probe["profiles"]; isProfiles {
			return File{}, fmt.Errorf("%s is a profile file, not a machine capture -- use `goop check`/`goop sync`", path)
		}
		return File{}, fmt.Errorf("%s is not a machine capture (no \"apps\")", path)
	}

	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("parse setup file %s: %w", path, err)
	}
	return f, nil
}

// Save writes a setup file, sorted so two captures of the same machine
// produce the same bytes and a diff shows only what moved.
func Save(path string, f File) error {
	sort.Slice(f.Buckets, func(i, j int) bool { return f.Buckets[i].Name < f.Buckets[j].Name })
	sort.Slice(f.Apps, func(i, j int) bool { return f.Apps[i].Name < f.Apps[j].Name })
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
