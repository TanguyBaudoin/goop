package installer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/TanguyBaudoin/goop/internal/envvars"
	"github.com/TanguyBaudoin/goop/internal/paths"
)

// linkPSModule wires a manifest's `psmodule.name` into goop's own
// PowerShell modules directory (paths.Modules(), goop's equivalent of
// real Scoop's modulesdir) as a junction pointing at versionDir, and
// makes sure that directory is on PSModulePath -- so `Import-Module
// <name>` finds it without the user needing to know goop installed it
// there. Mirrors real Scoop's own install_psmodule (lib/psmodules.ps1).
func linkPSModule(appName, moduleName, versionDir string) error {
	modulesDir := paths.Modules()
	if err := os.MkdirAll(modulesDir, 0o755); err != nil {
		return err
	}
	if err := envvars.AddToPSModulePath(modulesDir); err != nil {
		return fmt.Errorf("add %s to PSModulePath: %w", modulesDir, err)
	}

	linkFrom := filepath.Join(modulesDir, moduleName)
	if _, err := os.Lstat(linkFrom); err == nil {
		// Real Scoop warns and replaces here too -- a stale link from a
		// previous install of this (or another) app claiming the same
		// module name shouldn't block a fresh one.
		if out, err := exec.Command("cmd", "/c", "rmdir", linkFrom).CombinedOutput(); err != nil {
			return fmt.Errorf("replace existing module link %s: %w\n%s", linkFrom, err, out)
		}
	}

	Logf("%s: linking PowerShell module %s", appName, moduleName)
	out, err := exec.Command("cmd", "/c", "mklink", "/J", linkFrom, versionDir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("link module %s: %w\n%s", moduleName, err, out)
	}
	return nil
}

// unlinkPSModule removes moduleName's junction from paths.Modules(),
// if present -- mirrors real Scoop's uninstall_psmodule. Uses `rmdir`
// (not RemoveAll) since this is a junction: the app's own files live
// under versionDir and get removed separately by the normal uninstall
// path, not through this link.
func unlinkPSModule(appName, moduleName string) {
	linkFrom := filepath.Join(paths.Modules(), moduleName)
	if _, err := os.Lstat(linkFrom); err != nil {
		return
	}
	Logf("%s: unlinking PowerShell module %s", appName, moduleName)
	if out, err := exec.Command("cmd", "/c", "rmdir", linkFrom).CombinedOutput(); err != nil {
		Logf("%s: psmodule: remove %s: %v\n%s", appName, linkFrom, err, out) // non-fatal, same tier as shims/shortcuts
	}
}
