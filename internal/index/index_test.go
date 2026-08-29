package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/paths"
)

func withTempRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("GOOP_HOME", root)
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "localappdata"))
	return root
}

// The index is fetched through goop's downloader, so file:// works and a
// network share needs no different configuration from an internal HTTP
// server. This is also what lets the test run without a network.
func TestUpdate_FromFileURL(t *testing.T) {
	withTempRoot(t)

	src := filepath.Join(t.TempDir(), "index.json")
	body := `{"profiles":{"baseline.tool":["srecord","git"],"ide":["vscode"]}}`
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := paths.SetConfiguredIndex("file:///" + filepath.ToSlash(src)); err != nil {
		t.Fatal(err)
	}

	d, err := Update()
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(d.Profiles) != 2 {
		t.Fatalf("got %d profiles, want 2", len(d.Profiles))
	}

	// Published order is preserved: for a profile of alternatives the
	// first entry is the default, and sorting would pick a different one.
	apps, ok := Apps("baseline.tool")
	if !ok || len(apps) != 2 || apps[0] != "srecord" || apps[1] != "git" {
		t.Errorf("Apps = %v, %v; want [srecord git] as published, true", apps, ok)
	}
	if got := Names(); len(got) != 2 || got[0] != "baseline.tool" {
		t.Errorf("Names = %v", got)
	}
}

// A machine that cannot reach the index keeps working off the last good
// copy. Losing every profile because a server is down would be worse than
// serving a slightly stale list.
func TestLoad_UsesCacheWhenSourceIsGone(t *testing.T) {
	withTempRoot(t)

	src := filepath.Join(t.TempDir(), "index.json")
	if err := os.WriteFile(src, []byte(`{"profiles":{"ide":["vscode"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := paths.SetConfiguredIndex("file:///" + filepath.ToSlash(src)); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(); err == nil {
		t.Error("updating from a source that is gone should report an error")
	}
	if apps, ok := Apps("ide"); !ok || len(apps) != 1 {
		t.Errorf("the cache should still serve after a failed update, got %v %v", apps, ok)
	}
}

// A publish that produces invalid JSON must not replace a working cache.
func TestUpdate_BadJSONLeavesCacheIntact(t *testing.T) {
	withTempRoot(t)

	src := filepath.Join(t.TempDir(), "index.json")
	if err := os.WriteFile(src, []byte(`{"profiles":{"ide":["vscode"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := paths.SetConfiguredIndex("file:///" + filepath.ToSlash(src)); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(src, []byte(`{"profiles": oops`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(); err == nil {
		t.Error("a malformed index should be reported, not accepted")
	}
	if apps, ok := Apps("ide"); !ok || len(apps) != 1 {
		t.Errorf("a bad publish overwrote the good cache: %v %v", apps, ok)
	}
}

func TestUpdate_RequiresConfiguredURL(t *testing.T) {
	withTempRoot(t)
	if _, err := Update(); err == nil {
		t.Error("updating with no index configured should say so")
	}
}
