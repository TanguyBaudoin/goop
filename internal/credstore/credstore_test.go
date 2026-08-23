package credstore

import (
	"os"
	"testing"
)

// These tests write to the real Windows Credential Manager (there's no
// sandboxed store to test against) using a clearly-namespaced,
// collision-unlikely fake host, and clean up after themselves either
// way. Opt-in: set GOOP_TEST_CREDSTORE=1 to run them.
func skipUnlessOptedIn(t *testing.T) {
	t.Helper()
	if os.Getenv("GOOP_TEST_CREDSTORE") != "1" {
		t.Skip("set GOOP_TEST_CREDSTORE=1 to run tests that touch the real Windows Credential Manager")
	}
}

func TestSetGetDelete_Bearer(t *testing.T) {
	skipUnlessOptedIn(t)
	const host = "goop-test-host-7f3a9c.example"
	t.Cleanup(func() { Delete(host) })

	if err := Set(host, "bearer", "", "test-token-abc123"); err != nil {
		t.Fatal(err)
	}
	authType, username, secret, ok, err := Get(host)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected credential to be found")
	}
	if authType != "bearer" || username != "" || secret != "test-token-abc123" {
		t.Fatalf("got (%q, %q, %q), want (bearer, \"\", test-token-abc123)", authType, username, secret)
	}

	if err := Delete(host); err != nil {
		t.Fatal(err)
	}
	_, _, _, ok, err = Get(host)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("credential should be gone after Delete")
	}
}

func TestSetGetDelete_Basic(t *testing.T) {
	skipUnlessOptedIn(t)
	const host = "goop-test-host-basic-7f3a9c.example"
	t.Cleanup(func() { Delete(host) })

	if err := Set(host, "basic", "alice", "hunter2"); err != nil {
		t.Fatal(err)
	}
	authType, username, secret, ok, err := Get(host)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || authType != "basic" || username != "alice" || secret != "hunter2" {
		t.Fatalf("got (%q, %q, %q, %v), want (basic, alice, hunter2, true)", authType, username, secret, ok)
	}
}

func TestGet_NotFound(t *testing.T) {
	skipUnlessOptedIn(t)
	_, _, _, ok, err := Get("goop-test-host-definitely-not-set-7f3a9c.example")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no credential to be found")
	}
}

func TestDelete_NonexistentIsNotAnError(t *testing.T) {
	skipUnlessOptedIn(t)
	if err := Delete("goop-test-host-never-existed-7f3a9c.example"); err != nil {
		t.Fatal(err)
	}
}

func TestList_IncludesSetEntriesAndNoSecrets(t *testing.T) {
	skipUnlessOptedIn(t)
	const host = "goop-test-host-list-7f3a9c.example"
	t.Cleanup(func() { Delete(host) })

	if err := Set(host, "bearer", "", "should-never-appear-in-list"); err != nil {
		t.Fatal(err)
	}
	entries, err := List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Host == host {
			found = true
			if e.Type != "bearer" {
				t.Errorf("Type = %q, want bearer", e.Type)
			}
		}
	}
	if !found {
		t.Fatalf("List() did not include %s: %+v", host, entries)
	}
	// Entry has no field capable of holding a secret at all -- this is
	// really just documenting that guarantee, not testing new behavior.
}

func TestSet_RejectsEmptySecret(t *testing.T) {
	skipUnlessOptedIn(t)
	if err := Set("goop-test-host-empty-7f3a9c.example", "bearer", "", ""); err == nil {
		t.Fatal("expected error for empty secret")
	}
}

func TestSet_RejectsUnknownType(t *testing.T) {
	skipUnlessOptedIn(t)
	if err := Set("goop-test-host-badtype-7f3a9c.example", "digest", "", "x"); err == nil {
		t.Fatal("expected error for unknown auth type")
	}
}
