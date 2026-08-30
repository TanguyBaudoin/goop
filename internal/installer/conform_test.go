package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/paths"
	"github.com/TanguyBaudoin/goop/internal/profileset"
)

// isolateRoot points goop at an empty tree. GOOP_HOME alone does not
// isolate an install -- shortcuts follow APPDATA -- so all three move
// even though these tests only write receipts.
func isolateRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("GOOP_HOME", root)
	t.Setenv("APPDATA", filepath.Join(root, "appdata"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "localappdata"))
	return root
}

// fakeInstall writes the receipt an install would leave, without
// installing: Check reads receipts and nothing else, which is what makes
// it instant and offline, and is exactly what these tests exercise.
func fakeInstall(t *testing.T, rec Record) {
	t.Helper()
	dir := paths.AppCurrent(rec.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, recordFileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeShim creates the sidecar a working shim has, pointing at target.
func writeShim(t *testing.T, name, target string) {
	t.Helper()
	if err := os.MkdirAll(paths.Shims(), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "path = \"" + target + "\"\n"
	if err := os.WriteFile(filepath.Join(paths.Shims(), name+".shim"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func oneProfile(pkg string, pin profileset.Pin) profileset.File {
	return profileset.File{Profiles: map[string]profileset.Profile{
		"chipa": {Packages: map[string]profileset.Pin{pkg: pin}},
	}}
}

func checkReason(t *testing.T, f profileset.File) string {
	t.Helper()
	devs, err := Check(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) == 0 {
		return ""
	}
	if len(devs) > 1 {
		t.Fatalf("expected at most one deviation, got %+v", devs)
	}
	return devs[0].Reason
}

func TestCheck_Conformant(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "jq", Version: "1.8.2", State: "ready", ManifestDigest: "sha256:aa"})
	if got := checkReason(t, oneProfile("jq", profileset.Pin{Version: "1.8.2", Hash: "sha256:aa"})); got != "" {
		t.Errorf("expected conformance, got %q", got)
	}
}

func TestCheck_Reasons(t *testing.T) {
	cases := []struct {
		name string
		rec  *Record // nil = never installed
		pin  profileset.Pin
		want string
	}{
		{
			name: "absent",
			pin:  profileset.Pin{Version: "1.8.2"},
			want: "not installed",
		},
		{
			// The failure this whole design exists to prevent: a receipt
			// committed by the rename, then an install that died before
			// the shims existed.
			name: "committed but unfinished",
			rec:  &Record{Name: "jq", Version: "1.8.2", State: "pending"},
			pin:  profileset.Pin{Version: "1.8.2"},
			want: "install did not finish",
		},
		{
			name: "wrong version",
			rec:  &Record{Name: "jq", Version: "1.7", State: "ready"},
			pin:  profileset.Pin{Version: "1.8.2"},
			want: "wrong version",
		},
		{
			// Same version number, different instructions -- an edited
			// post_install, a moved URL. Only the digest sees it.
			name: "manifest republished",
			rec:  &Record{Name: "jq", Version: "1.8.2", State: "ready", ManifestDigest: "sha256:bb"},
			pin:  profileset.Pin{Version: "1.8.2", Hash: "sha256:aa"},
			want: "manifest changed since it was installed",
		},
		{
			name: "installed by an older goop",
			rec:  &Record{Name: "jq", Version: "1.8.2", State: "ready"},
			pin:  profileset.Pin{Version: "1.8.2", Hash: "sha256:aa"},
			want: "installed before digests were recorded",
		},
		{
			// An unpinned member is a membership statement, not a
			// version statement: any installed version conforms.
			name: "no version pinned",
			rec:  &Record{Name: "jq", Version: "1.7", State: "ready"},
			pin:  profileset.Pin{},
			want: "",
		},
		{
			// A receipt written before goop recorded state at all.
			name: "legacy receipt with no state",
			rec:  &Record{Name: "jq", Version: "1.8.2"},
			pin:  profileset.Pin{Version: "1.8.2"},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateRoot(t)
			if tc.rec != nil {
				fakeInstall(t, *tc.rec)
			}
			if got := checkReason(t, oneProfile("jq", tc.pin)); got != tc.want {
				t.Errorf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

// A version can be right while the command does not work.
func TestCheck_BrokenShim(t *testing.T) {
	root := isolateRoot(t)
	fakeInstall(t, Record{
		Name: "jq", Version: "1.8.2", State: "ready",
		Bin: []manifest.BinEntry{{Name: "jq"}},
	})

	writeShim(t, "jq", filepath.Join(root, "nowhere", "jq.exe"))
	if got := checkReason(t, oneProfile("jq", profileset.Pin{Version: "1.8.2"})); got != "broken shim: jq" {
		t.Errorf("a shim pointing at nothing must be a deviation, got %q", got)
	}

	real := filepath.Join(root, "jq.exe")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeShim(t, "jq", real)
	if got := checkReason(t, oneProfile("jq", profileset.Pin{Version: "1.8.2"})); got != "" {
		t.Errorf("a shim pointing at a real file conforms, got %q", got)
	}
}

// The defining semantic of the profile plane: the question is "does this
// machine have what the repository needs", not "is this machine clean".
func TestCheck_IgnoresPackagesOutsideTheProfile(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "jq", Version: "1.8.2", State: "ready"})
	fakeInstall(t, Record{Name: "unrelated", Version: "9.9", State: "ready"})
	fakeInstall(t, Record{Name: "also-broken", Version: "1.0", State: "pending"})

	devs, err := Check(oneProfile("jq", profileset.Pin{Version: "1.8.2"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 0 {
		t.Errorf("packages outside the profile must never be deviations, got %+v", devs)
	}
}

// Checking one profile must not report another's packages -- that is what
// makes syncing one circle safe to run without touching the others.
func TestCheck_SelectionIsPerProfile(t *testing.T) {
	isolateRoot(t)
	f := profileset.File{Profiles: map[string]profileset.Profile{
		"baseline.tool": {Packages: map[string]profileset.Pin{"gsudo": {Version: "2.6.1"}}},
		"chipa":         {Packages: map[string]profileset.Pin{"jq": {Version: "1.8.2"}}},
	}}
	fakeInstall(t, Record{Name: "gsudo", Version: "2.6.1", State: "ready"})

	devs, err := Check(f, []string{"chipa"})
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || devs[0].Package != "jq" || devs[0].Profile != "chipa" {
		t.Fatalf("checking chipa should report jq only, got %+v", devs)
	}

	// No selection means every profile in the file.
	all, err := Check(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Package != "jq" {
		t.Errorf("Check(nil) = %+v", all)
	}
}

// Silence about a profile that does not exist would read as conformance.
func TestCheck_UnknownProfileIsAnError(t *testing.T) {
	isolateRoot(t)
	if _, err := Check(oneProfile("jq", profileset.Pin{}), []string{"chipb"}); err == nil {
		t.Error("checking a profile the file does not have must fail")
	}
}

// ExportProfiles describes this machine, so a member that is not
// installed is refused rather than guessed at.
func TestExportProfiles_RefusesToInventPins(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "jq", Version: "1.8.2", State: "ready", ManifestDigest: "sha256:aa"})
	fakeInstall(t, Record{Name: "half", Version: "1.0", State: "pending"})

	members := func(string) ([]string, error) { return []string{"jq", "half", "absent"}, nil }
	rep, err := ExportProfiles([]string{"chipa"}, members)
	if err != nil {
		t.Fatal(err)
	}
	pin := rep.File.Profiles["chipa"].Packages["jq"]
	if pin.Version != "1.8.2" || pin.Hash != "sha256:aa" {
		t.Errorf("jq pin = %+v", pin)
	}
	if len(rep.File.Profiles["chipa"].Packages) != 1 {
		t.Errorf("only installed members may be pinned, got %+v", rep.File.Profiles["chipa"].Packages)
	}
	want := []string{"chipa/absent", "chipa/half"}
	if len(rep.Missing) != 2 || rep.Missing[0] != want[0] || rep.Missing[1] != want[1] {
		t.Errorf("missing = %v, want %v", rep.Missing, want)
	}
}

// A package installed before goop recorded digests, or adopted from
// Scoop, pins a version and nothing else. Exporting it silently produced
// a file whose maintainer had no way to know it had lost its only defence
// against a manifest republished under the same version number.
func TestExportProfiles_ReportsPinsWithNoDigest(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "jq", Version: "1.8.2", State: "ready", ManifestDigest: "sha256:aa"})
	fakeInstall(t, Record{Name: "gsudo", Version: "2.6.1", State: "ready"}) // installed by an older goop

	members := func(string) ([]string, error) { return []string{"jq", "gsudo"}, nil }
	rep, err := ExportProfiles([]string{"chipa"}, members)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pinned != 2 {
		t.Errorf("Pinned = %d, want 2", rep.Pinned)
	}
	if len(rep.Undigested) != 1 || rep.Undigested[0] != "chipa/gsudo" {
		t.Fatalf("Undigested = %v, want [chipa/gsudo]", rep.Undigested)
	}
	// It is still exported -- a version-only pin is weaker, not useless.
	if got := rep.File.Profiles["chipa"].Packages["gsudo"]; got.Version != "2.6.1" || got.Hash != "" {
		t.Errorf("gsudo pin = %+v", got)
	}
}
