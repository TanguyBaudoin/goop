package installer

import (
	"os"
	"path/filepath"
	"strings"

	"goop/internal/paths"
	"goop/internal/pwsh"
)

// repairStagingPaths rewrites any reference to the staging directory
// that a manifest hook persisted, pointing it at the committed
// versionDir instead. Called right after the commit rename.
//
// Hooks run before that rename (TR-04: a failing hook must leave no
// trace), so every path they see ends in "<version>.partial". Anything
// a script merely reads is fine; anything it *persists* outlives the
// rename and points at a directory that no longer exists. The prelude's
// own shim/startmenu_shortcut already normalize what they write, but
// manifests are third-party code that can persist a path any way it
// likes -- this is the backstop for the cases that normalization
// doesn't cover, so a future one degrades into a silent no-op here
// rather than a broken shim or a blank-icon shortcut.
//
// Everything is best-effort: a repair failure must never fail an
// install that otherwise succeeded.
func repairStagingPaths(appName, staging, versionDir string) {
	if staging == "" || staging == versionDir {
		return
	}
	repaired := 0

	// Shim sidecars are plain text holding `path = "..."`.
	if entries, err := os.ReadDir(paths.Shims()); err == nil {
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".shim" {
				continue
			}
			p := filepath.Join(paths.Shims(), e.Name())
			data, err := os.ReadFile(p)
			if err != nil || !strings.Contains(string(data), staging) {
				continue
			}
			fixed := strings.ReplaceAll(string(data), staging, versionDir)
			if os.WriteFile(p, []byte(fixed), 0o644) == nil {
				repaired++
			}
		}
	}

	// Shortcuts are binary .lnk files, so they're rewritten through the
	// same COM API that created them rather than patched as text.
	lnks, _ := filepath.Glob(filepath.Join(paths.StartMenu(), "*.lnk"))
	nested, _ := filepath.Glob(filepath.Join(paths.StartMenu(), "*", "*.lnk"))
	var b strings.Builder
	for _, l := range append(lnks, nested...) {
		b.WriteString("$l = $wsh.CreateShortcut(" + psArg(l) + ")\n")
		b.WriteString("if ($l.TargetPath -like " + psArg("*"+staging+"*") + ") {\n")
		b.WriteString("  $l.TargetPath = $l.TargetPath.Replace(" + psArg(staging) + "," + psArg(versionDir) + ")\n")
		b.WriteString("  $l.WorkingDirectory = $l.WorkingDirectory.Replace(" + psArg(staging) + "," + psArg(versionDir) + ")\n")
		b.WriteString("  $l.Save(); Write-Output 'fixed'\n}\n")
	}
	if b.Len() > 0 {
		out, err := pwsh.Run("$wsh = New-Object -ComObject WScript.Shell\n"+b.String(), pwsh.Vars{})
		if err == nil {
			repaired += strings.Count(out, "fixed")
		}
	}

	if repaired > 0 {
		Logf("%s: repaired %d reference(s) to the staging directory left by a manifest script", appName, repaired)
	}
}
