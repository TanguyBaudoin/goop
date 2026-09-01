package installer

import (
	"fmt"

	"github.com/TanguyBaudoin/goop/internal/bucket"
	"github.com/TanguyBaudoin/goop/internal/downloader"
	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/paths"
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
	return download(spec, false)
}

// download is Download, optionally without the per-asset log line.
//
// Prefetch runs several of these at once, and their lines interleaved
// into an unreadable block that then repeated itself as each package was
// installed -- the same URL announced twice, looking like it downloaded
// twice. The fetch phase reports itself once instead, and the progress
// bars show which transfers are live.
func download(spec string, quiet bool) (DownloadResult, error) {
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
		if !quiet {
			Logf("%s: downloading %s", parsed.Name, assetURL)
		}
		if _, err := downloader.Get(paths.Cache(), assetURL, fragName, resolved.Hashes[i]); err != nil {
			return DownloadResult{}, err
		}
	}
	return DownloadResult{Name: parsed.Name, Version: resolved.Version, Files: len(resolved.URLs)}, nil
}
