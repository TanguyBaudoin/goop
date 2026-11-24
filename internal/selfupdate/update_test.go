package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/paths"
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

// installedFlat puts a binary at <root>in\goop.exe with bin as a real
// directory -- the layout every install had before goop became a package
// -- so an update from it exercises the migration rather than skipping
// straight to the versioned shape.
func installedFlat(t *testing.T, version string) string {
	t.Helper()
	bin := paths.Bin()
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return buildStub(t, bin, version)
}

// reportedVersion runs a binary and returns the version line it prints.
func reportedVersion(t *testing.T, exe string) string {
	t.Helper()
	out, err := exec.Command(exe, "version").Output()
	if err != nil {
		t.Fatalf("running %s: %v", exe, err)
	}
	return strings.TrimSpace(string(out))
}

// The direction that matters, and the one that cannot be tested against
// real releases: a published version newer than the one running.
func TestUpdateAt_Upgrades(t *testing.T) {
	t.Setenv("GOOP_HOME", t.TempDir())
	base := serveRelease(t, "0.9.9")
	exe := installedFlat(t, "0.1.0")

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

	// What PATH points at must be the new version. This is the assertion
	// that matters: bin is the stable entry point, and an update that
	// installs a new version without repointing it has changed nothing
	// as far as a user's shell is concerned.
	binExe := filepath.Join(paths.Bin(), "goop.exe")
	if got := reportedVersion(t, binExe); got != "goop 0.9.9" {
		t.Errorf("%s reports %q, want goop 0.9.9", binExe, got)
	}

	// And it got there by repointing a junction rather than overwriting
	// a file: both versions must still be installed, so the one being
	// replaced stays a rollback target instead of becoming a renamed
	// corpse the way bin\goop.exe.old used to.
	for _, v := range []string{"0.1.0", "0.9.9"} {
		vp := filepath.Join(paths.AppVersion("goop", v), "goop.exe")
		if _, err := os.Stat(vp); err != nil {
			t.Errorf("goop %s should still be installed at %s: %v", v, vp, err)
		}
	}
	if _, err := os.Stat(filepath.Join(paths.Bin(), "goop.exe.old")); !os.IsNotExist(err) {
		t.Error("the package layout must not leave a .old behind")
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

// updateAt runs both phases against an explicit target and release
// source, the way `goop self-update` does with a prompt between them.
func updateAt(exe, base, currentVersion string, force bool) (Result, error) {
	p, err := checkAt(exe, base, currentVersion)
	if err != nil {
		return Result{}, err
	}
	return p.Apply(force)
}
