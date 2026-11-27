package installer

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/envvars"
	"github.com/TanguyBaudoin/goop/internal/paths"
)

// Records carry no Bin entries on purpose: createShims needs the shared
// shim binary, which a bare test root has no way to produce, and none of
// what `use` does differently depends on shims existing. Shims point at
// `current` rather than at a version directory, so they follow the
// junction without being rebuilt at all -- which is precisely why the
// interesting part of this command is the environment, not the shims.

func TestInstalledVersions_ListsNewestFirstAndMarksCurrent(t *testing.T) {
	isolateRoot(t)
	realInstall(t, "jq", "1.7.1")
	realInstall(t, "jq", "1.8.2") // the later call leaves current here

	versions, err := InstalledVersions("jq")
	if err != nil {
		t.Fatalf("InstalledVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("got %d versions, want 2: %+v", len(versions), versions)
	}
	if versions[0].Version != "1.8.2" {
		t.Errorf("newest first: got %q then %q", versions[0].Version, versions[1].Version)
	}
	if !versions[0].Current {
		t.Error("1.8.2 is what current points at and must be marked")
	}
	if versions[1].Current {
		t.Error("1.7.1 must not be marked current")
	}
}

// A version directory with no receipt cannot be switched to -- there
// would be nothing to rebuild the wiring from -- so offering it would be
// offering a switch that fails. doctor reports those separately.
func TestInstalledVersions_SkipsVersionsWithoutAReceipt(t *testing.T) {
	isolateRoot(t)
	realInstall(t, "jq", "1.8.2")
	if err := os.MkdirAll(paths.AppVersion("jq", "1.6"), 0o755); err != nil {
		t.Fatal(err)
	}

	versions, err := InstalledVersions("jq")
	if err != nil {
		t.Fatalf("InstalledVersions: %v", err)
	}
	for _, v := range versions {
		if v.Version == "1.6" {
			t.Error("a version with no receipt must not be offered")
		}
	}
}

func TestInstalledVersions_NotInstalled(t *testing.T) {
	isolateRoot(t)
	if _, err := InstalledVersions("ghost"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("want ErrNotInstalled, got %v", err)
	}
}

// The command's reason for existing: move current to a version already
// on disk, with no manifest involved. Proven against a real bucket
// elsewhere; here it is the mechanism.
func TestUse_SwitchesCurrent(t *testing.T) {
	isolateRoot(t)
	isolateEnvRegistry(t)
	realInstall(t, "jq", "1.7.1")
	realInstall(t, "jq", "1.8.2")

	if err := Use("jq", "1.7.1"); err != nil {
		t.Fatalf("Use: %v", err)
	}
	rec, ok := CurrentRecord("jq")
	if !ok {
		t.Fatal("no current record after the switch")
	}
	if rec.Version != "1.7.1" {
		t.Errorf("current is %s, want 1.7.1", rec.Version)
	}
}

// The subtle one, and the reason Use is not simply relink + Reset.
//
// Reset reverts and re-applies the record that current points at, so
// after relinking it would take back the *incoming* version's entries
// and leave the outgoing version's behind. env_add_path records an
// already-expanded, versioned path, so that stale entry would sit on
// PATH next to the new one -- which is how 80 of 98 per-user PATH
// entries came to be dead on the development machine.
func TestUse_TakesBackTheOutgoingVersionsPathEntry(t *testing.T) {
	isolateRoot(t)
	isolateEnvRegistry(t)

	oldBin := paths.AppVersion("tool", "1.0") + `\bin`
	newBin := paths.AppVersion("tool", "2.0") + `\bin`
	realInstall(t, "tool", "1.0", func(r *Record) { r.EnvAddedPaths = []string{oldBin} })
	realInstall(t, "tool", "2.0", func(r *Record) { r.EnvAddedPaths = []string{newBin} })

	// realInstall already leaves current on 2.0, so Use("2.0") would be
	// the no-op branch and apply nothing. Reset applies the current
	// record's environment, which is what the install itself would have
	// done -- that is the state this test needs to start from.
	if err := Reset("tool"); err != nil {
		t.Fatalf("applying 2.0's environment: %v", err)
	}
	if !pathHas(t, newBin) {
		t.Fatalf("2.0's entry should be on PATH before the switch")
	}

	if err := Use("tool", "1.0"); err != nil {
		t.Fatalf("Use 1.0: %v", err)
	}
	if !pathHas(t, oldBin) {
		t.Error("1.0's entry must be on PATH after switching to it")
	}
	if pathHas(t, newBin) {
		t.Error("2.0's versioned entry must be taken back, not left alongside 1.0's")
	}
}

// Switching to the version already in use must do nothing rather than
// relink and rewire for no reason -- Reset shells out to PowerShell to
// check for running processes, rebuilds every shim and writes to the
// registry, all of it pointless here.
//
// Asserted through the log line, because that is the only observable
// difference: without the early return the relink and Reset both succeed
// and leave exactly the same state behind, so a test that only checked
// the outcome passed either way. It did, until this was tightened.
func TestUse_AlreadyOnThatVersionIsANoOp(t *testing.T) {
	isolateRoot(t)
	isolateEnvRegistry(t)
	realInstall(t, "jq", "1.8.2")

	prev := Logf
	var logged []string
	Logf = func(format string, args ...any) {
		logged = append(logged, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() { Logf = prev })

	if err := Use("jq", "1.8.2"); err != nil {
		t.Errorf("switching to the version already in use must succeed quietly, got %v", err)
	}
	rec, _ := CurrentRecord("jq")
	if rec.Version != "1.8.2" {
		t.Errorf("current is %s, want 1.8.2 untouched", rec.Version)
	}

	joined := strings.Join(logged, "\n")
	if !strings.Contains(joined, "already on 1.8.2") {
		t.Errorf("want it to report the no-op, got:\n%s", joined)
	}
	// Reset's own chatter is the tell that work was done anyway.
	if strings.Contains(joined, "resetting") {
		t.Errorf("a no-op must not relink and rewire, got:\n%s", joined)
	}
}

// The refusal has to say what *is* available, because the whole point of
// the command is that the caller does not remember version strings.
func TestUse_RefusesAnUninstalledVersionAndListsWhatThereIs(t *testing.T) {
	isolateRoot(t)
	realInstall(t, "jq", "1.8.2")

	err := Use("jq", "1.6")
	if err == nil {
		t.Fatal("switching to a version that is not installed must be refused")
	}
	if !strings.Contains(err.Error(), "1.8.2") {
		t.Errorf("the refusal should list the installed versions, got: %v", err)
	}
}

// goop reaches its own binary through the bin junction, not through
// apps\goop\current, so moving the latter would change nothing.
func TestUse_RefusesGoopItself(t *testing.T) {
	isolateRoot(t)
	realInstall(t, SelfPackageName, "0.9.0")
	realInstall(t, SelfPackageName, "0.9.1")

	err := Use(SelfPackageName, "0.9.0")
	if err == nil {
		t.Fatal("switching goop's own version must be refused")
	}
	if !strings.Contains(err.Error(), "self-update") {
		t.Errorf("the refusal should point at self-update, got: %v", err)
	}
}

func pathHas(t *testing.T, want string) bool {
	t.Helper()
	entries, err := envvars.PathEntries()
	if err != nil {
		t.Fatalf("reading PATH: %v", err)
	}
	for _, e := range entries {
		if strings.EqualFold(strings.TrimRight(e, `\`), strings.TrimRight(want, `\`)) {
			return true
		}
	}
	return false
}
