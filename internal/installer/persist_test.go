package installer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/paths"
)

// An uninstall keeps persisted data, matching Scoop -- the app is gone,
// your VLC settings are not. Confirmed on a real machine: apps/vlc
// removed, persist/vlc still there with 6.9 MB in it, alongside four
// other persist directories for apps uninstalled months earlier.
//
// The confirmation prompt used to say the opposite ("data persisted by
// these packages goes with them"), which is worse than saying nothing:
// somebody reading it would either cancel a removal they wanted, or
// assume their data was gone and never look for it.
func TestUninstall_KeepsPersistedData(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "vlc", Version: "3.0", State: "ready"})

	persisted := paths.Persist("vlc")
	if err := os.MkdirAll(persisted, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(persisted, "vlcrc")
	if err := os.WriteFile(settings, []byte("volume=50"), 0o644); err != nil {
		t.Fatal(err)
	}

	size, ok := PersistedSize("vlc")
	if !ok || size == 0 {
		t.Fatalf("PersistedSize = %d, %v -- a caller cannot say what is kept without this", size, ok)
	}

	if err := Uninstall("vlc", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settings); err != nil {
		t.Errorf("persisted data must survive an uninstall: %v", err)
	}
	if _, err := os.Stat(paths.App("vlc")); !os.IsNotExist(err) {
		t.Error("the app itself should be gone")
	}
}

// Purging is separate because it destroys what an uninstall deliberately
// preserves, and that has to be asked for by name.
func TestPurgePersisted(t *testing.T) {
	isolateRoot(t)
	persisted := paths.Persist("vlc")
	if err := os.MkdirAll(persisted, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(persisted, "vlcrc"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PurgePersisted("vlc"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(persisted); !os.IsNotExist(err) {
		t.Error("--purge must actually delete it")
	}

	// Nothing to purge is success, not an error: a caller purging a whole
	// uninstall plan must not fail on the packages that persisted nothing.
	if err := PurgePersisted("never-had-any"); err != nil {
		t.Errorf("purging nothing must succeed: %v", err)
	}
	if _, ok := PersistedSize("never-had-any"); ok {
		t.Error("PersistedSize must report absence rather than zero")
	}
}
