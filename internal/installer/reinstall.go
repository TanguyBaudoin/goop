package installer

import (
	"fmt"

	"github.com/TanguyBaudoin/goop/internal/bucket"
	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/profile"
)

// ReinstallResult is what Reinstall put back.
type ReinstallResult struct {
	Name     string
	Version  string
	Profiles []string // the groupings it is still in, unchanged
}

// Reinstall lays the installed version down again from scratch.
//
// `goop reset` rebuilds shims, shortcuts and environment entries from the
// record without touching a single file, which repairs the wiring and
// nothing else. When the files themselves are the problem -- a half-
// extracted archive, something edited by hand, an app that broke its own
// installation -- the only route was uninstall then install, and that
// loses what the uninstall took with it.
//
// What this keeps, and why each matters:
//
//   - **Profile membership.** Uninstall removes an app from every profile
//     that named it, so uninstall-then-install drops it into `default`
//     and a committed profile file goes red. Reinstall never uninstalls:
//     the install path does not touch membership at all (Install and
//     InstallInto do, and this calls neither), so it survives by
//     construction rather than by being put back afterwards.
//   - **Persisted data.** Untouched for the same reason -- only uninstall
//     removes it, and only with --purge.
//   - **The version.** This is a repair, not an update. It reinstalls
//     what is installed. `goop update` is the command that moves a
//     version.
//
// Still atomic: the force flag only skips the "already installed"
// shortcut, so the work happens in a staging directory and commits with
// the same rename. A reinstall that fails leaves the old installation
// where it was.
func Reinstall(appName string) (ReinstallResult, error) {
	rec, ok := readCurrentRecord(appName)
	if !ok {
		return ReinstallResult{}, fmt.Errorf("%s is %w", appName, ErrNotInstalled)
	}
	if rec.Version == "" {
		return ReinstallResult{}, fmt.Errorf("%s: its install record names no version, so there is nothing to reinstall", appName)
	}
	if rec.Bucket == "maven" {
		return ReinstallResult{}, fmt.Errorf("%s was installed from a Maven coordinate; reinstall it with `goop install maven:...`", appName)
	}

	// Reinstalling needs the manifest for the version that is installed,
	// and a bucket only carries the current one. Checked here so the
	// refusal names the situation rather than surfacing as a version
	// constraint conflict from deep inside the install path.
	spec := manifest.Spec{Name: appName}
	if rec.Bucket != "" && configuredBucket(rec.Bucket) {
		spec.Bucket = rec.Bucket
	}
	_, m, err := bucket.Resolve(spec)
	if err != nil {
		return ReinstallResult{}, err
	}
	if m.Version != rec.Version {
		return ReinstallResult{}, fmt.Errorf(
			"%s is installed at %s, but bucket %q now offers %s.\n"+
				"  A bucket carries one manifest per package, so the instructions for %s\n"+
				"  are no longer available to reinstall from. `goop update %s` installs %s\n"+
				"  instead, which is a different thing and worth choosing deliberately.",
			appName, rec.Version, firstNonEmpty(spec.Bucket, rec.Bucket), m.Version,
			rec.Version, appName, m.Version)
	}

	// Captured before, reported after: the point of the command is that
	// these do not change, so saying what they are is how the caller sees
	// that they did not.
	profiles, _ := profile.ContainingProfiles(appName)

	target := appName
	if spec.Bucket != "" {
		target = spec.Bucket + "/" + appName
	}
	newRec, err := installSpec(target, nil, false, true)
	if err != nil {
		return ReinstallResult{}, err
	}
	return ReinstallResult{Name: newRec.Name, Version: newRec.Version, Profiles: profiles}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// CurrentRecord is the install record for appName, for a caller that
// needs to show what is installed before acting on it.
func CurrentRecord(appName string) (Record, bool) {
	return readCurrentRecord(appName)
}

// ProfilesOf lists the profiles that name appName. Empty on error: a
// listing is informational, and failing a command over it would be
// worse than showing nothing.
func ProfilesOf(appName string) []string {
	profiles, err := profile.ContainingProfiles(appName)
	if err != nil {
		return nil
	}
	return profiles
}
