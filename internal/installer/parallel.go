package installer

import (
	"errors"
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
func InstallAll(names []string, profileName string) map[string]error {
	return runConcurrent(names, defaultConcurrency(), func(name string) error {
		_, err := InstallInto(name, profileName)
		return err
	})
}

// UninstallAll uninstalls every installed app concurrently. Returns
// a map of name -> error (nil on success for each). If no apps are
// installed it returns nil rather than an empty map, so callers can
// distinguish "nothing to do" from "all succeeded".
func UninstallAll(force bool) map[string]error {
	records, err := List()
	if err != nil {
		return map[string]error{"": err}
	}
	names := make([]string, len(records))
	for i, r := range records {
		names[i] = r.Name
	}
	if len(names) == 0 {
		return nil
	}
	// Deliberately sequential, unlike InstallAll. Uninstall cascades to
	// dependent packages, so two goroutines routinely target the same
	// app at once -- one directly, one through another app's cascade.
	// Removing the same directory twice concurrently fails on Windows
	// with "Access is denied" partway through, which is how it showed up.
	// Uninstall is disk work, not network work, so bounded concurrency
	// bought little here and cost correctness.
	results := make(map[string]error, len(names))
	for _, name := range names {
		// A package a previous cascade already removed has still reached
		// the requested state; reporting it turned a fully successful
		// "remove everything" into a non-zero exit.
		if err := Uninstall(name, force); err != nil && !errors.Is(err, ErrNotInstalled) {
			results[name] = err
			continue
		}
		results[name] = nil
	}
	return results
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
