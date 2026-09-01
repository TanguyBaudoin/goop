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

// TestGet_ConcurrentDifferentPackagesRunInParallel is the A1 proof: N
// distinct downloads issued concurrently must all complete correctly,
// and must actually overlap.
//
// The overlap is proved structurally rather than timed. The handler
// blocks every request until n of them are in flight at once, then
// releases them all: downloads that ran one at a time can never reach n,
// so the first one waits out the deadline and the test fails with that
// said plainly. Nothing depends on how fast the machine is.
//
// It used to measure wall-clock time against a fraction of what
// sequential would cost, and failed twice on a shared CI runner -- 548ms
// against a 450ms bar, then 617ms against 600ms -- on runs that had in
// fact overlapped six 150ms downloads. Both were correct results called
// failures, and the second one only happened because the first "fix" was
// to move the number. A threshold tuned against somebody else's hardware
// is not a test.
func TestGet_ConcurrentDifferentPackagesRunInParallel(t *testing.T) {
	const n = 6
	// Not a latency budget: every request is released the instant the
	// nth arrives, so a healthy run never waits at all and machine speed
	// does not matter. It only bounds the failing case -- serialized
	// downloads wait it out once each, so n*gateDeadline is how long a
	// real failure takes to report itself. Kept short enough that it
	// arrives as this test's own message rather than as a suite timeout,
	// which would say nothing about what went wrong.
	const gateDeadline = 5 * time.Second

	var (
		mu       sync.Mutex
		inFlight int
	)
	allArrived := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight == n {
			close(allArrived) // the nth request unblocks every one of them
		}
		mu.Unlock()

		select {
		case <-allArrived:
		case <-time.After(gateDeadline):
			http.Error(w, "requests did not overlap", http.StatusInternalServerError)
			return
		}
		content := []byte("payload-" + r.URL.Query().Get("id"))
		http.ServeContent(w, r, "f.bin", time.Time{}, bytes.NewReader(content))
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
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

	for i, err := range errs {
		if err != nil {
			// The handler answers 500 when it waited out gateDeadline
			// without seeing n requests at once, which is the only way a
			// download fails here -- the payloads and hashes are fixed.
			t.Fatalf("download %d: %v\n"+
				"  a 500 here is the test server reporting that it never saw %d requests at once:\n"+
				"  the downloads ran one at a time", i, err, n)
		}
	}
	// Reaching here means the server saw all n at once and every payload
	// still verified against its own hash.
	mu.Lock()
	defer mu.Unlock()
	if inFlight != n {
		t.Errorf("server saw %d concurrent requests, want %d", inFlight, n)
	}
}
