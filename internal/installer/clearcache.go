package installer

import (
	"os"
	"path/filepath"
	"strings"

	"goop/internal/paths"
)

// ClearCache deletes cached downloads. With no patterns it empties the
// cache; otherwise it removes entries whose filename contains any of
// the given substrings (case-insensitive), so `goop cache rm firefox`
// does the obvious thing without the caller needing to know that cache
// filenames are hash-prefixed.
//
// Removing a cached file only costs a re-download later: the cache is
// derived data, never the source of truth for an installed app.
func ClearCache(patterns []string) (freed int64, removed int, err error) {
	dir := paths.Cache()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !matchesAny(e.Name(), patterns) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			continue // in use by a running download; the next run gets it
		}
		freed += info.Size()
		removed++
	}
	return freed, removed, nil
}

// matchesAny reports whether name contains any pattern; an empty
// pattern list matches everything (the "clear it all" case).
func matchesAny(name string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	lower := strings.ToLower(name)
	for _, p := range patterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}
