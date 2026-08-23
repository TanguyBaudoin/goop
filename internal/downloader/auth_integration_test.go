package downloader_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"goop/internal/credstore"
	"goop/internal/downloader"
)

// TestGet_EndToEndAuthentication proves the real wiring, not just each
// package in isolation: a credential stored via the real Windows
// Credential Manager (credstore) is what makes downloader.Get succeed
// against a server that genuinely rejects unauthenticated requests, via
// auth.Transport, which downloader's httpClient already uses. Opt-in
// (touches the real Credential Manager): GOOP_TEST_CREDSTORE=1.
func TestGet_EndToEndAuthentication(t *testing.T) {
	if os.Getenv("GOOP_TEST_CREDSTORE") != "1" {
		t.Skip("set GOOP_TEST_CREDSTORE=1 to run tests that touch the real Windows Credential Manager")
	}

	const token = "goop-e2e-test-token-9f3c7a"
	content := []byte("authenticated payload")
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write(content)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Hostname()
	cacheDir := t.TempDir()

	t.Run("fails without a stored credential", func(t *testing.T) {
		if _, err := downloader.Get(cacheDir, srv.URL, "a.bin", hash); err == nil {
			t.Fatal("expected download to fail against a server requiring auth with no credential stored")
		}
	})

	if err := credstore.Set(host, "bearer", "", token); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { credstore.Delete(host) })

	t.Run("succeeds once the credential is stored", func(t *testing.T) {
		path, err := downloader.Get(cacheDir, srv.URL, "b.bin", hash)
		if err != nil {
			t.Fatalf("download failed with a valid stored credential: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(content) {
			t.Fatalf("content mismatch: got %q, want %q", got, content)
		}
	})

	if err := credstore.Delete(host); err != nil {
		t.Fatal(err)
	}

	t.Run("fails again after the credential is removed", func(t *testing.T) {
		if _, err := downloader.Get(cacheDir, srv.URL, "c.bin", hash); err == nil {
			t.Fatal("expected download to fail again once the credential was removed")
		}
	})
}
