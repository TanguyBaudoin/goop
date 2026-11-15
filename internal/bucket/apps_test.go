package bucket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/paths"
)

// The cheap path reads directory entries and nothing else: a manifest's
// filename is the package name. That is what lets a 1642-package bucket
// answer in 30ms instead of the 7.4s `goop search .` took to approximate
// the same question.
func TestApps_NamesWithoutDecoding(t *testing.T) {
	isolateRoot(t)
	fakeBucket(t, "main", map[string]string{
		"ripgrep": manifestJSON("15.2.0"),
		"jq":      manifestJSON("1.8.2"),
		// Not a manifest, and must not be offered as a package.
		"README": "",
	})
	if err := os.Remove(filepath.Join(paths.Bucket("main"), "bucket", "README.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Bucket("main"), "bucket", "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	apps, err := Apps("main", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 {
		t.Fatalf("Apps = %+v, want jq and ripgrep only", apps)
	}
	// Sorted, so a listing is stable between runs and diffable.
	if apps[0].Name != "jq" || apps[1].Name != "ripgrep" {
		t.Errorf("order = %s, %s -- want sorted", apps[0].Name, apps[1].Name)
	}
	// Nothing decoded on this path.
	for _, a := range apps {
		if a.Version != "" {
			t.Errorf("%s: version %q filled without --versions; the cheap path must not decode", a.Name, a.Version)
		}
		if a.Bucket != "main" {
			t.Errorf("%s: bucket = %q", a.Name, a.Bucket)
		}
	}
}

func TestApps_WithVersionsDecodes(t *testing.T) {
	isolateRoot(t)
	fakeBucket(t, "main", map[string]string{"jq": manifestJSON("1.8.2")})

	apps, err := Apps("main", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Version != "1.8.2" {
		t.Fatalf("Apps = %+v, want jq at 1.8.2", apps)
	}
	if len(apps[0].Binaries) != 1 || apps[0].Binaries[0] != "x" {
		t.Errorf("binaries = %v, want the manifest's bin resolved to its command name", apps[0].Binaries)
	}
}

// One unparseable manifest must not hide the other sixteen hundred, and
// must not be silently listed as healthy either: a package you cannot
// install is better known now than at install time.
func TestApps_ReportsUndecodableManifestPerPackage(t *testing.T) {
	isolateRoot(t)
	fakeBucket(t, "main", map[string]string{
		"jq":     manifestJSON("1.8.2"),
		"broken": `{"version": oops`,
	})

	apps, err := Apps("main", true)
	if err != nil {
		t.Fatalf("one bad manifest must not fail the whole listing: %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("Apps = %+v, want both listed", apps)
	}
	byName := map[string]App{}
	for _, a := range apps {
		byName[a.Name] = a
	}
	if byName["broken"].Err == nil {
		t.Error("the undecodable manifest must carry its error")
	}
	if byName["jq"].Err != nil || byName["jq"].Version != "1.8.2" {
		t.Errorf("the healthy one must be unaffected: %+v", byName["jq"])
	}
}

// A typo must not read as an empty bucket.
func TestApps_UnknownBucketIsAnError(t *testing.T) {
	isolateRoot(t)
	fakeBucket(t, "main", nil)

	_, err := Apps("mian", false)
	if err == nil {
		t.Fatal("an unconfigured bucket must be an error, not an empty list")
	}
	if !strings.Contains(err.Error(), "main") {
		t.Errorf("the error should name the buckets that do exist: %v", err)
	}

	isolateRoot(t)
	if _, err := Apps("main", false); err == nil {
		t.Error("no buckets configured at all must also be an error")
	}
}

// Priority order is the order that decides which bucket wins a name
// collision, so a listing across buckets follows it rather than sorting
// the bucket names.
func TestAllApps_FollowsPriorityOrder(t *testing.T) {
	isolateRoot(t)
	fakeBucket(t, "zzz", map[string]string{"a": manifestJSON("1")})
	fakeBucket(t, "aaa", map[string]string{"b": manifestJSON("1")})

	apps, err := AllApps(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 {
		t.Fatalf("AllApps = %+v", apps)
	}
	if apps[0].Bucket != "zzz" {
		t.Errorf("first bucket = %q, want zzz -- it was added first, so it is searched first", apps[0].Bucket)
	}

	isolateRoot(t)
	if _, err := AllApps(false); err == nil {
		t.Error("no buckets configured must be an error rather than an empty listing")
	}
}
