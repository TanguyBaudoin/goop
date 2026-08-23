package installer

import (
	"fmt"

	"goop/internal/bucket"
	"goop/internal/downloader"
	"goop/internal/manifest"
	"goop/internal/paths"
)

// DownloadResult is what Download fetched for one app.
type DownloadResult struct {
	Name    string
	Version string
	Files   int
}

// Download fetches and hash-verifies every asset spec's manifest names,
// leaving them in the cache without installing anything -- mirrors real
// Scoop's `scoop download`. Useful to prime the cache before an offline
// `sync`, or to confirm a manifest's hashes still match what upstream
// serves before pinning it into a lockfile.
//
// Downloading is all it does: no staging directory, no hooks, no shims.
// downloader.Get already skips anything cached and verified, so
// re-running is cheap.
func Download(spec string) (DownloadResult, error) {
	if err := paths.EnsureLayout(); err != nil {
		return DownloadResult{}, err
	}
	parsed := manifest.ParseSpec(spec)
	_, m, err := bucket.Resolve(parsed)
	if err != nil {
		return DownloadResult{}, err
	}
	archKey, err := manifest.HostArchKey()
	if err != nil {
		return DownloadResult{}, err
	}
	resolved, err := m.Resolve(parsed.Name, archKey)
	if err != nil {
		return DownloadResult{}, err
	}

	for i, rawURL := range resolved.URLs {
		if i >= len(resolved.Hashes) {
			return DownloadResult{}, fmt.Errorf(
				"%s: url[%d] has no hash; refusing to download without verification (FR-40)", parsed.Name, i)
		}
		assetURL, fragName := manifest.SplitURLFragment(rawURL)
		if fragName == "" {
			fragName = basenameWithoutQuery(assetURL)
		}
		Logf("%s: downloading %s", parsed.Name, assetURL)
		if _, err := downloader.Get(paths.Cache(), assetURL, fragName, resolved.Hashes[i]); err != nil {
			return DownloadResult{}, err
		}
	}
	return DownloadResult{Name: parsed.Name, Version: resolved.Version, Files: len(resolved.URLs)}, nil
}
