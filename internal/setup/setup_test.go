package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine.json")
	want := File{
		Buckets: []Bucket{{Name: "main", URL: "https://github.com/ScoopInstaller/Main", Kind: "git"}},
		Apps:    []App{{Name: "jq", Version: "1.8.2", Bucket: "main", ManifestDigest: "sha256:aa"}},
	}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Buckets) != 1 || got.Buckets[0] != want.Buckets[0] {
		t.Errorf("buckets = %+v", got.Buckets)
	}
	if len(got.Apps) != 1 || got.Apps[0] != want.Apps[0] {
		t.Errorf("apps = %+v", got.Apps)
	}
}

// Two captures of the same machine must produce the same bytes, or every
// diff of a committed capture is noise. Map iteration and receipt order
// are both unordered upstream, so the sort has to happen here.
func TestSave_IsDeterministic(t *testing.T) {
	dir := t.TempDir()
	a := File{
		Buckets: []Bucket{{Name: "extras"}, {Name: "main"}},
		Apps:    []App{{Name: "gsudo"}, {Name: "jq"}, {Name: "ripgrep"}},
	}
	b := File{
		Buckets: []Bucket{{Name: "main"}, {Name: "extras"}},
		Apps:    []App{{Name: "ripgrep"}, {Name: "gsudo"}, {Name: "jq"}},
	}
	pathA, pathB := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	if err := Save(pathA, a); err != nil {
		t.Fatal(err)
	}
	if err := Save(pathB, b); err != nil {
		t.Fatal(err)
	}
	da, _ := os.ReadFile(pathA)
	db, _ := os.ReadFile(pathB)
	if string(da) != string(db) {
		t.Errorf("same machine, different bytes:\n%s\n---\n%s", da, db)
	}
}

func TestLoad_ReportsBadInput(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "absent.json")); err == nil {
		t.Error("a missing file should report an error")
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"apps": oops}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil {
		t.Error("malformed JSON should report an error")
	}
}

// A profile file is valid JSON with no `apps`, so it used to decode into
// an empty capture -- and `audit` then reported every installed package
// as "not in the capture". A confident wrong answer is worse than an
// error, and `check` already refuses a capture for the mirror reason.
func TestLoad_RefusesAProfileFile(t *testing.T) {
	dir := t.TempDir()
	profiles := filepath.Join(dir, "goop.profiles.json")
	body := `{"profiles":{"chipA":{"packages":{"jq":"1.8.2"}}}}`
	if err := os.WriteFile(profiles, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(profiles)
	if err == nil {
		t.Fatal("a profile file must not load as a machine capture")
	}
	if !strings.Contains(err.Error(), "profile file") {
		t.Errorf("the error should say which file this is: %v", err)
	}

	// Anything else without `apps` is refused too, but without guessing.
	other := filepath.Join(dir, "other.json")
	os.WriteFile(other, []byte(`{"something":1}`), 0o644)
	if _, err := Load(other); err == nil {
		t.Error("a file with no apps key is not a capture")
	}

	// A genuine capture of an empty machine still loads: `apps` is
	// present and null, which is a real answer.
	empty := filepath.Join(dir, "empty.json")
	os.WriteFile(empty, []byte(`{"buckets":null,"apps":null}`), 0o644)
	if _, err := Load(empty); err != nil {
		t.Errorf("an empty but genuine capture must load: %v", err)
	}
}
