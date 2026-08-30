package installer

import (
	"testing"

	"github.com/TanguyBaudoin/goop/internal/setup"
)

func auditReasons(t *testing.T, f setup.File) map[string]string {
	t.Helper()
	devs, err := AuditSetup(f)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range devs {
		out[d.Package] = d.Reason
	}
	return out
}

func TestAuditSetup_Matching(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "jq", Version: "1.8.2", State: "ready", ManifestDigest: "sha256:aa"})

	f := setup.File{Apps: []setup.App{{Name: "jq", Version: "1.8.2", ManifestDigest: "sha256:aa"}}}
	if got := auditReasons(t, f); len(got) != 0 {
		t.Errorf("a machine matching its capture has no deviations, got %v", got)
	}
}

// The audit plane asks "is this machine the one that was captured", so
// unlike a profile check it reports extras as well as gaps. Getting this
// direction wrong is what would let a capture silently drift.
func TestAuditSetup_IsBidirectional(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "jq", Version: "1.8.2", State: "ready"})
	fakeInstall(t, Record{Name: "extra", Version: "1.0", State: "ready"})

	f := setup.File{Apps: []setup.App{
		{Name: "jq", Version: "1.8.2"},
		{Name: "gone", Version: "2.0"},
	}}
	got := auditReasons(t, f)
	if got["gone"] != "not installed" {
		t.Errorf("captured but absent here: %q", got["gone"])
	}
	if got["extra"] != "not in the capture" {
		t.Errorf("here but not captured: %q", got["extra"])
	}
	if _, ok := got["jq"]; ok {
		t.Errorf("jq matches and should not be reported: %q", got["jq"])
	}
}

func TestAuditSetup_Reasons(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "old", Version: "1.0", State: "ready"})
	fakeInstall(t, Record{Name: "half", Version: "1.0", State: "pending"})
	fakeInstall(t, Record{Name: "moved", Version: "1.0", State: "ready", ManifestDigest: "sha256:bb"})
	// A capture taken before digests existed must not turn every package
	// into a deviation on a machine that now records them.
	fakeInstall(t, Record{Name: "legacy", Version: "1.0", State: "ready", ManifestDigest: "sha256:cc"})

	f := setup.File{Apps: []setup.App{
		{Name: "old", Version: "2.0"},
		{Name: "half", Version: "1.0"},
		{Name: "moved", Version: "1.0", ManifestDigest: "sha256:aa"},
		{Name: "legacy", Version: "1.0"},
	}}
	got := auditReasons(t, f)
	want := map[string]string{
		"old":   "wrong version",
		"half":  "install did not finish",
		"moved": "manifest differs from the capture",
	}
	for pkg, reason := range want {
		if got[pkg] != reason {
			t.Errorf("%s: reason = %q, want %q", pkg, got[pkg], reason)
		}
	}
	if r, ok := got["legacy"]; ok {
		t.Errorf("a capture with no digest cannot contradict one: %q", r)
	}
}

func TestExportSetup_CapturesBucketsAndApps(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "jq", Version: "1.8.2", Bucket: "main", State: "ready", ManifestDigest: "sha256:aa"})

	f, err := ExportSetup()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Apps) != 1 {
		t.Fatalf("apps = %+v", f.Apps)
	}
	got := f.Apps[0]
	if got.Name != "jq" || got.Version != "1.8.2" || got.Bucket != "main" || got.ManifestDigest != "sha256:aa" {
		t.Errorf("captured app = %+v", got)
	}

	// A capture must audit clean against the machine it came from,
	// otherwise nothing downstream of it can be trusted.
	if devs := auditReasons(t, f); len(devs) != 0 {
		t.Errorf("a capture must match its own machine, got %v", devs)
	}
}
