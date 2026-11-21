package installer

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"

	"github.com/TanguyBaudoin/goop/internal/envvars"
)

// Tests that install real packages must not write to the user's real
// per-user PATH.
//
// GOOP_HOME, APPDATA and LOCALAPPDATA do not cover the registry, and
// env_add_path writes to HKCU\Environment. The install harness installs
// `ack`, which depends on perl, whose manifest uses env_add_path -- so
// every harness run appended two entries naming a temp directory that
// would be deleted moments later. Nothing ever removed them. On the
// machine where this was found, 80 of 98 per-user PATH entries were dead
// and roughly thirty of them came from TestInstallHarness.
//
// isolateEnvRegistry redirects those writes to a scratch subkey and
// deletes it afterwards.
func isolateEnvRegistry(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf(`Environment-goop-test-%d`, os.Getpid())
	t.Setenv("GOOP_ENV_REGISTRY_SUBKEY", name)
	t.Cleanup(func() {
		if err := registry.DeleteKey(registry.CURRENT_USER, name); err != nil && err != registry.ErrNotExist {
			t.Errorf("leaving behind HKCU\\%s: %v", name, err)
		}
	})
	return name
}

// The override has to actually divert a write, or the isolation above is
// decoration. Writes through envvars must land in the scratch subkey and
// the real Environment key must be untouched.
func TestEnvRegistryIsolation_DivertsWrites(t *testing.T) {
	if os.Getenv("GOOP_TEST_ENVVARS") != "1" {
		t.Skip("GOOP_TEST_ENVVARS not set; this test writes to HKCU")
	}

	// What the real key holds before, so it can be compared after.
	realBefore := realPathValue(t)

	name := isolateEnvRegistry(t)
	const marker = `C:\goop-isolation-marker\bin`
	if err := envvars.Set("GOOP_ISOLATION_PROBE", marker); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// It landed in the scratch subkey.
	k, err := registry.OpenKey(registry.CURRENT_USER, name, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("the scratch subkey was never created: %v", err)
	}
	got, _, err := k.GetStringValue("GOOP_ISOLATION_PROBE")
	k.Close()
	if err != nil {
		t.Fatalf("reading the probe back from HKCU\\%s: %v", name, err)
	}
	if got != marker {
		t.Errorf("probe = %q, want %q", got, marker)
	}

	// And not in the real one.
	if realAfter := realPathValue(t); realAfter != realBefore {
		t.Errorf("the real HKCU\\Environment Path changed:\n  before: %q\n  after:  %q", realBefore, realAfter)
	}
	realKey, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("open the real Environment: %v", err)
	}
	defer realKey.Close()
	if v, _, err := realKey.GetStringValue("GOOP_ISOLATION_PROBE"); err == nil {
		t.Errorf("the probe leaked into the real HKCU\\Environment as %q", v)
	}
}

// `Environment` itself must never be treated as an override, so the
// escape hatch can never be why a real install writes to the wrong place.
func TestEnvRegistryIsolation_RefusesToRedirectToTheRealKey(t *testing.T) {
	if os.Getenv("GOOP_TEST_ENVVARS") != "1" {
		t.Skip("GOOP_TEST_ENVVARS not set; this test reads HKCU")
	}
	t.Setenv("GOOP_ENV_REGISTRY_SUBKEY", "")
	bare, err := envvars.PathEntries()
	if err != nil {
		t.Fatalf("PathEntries with no override: %v", err)
	}
	t.Setenv("GOOP_ENV_REGISTRY_SUBKEY", `Environment`)
	explicit, err := envvars.PathEntries()
	if err != nil {
		t.Fatalf("PathEntries with the override set to Environment: %v", err)
	}
	if strings.Join(bare, ";") != strings.Join(explicit, ";") {
		t.Error("setting the override to `Environment` must behave exactly like not setting it")
	}
}

func realPathValue(t *testing.T) string {
	t.Helper()
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("open the real HKCU\\Environment: %v", err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		t.Fatalf("read the real Path: %v", err)
	}
	return v
}
