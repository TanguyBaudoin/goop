package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/paths"
)

// CPT-07: import an existing Scoop installation without reinstalling
// packages. goop only ever *reads* from the Scoop tree -- `current` is
// junctioned straight at Scoop's real version directory, so goop's shims
// run the exact files Scoop already downloaded and extracted, and an
// imported app's own uninstaller hook is deliberately never stored or
// run (it could carry Scoop-specific side effects); `goop uninstall` on
// an imported app only ever removes goop's own bookkeeping/junction/
// shims, never anything under the Scoop tree (NR-07/GOV-07
// reversibility: the user can keep using real Scoop for it, untouched).

type scoopInstallJSON struct {
	Bucket       string `json:"bucket"`
	Architecture string `json:"architecture"`
}

// DetectScoopRoot finds a real Scoop installation: $SCOOP if set, else
// "<home>\scoop". Returns ok=false if no "apps" directory is found there.
func DetectScoopRoot() (root string, ok bool) {
	root = os.Getenv("SCOOP")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		root = filepath.Join(home, "scoop")
	}
	if info, err := os.Stat(filepath.Join(root, "apps")); err != nil || !info.IsDir() {
		return "", false
	}
	return root, true
}

// ImportableApps lists app names under scoopRoot with a resolvable
// `current` link. "scoop" itself (Scoop's own self-management app) is
// excluded -- goop doesn't manage Scoop.
func ImportableApps(scoopRoot string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(scoopRoot, "apps"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "scoop" {
			continue
		}
		current := filepath.Join(scoopRoot, "apps", e.Name(), "current")
		if _, err := os.Readlink(current); err != nil {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}

// Import brings appName, already installed by a real Scoop under
// scoopRoot, under goop's management: readable via `goop list`,
// runnable via goop's own shims, removable via `goop uninstall` --
// without downloading, extracting, or copying anything.
func Import(scoopRoot, appName string) (Record, error) {
	if err := paths.EnsureLayout(); err != nil {
		return Record{}, err
	}

	currentLink := filepath.Join(scoopRoot, "apps", appName, "current")
	realVersionDir, err := os.Readlink(currentLink)
	if err != nil {
		return Record{}, fmt.Errorf("%s: not found under Scoop root %s: %w", appName, scoopRoot, err)
	}
	version := filepath.Base(realVersionDir)

	localVersionDir := paths.AppVersion(appName, version)
	if rec, ok := readRecord(localVersionDir); ok {
		Logf("%s %s already imported", appName, version)
		if err := relinkCurrent(appName, realVersionDir); err != nil {
			return Record{}, err
		}
		return rec, nil
	}

	var sij scoopInstallJSON
	installData, err := os.ReadFile(filepath.Join(realVersionDir, "install.json"))
	if err != nil {
		return Record{}, fmt.Errorf("%s: read Scoop install.json: %w", appName, err)
	}
	if err := json.Unmarshal(installData, &sij); err != nil {
		return Record{}, fmt.Errorf("%s: parse Scoop install.json: %w", appName, err)
	}

	manifestData, err := os.ReadFile(filepath.Join(realVersionDir, "manifest.json"))
	if err != nil {
		return Record{}, fmt.Errorf("%s: read Scoop manifest.json: %w", appName, err)
	}
	m, err := manifest.Decode(manifestData)
	if err != nil {
		return Record{}, fmt.Errorf("%s: decode Scoop manifest.json: %w", appName, err)
	}
	resolved, err := m.Resolve(appName, sij.Architecture)
	if err != nil {
		return Record{}, fmt.Errorf("%s: %w", appName, err)
	}

	if err := os.MkdirAll(localVersionDir, 0o755); err != nil {
		return Record{}, err
	}
	rec := Record{
		Name:         appName,
		Version:      version,
		Bucket:       sij.Bucket,
		Architecture: sij.Architecture,
		URLs:         resolved.URLs,
		Hashes:       resolved.Hashes,
		Bin:          resolved.Bin,
		ExtractDirs:  resolved.ExtractDirs,
		ExtractTos:   resolved.ExtractTos,
		// Shortcuts and Uninstaller are deliberately omitted: Scoop
		// already created any shortcuts for this app, and running its
		// uninstaller hook against goop's own bookkeeping (rather than
		// Scoop's) could have Scoop-specific side effects goop has no
		// business triggering.
		InstalledAt: time.Now().UTC(),
	}
	if err := writeRecord(localVersionDir, rec); err != nil {
		return Record{}, err
	}

	if err := relinkCurrent(appName, realVersionDir); err != nil {
		return Record{}, err
	}
	if err := createShims(appName, resolved.Bin); err != nil {
		return Record{}, err
	}

	Logf("imported %s %s from Scoop (bucket: %s)", appName, version, sij.Bucket)
	return rec, nil
}
