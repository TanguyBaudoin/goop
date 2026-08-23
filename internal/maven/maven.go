// Package maven resolves a Maven-repository coordinate
// (groupId:artifactId:version:classifier:packaging) to a downloadable
// artifact URL and its published hash -- goop's second, manifest-free
// install path (`goop install maven:...`) for tools only ever published
// as a Maven distribution archive, never as a Scoop manifest.
package maven

import (
	"fmt"
	"strings"
)

// Coordinate identifies one Maven artifact.
type Coordinate struct {
	GroupID    string
	ArtifactID string
	Version    string
	Classifier string // may be empty
	Packaging  string
}

// ParseCoordinate parses "groupId:artifactId:version:classifier:packaging"
// -- exactly 5 colon-separated fields, Classifier may be the empty string
// (e.g. "org.foo:tool:1.0::zip" for an artifact with no classifier). The
// caller is responsible for stripping any "maven:" scheme prefix first.
func ParseCoordinate(s string) (Coordinate, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 5 {
		return Coordinate{}, fmt.Errorf(
			"invalid Maven coordinate %q: want groupId:artifactId:version:classifier:packaging (classifier may be empty)", s)
	}
	c := Coordinate{
		GroupID:    parts[0],
		ArtifactID: parts[1],
		Version:    parts[2],
		Classifier: parts[3],
		Packaging:  parts[4],
	}
	if c.GroupID == "" || c.ArtifactID == "" || c.Version == "" || c.Packaging == "" {
		return Coordinate{}, fmt.Errorf("invalid Maven coordinate %q: groupId, artifactId, version, and packaging must not be empty", s)
	}
	return c, nil
}

// Filename is the artifact's filename within its Maven-layout directory:
// "<artifactId>-<version>[-<classifier>].<packaging>".
func (c Coordinate) Filename() string {
	name := c.ArtifactID + "-" + c.Version
	if c.Classifier != "" {
		name += "-" + c.Classifier
	}
	return name + "." + c.Packaging
}

// URL builds the artifact's download URL under repoBase using standard
// Maven repository layout:
// "<repoBase>/<groupId with '.' -> '/'>/<artifactId>/<version>/<filename>".
func (c Coordinate) URL(repoBase string) string {
	groupPath := strings.ReplaceAll(c.GroupID, ".", "/")
	base := strings.TrimRight(repoBase, "/")
	return strings.Join([]string{base, groupPath, c.ArtifactID, c.Version, c.Filename()}, "/")
}

// SplitSpec splits a "maven:"-stripped spec into an optional repo-name
// qualifier and the coordinate string, e.g.
// "internal/org.foo:tool:1.0::zip" -> ("internal", "org.foo:tool:1.0::zip"),
// or "org.foo:tool:1.0::zip" -> ("", "org.foo:tool:1.0::zip") when
// unqualified -- "/" is an unambiguous separator since a Maven
// coordinate's fields never contain one, the same convention Scoop's
// own [bucket/]name spec grammar already uses.
func SplitSpec(s string) (repoName, coordStr string) {
	if before, after, found := strings.Cut(s, "/"); found {
		return before, after
	}
	return "", s
}
