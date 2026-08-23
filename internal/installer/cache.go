package installer

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/TanguyBaudoin/goop/internal/paths"
)

// CacheEntry is one cached download.
type CacheEntry struct {
	Name string
	Size int64
}

// CacheUsage reports what the download cache currently holds.
func CacheUsage() (total int64, entries []CacheEntry, err error) {
	dir := paths.Cache()
	items, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil, nil
		}
		return 0, nil, err
	}
	for _, it := range items {
		if it.IsDir() {
			continue
		}
		info, err := it.Info()
		if err != nil {
			continue
		}
		total += info.Size()
		entries = append(entries, CacheEntry{Name: it.Name(), Size: info.Size()})
	}
	return total, entries, nil
}

// PruneCache enforces the configured cache ceiling, evicting oldest
// first (by modification time) until the total fits -- the "circular"
// behavior: a full cache keeps making room by dropping what was
// downloaded longest ago. An unset limit keeps everything (goop's
// original behavior); a limit of 0 empties the cache entirely.
//
// Called once at the end of a command, after every download in the
// batch has been consumed -- never mid-download. Installs run
// concurrently (A1) and a cached file stays in use after
// downloader.Get returns it, so evicting from underneath a running
// install would be a race; doing it at the end has no such window and
// needs no locking.
//
// Returns how many bytes were freed.
func PruneCache() (freed int64, err error) {
	limit := paths.ConfiguredCacheLimit()
	if limit == paths.CacheUnlimited {
		return 0, nil
	}

	dir := paths.Cache()
	items, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	type cached struct {
		path  string
		size  int64
		mtime int64
	}
	var files []cached
	var total int64
	for _, it := range items {
		if it.IsDir() {
			continue
		}
		info, err := it.Info()
		if err != nil {
			continue
		}
		files = append(files, cached{filepath.Join(dir, it.Name()), info.Size(), info.ModTime().UnixNano()})
		total += info.Size()
	}
	if total <= limit {
		return 0, nil
	}

	// Oldest first: those are the least likely to be wanted again, and
	// evicting them is what makes room for what was just fetched.
	sort.Slice(files, func(i, j int) bool { return files[i].mtime < files[j].mtime })
	for _, f := range files {
		if total <= limit {
			break
		}
		if err := os.Remove(f.path); err != nil {
			continue // in use, or gone already -- try the next one
		}
		total -= f.size
		freed += f.size
	}
	return freed, nil
}
