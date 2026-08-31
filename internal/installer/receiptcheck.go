// Package-internal helpers for reading what a receipt promised against
// what is on disk. They outlived the lockfile machinery they were
// written for: a version match is not conformance, and both
// `goop profile check` and `goop machine audit` need to look at the
// files themselves.
package installer

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/paths"
)

// missingShimTargets returns the first shim whose sidecar names a target
// that is not there, or "" when every declared command resolves. Checked
// against the disk rather than the record, because the record is exactly
// what cannot be trusted here.
func missingShimTargets(rec Record) string {
	for _, b := range rec.Bin {
		sidecar := filepath.Join(paths.Shims(), b.Name+".shim")
		data, err := os.ReadFile(sidecar)
		if err != nil {
			return b.Name + " (no sidecar)"
		}
		target := sidecarTargetPath(string(data))
		if target == "" {
			continue
		}
		if _, err := os.Stat(target); err != nil {
			return b.Name
		}
	}
	return ""
}

// sidecarTargetPath pulls the path out of a `path = "..."` sidecar line.
func sidecarTargetPath(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), string(rune(0xFEFF)))
		rest, ok := strings.CutPrefix(line, "path")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		rest, ok = strings.CutPrefix(rest, "=")
		if !ok {
			continue
		}
		return strings.Trim(strings.TrimSpace(rest), `"`)
	}
	return ""
}
