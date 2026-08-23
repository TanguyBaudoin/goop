package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/paths"
	"github.com/TanguyBaudoin/goop/internal/pwsh"
)

// createShortcuts materializes Start Menu shortcuts (CPT-03), namespaced
// under goop's own Start Menu folder so uninstall can remove exactly
// what it created without touching anything else. Targets point through
// the `current` junction, same as shims.
func createShortcuts(appName string, shortcuts []manifest.ShortcutEntry) error {
	if len(shortcuts) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("$wsh = New-Object -ComObject WScript.Shell\n")
	any := false
	for _, s := range shortcuts {
		// Same absolute-vs-relative split as createShims: a manifest
		// `shortcuts` entry is relative to the app dir, but a rebuilt
		// script-created shortcut (Reset) carries an absolute target.
		target := filepath.FromSlash(s.Exe)
		if !filepath.IsAbs(target) {
			target = filepath.Join(paths.AppCurrent(appName), target)
		}
		// Real Scoop's own startmenu_shortcut (lib/shortcuts.ps1) checks
		// $target.Exists/$icon.Exists first and skips with a warning
		// rather than creating a shortcut that points nowhere -- without
		// this, a wrong `exe`/`icon` path in a manifest (or extraction
		// landing differently than expected) produces a silently-broken
		// .lnk with no warning at all, since WScript.Shell's
		// CreateShortcut doesn't validate its target.
		if _, err := os.Stat(target); err != nil {
			Logf("%s: shortcut %q: target %s not found, skipping", appName, s.Name, target)
			continue
		}
		var iconPath string
		if s.Icon != "" {
			iconPath = filepath.Join(paths.AppCurrent(appName), filepath.FromSlash(s.Icon))
			if _, err := os.Stat(iconPath); err != nil {
				Logf("%s: shortcut %q: icon %s not found, skipping", appName, s.Name, iconPath)
				continue
			}
		}

		linkPath := shortcutPath(s.Name)
		if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
			return err
		}
		any = true
		fmt.Fprintf(&b, "$lnk = $wsh.CreateShortcut(%s)\n", psArg(linkPath))
		fmt.Fprintf(&b, "$lnk.TargetPath = %s\n", psArg(target))
		if s.Args != "" {
			fmt.Fprintf(&b, "$lnk.Arguments = %s\n", psArg(s.Args))
		}
		fmt.Fprintf(&b, "$lnk.WorkingDirectory = %s\n", psArg(filepath.Dir(target)))
		if iconPath != "" {
			fmt.Fprintf(&b, "$lnk.IconLocation = %s\n", psArg(iconPath))
		}
		b.WriteString("$lnk.Save()\n")
	}
	if !any {
		return nil
	}

	_, err := pwsh.Run(b.String(), pwsh.Vars{})
	return err
}

// shortcutPath turns a manifest shortcut Name (possibly "Folder\Label",
// nesting it under a Start Menu subfolder) into the .lnk path, rejecting
// any ".."-style escape from goop's own Start Menu folder.
func shortcutPath(name string) string {
	parts := strings.Split(strings.ReplaceAll(name, "/", "\\"), "\\")
	var clean []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "." || p == ".." {
			continue
		}
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		clean = []string{"shortcut"}
	}
	return filepath.Join(paths.StartMenu(), filepath.Join(clean...)+".lnk")
}
