package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/paths"
)

// The layout change was tested with stub binaries and in isolated roots,
// which leaves the question the stubs cannot answer: does a real goop,
// laid down the way the installer lays it down, survive being migrated
// and still work afterwards?
//
// This builds two real goop binaries, puts the older one in a throwaway
// root in the flat shape install.ps1 produces -- including the
// bin\goop.exe.old corpse the previous self-update left behind -- runs
// the update, and then drives the migrated binary: version, list,
// doctor. Nothing outside the throwaway root is touched, which is the
// only reason this is safe to run at all.
//
// Opt-in because it compiles goop twice:
//
//	$env:GOOP_HARNESS = '1'; go test ./internal/selfupdate/ -run RealBinary -v
func TestRealBinary_MigratesAndStillWorks(t *testing.T) {
	if os.Getenv("GOOP_HARNESS") != "1" {
		t.Skip("GOOP_HARNESS not set; skipping the real-binary migration check")
	}

	root := t.TempDir()
	t.Setenv("GOOP_HOME", root)
	t.Setenv("APPDATA", filepath.Join(root, "appdata"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "localappdata"))

	// The release to update to, served over file:// so no network and no
	// published release are involved.
	relDir := t.TempDir()
	buildRealGoop(t, filepath.Join(relDir, "goop.exe"), "0.9.99")
	writeChecksums(t, relDir)
	base := "file:///" + filepath.ToSlash(relDir)

	// The install as install.ps1 leaves it: a real bin directory with the
	// binary in it, plus the corpse the rename-based self-update left.
	bin := paths.Bin()
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(bin, "goop.exe")
	buildRealGoop(t, exe, "0.9.1")
	corpse := exe + ".old"
	if err := os.WriteFile(corpse, []byte("stale 14MB binary stands in here"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := updateAt(exe, base, "0.9.1", false)
	if err != nil {
		t.Fatalf("updateAt on a real install: %v", err)
	}
	if res.NewVersion != "0.9.99" {
		t.Fatalf("new version = %q, want 0.9.99", res.NewVersion)
	}

	// --- the layout ---
	if _, err := os.Lstat(bin); err != nil {
		t.Fatalf("bin is gone after the migration: %v", err)
	}
	// Not an IsDir() check: Lstat reports a reparse point as a link
	// rather than a directory, which is the whole point of it being one.
	// Readlink succeeding is what says bin became a junction, and the
	// directory still being usable is proven below by running the binary
	// through it.
	if target, err := os.Readlink(bin); err != nil {
		t.Errorf("bin must be a link after migration: %v", err)
	} else if !strings.Contains(strings.ToLower(target), strings.ToLower(filepath.Join("apps", "goop", "0.9.99"))) {
		t.Errorf("bin points at %q, want the 0.9.99 version directory", target)
	}
	for _, v := range []string{"0.9.1", "0.9.99"} {
		if _, err := os.Stat(filepath.Join(paths.AppVersion("goop", v), "goop.exe")); err != nil {
			t.Errorf("goop %s should be installed: %v", v, err)
		}
	}

	// --- and the part the stubs could not answer: it still works ---
	binExe := filepath.Join(bin, "goop.exe")

	if out := run(t, binExe, "version"); !strings.Contains(out, "goop 0.9.99") {
		t.Errorf("the migrated goop reports:\n%s", out)
	}

	// It knows itself, and as a healthy package rather than a broken one.
	out := run(t, binExe, "list")
	if !strings.Contains(out, "goop") {
		t.Errorf("`goop list` should include goop itself:\n%s", out)
	}
	if strings.Contains(out, "broken") {
		t.Errorf("`goop list` reports something broken after migration:\n%s", out)
	}

	// doctor must not flag the manager it is running from. Exit 3 means
	// findings, which is allowed for other reasons in a bare root -- what
	// must not appear is goop itself.
	doctor := runAllowingExit(t, binExe, "doctor")
	for _, line := range strings.Split(doctor, "\n") {
		l := strings.ToLower(line)
		if strings.Contains(l, "goop ") && (strings.Contains(l, "problem") || strings.Contains(l, "current")) {
			t.Errorf("doctor flagged goop itself: %s", strings.TrimSpace(line))
		}
	}

	// `goop uninstall goop` has to be refused rather than half-done.
	if out := runAllowingExit(t, binExe, "uninstall", "goop", "-y"); !strings.Contains(out, "goop itself") {
		t.Errorf("uninstalling goop should be refused with an explanation, got:\n%s", out)
	}
	if out := run(t, binExe, "version"); !strings.Contains(out, "goop 0.9.99") {
		t.Errorf("goop stopped working after a refused self-uninstall:\n%s", out)
	}
}

// buildRealGoop compiles the actual cmd/goop at a given version, the way
// scripts/build.ps1 does.
func buildRealGoop(t *testing.T, out, version string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-ldflags", "-X main.version="+version, "-o", out, "./cmd/goop")
	cmd.Dir = repoRoot(t)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building goop %s: %v\n%s", version, err, b)
	}
}

// repoRoot walks up from the test's directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the module root")
		}
		dir = parent
	}
}

func writeChecksums(t *testing.T, dir string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "goop.exe"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	line := hex.EncodeToString(sum[:]) + "  goop.exe\n"
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, exe string, args ...string) string {
	t.Helper()
	out, err := exec.Command(exe, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", filepath.Base(exe), args, err, out)
	}
	return string(out)
}

// runAllowingExit is for commands whose non-zero exit is meaningful --
// doctor returns 3 when it finds anything, and a refusal returns 1.
func runAllowingExit(t *testing.T, exe string, args ...string) string {
	t.Helper()
	out, _ := exec.Command(exe, args...).CombinedOutput()
	return string(out)
}
