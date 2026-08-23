package installer

import (
	"fmt"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/bucket"
	"github.com/TanguyBaudoin/goop/internal/manifest"
)

// implicitHelpers returns the helper tools appName needs installed
// before its own extraction can run -- mirrors real Scoop's
// Get-InstallationHelper (lib/depends.ps1), which feeds them into
// Get-Dependency alongside the manifest's own declared `depends`.
// Without this a manifest whose URL needs 7z fails at extraction time
// telling the user to go install 7-Zip with some *other* package
// manager, which is a poor look for a package manager and something
// real Scoop never does -- it just installs the helper itself.
//
// Two deliberate divergences from Scoop's own list:
//
//   - No lessmsi. Scoop only adds it when its USE_LESSMSI config is
//     set; goop extracts MSIs via `msiexec /a` (extractViaMsi) and
//     never calls lessmsi at all.
//   - .zip/.tar/.tar.gz don't pull in 7zip. Scoop delegates every
//     archive to 7z, goop extracts those natively in Go
//     (internal/archive), so only the formats that genuinely reach
//     extractVia7z count -- see needsSevenZip.
func implicitHelpers(r manifest.Resolved) []string {
	script := r.PreInstall + r.Installer.Script + r.PostInstall

	var out []string
	if needsSevenZip(r) || strings.Contains(script, "Expand-7zipArchive") {
		out = append(out, "7zip")
	}
	if r.InnoSetup || strings.Contains(script, "Expand-InnoArchive") {
		out = append(out, "innounp")
	}
	if strings.Contains(script, "Expand-DarkArchive") {
		out = append(out, "dark")
	}
	return out
}

// needsSevenZip reports whether any of r's URLs lands on a format
// placeAsset routes through extractVia7z -- the same archiveExt/
// sevenZipExts pair that dispatch uses, so the two can't disagree
// about whether 7z.exe will be needed.
func needsSevenZip(r manifest.Resolved) bool {
	for _, rawURL := range r.URLs {
		assetURL, fragName := manifest.SplitURLFragment(rawURL)
		if fragName == "" {
			fragName = basenameWithoutQuery(assetURL)
		}
		if ext := archiveExt(fragName); ext == ".7z" || sevenZipExts[ext] {
			return true
		}
	}
	return false
}

// DependencyEntry is one app in a resolved dependency closure.
type DependencyEntry struct {
	Name   string
	Bucket string
}

// ResolveDependencies returns spec's full dependency closure in the
// order an install would process it, with spec's own app last --
// matching real Scoop's `scoop depends` (libexec/scoop-depends.ps1,
// via Get-Dependency): post-order depth-first, deduplicated, erroring
// on a cycle. Resolves manifests only; nothing is installed.
//
// Implicit helpers (implicitHelpers) are included, exactly as
// Get-Dependency includes Get-InstallationHelper's output, so this
// reports what an install will really do rather than just what the
// manifest's `depends` field says.
func ResolveDependencies(spec string) ([]DependencyEntry, error) {
	archKey, err := manifest.HostArchKey()
	if err != nil {
		return nil, err
	}
	var out []DependencyEntry
	seen := map[string]bool{}
	if err := resolveDependenciesInto(spec, archKey, &out, seen, nil); err != nil {
		return nil, err
	}
	return out, nil
}

func resolveDependenciesInto(spec, archKey string, out *[]DependencyEntry, seen map[string]bool, stack []string) error {
	parsed := manifest.ParseSpec(spec)
	appName := parsed.Name

	if seen[appName] {
		return nil
	}
	for _, s := range stack {
		if s == appName {
			return fmt.Errorf("dependency cycle detected: %s -> %s", strings.Join(stack, " -> "), appName)
		}
	}

	bucketName, m, err := bucket.Resolve(parsed)
	if err != nil {
		return err
	}
	resolved, err := m.Resolve(appName, archKey)
	if err != nil {
		return err
	}

	deps := append(append([]string{}, m.Depends...), implicitHelpers(resolved)...)
	childStack := append(append([]string{}, stack...), appName)
	for _, dep := range deps {
		if err := resolveDependenciesInto(dep, archKey, out, seen, childStack); err != nil {
			return err
		}
	}

	// Post-order: everything appName needs is already in out, so
	// appName itself goes last -- which makes the whole slice a valid
	// installation order, same as Get-Dependency's own output.
	if !seen[appName] {
		seen[appName] = true
		*out = append(*out, DependencyEntry{Name: appName, Bucket: bucketName})
	}
	return nil
}
