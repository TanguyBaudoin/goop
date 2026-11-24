package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/paths"
)

// SelfPackageName is the app name goop installs itself under, so the
// manager lives in the same versioned shape as everything it manages.
const SelfPackageName = "goop"

// goop's own binary used to sit flat at <root>\bin\goop.exe, and
// self-update replaced it by renaming the running image aside:
//
//	bin\goop.exe -> bin\goop.exe.old, then write the new one
//
// That works, and it is what rustup and deno do, but it leaves a corpse.
// Deleting the renamed file fails while the process that renamed it is
// still running from it, so the removal is best-effort and the next
// update is supposed to clear it -- which never happens if the next
// update arrives through the installer instead. Measured on the
// development machine: a 14 MB bin\goop.exe.old dated 81 days earlier.
//
// The shape here is the other well-trodden answer, the one Chrome, Edge
// and nvm-windows use: a versioned directory per release, with a stable
// *directory link* in front of it. The link is what PATH points at, so
// PATH never changes, and repointing it replaces the binary without
// renaming or deleting anything.
//
// Which of those operations Windows actually permits on a running
// executable was established by experiment rather than from memory:
//
//	repoint the junction containing the running .exe   OK
//	rename the real directory containing it            OK
//	rename the running .exe itself                     OK
//	delete the running .exe                            fails
//
// Only deletion is blocked. That is what makes both the update and the
// one-time migration safe: neither needs to delete anything that is in
// use.
//
// The stable element has to be the directory, not the file. A hardlink
// or copy at bin\goop.exe would have to be replaced on every update, and
// replacing it means renaming the running image again -- back to the
// corpse. A launcher stub would cost a process hop, measured at ~9.5 ms
// against a `goop list` that takes 21 ms in total.

// InstallSelf lays stagedExe down as apps\goop\<version>\goop.exe and
// points bin at it, returning the version directory.
//
// The caller is expected to have run stagedExe already and confirmed it
// reports the version claimed: this function replaces a working install,
// and the worst outcome it could produce is a goop that does not start.
func InstallSelf(stagedExe, version string) (string, error) {
	if version == "" {
		return "", fmt.Errorf("refusing to install goop with no version")
	}
	versionDir := paths.AppVersion(SelfPackageName, version)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", versionDir, err)
	}
	target := filepath.Join(versionDir, "goop.exe")

	// Writing into the version directory of a release that is already
	// installed would mean overwriting a file that may be the running
	// one. Nothing needs that: if the bytes are already there, only the
	// link has to move.
	if same, err := sameFileContents(stagedExe, target); err == nil && same {
		return versionDir, pointBinAt(versionDir)
	}
	if err := copyFile(stagedExe, target); err != nil {
		return "", fmt.Errorf("installing goop %s: %w -- nothing was replaced", version, err)
	}
	if err := writeSelfReceipt(versionDir, version); err != nil {
		return "", err
	}
	if err := relinkCurrent(SelfPackageName, versionDir); err != nil {
		return "", err
	}
	return versionDir, pointBinAt(versionDir)
}

// writeSelfReceipt gives goop the install record every other package
// has.
//
// Without it `goop list` resolves apps\goop\current, finds no readable
// record, and reports the manager itself as
// "(broken: no readable current record)" -- and `goop doctor` agrees.
// Making goop a package means making it a *well-formed* one, or the
// first thing the new layout does is make goop look broken to its own
// diagnostics.
func writeSelfReceipt(versionDir, version string) error {
	rec := Record{
		Name:        SelfPackageName,
		Version:     version,
		Description: "the goop package manager itself",
		Bin:         []manifest.BinEntry{{Name: "goop", Exe: "goop.exe"}},
		InstalledAt: time.Now().UTC(),
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding goop's own install record: %w", err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, recordFileName), append(data, "\n"...), 0o644); err != nil {
		return fmt.Errorf("writing goop's own install record: %w", err)
	}
	return nil
}

// pointBinAt makes <root>\bin a junction to versionDir.
//
// The window between removing the old link and creating the new one is
// the only moment goop is not on PATH, so a failure there is reported
// with the path the binary is actually at -- a user who has lost `goop`
// from their shell needs that, not a wrapped error.
func pointBinAt(versionDir string) error {
	if _, err := os.Stat(filepath.Join(versionDir, "goop.exe")); err != nil {
		return fmt.Errorf("refusing to point bin at %s: no goop.exe there (%w)", versionDir, err)
	}
	bin := paths.Bin()
	previous, wasLink := junctionTarget(bin)

	if err := removeJunction(bin); err != nil {
		return err
	}
	if err := makeJunction(bin, versionDir); err == nil {
		return nil
	} else if wasLink && previous != "" {
		// Put it back rather than leave the shell with no goop.
		if restoreErr := makeJunction(bin, previous); restoreErr == nil {
			return fmt.Errorf("pointing %s at %s failed (%w); it was restored to %s", bin, versionDir, err, previous)
		}
		return fmt.Errorf("pointing %s at %s failed (%w), and restoring it to %s also failed -- goop is at %s\\goop.exe; put that directory on PATH",
			bin, versionDir, err, previous, versionDir)
	} else {
		return fmt.Errorf("pointing %s at %s failed (%w) -- goop is at %s\\goop.exe; put that directory on PATH",
			bin, versionDir, err, versionDir)
	}
}

// MigrateSelfLayout converts a flat bin\goop.exe install into the
// versioned shape, reporting whether it did anything.
//
// Idempotent, and safe to call from a goop that is itself running out of
// the directory being replaced: the old bin is renamed, not deleted,
// which the experiment above confirms Windows allows. What is left
// behind holds the running image and so cannot be removed now; it is
// named so that a later run can clear it.
func MigrateSelfLayout(runningExe, version string) (bool, error) {
	bin := paths.Bin()
	if _, isLink := junctionTarget(bin); isLink {
		return false, nil // already migrated
	}
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		// Nothing to migrate: a fresh install can be laid down directly.
		_, err := InstallSelf(runningExe, version)
		return err == nil, err
	}

	versionDir := paths.AppVersion(SelfPackageName, version)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return false, fmt.Errorf("creating %s: %w", versionDir, err)
	}
	if err := copyFile(runningExe, filepath.Join(versionDir, "goop.exe")); err != nil {
		return false, fmt.Errorf("copying the running binary into %s: %w -- nothing was changed", versionDir, err)
	}
	if err := writeSelfReceipt(versionDir, version); err != nil {
		return false, err
	}
	if err := relinkCurrent(SelfPackageName, versionDir); err != nil {
		return false, err
	}

	aside := bin + ".pre-migration"
	_ = os.RemoveAll(aside) // a previous attempt's leftovers, if its exe is no longer running
	if err := os.Rename(bin, aside); err != nil {
		return false, fmt.Errorf("moving %s aside: %w -- nothing was changed", bin, err)
	}
	if err := makeJunction(bin, versionDir); err != nil {
		if restoreErr := os.Rename(aside, bin); restoreErr != nil {
			return false, fmt.Errorf("creating the bin junction failed (%w), and restoring %s also failed (%v) -- the old directory is at %s",
				err, bin, restoreErr, aside)
		}
		return false, fmt.Errorf("creating the bin junction: %w -- the previous layout was restored", err)
	}
	// Best-effort: it holds the running image, so this fails now and
	// succeeds on a later run.
	_ = os.RemoveAll(aside)
	return true, nil
}

// junctionTarget reports where path points, and whether it is a link at
// all. A real directory returns ("", false), which is what distinguishes
// a pre-migration bin from a migrated one.
func junctionTarget(path string) (string, bool) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", false
	}
	if !isReparsePoint(path) {
		_ = fi
		return "", false
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "", true // a link whose target cannot be read is still a link
	}
	return target, true
}

// removeJunction removes a directory link, leaving its target alone.
//
// `rmdir` rather than os.Remove: on a junction it unlinks and leaves the
// target untouched, which is the behaviour relinkCurrent relies on too.
// A path that is not a link is refused rather than removed -- deleting a
// real bin directory full of binaries is not something to do silently.
func removeJunction(link string) error {
	if _, err := os.Lstat(link); err != nil {
		return nil // nothing there
	}
	if !isReparsePoint(link) {
		return fmt.Errorf("%s is a real directory, not a link -- refusing to remove it", link)
	}
	if out, err := exec.Command("cmd", "/c", "rmdir", link).CombinedOutput(); err != nil {
		return fmt.Errorf("removing the link %s: %w\n%s", link, err, out)
	}
	return nil
}

func makeJunction(link, target string) error {
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		return fmt.Errorf("creating the junction %s -> %s: %w\n%s", link, target, err, out)
	}
	return nil
}

// sameFileContents reports whether two paths hold identical bytes,
// so an already-installed version is recognised without rewriting it.
func sameFileContents(a, b string) (bool, error) {
	ha, err := fileSHA256(a)
	if err != nil {
		return false, err
	}
	hb, err := fileSHA256(b)
	if err != nil {
		return false, err
	}
	return ha == hb, nil
}
