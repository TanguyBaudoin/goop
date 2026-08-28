package profile

import (
	"testing"

	"github.com/TanguyBaudoin/goop/internal/lockfile"
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

func TestPath_DefaultMapsToLockfilePath(t *testing.T) {
	withTempRoot(t)
	if got, want := Path(Default), Path(""); got != want {
		t.Errorf("Path(Default) = %q, Path(\"\") = %q, want equal", got, want)
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

	if err := Add(Default, "jq"); err != nil {
		t.Fatal(err)
	}
	if err := Add("projectA", "jq"); err != nil {
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
		t.Fatalf("ContainingProfiles(jq) = %v, want 2 entries (default, projectA)", containing)
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

func TestActiveUse(t *testing.T) {
	withTempRoot(t)

	if got := Active(); got != Default {
		t.Fatalf("Active() before any Use() = %q, want %q", got, Default)
	}

	if err := Use("projectA"); err != nil {
		t.Fatal(err)
	}
	if got := Active(); got != "projectA" {
		t.Fatalf("Active() after Use(projectA) = %q, want projectA", got)
	}

	if err := Use(""); err != nil {
		t.Fatal(err)
	}
	if got := Active(); got != Default {
		t.Fatalf("Active() after Use(\"\") = %q, want %q", got, Default)
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
	if err := Use("projectA"); err != nil {
		t.Fatal(err)
	}

	if err := Reset(); err != nil {
		t.Fatal(err)
	}

	// Active should be back to default.
	if got := Active(); got != Default {
		t.Fatalf("Active() after Reset() = %q, want %q", got, Default)
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
	if got, want := Active(), Default; got != want {
		t.Fatalf("Active() after Reset() = %q, want %q", got, want)
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
	f, err := lockfile.Load(Path(Default))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range f.Entries {
		if e.Name == "jq" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("default profile has %d entries for jq, want 1", count)
	}
}
