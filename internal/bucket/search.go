package bucket

import (
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"goop/internal/manifest"
)

// SearchResult is one manifest matched by Search: Binaries is empty
// when the match came from Name itself, or the matched `bin` shim
// name(s) (joined by " | ") when it came from --bin.
type SearchResult struct {
	Name     string
	Version  string
	Bucket   string
	Binaries string
}

// Search finds every manifest across configured buckets whose name
// matches query -- a case-insensitive regex, mirroring real Scoop's
// own `scoop search`. A plain word works fine as a substring match;
// anything more is a real regex.
//
// Without includeBin, only a manifest's filename is checked, so most
// manifests never need decoding at all -- just a directory listing per
// bucket. With includeBin, a manifest whose name doesn't match is also
// decoded and checked against its `bin` field, covering "I know the
// command, not the package name" (e.g. `rg` -> ripgrep) -- this means
// decoding every manifest in every configured bucket, so noticeably
// slower on a large bucket than the name-only default.
func Search(query string, includeBin bool) ([]SearchResult, error) {
	re, err := regexp.Compile("(?i)" + query)
	if err != nil {
		return nil, fmt.Errorf("invalid search pattern %q: %w", query, err)
	}

	entries, err := List()
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no buckets configured; add one with `goop bucket add <name> <url>`")
	}

	type candidate struct{ bucket, name string }
	var candidates []candidate
	for _, e := range entries {
		names, err := ManifestNames(e.Name)
		if err != nil {
			continue
		}
		for _, name := range names {
			candidates = append(candidates, candidate{e.Name, name})
		}
	}

	// Name-only matching never needs to decode anything (ManifestNames
	// above is just a directory listing), so it stays fast without any
	// of this. --bin has to decode every candidate that didn't already
	// match by name -- across a real ~5000-manifest bucket set that's
	// enough JSON decoding to be worth spreading across cores: measured
	// ~52s single-threaded on this machine, since manifest decode here
	// is CPU/disk-bound (small local files), not network-bound like
	// installs, so sizing by NumCPU rather than reusing installer's
	// network-oriented concurrency default.
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	if workers > len(candidates) {
		workers = len(candidates)
	}

	var mu sync.Mutex
	var results []SearchResult
	var wg sync.WaitGroup
	work := make(chan candidate)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range work {
				nameMatches := re.MatchString(c.name)
				if !nameMatches && !includeBin {
					continue
				}
				m, err := readManifest(c.bucket, c.name)
				if err != nil {
					continue
				}
				var r *SearchResult
				if nameMatches {
					r = &SearchResult{Name: c.name, Version: m.Version, Bucket: c.bucket}
				} else if bins := matchBin(m.Bin, re); len(bins) > 0 {
					r = &SearchResult{Name: c.name, Version: m.Version, Bucket: c.bucket, Binaries: strings.Join(bins, " | ")}
				}
				if r != nil {
					mu.Lock()
					results = append(results, *r)
					mu.Unlock()
				}
			}
		}()
	}
	for _, c := range candidates {
		work <- c
	}
	close(work)
	wg.Wait()

	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return results, nil
}

// matchBin checks each bin entry's resolved shim name -- already
// either the exe's own basename or an explicit alias, per BinEntry's
// own decode-time default -- against re, mirroring Scoop's bin_match.
func matchBin(bins manifest.BinList, re *regexp.Regexp) []string {
	var out []string
	for _, b := range bins {
		if re.MatchString(b.Name) {
			out = append(out, b.Name)
		}
	}
	return out
}
