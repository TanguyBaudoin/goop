package manifest

import (
	"fmt"
	"runtime"
	"strings"
)

// Resolved is a manifest with its architecture-specific fields merged in,
// ready to drive a download+install.
type Resolved struct {
	Name    string
	Version string

	// Digest is the manifest's fingerprint, recorded with the install so
	// a later check can tell whether the instructions changed without
	// going back to the bucket.
	Digest string

	Description       string
	Homepage          string
	LicenseIdentifier string
	LicenseURL        string
	URLs              []string
	Hashes            []string // aligned by index with URLs; may be shorter (unverified entries) but never longer
	Bin               BinList
	// ExtractDirs/ExtractTos are aligned by index with URLs. A manifest
	// giving a single value applies it to every URL; ExtractDirFor/
	// ExtractToFor resolve that.
	ExtractDirs []string
	ExtractTos  []string

	PreInstall    string
	PostInstall   string
	Installer     InstallHook
	Uninstaller   InstallHook
	PreUninstall  string
	PostUninstall string

	// Depends is the manifest's own declared `depends` only -- never the
	// implicit helper tools installer.implicitHelpers adds on top. It's
	// recorded per install so uninstall can find an app's dependents;
	// helpers are deliberately excluded there (removing 7zip doesn't
	// break an app that was merely extracted with it).
	Depends []string

	Persist    []PersistEntry
	Shortcuts  []ShortcutEntry
	EnvSet     map[string]string
	EnvAddPath []string
	Notes      []string
	Suggest    map[string]StringList

	InnoSetup    bool
	PSModuleName string
}

// ExtractDirFor returns the extract_dir to apply to URLs[i], honoring a
// manifest that gave one value for all URLs vs. one value per URL.
func (r Resolved) ExtractDirFor(i int) string {
	return pickAligned(r.ExtractDirs, i)
}

// ExtractToFor returns the extract_to to apply to URLs[i].
func (r Resolved) ExtractToFor(i int) string {
	return pickAligned(r.ExtractTos, i)
}

func pickAligned(values []string, i int) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	default:
		if i < len(values) {
			return values[i]
		}
		return ""
	}
}

// HostArchKey maps the running process's GOARCH to the Scoop manifest
// architecture key ("64bit", "32bit", "arm64").
func HostArchKey() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "64bit", nil
	case "386":
		return "32bit", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported host architecture %q", runtime.GOARCH)
	}
}

// Resolve merges m's base fields with its architecture.<archKey> override
// (if any), producing the concrete set of URLs/hashes/bin/extraction
// settings to install for that architecture.
func (m Manifest) Resolve(name, archKey string) (Resolved, error) {
	r := Resolved{
		Name:              name,
		Version:           m.Version,
		Digest:            m.Digest,
		Description:       m.Description,
		Homepage:          m.Homepage,
		LicenseIdentifier: m.LicenseIdentifier,
		LicenseURL:        m.LicenseURL,
		URLs:              m.URL,
		Hashes:            m.Hash,
		Bin:               m.Bin,
		ExtractDirs:       m.ExtractDir,
		ExtractTos:        m.ExtractTo,
		PreInstall:        m.PreInstall,
		PostInstall:       m.PostInstall,
		Installer:         m.Installer,
		Uninstaller:       m.Uninstaller,
		PreUninstall:      m.PreUninstall,
		PostUninstall:     m.PostUninstall,
		Persist:           m.Persist,
		Shortcuts:         m.Shortcuts,
		EnvSet:            m.EnvSet,
		EnvAddPath:        m.EnvAddPath,
		Depends:           m.Depends,
		Notes:             m.Notes,
		Suggest:           m.Suggest,
		InnoSetup:         m.InnoSetup,
		PSModuleName:      m.PSModuleName,
	}

	if ov, ok := m.Architecture[archKey]; ok {
		if len(ov.URL) > 0 {
			r.URLs = ov.URL
			r.Hashes = ov.Hash // only meaningful together with a matching URL override
		}
		if len(ov.Bin) > 0 {
			r.Bin = ov.Bin
		}
		if len(ov.ExtractDir) > 0 {
			r.ExtractDirs = ov.ExtractDir
		}
		if len(ov.ExtractTo) > 0 {
			r.ExtractTos = ov.ExtractTo
		}
		if ov.PreInstall != "" {
			r.PreInstall = ov.PreInstall
		}
		if ov.PostInstall != "" {
			r.PostInstall = ov.PostInstall
		}
		if !ov.Installer.IsZero() {
			r.Installer = ov.Installer
		}
		if !ov.Uninstaller.IsZero() {
			r.Uninstaller = ov.Uninstaller
		}
		if ov.PreUninstall != "" {
			r.PreUninstall = ov.PreUninstall
		}
		if ov.PostUninstall != "" {
			r.PostUninstall = ov.PostUninstall
		}
		if len(ov.Shortcuts) > 0 {
			r.Shortcuts = ov.Shortcuts
		}
	} else if len(m.Architecture) > 0 && len(r.URLs) == 0 {
		return Resolved{}, fmt.Errorf("manifest has no architecture %q and no base url", archKey)
	}

	if len(r.URLs) == 0 {
		return Resolved{}, fmt.Errorf("manifest has no url for architecture %q", archKey)
	}
	if len(r.Hashes) > 0 && len(r.Hashes) != len(r.URLs) {
		return Resolved{}, fmt.Errorf("manifest has %d url(s) but %d hash(es)", len(r.URLs), len(r.Hashes))
	}

	return r, nil
}

// SplitURLFragment splits a Scoop manifest URL of the form
// "https://.../asset.exe#/renamed.exe" into the real URL and the local
// filename it should be saved as (empty if no fragment is present, in
// which case the caller should fall back to the URL's own basename).
func SplitURLFragment(u string) (rawURL, filename string) {
	i := strings.Index(u, "#/")
	if i < 0 {
		return u, ""
	}
	return u[:i], u[i+2:]
}
