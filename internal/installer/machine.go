package installer

import (
	"sort"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/bucket"
	"github.com/TanguyBaudoin/goop/internal/downloader"
	"github.com/TanguyBaudoin/goop/internal/profile"
	"github.com/TanguyBaudoin/goop/internal/setup"
)

// warnMachineLocalSource reports a package installed from a source that
// only exists on this machine. A drive-letter file:// URL resolves
// nowhere else, so a captured or exported file would carry a pin nobody
// can replay. A UNC share resolves the same from any host that can reach
// it, and stays quiet.
//
// Both export paths write files meant to travel -- one committed with the
// code, one carried to a new machine -- so the check belongs to both.
func warnMachineLocalSource(rec Record) {
	for _, u := range rec.URLs {
		if downloader.IsMachineLocalFileURL(u) {
			Logf("%s: installed from %s, a path that exists only on this machine -- use file://server/share/... if this file will travel", rec.Name, u)
			return
		}
	}
}

// ExportSetup captures this machine: its buckets and everything
// installed on it.
func ExportSetup() (setup.File, error) {
	var f setup.File

	buckets, err := bucket.List()
	if err != nil {
		return setup.File{}, err
	}
	for _, b := range buckets {
		f.Buckets = append(f.Buckets, setup.Bucket{Name: b.Name, URL: b.URL, Kind: string(b.Kind)})
	}

	// Profile membership is part of what is on this machine, and a
	// restore without it drops everything into `default`.
	profiles, err := profile.List()
	if err != nil {
		return setup.File{}, err
	}
	f.Profiles = make(map[string][]string, len(profiles))
	for _, name := range profiles {
		d, err := profile.Load(name)
		if err != nil {
			return setup.File{}, err
		}
		f.Profiles[name] = d.Apps
	}

	records, err := List()
	if err != nil {
		return setup.File{}, err
	}
	for _, r := range records {
		// An unreadable record describes nothing reproducible, and
		// writing it out would produce a file that cannot be replayed.
		if strings.HasPrefix(r.Version, "(broken") {
			Logf("%s: skipped (unreadable record)", r.Name)
			continue
		}
		warnMachineLocalSource(r)
		f.Apps = append(f.Apps, setup.App{
			Name:           r.Name,
			Version:        r.Version,
			Bucket:         r.Bucket,
			ManifestDigest: r.ManifestDigest,
		})
	}
	return f, nil
}

// ImportSetup replays a captured machine: configure the buckets it
// names, then install its packages.
//
// Buckets first, and not optionally: a list of packages with no
// catalogue to resolve them against is not a setup, which is why Scoop's
// own export records them too.
func ImportSetup(f setup.File) (installed []string, errs map[string]error, err error) {
	errs = map[string]error{}

	existing := map[string]bool{}
	if current, err := bucket.List(); err == nil {
		for _, b := range current {
			existing[b.Name] = true
		}
	}
	for _, b := range f.Buckets {
		if existing[b.Name] {
			continue
		}
		if addErr := bucket.Add(b.Name, b.URL, bucket.Kind(b.Kind)); addErr != nil {
			errs["bucket "+b.Name] = addErr
		}
	}

	for _, a := range f.Apps {
		spec := a.Name
		if a.Version != "" {
			spec = a.Name + "@" + a.Version
		}
		if _, instErr := Install(spec); instErr != nil {
			errs[a.Name] = instErr
			continue
		}
		installed = append(installed, a.Name)
	}
	// After the installs: Install files a package under `default`, and
	// filing it under a named profile takes it back out again, so doing
	// this second is what makes the grouping stick.
	for _, name := range sortedProfileNames(f.Profiles) {
		for _, app := range f.Profiles[name] {
			if err := profile.Add(name, app); err != nil {
				errs["profile "+name] = err
				break
			}
		}
	}

	sort.Strings(installed)
	return installed, errs, nil
}

func sortedProfileNames(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// AuditSetup compares this machine against a captured one.
//
// Unlike a profile check, this one is a machine comparison: a package
// present here but absent from the file *is* reported, because the
// question is "is this machine the one that was captured".
func AuditSetup(f setup.File) ([]Deviation, error) {
	var out []Deviation

	here := map[string]Record{}
	records, err := List()
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		here[r.Name] = r
	}

	captured := map[string]bool{}
	for _, a := range f.Apps {
		captured[a.Name] = true
		rec, ok := here[a.Name]
		switch {
		case !ok:
			out = append(out, Deviation{Package: a.Name, Want: a.Version, Reason: "not installed"})
		case !rec.Ready():
			out = append(out, Deviation{Package: a.Name, Want: a.Version, Got: rec.Version, Reason: "install did not finish"})
		case rec.Version != a.Version:
			out = append(out, Deviation{Package: a.Name, Want: a.Version, Got: rec.Version, Reason: "wrong version"})
		case a.ManifestDigest != "" && rec.ManifestDigest != "" && rec.ManifestDigest != a.ManifestDigest:
			out = append(out, Deviation{Package: a.Name, Want: a.Version, Got: rec.Version, Reason: "manifest differs from the capture"})
		}
	}
	for name, rec := range here {
		if !captured[name] {
			out = append(out, Deviation{Package: name, Got: rec.Version, Reason: "not in the capture"})
		}
	}

	// The capture records how packages were grouped, so a machine whose
	// grouping differs is not the machine that was captured. Staying
	// silent about a difference the file describes is the failure this
	// design keeps guarding against.
	out = append(out, auditProfiles(f)...)
	sort.Slice(out, func(i, j int) bool { return out[i].Package < out[j].Package })
	return out, nil
}

// auditProfiles compares the recorded grouping against this machine's.
//
// Only membership the capture names is checked. A profile that exists
// only here is this machine's own business -- the same rule the profile
// plane uses for a package outside every profile.
func auditProfiles(f setup.File) []Deviation {
	var out []Deviation
	for _, name := range sortedProfileNames(f.Profiles) {
		d, err := profile.Load(name)
		if err != nil {
			out = append(out, Deviation{Profile: name, Reason: "profile could not be read"})
			continue
		}
		have := map[string]bool{}
		for _, a := range d.Apps {
			have[a] = true
		}
		for _, app := range f.Profiles[name] {
			if !have[app] {
				out = append(out, Deviation{
					Profile: name, Package: app,
					Reason: "not filed under " + name + " here",
				})
			}
		}
	}
	return out
}
