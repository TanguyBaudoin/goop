// Package archive extracts downloaded manifest assets. J1 supports zip
// (archive/zip, no external dependency); the remaining CPT-05 formats
// (7z, MSI, InnoSetup, NSIS) are J2 scope, delegated to 7z.exe/dark.exe
// per EXT-06.
package archive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// IsUnsupportedCompression reports whether err is Go's archive/zip
// rejecting a compression method it doesn't implement (e.g. Deflate64,
// used by a minority of real-world zips) -- distinct from a corrupt
// archive or an I/O error, so a caller can fall back to delegating the
// whole extraction to 7z.exe instead, which handles those methods.
func IsUnsupportedCompression(err error) bool {
	return errors.Is(err, zip.ErrAlgorithm)
}

// ExtractZip extracts src into destDir. If stripDir is non-empty, only
// entries under that top-level directory are extracted, with the prefix
// removed -- this is extract_dir's "hoist a nested folder's contents up
// to the version root" behavior.
func ExtractZip(src, destDir, stripDir string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("open zip %s: %w", src, err)
	}
	defer r.Close()

	prefix := normalizeZipPath(stripDir)
	if prefix != "" {
		prefix += "/"
	}

	wroteAny := false
	for _, f := range r.File {
		name := normalizeZipPath(f.Name)
		if name == "" {
			continue // directory entry with no name after normalization
		}

		if prefix != "" {
			rest, ok := trimDirPrefix(name, prefix)
			if !ok {
				continue // outside the extract_dir we're hoisting
			}
			name = rest
			if name == "" {
				continue // the extract_dir entry itself
			}
		}

		target, err := safeJoin(destDir, name)
		if err != nil {
			return fmt.Errorf("zip %s: %w", src, err)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := extractZipFile(f, target); err != nil {
			return fmt.Errorf("zip %s: extract %s: %w", src, f.Name, err)
		}
		wroteAny = true
	}

	if prefix != "" && !wroteAny {
		return fmt.Errorf("zip %s: extract_dir %q matched no entries", src, stripDir)
	}
	return nil
}

func extractZipFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	mode := f.Mode()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}

// normalizeZipPath converts zip-internal separators to "/" and strips
// leading "/" so path handling stays platform-independent.
func normalizeZipPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.Trim(p, "/")
}

// trimDirPrefix reports whether name sits under prefix (an already
// normalized, "/"-terminated extract_dir) and returns name relative to
// it. Matching is case-insensitive: Windows filesystems are, so a
// manifest's extract_dir routinely differs in case from what the
// archive actually contains -- extras/bleachbit.json asks for
// "BleachBit-portable" while the real zip ships "BleachBit-Portable",
// and Scoop extracts it fine because it delegates to tools that ignore
// case. A byte-exact comparison here rejected the whole archive with
// "matched no entries".
//
// Only the prefix is compared loosely; the returned remainder keeps the
// archive's own casing, so extracted files land under their real names.
func trimDirPrefix(name, prefix string) (string, bool) {
	if prefix == "" {
		return name, true
	}
	withSlash := name + "/"
	if len(withSlash) < len(prefix) || !strings.EqualFold(withSlash[:len(prefix)], prefix) {
		return "", false
	}
	return name[min(len(prefix), len(name)):], true
}

// safeJoin joins name onto root and rejects any result that would escape
// root (zip-slip protection against a malicious ".."-crafted entry).
func safeJoin(root, name string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(name))
	rootClean := filepath.Clean(root) + string(os.PathSeparator)
	if !strings.HasPrefix(target+string(os.PathSeparator), rootClean) {
		return "", fmt.Errorf("entry %q escapes destination directory", name)
	}
	return target, nil
}
