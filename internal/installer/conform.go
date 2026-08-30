package installer

import (
	"sort"

	"github.com/TanguyBaudoin/goop/internal/profileset"
)

// Deviation is one package that does not match what a profile requires.
type Deviation struct {
	Profile string
	Package string
	Want    string // required version
	Got     string // installed version, empty when absent
	Reason  string
}

// Check compares what is installed against the profiles named in f.
//
// It reads receipts and nothing else: no bucket, no network, so it is
// instant and gives the same answer on a disconnected machine. The
// question is "does this repository have what it needs", not "is this
// machine clean" -- packages installed outside the profile are never a
// deviation.
func Check(f profileset.File, names []string) ([]Deviation, error) {
	selected, err := f.Select(names)
	if err != nil {
		return nil, err
	}
	var out []Deviation
	for _, name := range selected {
		prof := f.Profiles[name]
		for _, pkg := range prof.SortedNames() {
			pin := prof.All()[pkg]
			if d, bad := checkOne(name, pkg, pin); bad {
				out = append(out, d)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Profile != out[j].Profile {
			return out[i].Profile < out[j].Profile
		}
		return out[i].Package < out[j].Package
	})
	return out, nil
}

func checkOne(profileName, pkg string, pin profileset.Pin) (Deviation, bool) {
	d := Deviation{Profile: profileName, Package: pkg, Want: pin.Version}

	rec, ok := readCurrentRecord(pkg)
	if !ok {
		d.Reason = "not installed"
		return d, true
	}
	d.Got = rec.Version

	// An install that failed after its receipt was committed leaves a
	// record claiming the right version with nothing working behind it.
	// Reporting that as conformant is the failure this whole design
	// exists to prevent.
	if !rec.Ready() {
		d.Reason = "install did not finish"
		return d, true
	}
	if pin.Version != "" && rec.Version != pin.Version {
		d.Reason = "wrong version"
		return d, true
	}
	// The manifest digest catches what a version cannot: the same version
	// republished with an edited post_install, a changed URL, a different
	// artifact hash.
	if pin.Hash != "" && rec.ManifestDigest != pin.Hash {
		if rec.ManifestDigest == "" {
			d.Reason = "installed before digests were recorded"
		} else {
			d.Reason = "manifest changed since it was installed"
		}
		return d, true
	}
	if missing := missingShimTargets(rec); missing != "" {
		d.Reason = "broken shim: " + missing
		return d, true
	}
	return Deviation{}, false
}

// SyncProfiles installs what the named profiles require and is not there.
// Returns what it changed and what it could not.
//
// Idempotent and needs no prior state: an empty machine and a drifted one
// take the same path, because the path is "make each deviation go away".
func SyncProfiles(f profileset.File, names []string) ([]Deviation, map[string]error, error) {
	deviations, err := Check(f, names)
	if err != nil {
		return nil, nil, err
	}
	fixed := make([]Deviation, 0, len(deviations))
	errs := map[string]error{}
	for _, d := range deviations {
		spec := d.Package
		if d.Want != "" {
			spec = d.Package + "@" + d.Want
		}
		// Register into the profile being repaired, not the active one.
		// A machine syncing chipA usually has `default` active, and
		// registering there emptied the very profile the sync was fixing.
		if _, err := InstallInto(spec, d.Profile); err != nil {
			errs[d.Package] = err
			continue
		}
		fixed = append(fixed, d)
	}
	return fixed, errs, nil
}

// VerifyPins reports packages whose installed manifest digest does not
// match what the profile pins, after a sync -- the case where the bucket
// has moved on and now offers different instructions under the same
// version number.
func VerifyPins(f profileset.File, names []string) ([]Deviation, error) {
	deviations, err := Check(f, names)
	if err != nil {
		return nil, err
	}
	var out []Deviation
	for _, d := range deviations {
		if d.Reason == "manifest changed since it was installed" {
			out = append(out, d)
		}
	}
	return out, nil
}

// ExportReport is what an export produced and what it could not.
//
// Undigested is the part a maintainer has to see: those pins carry a
// version and no manifest digest, so the file they are about to commit
// cannot detect a manifest republished under the same version number.
type ExportReport struct {
	File       profileset.File
	Pinned     int
	Missing    []string // member not installed, or its install did not finish
	Undigested []string // pinned, but with no manifest digest to pin to
}

// ExportProfiles turns local profiles into a profile file, pinning each
// member to the version and manifest digest actually installed.
//
// Reads receipts, not the bucket: it describes this machine, not the
// catalogue. If the bucket has moved on since, that is precisely what
// must not leak into the file -- otherwise a maintainer would publish a
// pin nobody has ever run.
//
// A member that is not installed is refused rather than guessed at.
func ExportProfiles(names []string, members func(string) ([]string, error)) (ExportReport, error) {
	out := profileset.File{Profiles: map[string]profileset.Profile{}}
	var missing, undigested []string
	pinned := 0

	for _, name := range names {
		apps, err := members(name)
		if err != nil {
			return ExportReport{}, err
		}
		prof := profileset.Profile{Packages: map[string]profileset.Pin{}}
		for _, app := range apps {
			rec, ok := readCurrentRecord(app)
			if !ok || !rec.Ready() {
				missing = append(missing, name+"/"+app)
				continue
			}
			pinned++
			warnMachineLocalSource(rec)
			// A pin with no digest is a version number and nothing more:
			// it cannot tell a republished manifest from the one that was
			// installed. Packages installed by goop before digests
			// existed, or adopted from Scoop, have none -- and exporting
			// them silently produced a weaker file whose maintainer had
			// no way to know.
			if rec.ManifestDigest == "" {
				undigested = append(undigested, name+"/"+app)
			}
			prof.Packages[app] = profileset.Pin{Version: rec.Version, Hash: rec.ManifestDigest}
		}
		out.Profiles[name] = prof
	}
	sort.Strings(missing)
	sort.Strings(undigested)
	return ExportReport{File: out, Pinned: pinned, Missing: missing, Undigested: undigested}, nil
}
