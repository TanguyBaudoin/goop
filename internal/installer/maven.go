package installer

import (
	"fmt"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/maven"
	"github.com/TanguyBaudoin/goop/internal/mavenrepo"
	"github.com/TanguyBaudoin/goop/internal/paths"
)

// installMaven resolves a
// "maven:[reponame/]groupId:artifactId:version:classifier:packaging"
// spec (the "maven:" prefix already confirmed by installSpec) against a
// configured Maven repo (`goop maven-repo add`) -- a specific one if
// reponame is given, otherwise every configured repo in priority order
// -- and installs the resulting artifact through the same atomic
// pipeline as any other install (installResolved): download,
// sha1-verify, extract, commit.
//
// V1 scope: distribution archives only. No shim is created automatically
// -- there's no manifest to declare a `bin` entry from, and guessing one
// from a random distribution's internal layout is too fragile to be
// worth it; `goop info <name>` shows the install path so the extracted
// tool can be run directly.
//
// This bypasses installSpec's normal appName/lock flow entirely (a
// Maven coordinate doesn't parse as "[bucket/]name[@constraint]"), so it
// mirrors installSpec's own locking/layout prologue itself rather than
// sharing it.
func installMaven(spec string) (Record, error) {
	repoName, coordStr := maven.SplitSpec(strings.TrimPrefix(spec, "maven:"))
	coord, err := maven.ParseCoordinate(coordStr)
	if err != nil {
		return Record{}, fmt.Errorf("%s: %w", spec, err)
	}

	unlock := lockInstall(coord.ArtifactID)
	defer unlock()

	if err := paths.EnsureLayout(); err != nil {
		return Record{}, err
	}

	url, hash, err := mavenrepo.Resolve(repoName, coord)
	if err != nil {
		return Record{}, fmt.Errorf("%s: %w", spec, err)
	}

	archKey, _ := manifest.HostArchKey() // best-effort, purely informational: Maven artifacts aren't architecture-keyed

	resolved := manifest.Resolved{
		Name:    coord.ArtifactID,
		Version: coord.Version,
		URLs:    []string{url},
		Hashes:  []string{hash},
	}
	return installResolved(coord.ArtifactID, "maven", archKey, resolved, false)
}
