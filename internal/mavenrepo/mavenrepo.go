// Package mavenrepo manages named Maven repository sources -- mirrors
// internal/bucket's config-list shape (Entry/Config/List/priority
// search), minus the git-clone/archive-fetch machinery that doesn't
// apply here: a Maven repo has no local directory, it's just a base URL
// resolved against per-install (internal/maven).
package mavenrepo

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"goop/internal/maven"
	"goop/internal/paths"
)

// Entry is one configured Maven repo: a name and its base URL. Priority
// is the order entries appear in Config.Repos -- earlier wins when a
// coordinate is resolved without a repo name (searched in order until
// one has the artifact), same convention as internal/bucket.
type Entry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Config struct {
	Repos []Entry `json:"repos"`
}

func loadConfig() (Config, error) {
	data, err := os.ReadFile(paths.MavenReposConfig())
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read Maven repo config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse Maven repo config: %w", err)
	}
	return cfg, nil
}

func saveConfig(cfg Config) error {
	if err := paths.EnsureLayout(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(paths.MavenReposConfig(), data, 0o644)
}

// List returns the configured Maven repos in priority order.
func List() ([]Entry, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	return cfg.Repos, nil
}

// Add registers a new Maven repo. Unlike bucket.Add there's nothing to
// fetch -- a Maven repo is a URL resolved against per-install, not a
// local clone -- so this is pure config bookkeeping.
func Add(name, repoURL string) error {
	if name == "" {
		return fmt.Errorf("Maven repo name must not be empty")
	}
	if _, err := url.Parse(repoURL); err != nil {
		return fmt.Errorf("invalid Maven repo URL %q: %w", repoURL, err)
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	for _, e := range cfg.Repos {
		if e.Name == name {
			return fmt.Errorf("Maven repo %q already added (%s)", name, e.URL)
		}
	}
	cfg.Repos = append(cfg.Repos, Entry{Name: name, URL: repoURL})
	return saveConfig(cfg)
}

// Remove deletes a configured Maven repo by name.
func Remove(name string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	idx := -1
	for i, e := range cfg.Repos {
		if e.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("Maven repo %q not configured", name)
	}
	cfg.Repos = append(cfg.Repos[:idx], cfg.Repos[idx+1:]...)
	return saveConfig(cfg)
}

// Resolve looks up coord against Maven repo repoName if non-empty
// (clear error if that name isn't configured), or searches every
// configured repo in priority order otherwise -- mirrors
// bucket.Resolve/Find's [bucket/]name split exactly, trying each in
// turn and aggregating tried names on total failure.
func Resolve(repoName string, coord maven.Coordinate) (artifactURL, hash string, err error) {
	entries, err := List()
	if err != nil {
		return "", "", err
	}

	if repoName != "" {
		for _, e := range entries {
			if e.Name == repoName {
				return maven.Resolve(e.URL, coord)
			}
		}
		return "", "", fmt.Errorf("Maven repo %q not configured; add one with `goop maven-repo add %s <url>`", repoName, repoName)
	}

	if len(entries) == 0 {
		return "", "", fmt.Errorf("no Maven repos configured; add one with `goop maven-repo add <name> <url>`")
	}
	var tried []string
	for _, e := range entries {
		u, h, err := maven.Resolve(e.URL, coord)
		if err == nil {
			return u, h, nil
		}
		tried = append(tried, e.Name)
	}
	return "", "", fmt.Errorf("artifact not found in Maven repo(s) %s", strings.Join(tried, ", "))
}
