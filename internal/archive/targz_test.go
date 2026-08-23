package archive

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeTestTarGz(t *testing.T, entries map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
	return path
}

func TestExtractTarGz_Flat(t *testing.T) {
	path := writeTestTarGz(t, map[string]string{
		"tool":         "binary-content",
		"sub/note.txt": "nested",
	})
	dest := t.TempDir()
	if err := ExtractTarGz(path, dest, ""); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(dest, "tool"), "binary-content")
	assertFileContent(t, filepath.Join(dest, "sub", "note.txt"), "nested")
}

func TestExtractTarGz_StripDir(t *testing.T) {
	path := writeTestTarGz(t, map[string]string{
		"mytool-1.0.0-linux/tool": "binary",
	})
	dest := t.TempDir()
	if err := ExtractTarGz(path, dest, "mytool-1.0.0-linux"); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(dest, "tool"), "binary")
}
