package lockfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "goop.lock.json")

	f := File{Entries: []Entry{
		{Name: "ripgrep", Version: "15.2.0", Bucket: "main", Architecture: "64bit",
			URLs: []string{"https://example.com/rg.zip"}, Hashes: []string{"abcd"}},
		{Name: "jq", Version: "1.8.2", Bucket: "main", Architecture: "64bit",
			URLs: []string{"https://example.com/jq.exe"}, Hashes: []string{"1234"}},
	}}
	if err := Save(path, f); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(got.Entries))
	}
	// Save sorts by name: jq before ripgrep.
	if got.Entries[0].Name != "jq" || got.Entries[1].Name != "ripgrep" {
		t.Fatalf("entries not sorted by name: %+v", got.Entries)
	}

	e, ok := got.Find("jq")
	if !ok || e.Version != "1.8.2" {
		t.Fatalf("Find(jq) = %+v, %v", e, ok)
	}
	if _, ok := got.Find("nonexistent"); ok {
		t.Fatal("Find(nonexistent) should not find anything")
	}
}

func TestSave_StableOutputForDiffability(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "goop.lock.json")

	f := File{Entries: []Entry{
		{Name: "b", Version: "1"},
		{Name: "a", Version: "1"},
	}}
	if err := Save(path, f); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Re-save the same logical content (entries given in a different
	// order) and confirm the bytes on disk are identical -- a lockfile
	// that reorders itself on every regeneration is useless to diff.
	f2 := File{Entries: []Entry{
		{Name: "a", Version: "1"},
		{Name: "b", Version: "1"},
	}}
	if err := Save(path, f2); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(first) != string(second) {
		t.Fatalf("Save output not stable:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}
