package installer

import (
	"testing"

	"github.com/TanguyBaudoin/goop/internal/profile"
	"github.com/TanguyBaudoin/goop/internal/profileset"
)

// A sync files packages under the profile the file names, and does it
// concurrently. profile.Add is a read-modify-write of a JSON file, so
// several goroutines filing at once is exactly where a lost update would
// hide -- run this with -race, and check every package actually landed.
//
// The filing path is the one that needs no network: a package already
// installed at the right version is only re-filed, never reinstalled.
func TestSyncProfiles_ConcurrentFilingLosesNothing(t *testing.T) {
	isolateRoot(t)

	const n = 24
	pkgs := map[string]profileset.Pin{}
	for i := 0; i < n; i++ {
		name := "pkg" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		fakeInstall(t, Record{Name: name, Version: "1.0", State: "ready"})
		// Installed, right version, but filed under `default` -- so the
		// only deviation is where it sits.
		if err := profile.Add(profile.Default, name); err != nil {
			t.Fatal(err)
		}
		pkgs[name] = profileset.Pin{Version: "1.0"}
	}

	f := profileset.File{Profiles: map[string]profileset.Profile{
		"chipA": {Packages: pkgs},
	}}

	before, err := Check(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != n {
		t.Fatalf("expected %d deviations to fix, got %d", n, len(before))
	}

	fixed, errs, err := SyncProfiles(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(fixed) != n {
		t.Errorf("fixed %d, want %d", len(fixed), n)
	}

	// The real assertion: every one of them is filed, none lost to a
	// concurrent write.
	after, err := Check(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Errorf("%d package(s) still deviating after sync: %+v", len(after), after)
	}
	d, err := profile.Load("chipA")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Apps) != n {
		t.Errorf("chipA has %d members, want %d -- a concurrent write was lost", len(d.Apps), n)
	}
}

// Concurrency decides what finishes first, so the report must not.
func TestSyncProfiles_ReportIsOrdered(t *testing.T) {
	isolateRoot(t)
	pkgs := map[string]profileset.Pin{}
	for _, n := range []string{"zeta", "alpha", "mike", "bravo"} {
		fakeInstall(t, Record{Name: n, Version: "1.0", State: "ready"})
		if err := profile.Add(profile.Default, n); err != nil {
			t.Fatal(err)
		}
		pkgs[n] = profileset.Pin{Version: "1.0"}
	}
	f := profileset.File{Profiles: map[string]profileset.Profile{"chipA": {Packages: pkgs}}}

	fixed, _, err := SyncProfiles(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "bravo", "mike", "zeta"}
	if len(fixed) != len(want) {
		t.Fatalf("fixed %d, want %d", len(fixed), len(want))
	}
	for i, w := range want {
		if fixed[i].Package != w {
			t.Errorf("fixed[%d] = %s, want %s (report must not shuffle between runs)", i, fixed[i].Package, w)
		}
	}
}
