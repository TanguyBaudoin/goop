package installer

import (
	"strings"
	"testing"
)

// Removing one package can remove three, because uninstall cascades to
// whatever declares it. That is the part worth showing before anything
// is deleted, so the plan has to find it.
func TestPlanUninstall_FindsTheCascade(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "openssl", Version: "3", State: "ready"})
	fakeInstall(t, Record{Name: "curl", Version: "8", State: "ready", Depends: []string{"openssl"}})
	fakeInstall(t, Record{Name: "myapp", Version: "1", State: "ready", Depends: []string{"curl"}})
	fakeInstall(t, Record{Name: "unrelated", Version: "1", State: "ready"})

	p, err := PlanUninstall([]string{"openssl"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Requested) != 1 || p.Requested[0] != "openssl" {
		t.Errorf("Requested = %v", p.Requested)
	}
	// Transitive: myapp depends on curl, which depends on openssl.
	if len(p.Cascaded) != 2 || p.Cascaded[0] != "curl" || p.Cascaded[1] != "myapp" {
		t.Errorf("Cascaded = %v, want [curl myapp]", p.Cascaded)
	}
	if p.Total() != 3 {
		t.Errorf("Total() = %d, want 3", p.Total())
	}
	for _, n := range p.Cascaded {
		if n == "unrelated" {
			t.Error("a package that depends on nothing removed must not be swept in")
		}
	}
}

// A package named outright and also reached as a dependent belongs in
// one list, not both -- otherwise the count is wrong and the table shows
// it twice.
func TestPlanUninstall_NoDoubleCounting(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "openssl", Version: "3", State: "ready"})
	fakeInstall(t, Record{Name: "curl", Version: "8", State: "ready", Depends: []string{"openssl"}})

	p, err := PlanUninstall([]string{"openssl", "curl"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Total() != 2 {
		t.Errorf("Total() = %d, want 2 (got Requested=%v Cascaded=%v)", p.Total(), p.Requested, p.Cascaded)
	}
	for _, n := range p.Cascaded {
		if n == "curl" {
			t.Error("curl was asked for, so it is not a cascade")
		}
	}
}

// A name that is not installed is reported rather than silently dropped,
// and does not make the plan look like it has work to do.
func TestPlanUninstall_ReportsMissing(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "jq", Version: "1.8.2", State: "ready"})

	p, err := PlanUninstall([]string{"jq", "never-installed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Missing) != 1 || p.Missing[0] != "never-installed" {
		t.Errorf("Missing = %v", p.Missing)
	}
	if p.Total() != 1 {
		t.Errorf("Total() = %d, want 1", p.Total())
	}

	only, err := PlanUninstall([]string{"never-installed"})
	if err != nil {
		t.Fatal(err)
	}
	if only.Total() != 0 {
		t.Errorf("nothing installed to remove, Total() = %d", only.Total())
	}
}

// The cascade follows declared `depends` only. An extraction helper --
// 7zip, innounp, dark -- is pulled in implicitly and is never recorded as
// a dependency, so removing it must not offer to remove everything ever
// unpacked with it.
func TestPlanUninstall_IgnoresImplicitHelpers(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "7zip", Version: "26", State: "ready"})
	// Extracted with 7zip, but declaring nothing.
	fakeInstall(t, Record{Name: "gcc", Version: "15", State: "ready"})

	p, err := PlanUninstall([]string{"7zip"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Cascaded) != 0 {
		t.Errorf("Cascaded = %v, want none", p.Cascaded)
	}
}

// A dependency declared with a version constraint is the same package.
func TestPlanUninstall_MatchesConstrainedDepends(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "openssl", Version: "3", State: "ready"})
	fakeInstall(t, Record{Name: "curl", Version: "8", State: "ready", Depends: []string{"main/openssl@>=3"}})

	p, err := PlanUninstall([]string{"openssl"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Cascaded) != 1 || !strings.Contains(p.Cascaded[0], "curl") {
		t.Errorf("Cascaded = %v, want [curl]", p.Cascaded)
	}
}
