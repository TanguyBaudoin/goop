// Package manifest decodes Scoop-format app manifests (CPT-01, CPT-02).
// Decoding is intentionally permissive about fields no milestone acts on
// yet (checkver, autoupdate, suggest, notes, ...): they're preserved as
// raw JSON rather than rejected, so a manifest goop can't fully act on
// yet still decodes cleanly instead of erroring at the parse stage.
package manifest

import (
	"encoding/json"
	"fmt"
)

// Manifest is a decoded Scoop app manifest, before architecture
// resolution.
type Manifest struct {
	Version string
	Bin     BinList

	// Description/Homepage/LicenseIdentifier/LicenseURL are purely
	// informational -- shown by `goop info`, not acted on. License is
	// either a plain string (an SPDX identifier, a URL, or a `|`/`,`
	// separated list of identifiers -- LicenseIdentifier covers all
	// three, LicenseURL stays empty) or {identifier, url} (both set),
	// same "anyOf" shape real Scoop's own schema.json defines and
	// scoop-info.ps1 displays.
	Description       string
	Homepage          string
	LicenseIdentifier string
	LicenseURL        string

	// Base download/extraction fields. May be empty if only set per-arch.
	URL  URLList
	Hash HashList
	// ExtractDir/ExtractTo are usually a single value applying to every
	// downloaded URL, but a manifest with multiple `url` entries may give
	// one value per URL instead (e.g. unxutils.json's extract_to).
	ExtractDir StringList
	ExtractTo  StringList

	// Depends lists other manifest names this one requires. Dependency
	// resolution itself is A4, targeted at J3; it's parsed here so
	// callers can at least report "this needs X first".
	Depends StringList

	// InnoSetup marks a downloaded .exe as an Inno Setup installer, which
	// goop extracts (never runs) via Expand-InnoArchive -- real Scoop
	// only dispatches a .exe through Inno extraction when this is set,
	// same as here; without it a .exe is left as-is (e.g. for an
	// installer.script to run directly). Top-level only, not
	// architecture-overridable -- no real manifest in the corpus sets it
	// differently per architecture.
	InnoSetup bool

	// PreInstall/PostInstall/Installer/Uninstaller/PreUninstall/
	// PostUninstall are PowerShell, delegated to pwsh, never
	// reinterpreted (CPT-04).
	PreInstall    string
	PostInstall   string
	Installer     InstallHook
	Uninstaller   InstallHook
	PreUninstall  string
	PostUninstall string

	Persist    []PersistEntry
	Shortcuts  []ShortcutEntry
	EnvSet     map[string]string
	EnvAddPath StringList

	// Suggest is a manifest's `suggest` field: {featureName: [alt1,
	// alt2, ...]}, each value a stringOrArrayOfStrings same as
	// EnvAddPath -- companion apps worth mentioning but not worth
	// forcing as a hard `depends`. Genuinely common (687 real manifests)
	// and, before this, silently dropped -- goop's own install output
	// never told the user about it at all, unlike real Scoop's own
	// show_notes-adjacent show_suggestions (lib/install.ps1), confirmed
	// firing for real (e.g. "'ripgrep' suggests installing
	// 'extras/vcredist2022'.") during this session's own benchmark runs
	// against real Scoop. Top-level only -- no real manifest overrides
	// it per architecture.
	Suggest map[string]StringList

	// PSModuleName is a manifest's `psmodule.name` -- present on 31 real
	// manifests in the corpus (e.g. main/psreadline.json), all of which
	// only ever set the one field real Scoop's own install_psmodule/
	// uninstall_psmodule (lib/psmodules.ps1) actually reads. Empty means
	// absent. Top-level only, like InnoSetup -- a per-architecture
	// PowerShell module name wouldn't make sense (it's not a binary).
	PSModuleName string

	// Notes is free-text shown after a successful install (real Scoop's
	// show_notes, lib/install.ps1) -- often the only place a manifest
	// documents a manual step it can't safely automate itself (e.g.
	// extras/vscode.json's contTR-menu/file-association `reg import`
	// commands, which touch the registry and so are left opt-in rather
	// than run automatically). A string or an array of lines in the raw
	// manifest, same StringList shape as EnvAddPath; joined with "\n"
	// for display. Top-level only, like InnoSetup -- no real manifest in
	// the corpus overrides it per architecture.
	Notes StringList

	Architecture map[string]ArchOverride

	Extra map[string]json.RawMessage
}

// ArchOverride holds the fields a manifest may override under
// architecture.<64bit|32bit|arm64> (CPT-02). A zero-value field means
// "inherit the base manifest's value"; PreInstall/PostInstall/Installer/
// Uninstaller can each be overridden independently, as real manifests do
// (e.g. 7-Zip's arm64 build entirely replaces pre_install).
type ArchOverride struct {
	URL        URLList
	Hash       HashList
	Bin        BinList
	ExtractDir StringList
	ExtractTo  StringList
	// Shortcuts overriding the base manifest's -- real Scoop's own
	// schema.json allows this (definitions.architecture.properties.
	// shortcuts) and it's genuinely common in practice: confirmed 253
	// real manifests set shortcuts only under architecture.<arch>, no
	// top-level shortcuts at all, including every JetBrains IDE
	// (webstorm.json, idea.json, pycharm.json, ...) -- an app installed
	// with this field silently unhandled ends up with no Start Menu
	// shortcut whatsoever, not a degraded one.
	Shortcuts []ShortcutEntry

	PreInstall    string
	PostInstall   string
	Installer     InstallHook
	Uninstaller   InstallHook
	PreUninstall  string
	PostUninstall string
}

// InstallHook is a manifest `installer`/`uninstaller` object: either a
// Script to run, or a File (relative to the app dir) to execute directly
// with Args, after extraction.
type InstallHook struct {
	Script string   `json:"script,omitempty"`
	File   string   `json:"file,omitempty"`
	Args   []string `json:"args,omitempty"`
}

func (h InstallHook) IsZero() bool {
	return h.Script == "" && h.File == "" && len(h.Args) == 0
}

// PersistEntry is one manifest `persist` entry: Source is the path
// (relative to the app dir) that should survive across versions; Target
// is where it lives in the stable persist store, which defaults to
// Source unless the manifest gives a [source, target] rename pair.
type PersistEntry struct {
	Source string
	Target string
}

// ShortcutEntry is one Start Menu shortcut: Exe (relative to the app
// dir) and Name (its label, possibly nested via "Folder\Name"), with
// optional Args and Icon.
type ShortcutEntry struct {
	Exe  string `json:"exe"`
	Name string `json:"name"`
	Args string `json:"args,omitempty"`
	Icon string `json:"icon,omitempty"`
}

type rawManifest struct {
	Version       string                     `json:"version"`
	Description   string                     `json:"description"`
	Homepage      string                     `json:"homepage"`
	License       json.RawMessage            `json:"license"`
	Bin           json.RawMessage            `json:"bin"`
	URL           json.RawMessage            `json:"url"`
	Hash          json.RawMessage            `json:"hash"`
	ExtractDir    json.RawMessage            `json:"extract_dir"`
	ExtractTo     json.RawMessage            `json:"extract_to"`
	Depends       json.RawMessage            `json:"depends"`
	InnoSetup     bool                       `json:"innosetup"`
	PreInstall    json.RawMessage            `json:"pre_install"`
	PostInstall   json.RawMessage            `json:"post_install"`
	Installer     json.RawMessage            `json:"installer"`
	Uninstaller   json.RawMessage            `json:"uninstaller"`
	PreUninstall  json.RawMessage            `json:"pre_uninstall"`
	PostUninstall json.RawMessage            `json:"post_uninstall"`
	Persist       json.RawMessage            `json:"persist"`
	Shortcuts     json.RawMessage            `json:"shortcuts"`
	EnvSet        map[string]string          `json:"env_set"`
	EnvAddPath    json.RawMessage            `json:"env_add_path"`
	Notes         json.RawMessage            `json:"notes"`
	Suggest       map[string]json.RawMessage `json:"suggest"`
	PSModule      *struct {
		Name string `json:"name"`
	} `json:"psmodule"`
	Architecture map[string]rawArchOverride `json:"architecture"`
}

type rawArchOverride struct {
	URL           json.RawMessage `json:"url"`
	Hash          json.RawMessage `json:"hash"`
	Bin           json.RawMessage `json:"bin"`
	ExtractDir    json.RawMessage `json:"extract_dir"`
	ExtractTo     json.RawMessage `json:"extract_to"`
	PreInstall    json.RawMessage `json:"pre_install"`
	PostInstall   json.RawMessage `json:"post_install"`
	Installer     json.RawMessage `json:"installer"`
	Uninstaller   json.RawMessage `json:"uninstaller"`
	PreUninstall  json.RawMessage `json:"pre_uninstall"`
	PostUninstall json.RawMessage `json:"post_uninstall"`
	Shortcuts     json.RawMessage `json:"shortcuts"`
}

// Decode parses raw Scoop manifest JSON.
func Decode(data []byte) (Manifest, error) {
	var raw rawManifest
	if err := json.Unmarshal(data, &raw); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}

	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	for _, known := range []string{
		"version", "bin", "url", "hash", "extract_dir", "extract_to",
		"depends", "architecture", "pre_install", "post_install",
		"installer", "uninstaller", "pre_uninstall", "post_uninstall",
		"persist", "shortcuts", "env_set", "env_add_path", "innosetup", "notes", "psmodule", "suggest",
		"description", "homepage", "license",
	} {
		delete(all, known)
	}

	m := Manifest{
		Version:      raw.Version,
		Description:  raw.Description,
		Homepage:     raw.Homepage,
		EnvSet:       raw.EnvSet,
		InnoSetup:    raw.InnoSetup,
		Extra:        all,
		Architecture: map[string]ArchOverride{},
	}
	var licenseErr error
	m.LicenseIdentifier, m.LicenseURL, licenseErr = decodeLicense(raw.License)
	if licenseErr != nil {
		return Manifest{}, fmt.Errorf("field license: %w", licenseErr)
	}
	if raw.PSModule != nil {
		if raw.PSModule.Name == "" {
			return Manifest{}, fmt.Errorf("field psmodule: the 'name' property is missing")
		}
		m.PSModuleName = raw.PSModule.Name
	}
	if len(raw.Suggest) > 0 {
		m.Suggest = make(map[string]StringList, len(raw.Suggest))
		for feature, rawVal := range raw.Suggest {
			vals, err := decodeStringList(rawVal)
			if err != nil {
				return Manifest{}, fmt.Errorf("field suggest.%s: %w", feature, err)
			}
			m.Suggest[feature] = vals
		}
	}

	var err error
	if m.Bin, err = decodeBinList(raw.Bin); err != nil {
		return Manifest{}, fmt.Errorf("field bin: %w", err)
	}
	if m.URL, err = decodeURLList(raw.URL); err != nil {
		return Manifest{}, fmt.Errorf("field url: %w", err)
	}
	if m.Hash, err = decodeHashList(raw.Hash); err != nil {
		return Manifest{}, fmt.Errorf("field hash: %w", err)
	}
	if m.ExtractDir, err = decodeStringList(raw.ExtractDir); err != nil {
		return Manifest{}, fmt.Errorf("field extract_dir: %w", err)
	}
	if m.ExtractTo, err = decodeStringList(raw.ExtractTo); err != nil {
		return Manifest{}, fmt.Errorf("field extract_to: %w", err)
	}
	if m.Depends, err = decodeStringList(raw.Depends); err != nil {
		return Manifest{}, fmt.Errorf("field depends: %w", err)
	}
	if m.PreInstall, err = decodeScript(raw.PreInstall); err != nil {
		return Manifest{}, fmt.Errorf("field pre_install: %w", err)
	}
	if m.PostInstall, err = decodeScript(raw.PostInstall); err != nil {
		return Manifest{}, fmt.Errorf("field post_install: %w", err)
	}
	if m.Installer, err = decodeInstallHook(raw.Installer); err != nil {
		return Manifest{}, fmt.Errorf("field installer: %w", err)
	}
	if m.Uninstaller, err = decodeInstallHook(raw.Uninstaller); err != nil {
		return Manifest{}, fmt.Errorf("field uninstaller: %w", err)
	}
	if m.PreUninstall, err = decodeScript(raw.PreUninstall); err != nil {
		return Manifest{}, fmt.Errorf("field pre_uninstall: %w", err)
	}
	if m.PostUninstall, err = decodeScript(raw.PostUninstall); err != nil {
		return Manifest{}, fmt.Errorf("field post_uninstall: %w", err)
	}
	if m.Persist, err = decodePersist(raw.Persist); err != nil {
		return Manifest{}, fmt.Errorf("field persist: %w", err)
	}
	if m.Shortcuts, err = decodeShortcuts(raw.Shortcuts); err != nil {
		return Manifest{}, fmt.Errorf("field shortcuts: %w", err)
	}
	if m.Notes, err = decodeStringList(raw.Notes); err != nil {
		return Manifest{}, fmt.Errorf("field notes: %w", err)
	}
	if m.EnvAddPath, err = decodeStringList(raw.EnvAddPath); err != nil {
		return Manifest{}, fmt.Errorf("field env_add_path: %w", err)
	}

	for key, rao := range raw.Architecture {
		var ov ArchOverride
		if ov.URL, err = decodeURLList(rao.URL); err != nil {
			return Manifest{}, fmt.Errorf("field architecture.%s.url: %w", key, err)
		}
		if ov.Hash, err = decodeHashList(rao.Hash); err != nil {
			return Manifest{}, fmt.Errorf("field architecture.%s.hash: %w", key, err)
		}
		if ov.Bin, err = decodeBinList(rao.Bin); err != nil {
			return Manifest{}, fmt.Errorf("field architecture.%s.bin: %w", key, err)
		}
		if ov.ExtractDir, err = decodeStringList(rao.ExtractDir); err != nil {
			return Manifest{}, fmt.Errorf("field architecture.%s.extract_dir: %w", key, err)
		}
		if ov.ExtractTo, err = decodeStringList(rao.ExtractTo); err != nil {
			return Manifest{}, fmt.Errorf("field architecture.%s.extract_to: %w", key, err)
		}
		if ov.PreInstall, err = decodeScript(rao.PreInstall); err != nil {
			return Manifest{}, fmt.Errorf("field architecture.%s.pre_install: %w", key, err)
		}
		if ov.PostInstall, err = decodeScript(rao.PostInstall); err != nil {
			return Manifest{}, fmt.Errorf("field architecture.%s.post_install: %w", key, err)
		}
		if ov.Installer, err = decodeInstallHook(rao.Installer); err != nil {
			return Manifest{}, fmt.Errorf("field architecture.%s.installer: %w", key, err)
		}
		if ov.Uninstaller, err = decodeInstallHook(rao.Uninstaller); err != nil {
			return Manifest{}, fmt.Errorf("field architecture.%s.uninstaller: %w", key, err)
		}
		if ov.PreUninstall, err = decodeScript(rao.PreUninstall); err != nil {
			return Manifest{}, fmt.Errorf("field architecture.%s.pre_uninstall: %w", key, err)
		}
		if ov.PostUninstall, err = decodeScript(rao.PostUninstall); err != nil {
			return Manifest{}, fmt.Errorf("field architecture.%s.post_uninstall: %w", key, err)
		}
		if ov.Shortcuts, err = decodeShortcuts(rao.Shortcuts); err != nil {
			return Manifest{}, fmt.Errorf("field architecture.%s.shortcuts: %w", key, err)
		}
		m.Architecture[key] = ov
	}

	return m, nil
}
