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

// TestInstallerHooks_RealCorpus counts, with goop's own decoder rather
// than a hand-written scan, the shapes of installer/uninstaller hook the
// real corpus actually uses.
//
// It exists because a hand-written scan was what first found that goop
// ignored `installer` hooks carrying only `args`, and a claim about the
// corpus should be checkable from goop's own code. Set GOOP_MAIN_BUCKET
// to any bucket/ directory; point it at several in turn to cover more
// than main.
//
// Top-level hooks only: an architecture-specific installer (extras'
// beyondcompare has one per architecture) needs Resolve to pick a
// variant, which is a different layer than this decode-level survey. The
// counts below are therefore a floor, not a census.
//
// Assertions are deliberately weak -- the corpus changes upstream daily,
// so pinning counts would make this a tripwire rather than a check. What
// it does assert is the invariant that matters: a manifest that declares
// an installer must decode into a hook that will actually run, i.e. one
// carrying a script, a file, or arguments. A hook with none of the three
// would be silently skipped at install time, and the package reported as
// installed without anything having been installed.
func TestInstallerHooks_RealCorpus(t *testing.T) {
	dir := os.Getenv("GOOP_MAIN_BUCKET")
	if dir == "" {
		t.Skip("GOOP_MAIN_BUCKET not set; skipping real-corpus hook survey")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var withScript, withFile, argsOnly, empty int
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		m, err := Decode(data)
		if err != nil {
			continue
		}
		for label, hook := range map[string]InstallHook{
			"installer":   m.Installer,
			"uninstaller": m.Uninstaller,
		} {
			if hook.IsZero() {
				continue
			}
			switch {
			case hook.Script != "":
				withScript++
			case hook.File != "":
				withFile++
			case len(hook.Args) > 0:
				// Legal: Scoop defaults the filename to the first URL's.
				argsOnly++
			default:
				empty++
				t.Errorf("%s: %s decoded to a hook with no script, file or args -- it would be skipped silently", e.Name(), label)
			}
		}
	}
	t.Logf("hooks: %d script, %d file, %d args-only (file defaults to the download), %d unrunnable",
		withScript, withFile, argsOnly, empty)
}
