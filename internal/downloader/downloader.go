// Package downloader fetches manifest URLs with mandatory hash
// verification before the result is trusted (FR-40), caching by content
// hash so a repeated install or a shared dependency doesn't re-fetch.
// Large files are fetched over multiple concurrent HTTP Range requests
// when the server supports it (TR-03: native Range requests, replacing
// Scoop's dependency on aria2 for the same purpose); Get itself is safe
// to call concurrently for different packages (A1), including for the
// same URL/hash from two packages at once.
package downloader

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TanguyBaudoin/goop/internal/auth"
	"github.com/TanguyBaudoin/goop/internal/manifest"
)

// httpClient bounds connection setup so a stalled/non-responding host
// fails with a clear error instead of hanging indefinitely -- Go's
// default client has no timeout at all. The overall per-download timeout
// (generous, since real packages can be hundreds of MB) is applied via
// context in fetch, separately from these connection-phase timeouts.
// auth.Transport is the sole place a per-host credential (FR-30) is
// ever added to a request -- every request this package makes, chunked
// or not, goes through it.
var httpClient = &http.Client{
	Transport: &auth.Transport{Base: &http.Transport{
		Proxy:                 resolveProxy,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
		MaxIdleConnsPerHost:   8, // enough for one file's chunks plus a couple of concurrent package downloads
	}},
}

// overallTimeout bounds an entire download (connect through last byte of
// body), so a connection that stalls partway through streaming still
// gets cut off instead of hanging forever.
const overallTimeout = 1 * time.Hour

const (
	// minChunkedSize is the threshold below which chunked downloading
	// isn't worth its own overhead (extra HEAD request, connection
	// setup per chunk).
	minChunkedSize  = 8 * 1024 * 1024
	numChunks       = 4
	maxChunkRetries = 3 // per-chunk retries on transient failure before falling back to serial
)

// downloadLocks serializes concurrent Get calls that target the exact
// same cache entry (same digest+filename -- e.g. two packages that
// happen to share a dependency's exact asset) so they don't race
// writing the same temp file; calls for different entries proceed
// fully in parallel.
var downloadLocks sync.Map // cachePath -> *sync.Mutex

func lockFor(cachePath string) func() {
	v, _ := downloadLocks.LoadOrStore(cachePath, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// OnDownloadStart/OnDownloadProgress/OnDownloadDone let the CLI render
// live progress bars (id is stable for one download's lifetime; label is
// a short human-readable name; total is -1 if the server didn't report
// Content-Length). OnDownloadProgress fires on a fixed timer
// (progressInterval), never once per Write, so hooking it costs nothing
// on the transfer's hot path -- the byte counter itself is a plain
// atomic add either way, hooked or not.
var (
	OnDownloadStart    func(id, label string)
	OnDownloadProgress func(id string, downloaded, total int64)
	OnDownloadDone     func(id string)
)

const progressInterval = 150 * time.Millisecond

// tracker accumulates bytes written for one download (possibly from
// several concurrent Range-chunk goroutines sharing the same tracker)
// and throttles OnDownloadProgress to progressInterval regardless of how
// many writers are feeding it.
type tracker struct {
	id       string
	total    int64
	n        atomic.Int64
	mu       sync.Mutex
	lastEmit time.Time
}

func startTracker(id, label string, total int64) *tracker {
	if OnDownloadStart != nil {
		OnDownloadStart(id, label)
	}
	return &tracker{id: id, total: total}
}

func (t *tracker) add(delta int64) {
	cur := t.n.Add(delta)
	if OnDownloadProgress == nil {
		return
	}
	t.mu.Lock()
	now := time.Now()
	emit := now.Sub(t.lastEmit) >= progressInterval
	if emit {
		t.lastEmit = now
	}
	t.mu.Unlock()
	if emit {
		OnDownloadProgress(t.id, cur, t.total)
	}
}

func (t *tracker) finish() {
	if OnDownloadProgress != nil {
		OnDownloadProgress(t.id, t.n.Load(), t.total)
	}
	if OnDownloadDone != nil {
		OnDownloadDone(t.id)
	}
}

// trackingWriter reports every write through to a tracker while passing
// bytes straight on to w.
type trackingWriter struct {
	w io.Writer
	t *tracker
}

func (tw *trackingWriter) Write(p []byte) (int, error) {
	n, err := tw.w.Write(p)
	if n > 0 {
		tw.t.add(int64(n))
	}
	return n, err
}

// Get downloads url (unless a validated copy is already cached),
// verifies it against expectedHash, and returns the local path of the
// verified file. filename is used only to make the cache entry
// recognizable; content identity comes from the hash.
func Get(cacheDir, url, filename, expectedHash string) (string, error) {
	parsed, err := manifest.ParseHash(expectedHash)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	cachePath := filepath.Join(cacheDir, parsed.Digest[:16]+"-"+filename)

	unlock := lockFor(cachePath)
	defer unlock()

	if verifyFile(cachePath, parsed) == nil {
		return cachePath, nil
	}

	tmp := cachePath + ".download"
	if err := fetch(url, tmp, filename); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("download %s: %w", url, err)
	}

	if err := verifyFile(tmp, parsed); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("download %s: %w", url, err)
	}

	if err := os.Rename(tmp, cachePath); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("finalize download %s: %w", url, err)
	}
	return cachePath, nil
}

// FetchUnverified downloads url to destPath as-is, through the same
// authenticated, timeout-bounded client as Get, but with no hash to
// check -- for content that doesn't come with a pinned hash to verify
// against, like a Git-less bucket's archive (FR-21). Package assets
// must always go through Get instead (FR-40).
func FetchUnverified(url, destPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), overallTimeout)
	defer cancel()
	if err := fetchSerial(ctx, url, destPath, filepath.Base(destPath)); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	return nil
}

// uncPrefix is the leading separator pair that marks a UNC path
// (\servershare). Built from os.PathSeparator rather than written as
// a literal so the source carries no bare backslash escapes.
var uncPrefix = string(os.PathSeparator) + string(os.PathSeparator)

// IsFileURL reports whether rawURL names a local file source rather than
// something to fetch over the network.
func IsFileURL(rawURL string) bool {
	return strings.HasPrefix(strings.ToLower(rawURL), "file://")
}

// IsMachineLocalFileURL reports whether rawURL is a file:// URL naming a
// path that only exists on this machine -- a drive-letter path such as
// file:///C:/tools/jdk.zip. A UNC URL (file://server/share/...) is not
// machine-local: it resolves the same way from any host that can reach
// the share, which is what makes it usable in a shared lockfile.
func IsMachineLocalFileURL(rawURL string) bool {
	if !IsFileURL(rawURL) {
		return false
	}
	p, err := fileURLToPath(rawURL)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(p, uncPrefix)
}

// fileURLToPath converts a file:// URL to a local filesystem path.
//
// Parsed as a URL rather than string-sliced, which is what makes the two
// forms that matter work. url.Path is percent-decoded, so a share called
// "Program Files" survives; and a non-empty url.Host is a UNC authority,
// so file://server/share/x.zip becomes \server\share\x.zip instead of
// the relative path "server\share\x.zip" that slicing produced -- the
// standard UNC form was the one portable spelling, and it was the one
// that silently did the wrong thing.
func fileURLToPath(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse file URL %q: %w", rawURL, err)
	}
	if !strings.EqualFold(u.Scheme, "file") {
		return "", fmt.Errorf("not a file URL: %q", rawURL)
	}

	// file://localhost/C:/... is spelled with a host but means "here".
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return uncPrefix + u.Host + filepath.FromSlash(u.Path), nil
	}

	p := u.Path
	// file:///C:/... arrives as /C:/... -- drop the leading separator.
	if len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p), nil
}

// fetchFile copies a local file (from a file:// URL) to dest. The copy
// is still hash-verified by the caller, so a local source is trusted no
// further than a remote one.
func fetchFile(rawURL, dest string) error {
	src, err := fileURLToPath(rawURL)
	if err != nil {
		return err
	}
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.Create(dest)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(w, r)
	closeErr := w.Close()
	if copyErr != nil {
		// Leave no half-written file behind for the next run to trip on.
		os.Remove(dest)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(dest)
	}
	return closeErr
}

// fetch downloads url to dest. For the common case (most manifest
// assets are a few MB) this is exactly one request, same as a plain
// sequential download -- there's no separate probe request adding
// latency to every download just to learn whether chunking might help.
// Only when the server's very first response reveals a large,
// Range-capable file does it abandon that (still-unread, so cheap to
// discard) response and switch to fetchChunked.
func fetch(url, dest, label string) error {
	if IsFileURL(url) {
		return fetchFile(url, dest)
	}
	ctx, cancel := context.WithTimeout(context.Background(), overallTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// auth.Transport resolves credentials fresh per request based on
	// that request's own target host, including on a redirect (each hop
	// is a separate RoundTrip call) -- so a redirect to a different host
	// never carries the original host's Authorization along (TR-05).
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}

	if resp.ContentLength >= minChunkedSize && resp.Header.Get("Accept-Ranges") == "bytes" {
		resp.Body.Close() // nothing read yet -- cheap to abandon and switch strategies
		size := resp.ContentLength
		t := startTracker(dest, label, size)
		err := fetchChunked(ctx, url, dest, size, t)
		t.finish()
		if err == nil {
			return nil
		}
		// A chunk's connection dropped, the server lied about Range
		// support mid-transfer, etc.: fall back to a fresh plain fetch
		// rather than surfacing an error a simpler download wouldn't
		// have hit. Its own tracker starts the bar over from zero under
		// the same id -- a rare path, not worth extra bookkeeping to
		// avoid a visual reset.
		return fetchSerial(ctx, url, dest, label)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	t := startTracker(dest, label, resp.ContentLength)
	defer t.finish()
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(&trackingWriter{w: f, t: t}, resp.Body)
	return err
}

// fetchChunked downloads url in numChunks concurrent byte-range
// requests, writing each directly to its offset in dest (TR-03). All
// chunks report through the same tracker, so progress reflects combined
// throughput rather than one chunk at a time.
func fetchChunked(ctx context.Context, url, dest string, size int64, t *tracker) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		return err
	}

	chunkSize := (size + numChunks - 1) / numChunks
	var wg sync.WaitGroup
	errCh := make(chan error, numChunks)

	for i := 0; i < numChunks; i++ {
		start := int64(i) * chunkSize
		end := start + chunkSize - 1
		if end >= size {
			end = size - 1
		}
		if start > end {
			continue
		}
		wg.Add(1)
		go func(start, end int64) {
			defer wg.Done()
			if err := fetchRangeWithRetry(ctx, url, f, start, end, t); err != nil {
				errCh <- err
			}
		}(start, end)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func fetchRange(ctx context.Context, url string, f *os.File, start, end int64, t *tracker) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("range request got status %s, want 206", resp.Status)
	}

	_, err = io.Copy(&trackingWriter{w: &offsetWriter{f: f, offset: start}, t: t}, resp.Body)
	return err
}

// fetchRangeWithRetry wraps fetchRange with retries on transient errors,
// using exponential backoff (base 1s, max 30s). A chunk that fails all
// retries propagates the last error up; the caller (fetchChunked) falls
// back to fetchSerial in that case rather than aborting the whole download.
func fetchRangeWithRetry(ctx context.Context, url string, f *os.File, start, end int64, t *tracker) error {
	var lastErr error
	for attempt := range maxChunkRetries {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			// Add jitter: ±25%
			jitter := time.Duration(float64(backoff) * (0.75 + 0.5*rand.Float64()))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(jitter):
			}
		}
		if err := fetchRange(ctx, url, f, start, end, t); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("chunk %d-%d failed after %d retries: %w", start, end, maxChunkRetries, lastErr)
}

// offsetWriter writes sequentially to f starting at a fixed offset, so
// io.Copy can stream a range's body straight to its place in the file.
type offsetWriter struct {
	f      *os.File
	offset int64
}

func (w *offsetWriter) Write(p []byte) (int, error) {
	n, err := w.f.WriteAt(p, w.offset)
	w.offset += int64(n)
	return n, err
}

func fetchSerial(ctx context.Context, url, dest, label string) error {
	if IsFileURL(url) {
		return fetchFile(url, dest)
	}

	var lastErr error
	for attempt := range maxChunkRetries {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			jitter := time.Duration(float64(backoff) * (0.75 + 0.5*rand.Float64()))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(jitter):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			lastErr = err
			continue
		}

		// auth.Transport resolves credentials fresh per request based on
		// that request's own target host, including on a redirect (each hop
		// is a separate RoundTrip call) -- so a redirect to a different host
		// never carries the original host's Authorization along (TR-05).
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("unexpected status %s", resp.Status)
			continue
		}

		t := startTracker(dest, label, resp.ContentLength)
		f, err := os.Create(dest)
		if err != nil {
			resp.Body.Close()
			t.finish()
			lastErr = err
			continue
		}

		_, copyErr := io.Copy(&trackingWriter{w: f, t: t}, resp.Body)
		f.Close()
		resp.Body.Close()
		t.finish()

		if copyErr != nil {
			lastErr = copyErr
			os.Remove(dest)
			continue
		}
		return nil
	}
	return fmt.Errorf("download failed after %d retries: %w", maxChunkRetries, lastErr)
}

func verifyFile(path string, parsed manifest.ParsedHash) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h, err := parsed.NewHasher()
	if err != nil {
		return err
	}
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != parsed.Digest {
		return fmt.Errorf("hash mismatch: got %s:%s, want %s:%s", parsed.Algo, got, parsed.Algo, parsed.Digest)
	}
	return nil
}
