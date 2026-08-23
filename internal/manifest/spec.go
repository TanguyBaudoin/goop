package manifest

import "strings"

// Spec is a parsed app reference, as it appears both in a `depends`
// entry (e.g. "extras/86box") and as a CLI install argument (e.g.
// "jq@1.8.2" or "extras/mpv@>=0.40"). Bucket empty means "search
// configured buckets in priority order"; Constraint empty means "any
// version" (A4, FR-06).
type Spec struct {
	Bucket     string
	Name       string
	Constraint string
}

// ParseSpec parses "[bucket/]name[@constraint]".
func ParseSpec(s string) Spec {
	var spec Spec
	rest := s
	if b, n, ok := strings.Cut(rest, "/"); ok {
		spec.Bucket, rest = b, n
	}
	if n, c, ok := strings.Cut(rest, "@"); ok {
		spec.Name, spec.Constraint = n, c
	} else {
		spec.Name = rest
	}
	return spec
}

// String reconstructs the spec's canonical text form, e.g. for error
// messages.
func (s Spec) String() string {
	out := s.Name
	if s.Bucket != "" {
		out = s.Bucket + "/" + out
	}
	if s.Constraint != "" {
		out += "@" + s.Constraint
	}
	return out
}
