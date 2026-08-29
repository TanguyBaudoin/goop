// Package repo reads goop.json: what a repository declares it needs.
//
// The split is deliberate. The repository states its *intent* -- which
// profiles it wants and where its lockfile is -- while the index says
// what those profiles contain. That is what keeps "add a tool to the
// team baseline" from meaning a commit in every repository.
package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is what a repository declares itself in.
const FileName = "goop.json"

// Config is a repository's declaration.
type Config struct {
	// Lockfile is where the pinned toolchain lives, relative to the
	// repository root unless absolute. Defaults to goop.lock.json.
	Lockfile string `json:"lockfile,omitempty"`

	// Profiles are the groupings this repository wants applied. Their
	// contents come from the index, not from here.
	Profiles []string `json:"profiles,omitempty"`

	// Dir is where this config was found. Not stored.
	Dir string `json:"-"`
}

// LockfilePath resolves the lockfile against the repository directory.
func (c Config) LockfilePath() string {
	name := c.Lockfile
	if name == "" {
		name = "goop.lock.json"
	}
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(c.Dir, name)
}

// Find walks up from dir looking for goop.json, the way git finds .git.
// Running bootstrap from a subdirectory is the common case, and failing
// there for no reason would be a poor first impression.
func Find(dir string) (Config, bool, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Config{}, false, err
	}
	for {
		path := filepath.Join(abs, FileName)
		if _, err := os.Stat(path); err == nil {
			c, err := load(path)
			if err != nil {
				return Config{}, false, err
			}
			return c, true, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return Config{}, false, nil
		}
		abs = parent
	}
}

func load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	c.Dir = filepath.Dir(path)
	return c, nil
}
