package installer

import (
	"testing"

	"github.com/TanguyBaudoin/goop/internal/profile"
	"github.com/TanguyBaudoin/goop/internal/profileset"
)

// A batch installs in the order given, not in whatever order goroutines
// finish. An install hook can call any goop-installed binary -- the shims
// directory is on its PATH -- so when one package's post_install needs a
// binary another package in the same batch provides, and the manifest
// does not declare it (Scoop manifests routinely do not declare runtime
// tools), concurrent installs made that a coin flip. Reproduced at 3
// successes in 6 against a local bucket before this changed.
//
// Nothing here downloads: every package is already installed at the right
// version, so each deviation is a re-filing. What is being pinned down is
// the order the batch works through them.
func TestSyncProfiles_AppliesInDeviationOrder(t *testing.T) {
	isolateRoot(t)

	names := []string{"zeta", "alpha", "mike", "bravo"}
	pkgs := map[string]profileset.Pin{}
	for _, n := range names {
		fakeInstall(t, Record{Name: n, Version: "1.0", State: "ready"})
		if err := profile.Add(profile.Default, n); err != nil {
			t.Fatal(err)
		}
		pkgs[n] = profileset.Pin{Version: "1.0"}
	}
	f := profileset.File{Profiles: map[string]profileset.Profile{"chipA": {Packages: pkgs}}}

	// Check returns deviations sorted, and the sync must follow that
	// order rather than reordering or shuffling it.
	want, err := Check(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	fixed, errs, err := SyncProfiles(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(fixed) != len(want) {
		t.Fatalf("fixed %d, want %d", len(fixed), len(want))
	}
	for i := range want {
		if fixed[i].Package != want[i].Package {
			t.Errorf("fixed[%d] = %s, want %s -- the batch must follow the reported order",
				i, fixed[i].Package, want[i].Package)
		}
	}
}

// Prefetch is best effort by design: a package it cannot fetch must fail
// in the install, where the error is reported against that package with
// everything else known about it. Failing here would report the same
// problem twice, or differently.
func TestPrefetch_SurvivesUnresolvableNames(t *testing.T) {
	isolateRoot(t)
	// No buckets configured at all, so every resolution fails.
	Prefetch([]string{"nope", "also-nope"})
	// Reaching here without a panic or a hang is the assertion.
}

func TestPrefetch_EmptyIsANoOp(t *testing.T) {
	isolateRoot(t)
	Prefetch(nil)
	Prefetch([]string{})
}
