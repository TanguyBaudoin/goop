package bucket

import (
	"fmt"
	"sort"
	"sync"

	"github.com/TanguyBaudoin/goop/internal/manifest"
)

// App is one package a bucket offers.
//
// Version and Binaries are only filled when the caller asks for detail:
// they require decoding the manifest, and a bucket the size of `main`
// has well over a thousand.
type App struct {
	Bucket   string
	Name     string
	Version  string
	Binaries []string
	Err      error // the manifest exists but could not be decoded
}

// Apps lists what a bucket offers.
//
// With detail false this reads directory entries and nothing else, so it
// answers instantly whatever the bucket's size -- a manifest's filename
// *is* the package name, which is what makes the cheap path possible.
// With detail true every manifest is decoded, concurrently, which is the
// only way to report versions.
//
// `goop search .` could already approximate this, but badly: it is a
// regex match across every bucket at once with no way to scope to one,
// and it decodes unconditionally -- 7.4s and 5088 lines on a real
// machine when the question was "what is in main".
func Apps(bucketName string, detail bool) ([]App, error) {
	if err := requireConfigured(bucketName); err != nil {
		return nil, err
	}
	names, err := ManifestNames(bucketName)
	if err != nil {
		return nil, fmt.Errorf("read bucket %q: %w", bucketName, err)
	}
	sort.Strings(names)

	out := make([]App, len(names))
	for i, n := range names {
		out[i] = App{Bucket: bucketName, Name: n}
	}
	if !detail {
		return out, nil
	}

	// Decoding is per-file and independent, so it parallelises cleanly.
	// Bounded rather than spawning sixteen hundred goroutines at once.
	const workers = 8
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range out {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()

			m, err := readManifest(bucketName, out[i].Name)
			if err != nil {
				// Reported against the package rather than failing the
				// whole listing: one unparseable manifest must not hide
				// the other sixteen hundred.
				out[i].Err = err
				return
			}
			out[i].Version = m.Version
			out[i].Binaries = binaryNames(m, out[i].Name)
		}(i)
	}
	wg.Wait()
	return out, nil
}

// AllApps lists what every configured bucket offers, in priority order --
// the order that decides which one wins a name collision.
func AllApps(detail bool) ([]App, error) {
	entries, err := List()
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no buckets configured; add one with `goop bucket add <name> <url>`")
	}
	var out []App
	for _, e := range entries {
		apps, err := Apps(e.Name, detail)
		if err != nil {
			return nil, err
		}
		out = append(out, apps...)
	}
	return out, nil
}

// requireConfigured refuses a bucket goop does not have, naming the ones
// it does -- a typo should not read as an empty bucket.
func requireConfigured(name string) error {
	entries, err := List()
	if err != nil {
		return err
	}
	var have []string
	for _, e := range entries {
		if e.Name == name {
			return nil
		}
		have = append(have, e.Name)
	}
	if len(have) == 0 {
		return fmt.Errorf("no buckets configured; add one with `goop bucket add <name> <url>`")
	}
	return fmt.Errorf("bucket %q is not configured (there is: %v)", name, have)
}

// binaryNames lists the commands a manifest would put on PATH, resolved
// for this host's architecture -- the useful half of a listing, since
// that is what you actually type.
func binaryNames(m manifest.Manifest, appName string) []string {
	archKey, err := manifest.HostArchKey()
	if err != nil {
		return nil
	}
	resolved, err := m.Resolve(appName, archKey)
	if err != nil {
		return nil
	}
	var out []string
	for _, b := range resolved.Bin {
		if b.Name != "" {
			out = append(out, b.Name)
		}
	}
	return out
}
