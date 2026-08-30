package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/paths"
)

// realCurrent builds the state a self-updating app leaves behind: a real
// directory where goop expects a junction, holding the app and its
// receipt. Zen Browser does exactly this, confirmed on a real install.
func realCurrent(t *testing.T, app, version string, extra ...string) string {
	t.Helper()
	cur := paths.AppCurrent(app)
	if err := os.MkdirAll(cur, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := Record{Name: app, Version: version}
	data, _ := json.MarshalIndent(rec, "", "  ")
	if err := os.WriteFile(filepath.Join(cur, recordFileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range extra {
		if err := os.WriteFile(filepath.Join(cur, f), []byte("payload"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return cur
}

// The version directory its own receipt names is where it belongs, so
// putting it back restores the layout and loses nothing.
func TestDetachRealCurrent_GoesHomeWhenThatIsFree(t *testing.T) {
	isolateRoot(t)
	cur := realCurrent(t, "zen-browser", "1.21.15b", "application.ini")
	// The near-empty version directory the app left behind.
	if err := os.MkdirAll(paths.AppVersion("zen-browser", "1.21.15b"), 0o755); err != nil {
		t.Fatal(err)
	}

	aside, err := detachRealCurrent("zen-browser", cur)
	if err != nil {
		t.Fatal(err)
	}
	if want := paths.AppVersion("zen-browser", "1.21.15b"); aside != want {
		t.Errorf("moved to %s, want %s", aside, want)
	}
	if _, err := os.Stat(filepath.Join(aside, "application.ini")); err != nil {
		t.Errorf("the payload must survive the move: %v", err)
	}
	if _, err := os.Lstat(cur); !os.IsNotExist(err) {
		t.Error("current must be gone so the junction can be created")
	}
}

// Overwriting a real install goop did not create would destroy it, so a
// home that already holds one means the directory goes aside instead.
func TestDetachRealCurrent_NeverOverwritesAnInstall(t *testing.T) {
	isolateRoot(t)
	cur := realCurrent(t, "zen-browser", "1.21.15b", "application.ini")

	home := paths.AppVersion("zen-browser", "1.21.15b")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "keep.txt"), []byte("do not lose me"), 0o644); err != nil {
		t.Fatal(err)
	}

	aside, err := detachRealCurrent("zen-browser", cur)
	if err != nil {
		t.Fatal(err)
	}
	if aside == home {
		t.Fatal("a version directory holding an install must not be overwritten")
	}
	if got, err := os.ReadFile(filepath.Join(home, "keep.txt")); err != nil || string(got) != "do not lose me" {
		t.Errorf("the existing install was damaged: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(aside, "application.ini")); err != nil {
		t.Errorf("the detached payload must survive: %v", err)
	}
}

// Nothing is ever deleted, so a second occurrence needs its own name.
func TestDetachRealCurrent_DoesNotCollideWithItself(t *testing.T) {
	isolateRoot(t)
	home := paths.AppVersion("zen-browser", "1.21.15b")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(home, "keep.txt"), []byte("x"), 0o644)

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		cur := realCurrent(t, "zen-browser", "1.21.15b", "application.ini")
		aside, err := detachRealCurrent("zen-browser", cur)
		if err != nil {
			t.Fatal(err)
		}
		if seen[aside] {
			t.Fatalf("reused %s, which would have destroyed the previous one", aside)
		}
		seen[aside] = true
	}
}

// A receipt that names no version cannot say where the directory belongs,
// so it still has to go somewhere rather than block the install.
func TestDetachRealCurrent_HandlesAnUnreadableReceipt(t *testing.T) {
	isolateRoot(t)
	cur := paths.AppCurrent("weird")
	if err := os.MkdirAll(cur, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(cur, "payload.txt"), []byte("x"), 0o644)

	aside, err := detachRealCurrent("weird", cur)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(aside, "payload.txt")); err != nil {
		t.Errorf("payload must survive even with no receipt: %v", err)
	}
}
