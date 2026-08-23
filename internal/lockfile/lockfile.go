// Package lockfile implements goop's reproducibility layer (A3):
// EXF-10, a versionable lockfile capturing name/version/bucket/resolved
// URL/hash/architecture (plus Bin/ExtractDirs/ExtractTos, goop's own
// addition beyond that minimum -- needed so a synced install actually
// gets working shims without consulting a bucket at all, per EXF-11).
package lockfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"goop/internal/manifest"
	"goop/internal/paths"
)

// FileName is the lockfile's name at the root of a goop install
// (EXF-13: plain text, meant to be diffed and checked into version
// control alongside whatever else describes a machine's setup).
const FileName = "goop.lock.json"

// Path returns the lockfile's location under the current goop root.
func Path() string {
	return filepath.Join(paths.Root(), FileName)
}

// Entry is one locked app.
type Entry struct {
	Name         string              `json:"name"`
	Version      string              `json:"version"`
	Bucket       string              `json:"bucket"`
	Architecture string              `json:"architecture"`
	URLs         []string            `json:"urls"`
	Hashes       []string            `json:"hashes"`
	Bin          []manifest.BinEntry `json:"bin,omitempty"`
	ExtractDirs  []string            `json:"extract_dirs,omitempty"`
	ExtractTos   []string            `json:"extract_tos,omitempty"`
}

// File is the on-disk lockfile: just a name-sorted list of entries, so
// two runs over the same installed state produce a byte-identical file
// and a real diff shows only what actually changed (EXF-13).
type File struct {
	Entries []Entry `json:"entries"`
}

// Load reads and decodes the lockfile at path.
func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, nil
}

// Save writes f to path as canonical (name-sorted, stably indented)
// JSON.
func Save(path string, f File) error {
	sort.Slice(f.Entries, func(i, j int) bool { return f.Entries[i].Name < f.Entries[j].Name })
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Find returns the entry for name, if locked.
func (f File) Find(name string) (Entry, bool) {
	for _, e := range f.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}
