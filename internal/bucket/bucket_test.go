package bucket

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveExt(t *testing.T) {
	tests := map[string]string{
		"https://example.com/bucket.zip":                           ".zip",
		"https://example.com/bucket.tar.gz":                        ".tar.gz",
		"https://example.com/bucket.tgz":                           ".tgz", // matched by the .tgz branch but normalized differently below
		"https://codeload.github.com/org/repo/zip/refs/heads/main": ".zip",
		"https://github.com/org/repo.git":                          "",
		"https://github.com/org/repo":                              "",
	}
	for url, want := range tests {
		got := archiveExt(url)
		if want == ".tgz" {
			want = ".tar.gz" // .tgz and .tar.gz share a detection branch
		}
		if got != want {
			t.Errorf("archiveExt(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestHoistSingleSubdir_HoistsWrapper(t *testing.T) {
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "repo-main")
	if err := os.MkdirAll(filepath.Join(wrapper, "bucket"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wrapper, "bucket", "app.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wrapper, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := hoistSingleSubdir(dir); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
		t.Fatal("wrapper directory should have been removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "bucket", "app.json")); err != nil {
		t.Fatalf("expected hoisted bucket/app.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Fatalf("expected hoisted README.md: %v", err)
	}
}

func TestHoistSingleSubdir_LeavesFlatLayoutAlone(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bucket"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := hoistSingleSubdir(dir); err != nil {
		t.Fatal(err)
	}
	// Two entries at the root (bucket/ and README.md): nothing to hoist.
	if _, err := os.Stat(filepath.Join(dir, "bucket")); err != nil {
		t.Fatalf("bucket/ should still be present: %v", err)
	}
}
