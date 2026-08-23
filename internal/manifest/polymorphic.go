package manifest

import (
	"encoding/json"
	"fmt"
	"strings"
)

// URLList is a manifest `url` field: absent, a single string, or an
// array of strings (CPT-02).
type URLList []string

// HashList is a manifest `hash` field, aligned by index with a URLList.
type HashList []string

// StringList is a manifest field that's either a single string or an
// array of strings (used for `depends`).
type StringList []string

// BinEntry is one shim to create: Exe is the path (relative to the
// installed app root) of the file to run; Name is the shim/command name
// (defaults to Exe's basename without extension); Args, if set, are
// baked into the shim as default arguments.
type BinEntry struct {
	Exe  string `json:"exe"`
	Name string `json:"name"`
	Args string `json:"args,omitempty"`
}

// BinList is a manifest `bin` field: absent, a single exe string, or an
// array whose elements are each either an exe string or a 1-3 element
// [exe, name, args] tuple (CPT-02).
type BinList []BinEntry

func decodeStringOrArray(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}, nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	return nil, fmt.Errorf("expected string or array of strings, got %s", raw)
}

func decodeURLList(raw json.RawMessage) (URLList, error) {
	s, err := decodeStringOrArray(raw)
	if err != nil {
		return nil, err
	}
	return URLList(s), nil
}

func decodeHashList(raw json.RawMessage) (HashList, error) {
	s, err := decodeStringOrArray(raw)
	if err != nil {
		return nil, err
	}
	return HashList(s), nil
}

func decodeStringList(raw json.RawMessage) (StringList, error) {
	s, err := decodeStringOrArray(raw)
	if err != nil {
		return nil, err
	}
	return StringList(s), nil
}

// decodeLicense handles the manifest `license` field's two real shapes
// (schema.json's "anyOf"): a plain string -- an SPDX identifier, a
// bare URL, or a `|`/`,` separated list of identifiers, all of which
// just pass through as identifier with url left empty -- or an object
// {identifier, url}.
func decodeLicense(raw json.RawMessage) (identifier, url string, err error) {
	if len(raw) == 0 {
		return "", "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, "", nil
	}
	var obj struct {
		Identifier string `json:"identifier"`
		URL        string `json:"url"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", "", fmt.Errorf("expected string or {identifier, url}, got %s", raw)
	}
	return obj.Identifier, obj.URL, nil
}

func decodeBinList(raw json.RawMessage) (BinList, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	// Single exe string.
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return BinList{binEntryFromExe(single, "")}, nil
	}

	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, fmt.Errorf("expected string, array of strings, or array of tuples, got %s", raw)
	}

	var out BinList
	for i, elem := range elems {
		var exe string
		if err := json.Unmarshal(elem, &exe); err == nil {
			out = append(out, binEntryFromExe(exe, ""))
			continue
		}
		var tuple []string
		if err := json.Unmarshal(elem, &tuple); err != nil {
			return nil, fmt.Errorf("bin[%d]: expected string or array, got %s", i, elem)
		}
		if len(tuple) == 0 {
			return nil, fmt.Errorf("bin[%d]: expected at least [exe], got empty array", i)
		}
		entry := binEntryFromExe(tuple[0], "")
		if len(tuple) >= 2 && tuple[1] != "" {
			entry.Name = tuple[1]
		}
		// Elements beyond [exe, name] are separate default CLI args baked
		// into the shim, e.g. ["zigup.exe", "zigup", "--path-link", "x"].
		if len(tuple) >= 3 {
			entry.Args = strings.Join(tuple[2:], " ")
		}
		out = append(out, entry)
	}
	return out, nil
}

func binEntryFromExe(exe, name string) BinEntry {
	if name == "" {
		name = baseNameNoExt(exe)
	}
	return BinEntry{Exe: exe, Name: name}
}

// decodeScript decodes a manifest script field (`pre_install`,
// `post_install`, or an install-hook's `script`): absent, a single
// string (possibly itself multi-line), or an array of lines joined with
// "\n".
func decodeScript(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var lines []string
	if err := json.Unmarshal(raw, &lines); err != nil {
		return "", fmt.Errorf("expected string or array of strings, got %s", raw)
	}
	return strings.Join(lines, "\n"), nil
}

type rawInstallHook struct {
	Script json.RawMessage `json:"script"`
	File   string          `json:"file"`
	Args   json.RawMessage `json:"args"`
}

// decodeInstallHook decodes a manifest `installer`/`uninstaller` field.
func decodeInstallHook(raw json.RawMessage) (InstallHook, error) {
	if len(raw) == 0 {
		return InstallHook{}, nil
	}
	var rh rawInstallHook
	if err := json.Unmarshal(raw, &rh); err != nil {
		return InstallHook{}, fmt.Errorf("expected object, got %s", raw)
	}
	script, err := decodeScript(rh.Script)
	if err != nil {
		return InstallHook{}, fmt.Errorf("script: %w", err)
	}
	args, err := decodeStringOrArray(rh.Args)
	if err != nil {
		return InstallHook{}, fmt.Errorf("args: %w", err)
	}
	return InstallHook{Script: script, File: rh.File, Args: args}, nil
}

// decodePersist decodes a manifest `persist` field: absent, a single
// path string, or an array whose elements are each either a path string
// or a [source, target] rename pair.
func decodePersist(raw json.RawMessage) ([]PersistEntry, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []PersistEntry{{Source: single, Target: single}}, nil
	}

	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, fmt.Errorf("expected string or array, got %s", raw)
	}
	var out []PersistEntry
	for i, elem := range elems {
		var s string
		if err := json.Unmarshal(elem, &s); err == nil {
			out = append(out, PersistEntry{Source: s, Target: s})
			continue
		}
		var pair []string
		if err := json.Unmarshal(elem, &pair); err != nil || len(pair) != 2 {
			return nil, fmt.Errorf("persist[%d]: expected a path string or [source, target] pair, got %s", i, elem)
		}
		out = append(out, PersistEntry{Source: pair[0], Target: pair[1]})
	}
	return out, nil
}

// decodeShortcuts decodes a manifest `shortcuts` field: an array whose
// elements are each a [exe, name, args?, icon?] tuple (1-4 elements).
func decodeShortcuts(raw json.RawMessage) ([]ShortcutEntry, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var elems [][]string
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, fmt.Errorf("expected array of arrays, got %s", raw)
	}
	var out []ShortcutEntry
	for i, tuple := range elems {
		if len(tuple) < 2 {
			return nil, fmt.Errorf("shortcuts[%d]: expected at least [exe, name], got %d elements", i, len(tuple))
		}
		entry := ShortcutEntry{Exe: tuple[0], Name: tuple[1]}
		if len(tuple) >= 3 {
			entry.Args = tuple[2]
		}
		if len(tuple) >= 4 {
			entry.Icon = tuple[3]
		}
		out = append(out, entry)
	}
	return out, nil
}

func baseNameNoExt(p string) string {
	base := p
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '\\' || base[i] == '/' {
			base = base[i+1:]
			break
		}
	}
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '.' {
			return base[:i]
		}
	}
	return base
}
