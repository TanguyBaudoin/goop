package bucket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/paths"
)

// isolateRoot points goop at an empty tree, so nothing here reads or
// writes a real bucket configuration.
func isolateRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("GOOP_HOME", root)
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "localappdata"))
	return root
}

// fakeBucket registers a bucket and lays manifests out the way a clone
// would, without cloning anything: Register only writes config, and
// every lookup path reads from disk.
func fakeBucket(t *testing.T, name string, manifests map[string]string) {
	t.Helper()
	if err := Register(name, "https://example.invalid/"+name, KindGit); err != nil {
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

func manifestJSON(version string) string {
	return `{"version":"` + version + `","url":"https://example.invalid/x.zip","bin":"x.exe"}`
}

func TestRegister(t *testing.T) {
	isolateRoot(t)

	if err := Register("", "https://example.invalid/x", KindGit); err == nil {
		t.Error("an empty bucket name must be refused")
	}
	if err := Register("main", "https://example.invalid/main", ""); err != nil {
		t.Fatal(err)
	}
	// Adding the same name twice silently would leave two entries and a
	// lookup order nobody chose.
	if err := Register("main", "https://example.invalid/other", KindGit); err == nil {
		t.Error("registering an existing name must be refused")
	}

	entries, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("List() = %+v, want one entry", entries)
	}
	// An empty kind means git, for entries written before Kind existed.
	if got := entries[0].kind(); got != KindGit {
		t.Errorf("kind = %q, want %q", got, KindGit)
	}
}

// Priority is the configured order, and it decides which bucket wins
// when two carry the same app -- the whole point of `bucket priority`.
func TestFind_FirstBucketWins(t *testing.T) {
	isolateRoot(t)
	fakeBucket(t, "main", map[string]string{"jq": manifestJSON("1.0-from-main")})
	fakeBucket(t, "extras", map[string]string{
		"jq":  manifestJSON("1.0-from-extras"),
		"mpv": manifestJSON("0.41.0"),
	})

	name, m, err := Find("jq")
	if err != nil {
		t.Fatal(err)
	}
	if name != "main" || m.Version != "1.0-from-main" {
		t.Errorf("Find(jq) = %s/%s, want main/1.0-from-main", name, m.Version)
	}

	// Only in the second bucket: still found.
	name, _, err = Find("mpv")
	if err != nil {
		t.Fatal(err)
	}
	if name != "extras" {
		t.Errorf("Find(mpv) = %s, want extras", name)
	}

	if err := SetPriority("extras", 1); err != nil {
		t.Fatal(err)
	}
	name, m, err = Find("jq")
	if err != nil {
		t.Fatal(err)
	}
	if name != "extras" || m.Version != "1.0-from-extras" {
		t.Errorf("after promoting extras, Find(jq) = %s/%s, want extras/1.0-from-extras", name, m.Version)
	}
}

func TestSetPriority_ClampsAndReports(t *testing.T) {
	isolateRoot(t)
	for _, n := range []string{"a", "b", "c"} {
		fakeBucket(t, n, nil)
	}

	order := func() []string {
		entries, err := List()
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(entries))
		for i, e := range entries {
			out[i] = e.Name
		}
		return out
	}

	if err := SetPriority("c", 1); err != nil {
		t.Fatal(err)
	}
	if got := order(); got[0] != "c" || len(got) != 3 {
		t.Errorf("order = %v, want c first and nothing lost", got)
	}

	// Out-of-range positions clamp rather than corrupting the list --
	// losing a bucket entry here would silently change every lookup.
	if err := SetPriority("c", 0); err != nil {
		t.Fatal(err)
	}
	if got := order(); len(got) != 3 || got[0] != "c" {
		t.Errorf("position 0 must clamp to first, got %v", got)
	}
	if err := SetPriority("c", 99); err != nil {
		t.Fatal(err)
	}
	if got := order(); len(got) != 3 || got[2] != "c" {
		t.Errorf("an oversized position must clamp to last, got %v", got)
	}

	if err := SetPriority("nope", 1); err == nil {
		t.Error("an unconfigured bucket must be reported, not silently ignored")
	}
}

// A bucket-qualified spec consults exactly that bucket, even when
// another one is higher priority and also has the app.
func TestFindIn_IgnoresOtherBuckets(t *testing.T) {
	isolateRoot(t)
	fakeBucket(t, "main", map[string]string{"jq": manifestJSON("from-main")})
	fakeBucket(t, "extras", map[string]string{"jq": manifestJSON("from-extras")})

	m, err := FindIn("extras", "jq")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "from-extras" {
		t.Errorf("FindIn(extras, jq) = %s, want from-extras", m.Version)
	}
	if _, err := FindIn("extras", "absent"); err == nil {
		t.Error("an app missing from the named bucket must not fall back to another")
	}
	if _, err := FindIn("nosuch", "jq"); err == nil {
		t.Error("an unconfigured bucket must be reported")
	}
}

func TestResolve_QualifiedAndUnqualified(t *testing.T) {
	isolateRoot(t)
	fakeBucket(t, "main", map[string]string{"jq": manifestJSON("from-main")})
	fakeBucket(t, "extras", map[string]string{"jq": manifestJSON("from-extras")})

	name, m, err := Resolve(manifest.Spec{Name: "jq"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "main" || m.Version != "from-main" {
		t.Errorf("unqualified Resolve = %s/%s, want main/from-main", name, m.Version)
	}

	name, m, err = Resolve(manifest.Spec{Bucket: "extras", Name: "jq"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "extras" || m.Version != "from-extras" {
		t.Errorf("qualified Resolve = %s/%s, want extras/from-extras", name, m.Version)
	}
}

// "not found" for a manifest that exists but will not decode sends you
// looking in entirely the wrong place.
func TestFind_MalformedManifestIsNotNotFound(t *testing.T) {
	isolateRoot(t)
	fakeBucket(t, "main", map[string]string{"broken": `{"version": oops`})

	_, _, err := Find("broken")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := err.Error(); got == `"broken" not found in bucket(s) main` {
		t.Errorf("a manifest that will not decode must not be reported as missing: %v", err)
	}
}

func TestFind_NoBucketsSaysSo(t *testing.T) {
	isolateRoot(t)
	_, _, err := Find("jq")
	if err == nil {
		t.Fatal("expected an error with no buckets configured")
	}
	if !strings.Contains(err.Error(), "no buckets configured") {
		t.Errorf("error should say no buckets are configured, got %v", err)
	}
}

// Removing a bucket drops the config entry and the local clone, and must
// leave the other buckets alone.
func TestRemove(t *testing.T) {
	isolateRoot(t)
	fakeBucket(t, "main", map[string]string{"jq": manifestJSON("1.0")})
	fakeBucket(t, "extras", map[string]string{"mpv": manifestJSON("1.0")})

	if err := Remove("main"); err != nil {
		t.Fatal(err)
	}
	entries, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "extras" {
		t.Errorf("List() = %+v, want only extras", entries)
	}
	if _, err := os.Stat(paths.Bucket("main")); !os.IsNotExist(err) {
		t.Error("the local clone should be gone")
	}
	if _, err := os.Stat(paths.Bucket("extras")); err != nil {
		t.Errorf("the other bucket must be untouched: %v", err)
	}
	if err := Remove("main"); err == nil {
		t.Error("removing an unconfigured bucket must be reported")
	}
}

func TestManifestNames(t *testing.T) {
	isolateRoot(t)
	fakeBucket(t, "main", map[string]string{
		"jq":      manifestJSON("1.0"),
		"ripgrep": manifestJSON("1.0"),
	})
	// Not a manifest, and must not be offered as an app.
	if err := os.WriteFile(filepath.Join(paths.Bucket("main"), "bucket", "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	names, err := ManifestNames("main")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("ManifestNames = %v, want two entries", names)
	}
	for _, n := range names {
		if n == "README" || n == "README.md" {
			t.Errorf("non-manifest file offered as an app: %v", names)
		}
	}
}

// Buckets whose manifests sit at the root rather than under bucket/ are
// a real layout, and goop reads both.
func TestManifestDir_FallsBackToTheBucketRoot(t *testing.T) {
	isolateRoot(t)
	if err := Register("flat", "https://example.invalid/flat", KindGit); err != nil {
		t.Fatal(err)
	}
	dir := paths.Bucket("flat")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "jq.json"), []byte(manifestJSON("1.0")), 0o644); err != nil {
		t.Fatal(err)
	}

	name, _, err := Find("jq")
	if err != nil {
		t.Fatalf("a bucket with manifests at its root must still resolve: %v", err)
	}
	if name != "flat" {
		t.Errorf("Find(jq) = %s, want flat", name)
	}
}
