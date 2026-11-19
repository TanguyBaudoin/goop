package profile

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/paths"
	"testing"
)

// withTempRoot points paths.Root() at an isolated temp directory for the
// duration of one test (t.Setenv auto-restores GOOP_HOME afterward) --
// full isolation, no real system state touched, unlike the
// GOOP_TEST_CREDSTORE/GOOP_TEST_ENVVARS-gated tests elsewhere in this
// codebase that need an opt-in because they touch the real registry.
func withTempRoot(t *testing.T) {
	t.Helper()
	t.Setenv("GOOP_HOME", t.TempDir())
}

// The default profile used to *be* goop.lock.json, which made a soft
// grouping indistinguishable from a pinned artifact. It is now a profile
// file like any other, and an empty name still means default.
func TestPath_DefaultIsAProfileFile(t *testing.T) {
	withTempRoot(t)
	if got, want := Path(Default), Path(""); got != want {
		t.Errorf("Path(Default) = %q, Path(\"\") = %q, want equal", got, want)
	}
	if filepath.Base(Path(Default)) != "default.json" {
		t.Errorf("Path(Default) = %q, want it to end in default.json", Path(Default))
	}
	if Path(Default) == filepath.Join(paths.Root(), "goop.lock.json") {
		t.Error("the default profile must no longer alias the root lockfile")
	}
}

// Membership is stored sorted so that two machines built from the same
// profile file hold the same bytes.
//
// This is the defect acceptance criterion 3 turned up: a root where the
// packages were installed one by one kept install order, a root synced
// from the exported file used the sync's order, and profiles/<name>.json
// was the only file in either tree that disagreed. Order cannot be
// carried by a profile file -- its packages are a JSON object -- so the
// stored set has to be canonical instead.
func TestSave_SortsMembersSoTwoMachinesAgree(t *testing.T) {
	withTempRoot(t)

	// Two machines reach the same membership by different routes.
	installOrder := Definition{Name: "crit3", Apps: []string{"jq", "ripgrep", "7zip"}}
	syncOrder := Definition{Name: "crit3-b", Apps: []string{"7zip", "jq", "ripgrep"}}

	for _, d := range []Definition{installOrder, syncOrder} {
		if err := Save(d); err != nil {
			t.Fatalf("Save(%s): %v", d.Name, err)
		}
	}
	a, err := Load("crit3")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b, err := Load("crit3-b")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !slices.Equal(a.Apps, b.Apps) {
		t.Errorf("the same membership reached two ways must store identically:\n  A: %v\n  B: %v", a.Apps, b.Apps)
	}
	if want := []string{"7zip", "jq", "ripgrep"}; !slices.Equal(a.Apps, want) {
		t.Errorf("members = %v, want them sorted as %v", a.Apps, want)
	}

	// And the bytes themselves must match, since that is what the
	// two-root comparison actually checks.
	rawA, err := os.ReadFile(Path("crit3"))
	if err != nil {
		t.Fatalf("reading crit3: %v", err)
	}
	rawB, err := os.ReadFile(Path("crit3-b"))
	if err != nil {
		t.Fatalf("reading crit3-b: %v", err)
	}
	// The name differs by construction; everything after it must not.
	normA := strings.Replace(string(rawA), `"crit3"`, `"X"`, 1)
	normB := strings.Replace(string(rawB), `"crit3-b"`, `"X"`, 1)
	if normA != normB {
		t.Errorf("profile files differ beyond their name:\n  A: %s\n  B: %s", normA, normB)
	}
}

// Adding members one at a time must land in the same place as saving them
// all at once -- Add goes through Save, so the invariant has to hold on
// the incremental path too.
func TestAdd_KeepsMembersSorted(t *testing.T) {
	withTempRoot(t)
	for _, app := range []string{"zoxide", "jq", "ripgrep", "7zip"} {
		if err := Add("p", app); err != nil {
			t.Fatalf("Add(%s): %v", app, err)
		}
	}
	d, err := Load("p")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := []string{"7zip", "jq", "ripgrep", "zoxide"}; !slices.Equal(d.Apps, want) {
		t.Errorf("members = %v, want %v", d.Apps, want)
	}
}

func TestAddRemove_RoundTrip(t *testing.T) {
	withTempRoot(t)

	if err := Add("projectA", "gcc"); err != nil {
		t.Fatal(err)
	}
	// Idempotent: adding again must not error or duplicate.
	if err := Add("projectA", "gcc"); err != nil {
		t.Fatal(err)
	}

	containing, err := ContainingProfiles("gcc")
	if err != nil {
		t.Fatal(err)
	}
	if len(containing) != 1 || containing[0] != "projectA" {
		t.Fatalf("ContainingProfiles(gcc) = %v, want [projectA]", containing)
	}

	if err := Remove("projectA", "gcc"); err != nil {
		t.Fatal(err)
	}
	// Idempotent: removing again (already absent) must not error.
	if err := Remove("projectA", "gcc"); err != nil {
		t.Fatal(err)
	}

	containing, err = ContainingProfiles("gcc")
	if err != nil {
		t.Fatal(err)
	}
	if len(containing) != 0 {
		t.Fatalf("ContainingProfiles(gcc) after remove = %v, want empty", containing)
	}
}

func TestContainingProfiles_MultipleProfiles(t *testing.T) {
	withTempRoot(t)

	// Two named profiles genuinely sharing a package: both must report
	// it, because that is what makes the uninstall safety net mean
	// something.
	if err := Add("projectA", "jq"); err != nil {
		t.Fatal(err)
	}
	if err := Add("projectB", "jq"); err != nil {
		t.Fatal(err)
	}
	if err := Add("projectB", "ripgrep"); err != nil {
		t.Fatal(err)
	}

	containing, err := ContainingProfiles("jq")
	if err != nil {
		t.Fatal(err)
	}
	if len(containing) != 2 {
		t.Fatalf("ContainingProfiles(jq) = %v, want [projectA projectB]", containing)
	}
}

// `default` is where a package lands when nobody has said otherwise.
// Once someone does say otherwise, it has no claim left -- otherwise
// `goop why` reports two owners for a package with one, and the app stays
// in `default` after being deliberately filed elsewhere.
func TestAdd_NamedProfileTakesOverFromDefault(t *testing.T) {
	withTempRoot(t)

	if err := Add(Default, "jq"); err != nil {
		t.Fatal(err)
	}
	if got, _ := ContainingProfiles("jq"); len(got) != 1 || got[0] != Default {
		t.Fatalf("before: %v, want [default]", got)
	}

	if err := Add("chipA", "jq"); err != nil {
		t.Fatal(err)
	}
	got, err := ContainingProfiles("jq")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "chipA" {
		t.Errorf("after: %v, want [chipA] -- default must have let go", got)
	}

	// Adding to default itself still works and does not remove anything.
	if err := Add(Default, "ripgrep"); err != nil {
		t.Fatal(err)
	}
	if got, _ := ContainingProfiles("ripgrep"); len(got) != 1 || got[0] != Default {
		t.Errorf("ripgrep = %v, want [default]", got)
	}
}

func TestList_IncludesDefaultEvenWhenEmpty(t *testing.T) {
	withTempRoot(t)

	names, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != Default {
		t.Fatalf("List() on a fresh root = %v, want [%s]", names, Default)
	}

	if err := Add("projectA", "gcc"); err != nil {
		t.Fatal(err)
	}
	names, err = List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("List() after adding projectA = %v, want 2 entries", names)
	}
}

// Deleting a profile is a grouping operation, not an uninstall -- and a
// member left in no profile at all would be invisible to the uninstall
// safety net, so it falls back to Default.
func TestDelete(t *testing.T) {
	withTempRoot(t)

	for _, a := range []string{"gcc", "cmake"} {
		if err := Add("chipA", a); err != nil {
			t.Fatal(err)
		}
	}
	// cmake is claimed by a second profile, so it must stay there rather
	// than fall back.
	if err := Add("chipB", "cmake"); err != nil {
		t.Fatal(err)
	}

	if err := Delete("chipA"); err != nil {
		t.Fatal(err)
	}
	names, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(names, "chipA") {
		t.Errorf("chipA still listed: %v", names)
	}
	if got, _ := ContainingProfiles("gcc"); len(got) != 1 || got[0] != Default {
		t.Errorf("gcc = %v, want [default] -- an orphan must fall back", got)
	}
	if got, _ := ContainingProfiles("cmake"); len(got) != 1 || got[0] != "chipB" {
		t.Errorf("cmake = %v, want [chipB] -- it still has a claimant", got)
	}
}

// Default is the fallback, so deleting it would leave nowhere to fall
// back to. And a name that does not exist is an error, not a silent no-op.
func TestDelete_RefusesDefaultAndUnknown(t *testing.T) {
	withTempRoot(t)

	if err := Delete(Default); err == nil {
		t.Error("deleting the default profile must fail")
	}
	if err := Delete(""); err == nil {
		t.Error("deleting an empty name must fail")
	}
	if err := Delete("never-existed"); err == nil {
		t.Error("deleting an unknown profile must fail")
	}
}

func TestReset_MergesIntoDefault(t *testing.T) {
	withTempRoot(t)

	if err := Add("projectA", "gcc"); err != nil {
		t.Fatal(err)
	}
	if err := Add("projectB", "ripgrep"); err != nil {
		t.Fatal(err)
	}
	if err := Add("projectB", "jq"); err != nil {
		t.Fatal(err)
	}
	if err := Reset(); err != nil {
		t.Fatal(err)
	}

	// Only default profile should remain.
	names, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != Default {
		t.Fatalf("List() after Reset() = %v, want [%s]", names, Default)
	}

	// Default should contain all members.
	containing, err := ContainingProfiles("gcc")
	if err != nil {
		t.Fatal(err)
	}
	if len(containing) != 1 || containing[0] != Default {
		t.Fatalf("ContainingProfiles(gcc) after Reset() = %v, want [%s]", containing, Default)
	}

	containing, err = ContainingProfiles("ripgrep")
	if err != nil {
		t.Fatal(err)
	}
	if len(containing) != 1 || containing[0] != Default {
		t.Fatalf("ContainingProfiles(ripgrep) after Reset() = %v, want [%s]", containing, Default)
	}

	containing, err = ContainingProfiles("jq")
	if err != nil {
		t.Fatal(err)
	}
	if len(containing) != 1 || containing[0] != Default {
		t.Fatalf("ContainingProfiles(jq) after Reset() = %v, want [%s]", containing, Default)
	}
}

func TestReset_Idempotent(t *testing.T) {
	withTempRoot(t)

	// Calling reset on an already-reset state must not error.
	if err := Reset(); err != nil {
		t.Fatal(err)
	}
	names, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != Default {
		t.Fatalf("List() after Reset() = %v, want [default]", names)
	}
}

func TestReset_NoDuplicateMembers(t *testing.T) {
	withTempRoot(t)

	if err := Add("projectA", "jq"); err != nil {
		t.Fatal(err)
	}
	if err := Add("default", "jq"); err != nil {
		t.Fatal(err)
	}

	if err := Reset(); err != nil {
		t.Fatal(err)
	}

	// jq should appear only once in the default profile.
	d, err := Load(Default)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, a := range d.Apps {
		if a == "jq" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("default profile has %d entries for jq, want 1", count)
	}
}

// Profiles used to be stored as lockfiles. Upgrading goop must not lose
// anyone's membership, so the old shape is still read -- and converted
// only when something writes the profile back.
func TestLoad_ReadsLegacyLockfileShape(t *testing.T) {
	withTempRoot(t)

	legacy := `{"entries":[{"name":"gcc","version":"13.2","hashes":["abc"]},{"name":"cmake","version":"3.29"}]}`
	if err := os.MkdirAll(profilesDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path("projectA"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := Load("projectA")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Apps) != 2 || d.Apps[0] != "gcc" || d.Apps[1] != "cmake" {
		t.Fatalf("got %v, want [gcc cmake]", d.Apps)
	}
}

// The default profile used to be the root lockfile. Its membership has to
// survive the separation, without the lockfile itself being touched --
// it is still a valid lockfile.
func TestLoad_RecoversDefaultFromRootLockfile(t *testing.T) {
	withTempRoot(t)

	rootLock := filepath.Join(paths.Root(), "goop.lock.json")
	if err := os.MkdirAll(filepath.Dir(rootLock), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"entries":[{"name":"jq","version":"1.8.2"}]}`
	if err := os.WriteFile(rootLock, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := Load(Default)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Apps) != 1 || d.Apps[0] != "jq" {
		t.Fatalf("got %v, want [jq]", d.Apps)
	}

	// And the lockfile must be exactly as it was.
	after, err := os.ReadFile(rootLock)
	if err != nil || string(after) != body {
		t.Error("reading the default profile modified the root lockfile")
	}
}
