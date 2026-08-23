package installer

import (
	"fmt"
	"sync"

	"goop/internal/bucket"
)

// UpdateResult is what Update did for one app.
type UpdateResult struct {
	Updated    bool
	OldVersion string
	NewVersion string
	Held       bool // pinned via `goop hold`; left untouched
}

// Update re-resolves appName against the bucket it was originally
// installed from (falling back to normal priority search if that's
// unknown, e.g. for an app imported without one) and installs the
// current version if it differs from what's installed (EXF-05). Reuses
// the same atomic install pipeline as Install/Sync -- an update is just
// an install where the target happens to already exist at an older
// version, not a separate code path.
func Update(appName string) (UpdateResult, error) {
	rec, ok := readCurrentRecord(appName)
	if !ok {
		return UpdateResult{}, fmt.Errorf("%s is not installed", appName)
	}
	// A held app is pinned on purpose: skip it rather than fail, so
	// `goop update` with no arguments stays a one-command operation
	// instead of erroring out on every held package.
	if rec.Hold {
		return UpdateResult{Updated: false, OldVersion: rec.Version, NewVersion: rec.Version, Held: true}, nil
	}
	if rec.Bucket == "maven" {
		return UpdateResult{}, fmt.Errorf("%s was installed via a Maven coordinate, not a bucket; install the new version directly with `goop install maven:...`", appName)
	}

	// A renamed or removed bucket (e.g. `goop bucket remove <name>`)
	// shouldn't strand every app recorded against it -- confirmed for
	// real: removing a duplicate bucket entry left three genuinely
	// still-installed apps unable to update at all, hard-failing on a
	// name that no longer resolves to anything, when a normal priority
	// search across whatever's configured now would have found the
	// exact same manifest just fine. Same fallback already applied to
	// an empty recorded bucket (e.g. an imported app), just extended to
	// cover "recorded but no longer configured" too.
	spec := appName
	if rec.Bucket != "" && configuredBucket(rec.Bucket) {
		spec = rec.Bucket + "/" + appName
	}
	newRec, err := installSpec(spec, nil, true)
	if err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{
		Updated:    newRec.Version != rec.Version,
		OldVersion: rec.Version,
		NewVersion: newRec.Version,
	}, nil
}

// configuredBucket reports whether name is among the currently
// configured buckets (bucket.List) -- a plain linear scan, since the
// list is at most a handful of entries and this only runs once per
// Update call, not per app in a batch.
func configuredBucket(name string) bool {
	entries, err := bucket.List()
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name == name {
			return true
		}
	}
	return false
}

// UpdateAll updates every name given (or every installed app, if names
// is empty) concurrently (A1), the same bounded pattern InstallAll and
// Sync use.
func UpdateAll(names []string) (map[string]UpdateResult, map[string]error, error) {
	if len(names) == 0 {
		records, err := List()
		if err != nil {
			return nil, nil, err
		}
		for _, r := range records {
			names = append(names, r.Name)
		}
	}

	concurrency := defaultConcurrency()
	if concurrency > len(names) {
		concurrency = len(names)
	}
	if concurrency < 1 {
		concurrency = 1
	}

	results := make(map[string]UpdateResult, len(names))
	errs := make(map[string]error)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	for _, name := range names {
		wg.Add(1)
		sem <- struct{}{}
		go func(name string) {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := Update(name)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[name] = err
				return
			}
			results[name] = res
		}(name)
	}
	wg.Wait()
	return results, errs, nil
}
