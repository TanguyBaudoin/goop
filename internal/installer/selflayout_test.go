package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/paths"
)

// stubExe writes a file standing in for goop.exe. The layout functions
// never run it -- the caller is responsible for having verified the
// candidate -- so its contents only need to be distinguishable.
func stubExe(t *testing.T, path, marker string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func binExeContents(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(paths.Bin(), "goop.exe"))
	if err != nil {
		t.Fatalf("reading through the bin junction: %v", err)
	}
	return string(data)
}

// The one-time conversion from the flat layout every install had before.
//
// Safe to run from a goop executing out of the directory being replaced,
// because it renames rather than deletes -- the one operation Windows
// refuses on a running image is deletion.
func TestMigrateSelfLayout_ConvertsFlatBin(t *testing.T) {
	isolateRoot(t)
	exe := stubExe(t, filepath.Join(paths.Bin(), "goop.exe"), "v-0.9.1")
	// The corpse the old rename-based self-update left behind.
	stubExe(t, filepath.Join(paths.Bin(), "goop.exe.old"), "v-0.8.0")

	migrated, err := MigrateSelfLayout(exe, "0.9.1")
	if err != nil {
		t.Fatalf("MigrateSelfLayout: %v", err)
	}
	if !migrated {
		t.Error("a flat bin must be reported as migrated")
	}

	// bin is now a link, and reading through it finds the binary.
	if target, isLink := junctionTarget(paths.Bin()); !isLink {
		t.Error("bin must be a junction after migration")
	} else if !strings.EqualFold(filepath.Clean(target), filepath.Clean(paths.AppVersion("goop", "0.9.1"))) {
		t.Errorf("bin points at %q, want the 0.9.1 version directory", target)
	}
	if got := binExeContents(t); got != "v-0.9.1" {
		t.Errorf("through bin: %q, want v-0.9.1", got)
	}

	// The version that was running is now installed as a package, which
	// is what makes it a rollback target rather than a renamed corpse.
	if _, err := os.Stat(filepath.Join(paths.AppVersion("goop", "0.9.1"), "goop.exe")); err != nil {
		t.Errorf("the running version should be installed as a package: %v", err)
	}
}

// Running it twice must not disturb a working install: self-update calls
// it on every run.
func TestMigrateSelfLayout_Idempotent(t *testing.T) {
	isolateRoot(t)
	exe := stubExe(t, filepath.Join(paths.Bin(), "goop.exe"), "v-0.9.1")
	if _, err := MigrateSelfLayout(exe, "0.9.1"); err != nil {
		t.Fatal(err)
	}

	migrated, err := MigrateSelfLayout(exe, "0.9.1")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if migrated {
		t.Error("an already-migrated layout must report no change")
	}
	if got := binExeContents(t); got != "v-0.9.1" {
		t.Errorf("the second call disturbed bin: %q", got)
	}
}

// An install with no bin at all -- a fresh one -- is laid down directly.
func TestMigrateSelfLayout_FreshInstall(t *testing.T) {
	isolateRoot(t)
	exe := stubExe(t, filepath.Join(t.TempDir(), "goop.exe"), "v-1.0.0")

	if _, err := MigrateSelfLayout(exe, "1.0.0"); err != nil {
		t.Fatalf("MigrateSelfLayout: %v", err)
	}
	if got := binExeContents(t); got != "v-1.0.0" {
		t.Errorf("through bin: %q, want v-1.0.0", got)
	}
}

// The update itself: a new version directory, and bin moved to it.
func TestInstallSelf_RepointsBinAndKeepsTheOldVersion(t *testing.T) {
	isolateRoot(t)
	old := stubExe(t, filepath.Join(paths.Bin(), "goop.exe"), "v-0.9.1")
	if _, err := MigrateSelfLayout(old, "0.9.1"); err != nil {
		t.Fatal(err)
	}

	staged := stubExe(t, filepath.Join(t.TempDir(), "goop.exe"), "v-0.9.2")
	versionDir, err := InstallSelf(staged, "0.9.2")
	if err != nil {
		t.Fatalf("InstallSelf: %v", err)
	}
	if want := paths.AppVersion("goop", "0.9.2"); !strings.EqualFold(versionDir, want) {
		t.Errorf("version directory = %q, want %q", versionDir, want)
	}
	if got := binExeContents(t); got != "v-0.9.2" {
		t.Errorf("through bin: %q, want v-0.9.2 -- bin was not repointed", got)
	}
	// Nothing was overwritten or renamed, so the previous release is
	// still there to go back to.
	prev := filepath.Join(paths.AppVersion("goop", "0.9.1"), "goop.exe")
	if data, err := os.ReadFile(prev); err != nil {
		t.Errorf("the previous version must survive an update: %v", err)
	} else if string(data) != "v-0.9.1" {
		t.Errorf("the previous version was modified: %q", string(data))
	}
}

// Installing the version that is already there must only move the link,
// never rewrite the file -- that file may be the running image, and
// writing over a running image is exactly what this layout avoids.
func TestInstallSelf_SameBytesDoesNotRewrite(t *testing.T) {
	isolateRoot(t)
	exe := stubExe(t, filepath.Join(paths.Bin(), "goop.exe"), "v-0.9.1")
	if _, err := MigrateSelfLayout(exe, "0.9.1"); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(paths.AppVersion("goop", "0.9.1"), "goop.exe")
	before, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}

	staged := stubExe(t, filepath.Join(t.TempDir(), "goop.exe"), "v-0.9.1")
	if _, err := InstallSelf(staged, "0.9.1"); err != nil {
		t.Fatalf("InstallSelf: %v", err)
	}
	after, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("identical bytes must not be rewritten over a possibly-running image")
	}
}

// Pointing bin at a directory with no goop.exe in it would leave the
// shell with a `goop` that does not exist. Refused before the link is
// touched, not after.
func TestPointBinAt_RefusesADirectoryWithNoBinary(t *testing.T) {
	isolateRoot(t)
	exe := stubExe(t, filepath.Join(paths.Bin(), "goop.exe"), "v-0.9.1")
	if _, err := MigrateSelfLayout(exe, "0.9.1"); err != nil {
		t.Fatal(err)
	}

	empty := filepath.Join(t.TempDir(), "nothing-here")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := pointBinAt(empty); err == nil {
		t.Fatal("pointing bin at a directory with no goop.exe must be refused")
	}
	// And the refusal must come before anything is unlinked.
	if got := binExeContents(t); got != "v-0.9.1" {
		t.Errorf("bin was disturbed by a refused repoint: %q", got)
	}
}

func TestInstallSelf_RefusesAnEmptyVersion(t *testing.T) {
	isolateRoot(t)
	staged := stubExe(t, filepath.Join(t.TempDir(), "goop.exe"), "x")
	if _, err := InstallSelf(staged, ""); err == nil {
		t.Error("installing goop with no version must be refused")
	}
}

// removeJunction unlinks; it must never be the thing that deletes a real
// directory full of binaries.
func TestRemoveJunction_RefusesARealDirectory(t *testing.T) {
	isolateRoot(t)
	real := filepath.Join(t.TempDir(), "bin")
	stubExe(t, filepath.Join(real, "goop.exe"), "precious")

	if err := removeJunction(real); err == nil {
		t.Error("removeJunction must refuse a real directory")
	}
	if _, err := os.Stat(filepath.Join(real, "goop.exe")); err != nil {
		t.Errorf("the real directory's contents must be untouched: %v", err)
	}
	// And a path that is not there at all is simply nothing to do.
	if err := removeJunction(filepath.Join(real, "absent")); err != nil {
		t.Errorf("removing a link that does not exist should be a no-op, got %v", err)
	}

	// An EMPTY real directory is the case that proves the guard carries
	// its own weight. A mutation deleting the check left the test above
	// passing, because `rmdir` refuses a non-empty directory by itself --
	// so the guard looked covered when only cmd.exe was protecting it.
	// On an empty directory `rmdir` succeeds, and without the check
	// removeJunction would quietly delete a directory it was never asked
	// to.
	empty := filepath.Join(t.TempDir(), "empty-real-dir")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeJunction(empty); err == nil {
		t.Error("removeJunction must refuse an empty real directory, not delete it")
	}
	if _, err := os.Stat(empty); err != nil {
		t.Errorf("the empty real directory must still be there: %v", err)
	}
}

// Making goop a package means making it a well-formed one. Without a
// receipt and an apps\goop\current junction, List resolves the manager,
// finds no readable record, and reports it as
// "(broken: no readable current record)" -- so the first thing the new
// layout would do is make goop look broken to `goop list` and to
// `goop doctor`.
func TestMigrateSelfLayout_GoopIsAWellFormedPackage(t *testing.T) {
	isolateRoot(t)
	isolateEnvRegistry(t)
	exe := stubExe(t, filepath.Join(paths.Bin(), "goop.exe"), "v-0.9.1")
	if _, err := MigrateSelfLayout(exe, "0.9.1"); err != nil {
		t.Fatalf("MigrateSelfLayout: %v", err)
	}

	recs, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var self *Record
	for i := range recs {
		if recs[i].Name == SelfPackageName {
			self = &recs[i]
		}
	}
	if self == nil {
		t.Fatal("goop must appear in `goop list` once it is a package")
	}
	if self.Version != "0.9.1" {
		t.Errorf("goop version = %q, want 0.9.1", self.Version)
	}
	if strings.Contains(self.Version, "broken") {
		t.Errorf("goop reported as broken: %q", self.Version)
	}
	if !self.Ready() {
		t.Error("goop's own record must be ready, not pending")
	}

	// And doctor must be silent about it: a diagnostic that flags the
	// manager itself on every run is worse than no diagnostic.
	findings, err := Diagnose()
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	for _, f := range findings {
		for _, item := range f.Items {
			if strings.Contains(item, SelfPackageName) {
				t.Errorf("doctor flagged goop itself: %s -- %s", f.Check, item)
			}
		}
	}
}

// goop uninstall goop parses now that goop is a package, and it cannot
// succeed: Windows refuses to delete a running image, so the removal
// would fail after the shims and environment entries were already gone.
// Refusing outright is the only outcome that leaves a working install.
func TestUninstall_RefusesGoopItself(t *testing.T) {
	isolateRoot(t)
	exe := stubExe(t, filepath.Join(paths.Bin(), "goop.exe"), "v-0.9.1")
	if _, err := MigrateSelfLayout(exe, "0.9.1"); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"goop", "GOOP", "Goop"} {
		err := Uninstall(name, true)
		if err == nil {
			t.Errorf("Uninstall(%q) must be refused", name)
			continue
		}
		if !strings.Contains(err.Error(), "goop itself") {
			t.Errorf("Uninstall(%q) should explain why, got: %v", name, err)
		}
	}
	// And nothing was touched on the way to refusing.
	if got := binExeContents(t); got != "v-0.9.1" {
		t.Errorf("the refusal disturbed the install: %q", got)
	}
}

// Cleanup must skip the version bin points at -- the one executing the
// cleanup -- rather than log a failure for goop on every run.
func TestIsRunningSelfVersion(t *testing.T) {
	isolateRoot(t)
	exe := stubExe(t, filepath.Join(paths.Bin(), "goop.exe"), "v-0.9.1")
	if _, err := MigrateSelfLayout(exe, "0.9.1"); err != nil {
		t.Fatal(err)
	}
	if !isRunningSelfVersion(paths.AppVersion("goop", "0.9.1")) {
		t.Error("the version bin points at must be recognised as the running one")
	}
	if isRunningSelfVersion(paths.AppVersion("goop", "0.9.0")) {
		t.Error("a version bin does not point at must not be treated as running")
	}
}
