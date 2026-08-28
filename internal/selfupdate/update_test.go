package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildStub compiles a binary that reports `goop <version>`, which is all
// probeVersion asks of a candidate. Building a real goop would need the
// embedded shim and take far longer, and would not exercise anything
// extra here.
func buildStub(t *testing.T, dir, version string) string {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module stub\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := fmt.Sprintf("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(%q) }\n", "goop "+version)
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "goop.exe")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = src
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the stub: %v\n%s", err, b)
	}
	return out
}

// serveRelease writes goop.exe and checksums.txt into a directory and
// returns a file:// base URL for it. goop's downloader handles file://,
// so the whole update path can be exercised with no network and no
// published release.
func serveRelease(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	exe := buildStub(t, dir, version)

	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	line := hex.EncodeToString(sum[:]) + "  goop.exe\n"
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return "file:///" + filepath.ToSlash(dir)
}

// installed puts a binary where updateAt will find it, standing in for
// the goop.exe an update would replace.
func installed(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	return buildStub(t, dir, version)
}

// The direction that matters, and the one that cannot be tested against
// real releases: a published version newer than the one running.
func TestUpdateAt_Upgrades(t *testing.T) {
	t.Setenv("GOOP_HOME", t.TempDir())
	base := serveRelease(t, "0.9.9")
	exe := installed(t, "0.1.0")

	res, err := updateAt(exe, base, "0.1.0", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.AlreadyCurrent {
		t.Error("reported already current when a newer release was served")
	}
	if res.NewVersion != "0.9.9" {
		t.Errorf("new version = %q, want 0.9.9", res.NewVersion)
	}

	// The binary in place must actually be the new one.
	out, err := exec.Command(exe, "version").Output()
	if err != nil {
		t.Fatalf("running the replaced binary: %v", err)
	}
	if got := string(out); got != "goop 0.9.9\r\n" && got != "goop 0.9.9\n" {
		t.Errorf("the replaced binary reports %q", got)
	}
	// Nothing is holding it open here, so the swap should leave nothing.
	if _, err := os.Stat(exe + oldSuffix); !os.IsNotExist(err) {
		t.Errorf("leftover %s survived a swap that could delete it", exe+oldSuffix)
	}
}

func TestUpdateAt_AlreadyCurrent(t *testing.T) {
	t.Setenv("GOOP_HOME", t.TempDir())
	base := serveRelease(t, "0.2.0")

	// Same bytes as what is served.
	dir := t.TempDir()
	exe := filepath.Join(dir, "goop.exe")
	data, err := os.ReadFile(filepath.Join(filepath.FromSlash(base[len("file:///"):]), "goop.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, data, 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := updateAt(exe, base, "0.2.0", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.AlreadyCurrent {
		t.Error("identical bytes should report already current")
	}
}

// Differing bytes do not mean newer: a local build must not be silently
// replaced by an older release.
func TestUpdateAt_RefusesDowngrade(t *testing.T) {
	t.Setenv("GOOP_HOME", t.TempDir())
	base := serveRelease(t, "0.1.0")
	exe := installed(t, "0.2.0")

	if _, err := updateAt(exe, base, "0.2.0", false); err == nil {
		t.Fatal("expected a downgrade to be refused")
	}
	// It must still be the binary that was there.
	out, _ := exec.Command(exe, "version").Output()
	if got := string(out); got[:len("goop 0.2.0")] != "goop 0.2.0" {
		t.Errorf("the refused update changed the binary: %q", got)
	}

	res, err := updateAt(exe, base, "0.2.0", true)
	if err != nil {
		t.Fatalf("--force should allow it: %v", err)
	}
	if res.NewVersion != "0.1.0" {
		t.Errorf("forced version = %q, want 0.1.0", res.NewVersion)
	}
}
