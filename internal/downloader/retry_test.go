package downloader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// A definitive status is the server's answer, not a hiccup: retrying it
// only delays the message. A 5xx or 429 is worth another attempt.
func TestFetchSerial_RetriesOnlyTransientStatuses(t *testing.T) {
	defer func(orig time.Duration) { backoffUnit = orig }(backoffUnit)
	backoffUnit = time.Millisecond
	cases := []struct {
		name         string
		status       int
		wantAttempts int32
	}{
		{"not found is final", http.StatusNotFound, 1},
		{"unauthorized is final", http.StatusUnauthorized, 1},
		{"forbidden is final", http.StatusForbidden, 1},
		{"server error is retried", http.StatusInternalServerError, maxChunkRetries},
		{"too many requests is retried", http.StatusTooManyRequests, maxChunkRetries},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var attempts int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&attempts, 1)
				w.WriteHeader(c.status)
			}))
			defer srv.Close()

			dest := filepath.Join(t.TempDir(), "out.bin")
			if err := fetchSerial(context.Background(), srv.URL, dest, "test"); err == nil {
				t.Fatal("expected an error")
			}
			if got := atomic.LoadInt32(&attempts); got != c.wantAttempts {
				t.Errorf("server saw %d request(s), want %d", got, c.wantAttempts)
			}
		})
	}
}

func TestRetryable(t *testing.T) {
	if retryable(&statusError{code: 404, status: "404 Not Found"}) {
		t.Error("404 should not be retried")
	}
	if !retryable(&statusError{code: 503, status: "503 Service Unavailable"}) {
		t.Error("503 should be retried")
	}
	if !retryable(context.DeadlineExceeded) {
		t.Error("a transport-level error should be retried")
	}
}
