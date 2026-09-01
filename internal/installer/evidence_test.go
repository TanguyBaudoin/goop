package installer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/profile"
	"github.com/TanguyBaudoin/goop/internal/profileset"
)

func writeProfileFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chipA.goop")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The packages that pass are the point. Reporting only failures leaves
// "was this package even looked at?" unanswered, which is the question
// an audit exists to close.
func TestCheckEvidence_RecordsEveryPackage(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "jq", Version: "1.8.2", State: "ready", Bucket: "main", ManifestDigest: "sha256:aa"})
	fakeInstall(t, Record{Name: "gsudo", Version: "2.0", State: "ready", Bucket: "main"})
	// Membership is part of conformance since 0.4.0: a package filed
	// somewhere else does not satisfy the profile that names it.
	for _, n := range []string{"jq", "gsudo"} {
		if err := profile.Add("chipA", n); err != nil {
			t.Fatal(err)
		}
	}

	f := profileset.File{Profiles: map[string]profileset.Profile{"chipA": {Packages: map[string]profileset.Pin{
		"jq":      {Version: "1.8.2", Hash: "sha256:aa"},
		"gsudo":   {Version: "2.6.1"},
		"missing": {Version: "1.0"},
	}}}}
	path := writeProfileFile(t, `{"profiles":{"chipA":{"packages":{"jq":"1.8.2"}}}}`)

	ev, err := CheckEvidence(f, nil, path, "9.9.9", "abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Packages) != 3 {
		t.Fatalf("recorded %d packages, want all 3", len(ev.Packages))
	}
	if ev.Conformant {
		t.Error("two packages deviate; the report must not say conformant")
	}
	if ev.Deviations != 2 {
		t.Errorf("Deviations = %d, want 2", ev.Deviations)
	}

	byName := map[string]PackageEvidence{}
	for _, p := range ev.Packages {
		byName[p.Package] = p
	}
	if p := byName["jq"]; !p.Conformant || p.Verdict != "matches" {
		t.Errorf("jq = %+v, want a passing verdict", p)
	}
	// A passing package must still carry what it was found to be --
	// otherwise the record proves nothing about it.
	if p := byName["jq"]; p.Installed != "1.8.2" || p.InstalledDigest != "sha256:aa" || p.Bucket != "main" {
		t.Errorf("jq evidence is missing its findings: %+v", p)
	}
	if p := byName["gsudo"]; p.Conformant || p.Installed != "2.0" || p.Required != "2.6.1" {
		t.Errorf("gsudo = %+v, want a wrong-version verdict with both versions recorded", p)
	}
	// Never installed: no findings to record, but it is still listed.
	if p := byName["missing"]; p.Conformant || p.Installed != "" || p.Verdict != "not installed" {
		t.Errorf("missing = %+v", p)
	}
}

// A path proves nothing when the file can be edited between the check
// and the review, so the record hashes what it actually read.
func TestCheckEvidence_IdentifiesTheFileByContent(t *testing.T) {
	isolateRoot(t)
	f := profileset.File{Profiles: map[string]profileset.Profile{
		"chipA": {Packages: map[string]profileset.Pin{"jq": {Version: "1.8.2"}}},
	}}

	path := writeProfileFile(t, `{"profiles":{"chipA":{"packages":{"jq":"1.8.2"}}}}`)
	first, err := CheckEvidence(f, nil, path, "1.0", "")
	if err != nil {
		t.Fatal(err)
	}
	if first.FileSHA256 == "" {
		t.Fatal("the file hash is the point; it must not be empty")
	}

	if err := os.WriteFile(path, []byte(`{"profiles":{"chipA":{"packages":{"jq":"9.9"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := CheckEvidence(f, nil, path, "1.0", "")
	if err != nil {
		t.Fatal(err)
	}
	if second.FileSHA256 == first.FileSHA256 {
		t.Error("editing the file must change the recorded hash")
	}
}

// A record that cannot say which build produced it is not reproducible.
func TestCheckEvidence_NamesTheBuild(t *testing.T) {
	isolateRoot(t)
	f := profileset.File{Profiles: map[string]profileset.Profile{"chipA": {}}}
	path := writeProfileFile(t, `{"profiles":{"chipA":{}}}`)

	ev, err := CheckEvidence(f, nil, path, "0.7.0", "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if ev.Tool != "goop" || ev.Version != "0.7.0" || ev.Commit != "deadbeef" {
		t.Errorf("build identity = %q %q %q", ev.Tool, ev.Version, ev.Commit)
	}
	if ev.CheckedAt.IsZero() {
		t.Error("a record with no timestamp cannot be placed in time")
	}
	if ev.CheckedAt.Location().String() != "UTC" {
		t.Errorf("timestamp is %s; UTC keeps records from two machines comparable", ev.CheckedAt.Location())
	}
	// An empty profile is conformant: it required nothing and nothing was
	// missing. Saying otherwise would be a false negative.
	if !ev.Conformant {
		t.Error("a profile with no packages has nothing to deviate from")
	}
}

// Naming a profile the file does not declare is an error, not an empty
// clean report -- the same rule Check follows, and for the same reason.
func TestCheckEvidence_UnknownProfileIsAnError(t *testing.T) {
	isolateRoot(t)
	f := profileset.File{Profiles: map[string]profileset.Profile{"chipA": {}}}
	path := writeProfileFile(t, `{"profiles":{"chipA":{}}}`)

	if _, err := CheckEvidence(f, []string{"ghost"}, path, "1.0", ""); err == nil {
		t.Error("an unknown profile must fail rather than produce a clean record")
	}
}
