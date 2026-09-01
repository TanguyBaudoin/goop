package downloader

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// rangeAwareServer serves content via http.ServeContent, which natively
// implements Range/Accept-Ranges -- the same contract a real CDN gives
// goop, without hand-rolling range logic on both sides of the test.
func rangeAwareServer(t *testing.T, content []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "asset.bin", time.Time{}, bytes.NewReader(content))
	}))
}

func TestGet_ChunkedDownloadMatchesContent(t *testing.T) {
	// Above minChunkedSize so this actually exercises the chunked path,
	// not the serial fallback.
	content := bytes.Repeat([]byte("goop-parallel-download-test-data-"), 300000) // ~10MB
	if len(content) < minChunkedSize {
		t.Fatalf("test content (%d bytes) must exceed minChunkedSize (%d)", len(content), minChunkedSize)
	}
	srv := rangeAwareServer(t, content)
	defer srv.Close()

	cacheDir := t.TempDir()
	hash := hashOf(content)

	path, err := Get(cacheDir, srv.URL, "asset.bin", hash)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("downloaded content mismatch: got %d bytes, want %d bytes", len(got), len(content))
	}
}

func TestGet_FallsBackWhenServerDoesNotSupportRange(t *testing.T) {
	content := bytes.Repeat([]byte("x"), minChunkedSize+1024) // large enough to prefer chunking...
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ...but this handler ignores Range entirely and never
		// advertises Accept-Ranges, like a plain static file server.
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			w.Write(content)
		}
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	hash := hashOf(content)

	path, err := Get(cacheDir, srv.URL, "asset.bin", hash)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("downloaded content mismatch after falling back to serial download")
	}
}

func TestGet_SmallFileSkipsChunking(t *testing.T) {
	content := []byte("small file, well under the chunking threshold")
	srv := rangeAwareServer(t, content)
	defer srv.Close()

	cacheDir := t.TempDir()
	path, err := Get(cacheDir, srv.URL, "small.txt", hashOf(content))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, content) {
		t.Fatal("small file content mismatch")
	}
}

func TestGet_CachesAndSkipsRedownload(t *testing.T) {
	var hits int32
	content := []byte("cache me")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.ServeContent(w, r, "f.txt", time.Time{}, bytes.NewReader(content))
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	hash := hashOf(content)

	if _, err := Get(cacheDir, srv.URL, "f.txt", hash); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(cacheDir, srv.URL, "f.txt", hash); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("server got %d requests, want 1 (second Get should hit cache)", got)
	}
}

func TestGet_HashMismatchIsRejected(t *testing.T) {
	content := []byte("real content")
	srv := rangeAwareServer(t, content)
	defer srv.Close()

	cacheDir := t.TempDir()
	wrongHash := hashOf([]byte("different content"))
	if _, err := Get(cacheDir, srv.URL, "f.txt", wrongHash); err == nil {
		t.Fatal("expected hash mismatch error")
	}
}

// TestGet_ConcurrentDifferentPackagesRunInParallel is the actual A1
// proof: N distinct downloads issued concurrently must all complete
// correctly, and do so faster than N times a single download's latency.
func TestGet_ConcurrentDifferentPackagesRunInParallel(t *testing.T) {
	const n = 6
	const perRequestDelay = 150 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(perRequestDelay)
		content := []byte("payload-" + r.URL.Query().Get("id"))
		http.ServeContent(w, r, "f.bin", time.Time{}, bytes.NewReader(content))
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	start := time.Now()

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			content := []byte(fmt.Sprintf("payload-%d", i))
			url := fmt.Sprintf("%s?id=%d", srv.URL, i)
			_, err := Get(cacheDir, url, fmt.Sprintf("f%d.bin", i), hashOf(content))
			errs[i] = err
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	for i, err := range errs {
		if err != nil {
			t.Fatalf("download %d: %v", i, err)
		}
	}
	// What this proves is "concurrent, not sequential". Sequential cannot
	// finish in less than n*perRequestDelay, so any bar below that is
	// evidence of overlap; the margin only decides how much CPU
	// contention the test tolerates before calling a real result a
	// failure.
	//
	// It used to demand half of sequential, which a shared CI runner
	// missed at 548ms against a 450ms bar -- a run that had in fact
	// overlapped six 150ms downloads into well under the 900ms they would
	// have taken one at a time. Two thirds keeps the claim (still
	// unreachable sequentially, still 4x the ideal 150ms) without failing
	// on a busy machine.
	//
	// The message quoted n*perRequestDelay while the check used half of
	// it, so the failure read as self-contradictory -- "took 548ms,
	// expected well under 900ms" -- and sent the next reader looking for
	// an inverted comparison. It reports the bar it actually applies now.
	limit := n * perRequestDelay * 2 / 3
	if elapsed > limit {
		t.Fatalf("took %v, want under %v -- sequential would need %v, so this did not overlap",
			elapsed, limit, n*perRequestDelay)
	}
}
