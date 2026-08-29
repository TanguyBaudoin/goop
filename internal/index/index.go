// Package index reads the profile index: what each profile contains,
// published by a team rather than shipped with goop or committed into
// every repository.
//
// It is deliberately separate from buckets. A bucket is a *catalogue* --
// how to install each package, published for anyone to consume. The index
// is one team's configuration: which packages make up `baseline.tool`.
// Putting the second inside the first would make a catalogue carry
// something only its publisher's team cares about.
//
// The document is a single JSON object:
//
//	{"profiles": {"baseline.tool": ["git", "graphviz", "srecord"]}}
package index

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/TanguyBaudoin/goop/internal/downloader"
	"github.com/TanguyBaudoin/goop/internal/paths"
)

// Document is the index as published.
type Document struct {
	Profiles map[string][]string `json:"profiles"`
}

// Load returns the cached index. A machine with no index configured, or
// one that has never fetched it, has an empty index rather than an error
// -- profiles simply come from local files only.
func Load() Document {
	data, err := os.ReadFile(paths.IndexCache())
	if err != nil {
		return Document{}
	}
	var d Document
	if err := json.Unmarshal(data, &d); err != nil {
		// A corrupt cache must not break every profile lookup on the
		// machine. `goop index update` replaces it.
		return Document{}
	}
	return d
}

// Update fetches the configured index and replaces the cache.
//
// The fetch goes through goop's own client, so per-host auth, the
// configured proxy and file:// all apply -- an internal HTTP server and a
// network share need no different configuration.
func Update() (Document, error) {
	url, ok := paths.ConfiguredIndex()
	if !ok {
		return Document{}, fmt.Errorf("no index configured; set one with `goop config set-index <url>`")
	}
	body, err := downloader.FetchText(url)
	if err != nil {
		return Document{}, fmt.Errorf("fetch index %s: %w", url, err)
	}
	var d Document
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		return Document{}, fmt.Errorf("parse index %s: %w", url, err)
	}

	// Written only after it parses: a cache that survives a bad publish
	// is what keeps the machine working until the next good one.
	pretty, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return Document{}, err
	}
	if err := paths.EnsureLayout(); err != nil {
		return Document{}, err
	}
	if err := os.WriteFile(paths.IndexCache(), append(pretty, '\n'), 0o644); err != nil {
		return Document{}, fmt.Errorf("write index cache: %w", err)
	}
	return d, nil
}

// Apps returns the packages the index lists for a profile, in the order
// they were published.
//
// Order is deliberately preserved rather than sorted. For most profiles
// it carries no meaning, but for a profile of alternatives -- `ide`, say
// -- the first entry is the default, and sorting it would silently pick
// whichever editor happens to come first alphabetically.
func Apps(profileName string) ([]string, bool) {
	d := Load()
	apps, ok := d.Profiles[profileName]
	if !ok {
		return nil, false
	}
	return append([]string(nil), apps...), true
}

// Names returns every profile the index defines, sorted.
func Names() []string {
	d := Load()
	out := make([]string, 0, len(d.Profiles))
	for name := range d.Profiles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
