package profileset

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A pin is normally an object, but a bare version string stays readable
// for a profile that only cares about versions.
func TestPin_AcceptsBothShapes(t *testing.T) {
	var f File
	body := `{"profiles":{"p":{"packages":{
		"a":{"version":"1.0","hash":"sha256:aa"},
		"b":"2.0"
	}}}}`
	if err := json.Unmarshal([]byte(body), &f); err != nil {
		t.Fatal(err)
	}
	if got := f.Profiles["p"].Packages["a"]; got.Version != "1.0" || got.Hash != "sha256:aa" {
		t.Errorf("object pin = %+v", got)
	}
	if got := f.Profiles["p"].Packages["b"]; got.Version != "2.0" || got.Hash != "" {
		t.Errorf("string pin = %+v", got)
	}
}

// resolved will hold transitive dependencies later. Files written today
// must stay valid when it arrives, so its absence is an empty map and
// never an error.
func TestProfile_ResolvedAbsentIsEmpty(t *testing.T) {
	var f File
	if err := json.Unmarshal([]byte(`{"profiles":{"p":{"packages":{"a":"1.0"}}}}`), &f); err != nil {
		t.Fatal(err)
	}
	p := f.Profiles["p"]
	if p.Resolved != nil {
		t.Errorf("Resolved = %v, want nil", p.Resolved)
	}
	if all := p.All(); len(all) != 1 || all["a"].Version != "1.0" {
		t.Errorf("All() = %v", all)
	}
}

func TestProfile_AllMergesResolvedUnderDeclared(t *testing.T) {
	var f File
	body := `{"profiles":{"p":{
		"packages":{"a":"1.0"},
		"resolved":{"a":"9.9","dep":"3.0"}
	}}}`
	if err := json.Unmarshal([]byte(body), &f); err != nil {
		t.Fatal(err)
	}
	all := f.Profiles["p"].All()
	if all["a"].Version != "1.0" {
		t.Errorf("declared should win over resolved, got %q", all["a"].Version)
	}
	if all["dep"].Version != "3.0" {
		t.Errorf("resolved entries should be included, got %v", all)
	}
}

// Never report conformance because there was nothing to verify.
func TestSelect_UnknownProfileIsAnError(t *testing.T) {
	f := File{Profiles: map[string]Profile{"chipa": {}}}
	if _, err := f.Select([]string{"chipb"}); err == nil {
		t.Error("naming a profile the file does not have must fail")
	}
	if _, err := (File{Profiles: map[string]Profile{}}).Select(nil); err == nil {
		t.Error("a file with no profiles must fail rather than pass vacuously")
	}
	got, err := f.Select(nil)
	if err != nil || len(got) != 1 || got[0] != "chipa" {
		t.Errorf("Select(nil) = %v, %v", got, err)
	}
}

func TestLoadSave_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mainline.json")
	want := File{Profiles: map[string]Profile{
		"chipa": {Packages: map[string]Pin{"cmake": {Version: "3.31.2", Hash: "sha256:aa"}}},
	}}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Profiles["chipa"].Packages["cmake"] != want.Profiles["chipa"].Packages["cmake"] {
		t.Errorf("round trip lost data: %+v", got)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("a missing file should report an error")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte(`{"profiles": oops`), 0o644)
	if _, err := Load(bad); err == nil {
		t.Error("malformed JSON should report an error")
	}
}
