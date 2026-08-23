package installer

import (
	"fmt"
	"path/filepath"

	"goop/internal/paths"
)

// SetHold pins (or unpins) appName at its currently installed version.
// A held app is skipped by `goop update`, so a toolchain you deliberately
// froze -- a specific SDK or IDE build a project is validated against --
// isn't carried forward by a routine update-everything run.
//
// The flag lives in the install record, the same place real Scoop keeps
// it (libexec/scoop-hold.ps1), so it survives a goop upgrade and shows
// up in `goop info`. It is deliberately *not* a profile or lockfile
// concern: a lockfile pins what a project needs, a hold pins what this
// machine must not change.
func SetHold(appName string, hold bool) error {
	rec, ok := readCurrentRecord(appName)
	if !ok {
		return fmt.Errorf("%s is not installed", appName)
	}
	if rec.Hold == hold {
		if hold {
			return fmt.Errorf("%s is already held", appName)
		}
		return fmt.Errorf("%s is not held", appName)
	}

	// Written back to the version directory the record was read from,
	// not to `current`: that junction is what a self-updating app can
	// replace with a real directory, and the record must stay with the
	// version it describes.
	versionDir := paths.AppVersion(appName, rec.Version)
	if _, err := readRecordAt(filepath.Join(versionDir, recordFileName)); err != nil {
		// The record lives inside `current` instead (self-updater case,
		// see readCurrentRecord) -- update it where it actually is.
		versionDir = paths.AppCurrent(appName)
	}

	rec.Hold = hold
	return writeRecord(versionDir, rec)
}

// readRecordAt reports whether a record file exists and parses at path.
func readRecordAt(path string) (Record, error) {
	rec, ok := readRecord(filepath.Dir(path))
	if !ok {
		return Record{}, fmt.Errorf("no readable record at %s", path)
	}
	return rec, nil
}

// IsHeld reports whether appName is pinned.
func IsHeld(appName string) bool {
	rec, ok := readCurrentRecord(appName)
	return ok && rec.Hold
}
