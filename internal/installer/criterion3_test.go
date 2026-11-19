package installer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/TanguyBaudoin/goop/internal/bucket"
	"github.com/TanguyBaudoin/goop/internal/paths"
	"github.com/TanguyBaudoin/goop/internal/profileset"
)

// Acceptance criterion 3 -- "a `sync` on two separate machines yields
// matching trees" -- was the last one marked Unverified for the honest
// reason that nobody had ever run it. It is also the criterion the whole
// profile mechanism exists to satisfy: if a profile file does not fully
// determine the tree, then pinning is decoration.
//
// What this test does: installs into root A, exports a profile file from
// A's receipts, syncs a second independent root B from that file, and
// compares the two trees file by file.
//
// What "independent" covers here, stated plainly because the criterion
// says *machines* and this is one host: separate GOOP_HOME, separate
// APPDATA and LOCALAPPDATA (so separate config, separate profile store
// and separate Start Menu), and a bucket cloned separately into each
// root. Nothing is shared but the network and the profile file.
//
// What it therefore does NOT cover, and what still needs two real hosts:
// a different Windows build, a different CPU architecture, a different
// system locale or code page, and a manifest whose script branches on
// any of those. Those are real risks and this test is blind to them. It
// settles the question it can settle -- whether the *same* inputs
// produce the same tree twice over -- which is the part that was never
// checked at all.
//
// Opt-in, same gate as the install harness, because it installs real
// packages twice:
//
//	$env:GOOP_HARNESS = '1'; go test ./internal/installer/ -run Criterion3 -v -timeout 30m
const criterion3Profile = "crit3"

var criterion3Apps = []struct{ name, shape string }{
	{"jq", "a bare executable: only the download and the shim can differ"},
	{"ripgrep", "a zip with extract_dir, so the unpack must lay out identically twice"},
	{"7zip", "an MSI with post_install, persist and shortcuts -- a script runs, and scripts are where nondeterminism lives"},
}

func TestCriterion3_SyncOnTwoRootsYieldsMatchingTrees(t *testing.T) {
	if os.Getenv("GOOP_HARNESS") != "1" {
		t.Skip("GOOP_HARNESS not set; skipping the two-root reproducibility check")
	}

	bucketURL := os.Getenv("GOOP_HARNESS_BUCKET_URL")
	if bucketURL == "" {
		bucketURL = "https://github.com/ScoopInstaller/Main"
	}
	names := make([]string, len(criterion3Apps))
	for i, a := range criterion3Apps {
		names[i] = a.name
	}

	// Root A plays the machine where the profile was authored.
	rootA := useRoot(t, "A")
	if err := bucket.Add("main", bucketURL, ""); err != nil {
		t.Fatalf("root A, adding the bucket: %v", err)
	}
	for _, a := range criterion3Apps {
		if _, err := InstallInto(a.name, criterion3Profile); err != nil {
			if IsUnsupported(err) {
				t.Skipf("%s is unsupported, so the comparison would be vacuous: %v", a.name, err)
			}
			t.Fatalf("root A, installing %s (%s): %v", a.name, a.shape, err)
		}
	}
	rep, err := ExportProfiles([]string{criterion3Profile}, func(string) ([]string, error) {
		return names, nil
	})
	if err != nil {
		t.Fatalf("root A, exporting the profile: %v", err)
	}
	if len(rep.Missing) > 0 {
		t.Fatalf("root A exported with members missing: %v", rep.Missing)
	}
	// A pin with no digest would weaken the very thing under test: root B
	// would match on version alone and the manifest could have changed
	// underneath. Fail rather than quietly prove less.
	if len(rep.Undigested) > 0 {
		t.Fatalf("root A exported pins with no manifest digest: %v -- the comparison would not cover the manifest", rep.Undigested)
	}
	t.Logf("root A: installed %d packages, exported %d pins", len(names), rep.Pinned)
	snapA := snapshotRoot(t, rootA)

	// The file is what crosses between the two machines, so it crosses as
	// a file here too. A report that only ever stayed in memory would not
	// prove the thing that actually travels.
	file := profileFileForTest(t, t.TempDir(), rep.File)

	// Root B plays the machine that only ever sees the file.
	rootB := useRoot(t, "B")
	if err := bucket.Add("main", bucketURL, ""); err != nil {
		t.Fatalf("root B, adding the bucket: %v", err)
	}
	devs, failures, err := SyncProfiles(file, []string{criterion3Profile})
	if err != nil {
		t.Fatalf("root B, syncing: %v", err)
	}
	if len(failures) > 0 {
		for pkg, ferr := range failures {
			t.Errorf("root B, syncing %s: %v", pkg, ferr)
		}
		t.Fatal("root B did not reach the pinned state, so there is nothing to compare")
	}
	t.Logf("root B: sync resolved %d deviation(s) from an empty root", len(devs))

	// The sync must have left nothing outstanding, or "matching" would be
	// measured against a state the file does not describe.
	left, err := Check(file, []string{criterion3Profile})
	if err != nil {
		t.Fatalf("root B, re-checking after sync: %v", err)
	}
	if len(left) > 0 {
		for _, d := range left {
			t.Errorf("root B still deviates after sync: %s wants %s, got %s (%s)", d.Package, d.Want, d.Got, d.Reason)
		}
	}
	snapB := snapshotRoot(t, rootB)

	compareTrees(t, snapA, snapB, rootA, rootB)
}

// useRoot points goop at a fresh root and returns it.
//
// GOOP_HOME alone does not isolate anything: APPDATA decides where
// shortcuts land (the real Start Menu, which has been clobbered during
// development) and LOCALAPPDATA holds goop's own config and profile
// store. All three move, and each root gets siblings rather than
// children so that walking the root sees only goop's tree.
func useRoot(t *testing.T, label string) string {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("creating root %s: %v", label, err)
	}
	t.Setenv("GOOP_HOME", root)
	t.Setenv("APPDATA", filepath.Join(base, "appdata"))
	t.Setenv("LOCALAPPDATA", filepath.Join(base, "localappdata"))
	if got := paths.Root(); got != root {
		t.Fatalf("root %s did not take effect: paths.Root() = %q, want %q", label, got, root)
	}
	return root
}

// treeSnapshot is what a root looks like, reduced to what two machines
// are expected to agree on.
type treeSnapshot struct {
	versions map[string]string         // app -> installed version
	files    map[string]string         // slash-separated relative path -> sha256
	receipts map[string]map[string]any // app -> receipt, decoded
	// links are the reparse points: `current` for each app, and one per
	// persisted path. Their targets are recorded with the root replaced
	// by a placeholder, so two roots can be compared on where each link
	// points rather than merely on the fact that one exists. Discovered
	// the hard way: this test first failed trying to read 7zip's
	// persisted `Codecs` directory as a file.
	links map[string]string // relative path -> target, root normalised
}

// snapshotRoot records the tree under a goop root.
//
// buckets/ and cache/ are excluded on purpose. A bucket is a git clone:
// its packfiles and refs differ between two clones of the same commit,
// which says nothing about whether the install matched. The cache holds
// downloaded archives, whose bytes are already covered by the hash check
// the installer performs before unpacking.
func snapshotRoot(t *testing.T, root string) treeSnapshot {
	t.Helper()
	snap := treeSnapshot{
		versions: map[string]string{},
		files:    map[string]string{},
		receipts: map[string]map[string]any{},
		links:    map[string]string{},
	}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		switch strings.SplitN(rel, "/", 2)[0] {
		case "buckets", "cache":
			return filepath.SkipDir
		}
		// Reparse points come before the file/directory question, because
		// on Windows a junction is neither: `current` is a link to a
		// version directory, and every persisted path is a link into
		// persist/. Record where each one points and do not descend --
		// descending would count the same bytes twice under two names.
		//
		// isReparsePoint is the installer's own, which reads the NTFS
		// attribute bit instead of Go's mode bits. Its doc comment warns
		// that Go reports some junctions as ModeIrregular rather than
		// ModeSymlink; this test hit exactly that, failing on 7zip's
		// persisted Codecs directory, before switching to it.
		//
		// SkipDir is returned only when WalkDir itself believes the entry
		// is a directory. On a non-directory, SkipDir would skip the rest
		// of the *containing* directory and silently drop files from the
		// snapshot.
		if isReparsePoint(path) {
			target, lerr := os.Readlink(path)
			if lerr != nil {
				// A link whose target cannot even be read is a finding, not
				// something to paper over.
				return fmt.Errorf("%s: reading link target: %w", rel, lerr)
			}
			snap.links[rel] = string(normalizeRoot([]byte(target), root))
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == recordFileName {
			rec, rerr := decodeReceiptFile(path)
			if rerr != nil {
				return fmt.Errorf("%s: %w", rel, rerr)
			}
			app := receiptApp(rel)
			snap.receipts[app] = rec
			if v, ok := rec["version"].(string); ok {
				snap.versions[app] = v
			}
			return nil
		}
		sum, serr := fileSHA256(path)
		if serr != nil {
			return fmt.Errorf("%s: %w", rel, serr)
		}
		snap.files[rel] = sum
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return snap
}

// receiptApp pulls the app name out of "apps/<app>/<version>/<receipt>".
func receiptApp(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) >= 2 && parts[0] == "apps" {
		return parts[1]
	}
	return rel
}

func decodeReceiptFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// receiptVolatile are the receipt fields two machines are not expected to
// agree on, with the reason each one is allowed to differ.
var receiptVolatile = map[string]string{
	"installed_at": "the wall clock at install time",
}

// compareTrees is the assertion the criterion actually asks for.
//
// A difference is not automatically a failure: a shim sidecar names an
// absolute path, and so does anything a post_install script wrote, so
// those files *must* differ between two roots. What must not differ is
// the content once the root prefix is accounted for. Every difference is
// therefore classified, and the test reports which kind it found rather
// than only that the trees were unequal -- the distinction between
// "reproducible, modulo install location" and "nondeterministic" is the
// entire point.
func compareTrees(t *testing.T, a, b treeSnapshot, rootA, rootB string) {
	t.Helper()

	// Versions first: a version mismatch makes every file difference
	// meaningless, so say so plainly instead of dumping a file list.
	for app, va := range a.versions {
		vb, ok := b.versions[app]
		if !ok {
			t.Errorf("%s is installed in root A (%s) and absent from root B", app, va)
			continue
		}
		if va != vb {
			t.Errorf("%s: root A has %s, root B has %s -- the profile file did not determine the version", app, va, vb)
		}
	}
	for app := range b.versions {
		if _, ok := a.versions[app]; !ok {
			t.Errorf("%s is installed in root B and absent from root A", app)
		}
	}

	// Receipts: the recorded description of what was installed.
	for app, ra := range a.receipts {
		rb, ok := b.receipts[app]
		if !ok {
			continue // already reported above
		}
		for _, field := range sortedKeys(ra, rb) {
			if _, volatile := receiptVolatile[field]; volatile {
				continue
			}
			ja, _ := json.Marshal(ra[field])
			jb, _ := json.Marshal(rb[field])
			if bytes.Equal(ja, jb) {
				continue
			}
			// Paths inside the receipt (env_add_path, extra shims) hold the
			// root, and are expected to differ by exactly that.
			if bytes.Equal(normalizeRoot(ja, rootA), normalizeRoot(jb, rootB)) {
				continue
			}
			t.Errorf("%s receipt field %q differs beyond the install path:\n  A: %s\n  B: %s", app, field, ja, jb)
		}
	}

	// Links. Targets are already root-normalised, so they must match
	// exactly: a `current` junction pointing at a different version, or a
	// persist link pointing somewhere else, is precisely the kind of
	// divergence this criterion is about.
	for rel, ta := range a.links {
		tb, ok := b.links[rel]
		if !ok {
			t.Errorf("root B has no link at %s (root A points it at %s)", rel, ta)
			continue
		}
		if ta != tb {
			t.Errorf("link %s points elsewhere: A -> %s, B -> %s", rel, ta, tb)
		}
	}
	for rel := range b.links {
		if _, ok := a.links[rel]; !ok {
			t.Errorf("root B has an extra link at %s", rel)
		}
	}
	// Every app must have its `current` junction: without it the shims
	// point at a path that stops existing on the next upgrade.
	for app := range a.versions {
		rel := "apps/" + app + "/current"
		if _, ok := a.links[rel]; !ok {
			t.Errorf("root A has no `current` junction for %s", app)
		}
		if _, ok := b.links[rel]; !ok {
			t.Errorf("root B has no `current` junction for %s", app)
		}
	}
	t.Logf("compared %d links (junctions and persist paths)", len(a.links))

	// Files.
	var onlyA, onlyB, differ, rootOnly []string
	for rel, sa := range a.files {
		sb, ok := b.files[rel]
		if !ok {
			onlyA = append(onlyA, rel)
			continue
		}
		if sa == sb {
			continue
		}
		same, err := equalIgnoringRoot(filepath.Join(rootA, rel), rootA, filepath.Join(rootB, rel), rootB)
		if err != nil {
			t.Errorf("comparing %s: %v", rel, err)
			continue
		}
		if same {
			rootOnly = append(rootOnly, rel)
			continue
		}
		differ = append(differ, rel)
	}
	for rel := range b.files {
		if _, ok := a.files[rel]; !ok {
			onlyB = append(onlyB, rel)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	sort.Strings(differ)
	sort.Strings(rootOnly)

	t.Logf("compared %d files: %d identical, %d equal once the install path is normalised, %d genuinely different, %d only in A, %d only in B",
		len(a.files), len(a.files)-len(differ)-len(rootOnly)-len(onlyA), len(rootOnly), len(differ), len(onlyA), len(onlyB))
	for _, rel := range rootOnly {
		t.Logf("  embeds the install path (expected): %s", rel)
	}
	for _, rel := range onlyA {
		t.Errorf("only in root A: %s", rel)
	}
	for _, rel := range onlyB {
		t.Errorf("only in root B: %s", rel)
	}
	for _, rel := range differ {
		t.Errorf("content differs and not only by the install path: %s%s", rel,
			describeDifference(filepath.Join(rootA, rel), rootA, filepath.Join(rootB, rel), rootB))
	}
}

func sortedKeys(maps ...map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range maps {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// describeDifference shows what actually differs, for a file that is not
// explained by the install path.
//
// "the trees differ" is not a usable finding -- the first real difference
// this test found was a single JSON file, and whether that mattered came
// down to which bytes. Reproducibility failures have to arrive with their
// evidence attached or they get dismissed.
func describeDifference(pathA, rootA, pathB, rootB string) string {
	da, erra := os.ReadFile(pathA)
	db, errb := os.ReadFile(pathB)
	if erra != nil || errb != nil {
		return fmt.Sprintf("\n  (could not read both sides: %v / %v)", erra, errb)
	}
	na := normalizeRoot(da, rootA)
	nb := normalizeRoot(db, rootB)
	if !looksTextual(na) || !looksTextual(nb) {
		return fmt.Sprintf("\n  binary: A is %d bytes, B is %d bytes", len(da), len(db))
	}
	return fmt.Sprintf("\n  A: %s\n  B: %s", clip(string(na), 400), clip(string(nb), 400))
}

func looksTextual(b []byte) bool {
	if len(b) > 64*1024 {
		return false
	}
	return !bytes.Contains(b, []byte{0}) || bytes.Contains(b, []byte(rootPlaceholder))
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// equalIgnoringRoot reports whether two files say the same thing once
// each one's own install path is replaced by a common placeholder.
func equalIgnoringRoot(pathA, rootA, pathB, rootB string) (bool, error) {
	da, err := os.ReadFile(pathA)
	if err != nil {
		return false, err
	}
	db, err := os.ReadFile(pathB)
	if err != nil {
		return false, err
	}
	return bytes.Equal(normalizeRoot(da, rootA), normalizeRoot(db, rootB)), nil
}

// rootPlaceholder stands in for whichever root a file was written under.
//
// Printable on purpose: it shows up in this test's own failure messages
// ("root A points it at ${GOOP_ROOT}\apps\jq\1.8.2"), and a placeholder
// made of NUL bytes rendered there as blanks.
const rootPlaceholder = "${GOOP_ROOT}"

// normalizeRoot replaces every spelling of root in b with a placeholder.
//
// There are more spellings than one would hope. A shim sidecar is written
// by PowerShell, so it can be UTF-16LE rather than UTF-8. A path may use
// backslashes or forward slashes depending on which layer wrote it, and
// Windows is case-insensitive, so a script may well have echoed the path
// back in a different case than goop used.
func normalizeRoot(b []byte, root string) []byte {
	for _, form := range rootSpellings(root) {
		b = bytes.ReplaceAll(b, []byte(form), []byte(rootPlaceholder))
		b = bytes.ReplaceAll(b, utf16le(form), utf16le(rootPlaceholder))
	}
	return b
}

// rootSpellings enumerates the ways a root path can appear in a file.
func rootSpellings(root string) []string {
	bases := []string{
		filepath.FromSlash(root),
		filepath.ToSlash(root),
	}
	// A JSON-encoded Windows path has its separators doubled.
	bases = append(bases, strings.ReplaceAll(filepath.FromSlash(root), `\`, `\\`))

	seen := map[string]bool{}
	var out []string
	for _, base := range bases {
		for _, v := range []string{base, strings.ToLower(base), strings.ToUpper(base)} {
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	// Longest first, so a shorter spelling cannot eat the prefix of a
	// longer one and leave a tail behind.
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// utf16le encodes s the way PowerShell writes a text file.
func utf16le(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// profileFileForTest keeps the profileset import honest: the exported
// report is written and read back, because a file that cannot survive
// the round trip would not reach a second machine either.
func profileFileForTest(t *testing.T, dir string, f profileset.File) profileset.File {
	t.Helper()
	path := filepath.Join(dir, "goop.profile.json")
	if err := profileset.Save(path, f); err != nil {
		t.Fatalf("saving the profile file: %v", err)
	}
	back, err := profileset.Load(path)
	if err != nil {
		t.Fatalf("loading the profile file back: %v", err)
	}
	return back
}
