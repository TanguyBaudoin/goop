package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/TanguyBaudoin/goop/internal/bucket"
	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/paths"
)

// DigestOutcome is what happened to one package.
type DigestOutcome string

const (
	// DigestRecorded: the bucket still offers this exact version, every
	// field the receipt remembers matches it, and the digest was written.
	DigestRecorded DigestOutcome = "recorded"
	// DigestAlreadyHad: nothing to do.
	DigestAlreadyHad DigestOutcome = "already had one"
	// DigestUnavailable: the bucket has moved on, so the manifest this
	// was installed from is simply not obtainable any more.
	DigestUnavailable DigestOutcome = "unavailable"
	// DigestMismatch: the bucket offers this version and it does not
	// match what was installed. Recording it would be a lie.
	DigestMismatch DigestOutcome = "manifest differs"
	// DigestMoved: --recheck only. The package has a digest and the
	// bucket's manifest for the same version no longer matches it.
	DigestMoved DigestOutcome = "bucket republished this version"
)

// DigestResult is one package's outcome, with enough detail to act on.
type DigestResult struct {
	Package string
	Version string
	Outcome DigestOutcome
	Detail  string // which field differs, or what the bucket offers instead
}

// BackfillDigests records a manifest digest for installs that have none.
//
// A package installed before goop recorded digests -- or adopted from a
// real Scoop, which never had them -- pins a version and nothing else,
// and `goop profile export` can only write a version-only pin for it.
//
// The digest cannot simply be recomputed. It fingerprints the manifest
// the package was *installed from*, and the only manifest available now
// is whatever the bucket offers today. Stamping today's manifest onto an
// old install would assert something nobody checked, and would then make
// `goop profile check` green about it -- exactly the confident wrong
// answer the digest exists to prevent.
//
// So it is only recorded when it can be corroborated:
//
//   - the bucket must still offer this exact version. goop cannot fetch a
//     historical manifest (bucket.Resolve returns the current one), so if
//     the bucket has moved on there is nothing honest to record.
//   - every field the receipt captured at install time -- URLs, hashes,
//     bin entries, extract dirs, shortcuts, the uninstaller and uninstall
//     scripts, psmodule, depends -- must match that manifest.
//
// What this cannot corroborate: `pre_install`, `post_install` and
// `installer.script`. The receipt never recorded them, so a manifest
// republished with an edited install script and everything else
// unchanged would pass. Callers must say so rather than imply the pin is
// as strong as one written by an install. A digest recorded here is an
// adoption of the current manifest, corroborated by everything the
// receipt remembers -- not a recovery of what actually ran.
//
// names selects packages; empty means every install. recheck also
// examines packages that already have a digest, reporting where the
// bucket has republished the same version since.
func BackfillDigests(names []string, recheck bool) ([]DigestResult, error) {
	records, err := List()
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, n := range names {
		wanted[n] = true
	}

	var out []DigestResult
	for _, rec := range records {
		if len(wanted) > 0 && !wanted[rec.Name] {
			continue
		}
		if !rec.Ready() {
			continue
		}
		if rec.ManifestDigest != "" && !recheck {
			out = append(out, DigestResult{rec.Name, rec.Version, DigestAlreadyHad, ""})
			continue
		}
		out = append(out, backfillOne(rec))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Package < out[j].Package })
	return out, nil
}

func backfillOne(rec Record) DigestResult {
	res := DigestResult{Package: rec.Name, Version: rec.Version}

	spec := manifest.Spec{Bucket: rec.Bucket, Name: rec.Name}
	_, m, err := bucket.Resolve(spec)
	if err != nil {
		res.Outcome = DigestUnavailable
		res.Detail = err.Error()
		return res
	}
	if m.Version != rec.Version {
		// goop cannot ask a bucket for a historical manifest, so the one
		// this was installed from no longer exists as far as goop is
		// concerned. Saying that is the only honest answer.
		res.Outcome = DigestUnavailable
		res.Detail = fmt.Sprintf("bucket now offers %s", m.Version)
		return res
	}

	resolved, err := m.Resolve(rec.Name, rec.Architecture)
	if err != nil {
		res.Outcome = DigestUnavailable
		res.Detail = err.Error()
		return res
	}
	if resolved.Digest == "" {
		res.Outcome = DigestUnavailable
		res.Detail = "the manifest could not be fingerprinted"
		return res
	}

	if rec.ManifestDigest != "" {
		if rec.ManifestDigest == resolved.Digest {
			res.Outcome = DigestAlreadyHad
		} else {
			res.Outcome = DigestMoved
			res.Detail = "same version, different manifest"
		}
		return res
	}

	if field := firstFieldMismatch(rec, resolved); field != "" {
		res.Outcome = DigestMismatch
		res.Detail = field + " differs from what was installed"
		return res
	}

	if err := recordDigest(rec, resolved.Digest); err != nil {
		res.Outcome = DigestUnavailable
		res.Detail = err.Error()
		return res
	}
	res.Outcome = DigestRecorded
	return res
}

// firstFieldMismatch names the first install-relevant field where the
// receipt and the manifest disagree, or "" when everything the receipt
// remembers matches.
//
// Only fields installResolved copies verbatim from the resolved manifest
// are comparable. EnvSet and EnvAddedPaths are deliberately left out:
// they are recorded already expanded ($dir/$version substituted), so
// they cannot be compared against a manifest without redoing the
// substitution, and a difference would say nothing.
func firstFieldMismatch(rec Record, r manifest.Resolved) string {
	checks := []struct {
		name       string
		have, want any
	}{
		{"urls", rec.URLs, r.URLs},
		{"hashes", rec.Hashes, r.Hashes},
		{"bin", rec.Bin, []manifest.BinEntry(r.Bin)},
		{"extract_dir", rec.ExtractDirs, r.ExtractDirs},
		{"extract_to", rec.ExtractTos, r.ExtractTos},
		{"shortcuts", rec.Shortcuts, r.Shortcuts},
		{"uninstaller", rec.Uninstaller, r.Uninstaller},
		{"pre_uninstall", rec.PreUninstall, r.PreUninstall},
		{"post_uninstall", rec.PostUninstall, r.PostUninstall},
		{"psmodule", rec.PSModuleName, r.PSModuleName},
		{"depends", rec.Depends, functionalDepends(r.Depends, r)},
	}
	for _, c := range checks {
		if !equalIgnoringEmpty(c.have, c.want) {
			return c.name
		}
	}
	return ""
}

// equalIgnoringEmpty compares two recorded values, treating a nil slice
// and an empty one as the same thing -- JSON round-tripping turns one
// into the other, and a package whose manifest has no shortcuts must not
// be refused over it.
func equalIgnoringEmpty(a, b any) bool {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if va.Kind() == reflect.Slice && vb.Kind() == reflect.Slice {
		if va.Len() == 0 && vb.Len() == 0 {
			return true
		}
	}
	return reflect.DeepEqual(a, b)
}

// recordDigest writes the digest into the receipt in place, leaving
// everything else untouched.
func recordDigest(rec Record, digest string) error {
	dir, err := currentRecordDir(rec.Name)
	if err != nil {
		return err
	}
	onDisk, ok := readRecord(dir)
	if !ok {
		return fmt.Errorf("%s: receipt disappeared", rec.Name)
	}
	onDisk.ManifestDigest = digest
	return writeRecord(dir, onDisk)
}

// currentRecordDir resolves where the live receipt actually lives.
// Mirrors readCurrentRecord: `current` is normally a junction, but a
// self-updating app can leave a real directory there with the receipt
// inside it.
func currentRecordDir(appName string) (string, error) {
	current := paths.AppCurrent(appName)
	if target, err := os.Readlink(current); err == nil {
		return paths.AppVersion(appName, filepath.Base(target)), nil
	}
	if _, err := os.Stat(filepath.Join(current, recordFileName)); err == nil {
		return current, nil
	}
	return "", fmt.Errorf("%s: no readable install record", appName)
}
