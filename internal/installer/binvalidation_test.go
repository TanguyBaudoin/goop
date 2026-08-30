package installer

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/paths"
)

// A manifest whose `bin` does not match what the archive contains used to
// install cleanly, leaving a shim that pointed at nothing. goop then
// reported the machine as conformant, because conformance was decided
// from the record's version alone -- so a package that could not run at
// all was indistinguishable from a healthy one.
//
// No network: the bucket and the payload are both built here and served
// over file://, so this runs on every push rather than only in the weekly
// harness.
func TestInstall_RejectsMissingBinTarget(t *testing.T) {
	root := t.TempDir()
	// GOOP_HOME alone does not isolate an install: shortcuts follow
	// APPDATA, and goop's own config follows LOCALAPPDATA.
	t.Setenv("GOOP_HOME", root)
	t.Setenv("APPDATA", filepath.Join(root, "appdata"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "localappdata"))

	// A payload holding real.exe and nothing else.
	payload := filepath.Join(t.TempDir(), "payload.zip")
	writeZip(t, payload, map[string]string{"real.exe": "MZ not really"})
	sum := fileSum(t, payload)
	payloadURL := "file:///" + filepath.ToSlash(payload)

	bucketDir := filepath.Join(root, "buckets", "test", "bucket")
	if err := os.MkdirAll(bucketDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"version":"1.0","url":%q,"hash":"sha256:%s","bin":"missing.exe"}`, payloadURL, sum)
	if err := os.WriteFile(filepath.Join(bucketDir, "brokenbin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writeBucketConfig(t, "test", payloadURL)

	_, err := Install("brokenbin")
	if err == nil {
		t.Fatal("installing a package whose bin target is absent should fail, not report success")
	}

	// And it must not leave a shim behind claiming the command exists.
	if _, err := os.Stat(filepath.Join(paths.Shims(), "missing.exe")); !os.IsNotExist(err) {
		t.Error("a shim was created for a target that does not exist")
	}

	// A second attempt must not report success off the back of the record
	// the failed one committed -- that turned one failure into a machine
	// that looked correctly installed forever after.
	if _, err := Install("brokenbin"); err == nil {
		t.Error("retrying a failed install reported success")
	}
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for name, body := range files {
		e, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func fileSum(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeBucketConfig registers a bucket whose content is already on disk,
// which is all this test needs -- Add would try to fetch it.
func writeBucketConfig(t *testing.T, name, url string) {
	t.Helper()
	cfg := map[string]any{
		"buckets": []map[string]string{{"name": name, "url": url, "kind": "archive"}},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.BucketsConfig(), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
