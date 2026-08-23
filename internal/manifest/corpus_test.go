package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDecode_RealMainBucketCorpus decodes every manifest in a local
// checkout of ScoopInstaller/Main, if one is present (set
// GOOP_MAIN_BUCKET to the bucket/ directory). It is skipped otherwise --
// this is a corroboration check against the real upstream corpus, not a
// substitute for the fixture-based unit tests above, and shouldn't fail
// CI on machines without that checkout.
func TestDecode_RealMainBucketCorpus(t *testing.T) {
	dir := os.Getenv("GOOP_MAIN_BUCKET")
	if dir == "" {
		t.Skip("GOOP_MAIN_BUCKET not set; skipping real-corpus decode check")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	total, failed := 0, 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		total++
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Errorf("%s: read: %v", e.Name(), err)
			failed++
			continue
		}
		if _, err := Decode(data); err != nil {
			t.Errorf("%s: decode: %v", e.Name(), err)
			failed++
		}
	}
	t.Logf("decoded %d/%d manifests without error", total-failed, total)
	if failed > 0 {
		t.Errorf("%d/%d manifests failed to decode", failed, total)
	}
}
