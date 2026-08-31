package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolate points the persisted config at a temp file and clears the
// environment overrides, so a test never reads or writes the real one.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("GOOP_HOME", "")
	for _, n := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		t.Setenv(n, "")
	}
	return dir
}

// The order decides where every file goop writes ends up, so getting it
// backwards would move an entire installation.
func TestRoot_ResolutionOrder(t *testing.T) {
	isolate(t)

	def := Root()
	if !strings.HasSuffix(def, string(os.PathSeparator)+"goop") {
		t.Errorf("with nothing set, Root() = %q, want <home>\\goop", def)
	}

	persisted := filepath.Join(t.TempDir(), "persisted")
	if err := SetConfiguredRoot(persisted); err != nil {
		t.Fatal(err)
	}
	if got := Root(); got != persisted {
		t.Errorf("a persisted root must beat the default: got %q, want %q", got, persisted)
	}

	env := filepath.Join(t.TempDir(), "fromenv")
	t.Setenv("GOOP_HOME", env)
	if got := Root(); got != env {
		t.Errorf("GOOP_HOME must beat a persisted root: got %q, want %q", got, env)
	}

	// An empty GOOP_HOME is not a choice -- it is an unset variable that
	// happens to exist, and must not shadow the persisted root.
	t.Setenv("GOOP_HOME", "")
	if got := Root(); got != persisted {
		t.Errorf("an empty GOOP_HOME must not win: got %q, want %q", got, persisted)
	}

	if err := UnsetConfiguredRoot(); err != nil {
		t.Fatal(err)
	}
	if got := Root(); got != def {
		t.Errorf("after unsetting, Root() = %q, want the default %q", got, def)
	}
}

// The config file says where the root is, so it cannot live under it.
func TestConfigFilePath_IsIndependentOfRoot(t *testing.T) {
	isolate(t)
	before := ConfigFilePath()
	t.Setenv("GOOP_HOME", filepath.Join(t.TempDir(), "elsewhere"))
	if after := ConfigFilePath(); after != before {
		t.Errorf("ConfigFilePath moved with the root: %q -> %q", before, after)
	}
	if strings.HasPrefix(before, Root()) {
		t.Errorf("ConfigFilePath %q is under Root %q -- goop could not find it to read the root", before, Root())
	}
}

// A machine behind a corporate proxy with an internal bucket depends on
// this matching correctly: a miss sends internal traffic through the
// proxy, and a false hit sends external traffic around it.
func TestProxyFor(t *testing.T) {
	isolate(t)
	const proxy = "http://proxy.corp:8080"
	if err := SetConfiguredProxy(proxy); err != nil {
		t.Fatal(err)
	}
	if err := SetConfiguredNoProxy([]string{"*.corp", "localhost", ".internal", "example.com"}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		host  string
		proxy bool // true = should go through the proxy
	}{
		{"github.com", true},
		{"artifacts.corp", false},   // *.corp, matched as a suffix
		{"deep.nested.corp", false}, // still under it
		{"corp", false},             // the bare domain itself
		{"notcorp", true},           // must not match on substring
		{"corp.example.org", true},  // a prefix is not a suffix
		{"localhost", false},        // no wildcard, exact
		{"srv.internal", false},     // leading dot form
		{"EXAMPLE.COM", false},      // case-insensitive
		{"sub.example.com", false},  // subdomain of a bare entry
		{"badexample.com", true},    // suffix must be on a dot boundary
	}
	for _, tc := range cases {
		got := ProxyFor(tc.host) != ""
		if got != tc.proxy {
			verb := "should not"
			if tc.proxy {
				verb = "should"
			}
			t.Errorf("%s %s go through the proxy (ProxyFor = %q)", tc.host, verb, ProxyFor(tc.host))
		}
	}

	// "*" means everything is direct.
	if err := SetConfiguredNoProxy([]string{"*"}); err != nil {
		t.Fatal(err)
	}
	if got := ProxyFor("github.com"); got != "" {
		t.Errorf(`with no-proxy "*", ProxyFor = %q, want ""`, got)
	}

	// No proxy configured at all: nothing is proxied, whatever no-proxy
	// says.
	if err := UnsetConfiguredProxy(); err != nil {
		t.Fatal(err)
	}
	if got := ProxyFor("github.com"); got != "" {
		t.Errorf("no proxy configured, ProxyFor = %q", got)
	}
}

// A stale bucket makes `goop update` report "up to date" against old
// data, which is worse than being slow -- so the threshold matters.
func TestBucketsStale(t *testing.T) {
	isolate(t)
	t.Setenv("GOOP_HOME", t.TempDir())

	if !BucketsStale() {
		t.Error("buckets never refreshed must count as stale")
	}
	if err := MarkBucketsUpdated(); err != nil {
		t.Fatal(err)
	}
	if BucketsStale() {
		t.Error("just refreshed must not be stale")
	}

	// A TTL of zero disables the automatic refresh entirely, which is
	// the documented way to stop goop touching the network on install.
	if err := SetConfiguredBucketTTL(0); err != nil {
		t.Fatal(err)
	}
	if BucketsStale() {
		t.Error("a zero TTL must never report stale")
	}

	// A sub-second TTL would be stored as 0 seconds, which means "never
	// refresh" -- the opposite of what was asked. Refused, not rounded.
	if err := SetConfiguredBucketTTL(500 * time.Millisecond); err == nil {
		t.Error("a sub-second TTL must be refused rather than silently disabling the refresh")
	}
	if err := SetConfiguredBucketTTL(-time.Second); err == nil {
		t.Error("a negative TTL must be refused")
	}

	// Elapsed: back-date the recorded refresh rather than sleeping.
	if err := UnsetConfiguredBucketTTL(); err != nil {
		t.Fatal(err)
	}
	if err := updateConfig(func(c *config) {
		c.LastBucketUpdate = time.Now().Add(-4 * time.Hour).Format(time.RFC3339)
	}); err != nil {
		t.Fatal(err)
	}
	if !BucketsStale() {
		t.Error("a refresh older than the 3h default must report stale")
	}

	if err := UnsetConfiguredBucketTTL(); err != nil {
		t.Fatal(err)
	}
	if got := ConfiguredBucketTTL(); got != 3*time.Hour {
		t.Errorf("default TTL = %v, want 3h (Scoop's own threshold)", got)
	}
}

// Every path is derived from Root, so moving Root must move all of them
// -- a stray absolute path would write outside an isolated install and,
// in a test, outside the temp directory.
func TestPathsFollowRoot(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	t.Setenv("GOOP_HOME", root)

	derived := map[string]string{
		"Buckets":       Buckets(),
		"Bucket":        Bucket("main"),
		"BucketsConfig": BucketsConfig(),
		"Apps":          Apps(),
		"App":           App("jq"),
		"AppVersion":    AppVersion("jq", "1.8.2"),
		"AppCurrent":    AppCurrent("jq"),
		"Shims":         Shims(),
		"ShimMaster":    ShimMaster(),
		"Cache":         Cache(),
		"Modules":       Modules(),
		"Persist":       Persist("jq"),
	}
	for name, p := range derived {
		if !strings.HasPrefix(p, root) {
			t.Errorf("%s() = %q, which is not under Root %q", name, p, root)
		}
	}

	// The staging directory has to be a sibling of the version directory,
	// not inside it: the install is committed by renaming one onto the
	// other, and a rename across directories is what makes it atomic.
	staging := AppVersionStaging("jq", "1.8.2")
	if filepath.Dir(staging) != filepath.Dir(AppVersion("jq", "1.8.2")) {
		t.Errorf("staging %q is not a sibling of the version directory %q", staging, AppVersion("jq", "1.8.2"))
	}
	if staging == AppVersion("jq", "1.8.2") {
		t.Error("staging and the committed directory must differ")
	}
}
