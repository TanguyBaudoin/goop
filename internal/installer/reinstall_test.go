package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/bucket"
	"github.com/TanguyBaudoin/goop/internal/paths"

	"github.com/TanguyBaudoin/goop/internal/profile"
)

// Reinstall needs the manifest for the version that is *installed*, and
// a bucket carries only the current one. Refusing here, by name, beats
// surfacing a version-constraint conflict from deep inside the install
// path -- and beats quietly installing a different version, which is
// what `goop update` is for.
func TestReinstall_RefusesWhenTheBucketHasMovedOn(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "jq", Version: "1.6", State: "ready", Bucket: "main"})
	fakeBucketWith(t, "main", map[string]string{"jq": `{"version":"1.8.2","url":"https://example.invalid/x.zip","bin":"x.exe"}`})

	_, err := Reinstall("jq")
	if err == nil {
		t.Fatal("expected a refusal: 1.6 is not what the bucket offers")
	}
	msg := err.Error()
	for _, want := range []string{"1.6", "1.8.2", "goop update"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal should mention %q: %v", want, err)
		}
	}
}

func TestReinstall_NotInstalled(t *testing.T) {
	isolateRoot(t)
	_, err := Reinstall("never-installed")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Errorf("error = %v, want it to say not installed", err)
	}
}

// Maven packages come from a coordinate, not a bucket, so there is no
// manifest to reinstall from. Named rather than failing obscurely.
func TestReinstall_RefusesMaven(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "guava", Version: "33.0", State: "ready", Bucket: "maven"})

	_, err := Reinstall("guava")
	if err == nil {
		t.Fatal("expected a refusal for a Maven install")
	}
	if !strings.Contains(err.Error(), "maven:") {
		t.Errorf("the refusal should point at `goop install maven:...`: %v", err)
	}
}

// The reason this command exists instead of uninstall-then-install:
// uninstall drops a package out of every profile that named it. Reinstall
// must not, and it does not because the install path never touches
// membership -- Install and InstallInto do, and Reinstall calls neither.
//
// Checked on the refusal path, which is the one that needs no network:
// whatever the outcome, membership is still there afterwards.
func TestReinstall_LeavesProfileMembershipAlone(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "jq", Version: "1.6", State: "ready", Bucket: "main"})
	fakeBucketWith(t, "main", map[string]string{"jq": `{"version":"1.8.2","url":"https://example.invalid/x.zip","bin":"x.exe"}`})
	if err := profile.Add("chipA", "jq"); err != nil {
		t.Fatal(err)
	}

	_, _ = Reinstall("jq")

	got, err := profile.ContainingProfiles("jq")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "chipA" {
		t.Errorf("membership = %v, want [chipA] -- a reinstall must never re-file a package", got)
	}
}

// ProfilesOf is what the plan shows before asking, so it has to agree
// with the membership that actually exists.
func TestProfilesOf(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{Name: "jq", Version: "1.8.2", State: "ready"})
	if got := ProfilesOf("jq"); len(got) != 0 {
		t.Errorf("unfiled package: %v, want none", got)
	}
	if err := profile.Add("chipA", "jq"); err != nil {
		t.Fatal(err)
	}
	if err := profile.Add("ide", "jq"); err != nil {
		t.Fatal(err)
	}
	got := ProfilesOf("jq")
	if len(got) != 2 {
		t.Errorf("ProfilesOf = %v, want both profiles", got)
	}
	if len(ProfilesOf("never-installed")) != 0 {
		t.Error("an unknown package has no profiles, and must not error")
	}
}

// fakeBucketWith registers a bucket and lays manifests out the way a
// clone would, without cloning: bucket.Register only writes config, and
// resolution reads from disk.
func fakeBucketWith(t *testing.T, name string, manifests map[string]string) {
	t.Helper()
	if err := bucket.Register(name, "https://example.invalid/"+name, ""); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(paths.Bucket(name), "bucket")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for app, body := range manifests {
		if err := os.WriteFile(filepath.Join(dir, app+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
