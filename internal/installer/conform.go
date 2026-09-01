package installer

import (
	"sort"
	"strconv"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/profile"
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
	// The file says which profile this package belongs to. A machine
	// where it is installed but filed somewhere else does not match the
	// file -- which is what someone means by "I synced ide and idea is
	// still under default".
	//
	// Only absence from the declared profile counts. Extra local
	// memberships are the machine's own business, the same way a package
	// outside every profile is never a deviation.
	if in, err := profile.ContainingProfiles(pkg); err == nil && !contains(in, profileName) {
		d.Reason = "not filed under " + strconv.Quote(profileName)
		if len(in) > 0 {
			d.Reason += " (it is in " + strings.Join(in, ", ") + ")"
		}
		return d, true
	}
	return Deviation{}, false
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// SyncProfiles installs what the named profiles require and is not there.
// Returns what it changed and what it could not.
//
// Idempotent and needs no prior state: an empty machine and a drifted one
// take the same path, because the path is "make each deviation go away".
//
// Concurrent, with the same bound as `goop install` -- a first sync on a
// fresh machine is a batch of downloads, which is exactly the case
// bounded concurrency exists for. installSpec is already safe to run in
// parallel (InstallAll does), a per-app lock serializes two profiles
// wanting the same package, and profile.Add takes a mutex.
//
// One package failing does not stop the others: each deviation is
// independent, and a dead download URL in a twenty-package profile should
// leave nineteen installed and one reported, not nineteen unattempted.
func SyncProfiles(f profileset.File, names []string) ([]Deviation, map[string]error, error) {
	deviations, err := Check(f, names)
	if err != nil {
		return nil, nil, err
	}

	// Fetch everything first, in parallel, then install one at a time.
	// An install hook can call any goop-installed binary, so installing
	// concurrently made "does the package I need exist yet" a race -- see
	// Prefetch.
	var specs []string
	for _, d := range deviations {
		if strings.HasPrefix(d.Reason, "not filed under") {
			continue // nothing to download; only a line in a text file
		}
		spec := d.Package
		if d.Want != "" {
			spec = d.Package + "@" + d.Want
		}
		specs = append(specs, spec)
	}
	Prefetch(specs)

	fixed := make([]Deviation, 0, len(deviations))
	errs := map[string]error{}
	for _, d := range deviations {
		if err := fixOne(d); err != nil {
			errs[d.Package] = err
			continue
		}
		fixed = append(fixed, d)
	}

	// Check already returns deviations sorted, so this order is stable
	// and diffable without sorting again.
	return fixed, errs, nil
}

// fixOne makes a single deviation go away.
func fixOne(d Deviation) error {
	// A package already installed at the right version needs no install
	// -- only filing. Running one anyway would re-download and re-extract
	// a working package to fix a line in a text file.
	if strings.HasPrefix(d.Reason, "not filed under") {
		return profile.Add(d.Profile, d.Package)
	}
	spec := d.Package
	if d.Want != "" {
		spec = d.Package + "@" + d.Want
	}
	// Register into the profile being repaired, not the active one: the
	// file says which profile each package belongs to, and that is better
	// information than any machine-local default.
	_, err := InstallInto(spec, d.Profile)
	return err
}

// SyncSummary is what a sync established, so it can report what it
// verified rather than "nothing to do" -- which says nothing about
// whether anything was actually checked.
type SyncSummary struct {
	Profiles []string
	Packages int // packages the selected profiles declare
}

// Summarize describes what checking these profiles covers.
func Summarize(f profileset.File, names []string) (SyncSummary, error) {
	selected, err := f.Select(names)
	if err != nil {
		return SyncSummary{}, err
	}
	n := 0
	for _, name := range selected {
		n += len(f.Profiles[name].All())
	}
	return SyncSummary{Profiles: selected, Packages: n}, nil
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
