package installer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/paths"
)

// linkPersist wires each persist entry to goop's stable per-app persist
// store (NR-04): on first install, whatever the app shipped at that path
// seeds the store (or an empty directory is created if it shipped
// nothing there); on every install, the staging path is replaced with a
// link into the store, so app data survives version upgrades. The store
// itself is never removed by an ordinary uninstall.
func linkPersist(appName string, entries []manifest.PersistEntry, stagingDir string) error {
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(paths.Persist(appName), 0o755); err != nil {
		return err
	}

	for _, e := range entries {
		stagingPath := filepath.Join(stagingDir, filepath.FromSlash(e.Source))
		persistPath := paths.PersistEntry(appName, e.Target)

		if _, err := os.Stat(persistPath); os.IsNotExist(err) {
			if _, statErr := os.Lstat(stagingPath); statErr == nil {
				if err := os.MkdirAll(filepath.Dir(persistPath), 0o755); err != nil {
					return err
				}
				if err := os.Rename(stagingPath, persistPath); err != nil {
					return fmt.Errorf("persist %s: seed store: %w", e.Source, err)
				}
			} else {
				if err := os.MkdirAll(persistPath, 0o755); err != nil {
					return fmt.Errorf("persist %s: create empty store: %w", e.Source, err)
				}
			}
		} else {
			// Upgrade: whatever the new version shipped there is
			// discarded in favor of the already-persisted data.
			os.RemoveAll(stagingPath)
		}

		if err := os.MkdirAll(filepath.Dir(stagingPath), 0o755); err != nil {
			return err
		}
		info, err := os.Stat(persistPath)
		if err != nil {
			return fmt.Errorf("persist %s: %w", e.Target, err)
		}
		if info.IsDir() {
			out, err := exec.Command("cmd", "/c", "mklink", "/J", stagingPath, persistPath).CombinedOutput()
			if err != nil {
				return fmt.Errorf("persist %s: link directory: %w\n%s", e.Source, err, out)
			}
		} else if err := os.Link(persistPath, stagingPath); err != nil {
			return fmt.Errorf("persist %s: link file: %w", e.Source, err)
		}
	}
	return nil
}
