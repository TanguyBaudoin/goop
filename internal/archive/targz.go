package archive

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ExtractTarGz extracts a gzip-compressed tar archive into destDir, with
// the same extract_dir semantics as ExtractZip.
func ExtractTarGz(src, destDir, stripDir string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer gz.Close()

	return extractTar(tar.NewReader(gz), src, destDir, stripDir)
}

// ExtractTar extracts an uncompressed tar archive into destDir.
func ExtractTar(src, destDir, stripDir string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer f.Close()
	return extractTar(tar.NewReader(f), src, destDir, stripDir)
}

func extractTar(tr *tar.Reader, src, destDir, stripDir string) error {
	prefix := normalizeZipPath(stripDir)
	if prefix != "" {
		prefix += "/"
	}

	wroteAny := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar %s: %w", src, err)
		}

		name := normalizeZipPath(hdr.Name)
		if name == "" {
			continue
		}
		if prefix != "" {
			rest, ok := trimDirPrefix(name, prefix)
			if !ok {
				continue
			}
			name = rest
			if name == "" {
				continue
			}
		}

		target, err := safeJoin(destDir, name)
		if err != nil {
			return fmt.Errorf("tar %s: %w", src, err)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode)
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			out.Close()
			if copyErr != nil {
				return fmt.Errorf("tar %s: extract %s: %w", src, hdr.Name, copyErr)
			}
			wroteAny = true
		default:
			// symlinks, devices, etc.: skip, not relevant to app payloads
		}
	}

	if prefix != "" && !wroteAny {
		return fmt.Errorf("tar %s: extract_dir %q matched no entries", src, stripDir)
	}
	return nil
}

// ExtractGzip decompresses a single-file gzip (not a tar) to destPath.
func ExtractGzip(src, destPath string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer gz.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, gz)
	return err
}
