package archive

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func writeTestZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "test.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range entries {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return zipPath
}

func TestExtractZip_Flat(t *testing.T) {
	zipPath := writeTestZip(t, map[string]string{
		"rg.exe":    "binary-content",
		"README.md": "readme",
		"sub/x.txt": "nested",
	})
	dest := t.TempDir()
	if err := ExtractZip(zipPath, dest, ""); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(dest, "rg.exe"), "binary-content")
	assertFileContent(t, filepath.Join(dest, "sub", "x.txt"), "nested")
}

func TestExtractZip_StripDir(t *testing.T) {
	zipPath := writeTestZip(t, map[string]string{
		"ripgrep-15.2.0-x86_64-pc-windows-msvc/rg.exe":       "binary",
		"ripgrep-15.2.0-x86_64-pc-windows-msvc/CHANGELOG.md": "changelog",
	})
	dest := t.TempDir()
	if err := ExtractZip(zipPath, dest, "ripgrep-15.2.0-x86_64-pc-windows-msvc"); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(dest, "rg.exe"), "binary")
	if _, err := os.Stat(filepath.Join(dest, "ripgrep-15.2.0-x86_64-pc-windows-msvc")); !os.IsNotExist(err) {
		t.Fatal("expected the stripped prefix directory not to exist in dest")
	}
}

func TestExtractZip_ZipSlipRejected(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "evil.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	fw, err := w.Create("../../evil.txt")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte("pwned"))
	w.Close()
	f.Close()

	dest := filepath.Join(dir, "dest")
	os.MkdirAll(dest, 0o755)
	if err := ExtractZip(zipPath, dest, ""); err == nil {
		t.Fatal("expected zip-slip entry to be rejected")
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s content = %q, want %q", path, got, want)
	}
}
