package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/user"
	"time"

	"github.com/TanguyBaudoin/goop/internal/profileset"
)

// Evidence is what a conformance check actually established, in a form
// somebody who was not there can check.
//
// `✓ conformant` is an assertion. It does not say which file was read,
// which machine was examined, when, or what any individual package was
// found to be -- so it proves nothing to anyone reviewing it later. This
// records all of it, including the packages that passed: showing only
// the failures leaves "was this package even looked at?" unanswered,
// which is the question an audit exists to close.
type Evidence struct {
	Tool      string    `json:"tool"`    // "goop"
	Version   string    `json:"version"` // the binary that produced this
	Commit    string    `json:"commit,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	Host      string    `json:"host"`
	User      string    `json:"user"`
	Root      string    `json:"root"` // which goop installation was examined

	// File and FileSHA256 identify the requirement, not just its path: a
	// path proves nothing when the file can be edited between the check
	// and the review.
	File       string   `json:"file"`
	FileSHA256 string   `json:"file_sha256"`
	Profiles   []string `json:"profiles"`

	Packages   []PackageEvidence `json:"packages"`
	Conformant bool              `json:"conformant"`
	Deviations int               `json:"deviations"`
}

// PackageEvidence is one package's required state, its found state, and
// the verdict between them.
type PackageEvidence struct {
	Profile         string `json:"profile"`
	Package         string `json:"package"`
	Required        string `json:"required_version,omitempty"`
	RequiredDigest  string `json:"required_digest,omitempty"`
	Installed       string `json:"installed_version,omitempty"`
	InstalledDigest string `json:"installed_digest,omitempty"`
	Bucket          string `json:"bucket,omitempty"`
	// InstalledAt is when this machine installed what it has, which is
	// often the fact an audit is actually after.
	InstalledAt *time.Time `json:"installed_at,omitempty"`
	Conformant  bool       `json:"conformant"`
	Verdict     string     `json:"verdict"` // "matches" or the reason it does not
}

// CheckEvidence runs the same comparison as Check and records every
// package it looked at, not only the ones that failed.
//
// version and commit come from the caller because they belong to the
// binary, not to this package -- and an evidence file that cannot say
// which build produced it is missing the one thing that makes it
// reproducible.
func CheckEvidence(f profileset.File, names []string, path, version, commit string) (Evidence, error) {
	selected, err := f.Select(names)
	if err != nil {
		return Evidence{}, err
	}

	ev := Evidence{
		Tool:      "goop",
		Version:   version,
		Commit:    commit,
		CheckedAt: time.Now().UTC(),
		Root:      AppsDir(),
		File:      path,
		Profiles:  selected,
	}
	if h, err := fileSHA256(path); err == nil {
		ev.FileSHA256 = h
	}
	if hostname, err := os.Hostname(); err == nil {
		ev.Host = hostname
	}
	if u, err := user.Current(); err == nil {
		ev.User = u.Username
	}

	for _, name := range selected {
		prof := f.Profiles[name]
		all := prof.All()
		for _, pkg := range prof.SortedNames() {
			pin := all[pkg]
			pe := PackageEvidence{
				Profile:        name,
				Package:        pkg,
				Required:       pin.Version,
				RequiredDigest: pin.Hash,
			}
			if rec, ok := readCurrentRecord(pkg); ok {
				pe.Installed = rec.Version
				pe.InstalledDigest = rec.ManifestDigest
				pe.Bucket = rec.Bucket
				if !rec.InstalledAt.IsZero() {
					at := rec.InstalledAt.UTC()
					pe.InstalledAt = &at
				}
			}
			if d, bad := checkOne(name, pkg, pin); bad {
				pe.Verdict = d.Reason
				ev.Deviations++
			} else {
				pe.Conformant = true
				pe.Verdict = "matches"
			}
			ev.Packages = append(ev.Packages, pe)
		}
	}
	ev.Conformant = ev.Deviations == 0
	return ev, nil
}

// fileSHA256 hashes the requirement file, so the evidence names the exact
// bytes it was checked against rather than a path that may since have
// changed.
func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
