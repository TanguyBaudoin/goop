package installer

import (
	"path/filepath"
	"testing"
)

// env_add_path names a versioned directory, so an update's entry is a
// different string from the one it replaces. Uninstall reverses entries;
// an update never uninstalls, so nothing did, and PATH grew on every
// update. Found on a real machine after a few months of use: 17 goop
// entries under apps/, 8 pointing at superseded versions and 5 at
// directories that no longer existed.
func TestStaleEnvEntries_DropsTheSupersededVersion(t *testing.T) {
	previous := Record{
		Name:          "nodejs-lts",
		EnvAddedPaths: []string{`C:\goop\apps\nodejs-lts\24.19.0`, `C:\goop\apps\nodejs-lts\24.19.0\bin`},
	}
	newPaths := []string{`C:\goop\apps\nodejs-lts\24.20.0`, `C:\goop\apps\nodejs-lts\24.20.0\bin`}

	_, stale := staleEnvEntries(previous, nil, newPaths)
	if len(stale) != 2 {
		t.Fatalf("stale = %v, want both 24.19.0 entries", stale)
	}
	for _, p := range stale {
		if filepath.Base(filepath.Dir(p)) == "24.20.0" || p == newPaths[0] {
			t.Errorf("the new version's own entry must not be removed: %s", p)
		}
	}
}

// A manifest whose env_add_path carries no version produces the same
// string on both sides. Removing it here would undo what applyEnv is
// about to do, leaving the app off PATH entirely.
func TestStaleEnvEntries_KeepsUnversionedPaths(t *testing.T) {
	shared := `C:\goop\apps\thing\shared\bin`
	previous := Record{Name: "thing", EnvAddedPaths: []string{shared}}

	_, stale := staleEnvEntries(previous, nil, []string{shared})
	if len(stale) != 0 {
		t.Errorf("an entry the new version also wants must be kept, got %v", stale)
	}

	// Same path, different spelling: still the same path.
	_, stale = staleEnvEntries(previous, nil, []string{`c:\goop\apps\thing\shared\.\bin`})
	if len(stale) != 0 {
		t.Errorf("case and separators must not make it look like a different entry, got %v", stale)
	}
}

// env_set is replaced rather than accumulated, so only a variable the new
// version stops setting needs unsetting -- unsetting one it still wants
// would clear it right before applyEnv sets it again, and lose it if that
// failed.
func TestStaleEnvEntries_OnlyAbandonedVariables(t *testing.T) {
	previous := Record{
		Name:   "jdk",
		EnvSet: map[string]string{"JAVA_HOME": `C:\old`, "GONE": "x"},
	}
	newSet := map[string]string{"JAVA_HOME": `C:\new`}

	staleSet, _ := staleEnvEntries(previous, newSet, nil)
	if _, ok := staleSet["JAVA_HOME"]; ok {
		t.Error("a variable the new version still sets must not be unset")
	}
	if _, ok := staleSet["GONE"]; !ok {
		t.Errorf("a variable the new version abandons must be unset, got %v", staleSet)
	}
}

// A first install has nothing to supersede, and must not try.
func TestStaleEnvEntries_FirstInstall(t *testing.T) {
	staleSet, stale := staleEnvEntries(Record{}, map[string]string{"A": "1"}, []string{`C:\x`})
	if len(staleSet) != 0 || len(stale) != 0 {
		t.Errorf("nothing to supersede, got %v / %v", staleSet, stale)
	}
}
