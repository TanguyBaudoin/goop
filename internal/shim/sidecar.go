// Package shim implements the goop shim: a single native executable,
// hard-linked under many command names, that reads a sidecar file
// describing its real target and execs it with exact argument, exit-code,
// and stdio fidelity. See TR-20 through TR-26 in the project spec.
package shim

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Sidecar describes a shim's target, decoded from a Scoop-compatible
// .shim file: a small `key = "value"` grammar, one assignment per line.
type Sidecar struct {
	Path string // full path to the real target (.exe, .bat, .cmd, .ps1, .jar)
	Args string // optional default arguments, prepended before the caller's own
}

// ErrSidecarMissingPath is returned when a .shim file has no `path` key.
var ErrSidecarMissingPath = errors.New(`sidecar missing required "path" key`)

// SidecarPath returns the sidecar file path for a shim executable path,
// e.g. "C:\shims\git.exe" -> "C:\shims\git.shim".
func SidecarPath(shimExePath string) string {
	base := shimExePath
	for i := len(base) - 1; i >= 0; i-- {
		c := base[i]
		if c == '\\' || c == '/' {
			break
		}
		if c == '.' {
			base = base[:i]
			break
		}
	}
	return base + ".shim"
}

// LoadSidecar reads and decodes the .shim file at path.
func LoadSidecar(path string) (Sidecar, error) {
	f, err := os.Open(path)
	if err != nil {
		return Sidecar{}, fmt.Errorf("open sidecar: %w", err)
	}
	defer f.Close()
	sc, err := ParseSidecar(f)
	if err != nil {
		return Sidecar{}, fmt.Errorf("parse sidecar %s: %w", path, err)
	}
	return sc, nil
}

// ParseSidecar decodes a .shim file body. Unknown keys are ignored for
// forward compatibility with newer Scoop shim fields.
func ParseSidecar(r io.Reader) (Sidecar, error) {
	var sc Sidecar
	havePath := false

	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if lineNo == 1 {
			// A sidecar written by PowerShell 5.1's `Set-Content
			// -Encoding utf8` carries a UTF-8 BOM, which would otherwise
			// glue itself to the first key and make a perfectly valid
			// file read as `missing required "path" key`. goop writes
			// them BOM-less now, but sidecars already on disk from
			// earlier installs keep working.
			line = strings.TrimPrefix(line, "\uFEFF")
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := splitAssignment(line)
		if !ok {
			return Sidecar{}, fmt.Errorf("line %d: expected `key = value`, got %q", lineNo, line)
		}
		switch key {
		case "path":
			sc.Path = value
			havePath = true
		case "args":
			sc.Args = value
		}
	}
	if err := scanner.Err(); err != nil {
		return Sidecar{}, err
	}
	if !havePath || sc.Path == "" {
		return Sidecar{}, ErrSidecarMissingPath
	}
	return sc, nil
}

func splitAssignment(line string) (key, value string, ok bool) {
	i := strings.IndexByte(line, '=')
	if i < 0 {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(line[:i]))
	value = unquote(strings.TrimSpace(line[i+1:]))
	return key, value, true
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
