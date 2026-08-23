package envvars

import (
	"os"
	"testing"
)

// These tests write to the real HKCU\Environment (there's no sandboxed
// registry to test against) using clearly-namespaced, collision-unlikely
// names, and clean up after themselves either way. Still real registry
// mutation, so they're opt-in rather than part of the default `go test
// ./...` run -- set GOOP_TEST_ENVVARS=1 to run them.
func skipUnlessOptedIn(t *testing.T) {
	t.Helper()
	if os.Getenv("GOOP_TEST_ENVVARS") != "1" {
		t.Skip("set GOOP_TEST_ENVVARS=1 to run tests that touch the real HKCU\\Environment")
	}
}

func TestSetUnset(t *testing.T) {
	skipUnlessOptedIn(t)
	const name = "GOOP_ENVVARS_TEST_VAR_7f3a9c"
	t.Cleanup(func() { Unset(name) })

	if err := Set(name, "hello"); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(name); got != "" {
		// Setting HKCU\Environment doesn't retroactively update this
		// already-running process's own environment block -- expected,
		// not a bug (same limitation real Scoop has).
		t.Logf("os.Getenv sees %q (expected to be unset in this process)", got)
	}

	if err := Unset(name); err != nil {
		t.Fatal(err)
	}
}

func TestAddRemovePath(t *testing.T) {
	skipUnlessOptedIn(t)
	const dir = `X:\goop-envvars-test-sentinel-7f3a9c`
	t.Cleanup(func() { RemoveFromPath(dir) })

	if err := AddToPath(dir); err != nil {
		t.Fatal(err)
	}
	if err := AddToPath(dir); err != nil { // idempotent: adding twice must not duplicate
		t.Fatal(err)
	}

	k, err := openWritable()
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	parts, err := currentPath(k)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, p := range parts {
		if samePathEntry(p, dir) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("dir appears %d times in Path, want 1", count)
	}

	if err := RemoveFromPath(dir); err != nil {
		t.Fatal(err)
	}
	parts, err = currentPath(k)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		if samePathEntry(p, dir) {
			t.Fatalf("dir still present in Path after RemoveFromPath")
		}
	}
}
