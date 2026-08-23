package installer

import (
	"os"
	"strconv"
	"sync"
)

// defaultConcurrencyValue bounds how many apps install/sync at once
// (A1). High enough to actually saturate a typical connection across
// several small-to-medium downloads, low enough not to hammer a host or
// a slow link with dozens of simultaneous connections. Override with
// GOOP_CONCURRENCY (e.g. "1" to force fully sequential behavior).
const defaultConcurrencyValue = 8

func defaultConcurrency() int {
	if v := os.Getenv("GOOP_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultConcurrencyValue
}

// InstallAll installs every name concurrently (bounded), the real-world
// shape of A1's win: a batch of packages no longer waits on one
// download before starting the next. Safe to call with any names,
// including ones sharing a bucket, a cached asset, or a shim name --
// the shared state that touches (the download cache, the shim master
// file) is synchronized internally.
func InstallAll(names []string) map[string]error {
	return runConcurrent(names, defaultConcurrency(), func(name string) error {
		_, err := Install(name)
		return err
	})
}

// runConcurrent runs fn(name) for every name with at most concurrency
// running at once, returning each name's error (nil on success).
func runConcurrent(names []string, concurrency int, fn func(string) error) map[string]error {
	results := make(map[string]error, len(names))
	if len(names) == 0 {
		return results
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > len(names) {
		concurrency = len(names)
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	for _, name := range names {
		wg.Add(1)
		sem <- struct{}{}
		go func(name string) {
			defer wg.Done()
			defer func() { <-sem }()
			err := fn(name)
			mu.Lock()
			results[name] = err
			mu.Unlock()
		}(name)
	}
	wg.Wait()
	return results
}
