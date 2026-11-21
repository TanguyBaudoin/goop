package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/pwsh"
	goopshim "github.com/TanguyBaudoin/goop/internal/shim"
)

// runInstallHook runs a manifest's `installer` or `uninstaller`.
//
// Faithful to Scoop's Invoke-Installer (lib/install.ps1), which this was
// checked against line by line after three separate defects turned up in
// the corpus:
//
//   - The hook runs when `file` OR `args` is present, not only `file`.
//     Omitting `file` is legal and means "the file that was downloaded";
//     19 manifests across main, extras, versions and nonportable rely on
//     it -- vulkan, latex, miktex among them -- and goop silently did
//     nothing for every one, then reported the install a success.
//   - `$dir`, `$version` and `$global` are substituted into the
//     arguments. 5 manifests were being run with the literal string
//     "$dir" as an argument, which is worse than skipping them: the
//     installer actually ran, with a wrong path.
//   - A target ending in .ps1 is invoked as PowerShell, not spawned as a
//     process. 4 manifests need it, including main's pshazz.
//
// Scoop's two safety checks are kept as well: the resolved target must
// lie inside the app directory, and it must exist. Both abort there, and
// refusing is right -- a manifest naming something outside its own
// directory is either broken or hostile.
func runInstallHook(appName, label string, hook manifest.InstallHook, dir string, vars pwsh.Vars) error {
	if hook.Script != "" {
		Logf("%s: running %s.script", appName, label)
		out, err := pwsh.Run(hook.Script, vars)
		if strings.TrimSpace(out) != "" {
			Logf("%s", strings.TrimRight(out, "\n"))
		}
		if err != nil {
			return fmt.Errorf("%s: %s.script: %w", appName, label, err)
		}
		return nil
	}

	if hook.File == "" && len(hook.Args) == 0 {
		return nil
	}

	name := hook.File
	if name == "" {
		// Scoop defaults to url_filename of the first URL; vars.Fname is
		// goop's name for exactly that.
		name = vars.Fname
	}
	if name == "" {
		return fmt.Errorf("%s: %s: has args but no file, and nothing was downloaded to default to", appName, label)
	}

	target := filepath.Join(dir, filepath.FromSlash(name))
	if !isInDir(dir, target) {
		return fmt.Errorf("%s: %s %s is outside the app directory", appName, label, target)
	}
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("%s: %s %s is missing", appName, label, target)
	}

	args := substituteHookArgs(hook.Args, dir, vars.Version)

	// A .ps1 is a script, not an executable: spawning it would hand the
	// path to whatever happens to be registered for the extension, which
	// is not PowerShell on every machine and is not Scoop's behaviour
	// anywhere.
	if strings.EqualFold(filepath.Ext(target), ".ps1") {
		Logf("%s: running %s %s (powershell)", appName, name, strings.Join(args, " "))
		var b strings.Builder
		b.WriteString("& " + psSingleQuote(target))
		for _, a := range args {
			b.WriteString(" " + psSingleQuote(a))
		}
		out, err := pwsh.Run(b.String(), vars)
		if strings.TrimSpace(out) != "" {
			Logf("%s", strings.TrimRight(out, "\n"))
		}
		if err != nil {
			return fmt.Errorf("%s: %s %s: %w", appName, label, name, err)
		}
		return nil
	}

	Logf("%s: running %s %s", appName, name, strings.Join(args, " "))
	cmdLine := goopshim.QuoteArg(target)
	for _, a := range args {
		cmdLine += " " + goopshim.QuoteArg(a)
	}
	exitCode, err := goopshim.Run(target, cmdLine)
	if err != nil {
		return fmt.Errorf("%s: %s: %w", appName, label, err)
	}
	if exitCode != 0 {
		return fmt.Errorf("%s: %s %s exited with code %d", appName, label, name, exitCode)
	}
	return nil
}

// substituteHookArgs expands the three variables Scoop expands in
// installer/uninstaller arguments.
//
// $global is always False: goop installs per-user only (NR-01), so there
// is no state in which it could be true. No manifest in the corpus
// actually uses it in arguments, but matching Scoop costs nothing and
// leaving it unexpanded would pass the literal text through.
//
// One pass, so a replacement that happens to contain "$dir" -- an install
// path with a dollar sign in it is legal on Windows -- is not rescanned.
func substituteHookArgs(args []string, dir, version string) []string {
	if len(args) == 0 {
		return nil
	}
	r := strings.NewReplacer(
		"$dir", dir,
		"$version", version,
		"$global", "False",
	)
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = r.Replace(a)
	}
	return out
}

// isInDir reports whether child is parent itself or sits beneath it,
// comparing the way Windows does.
//
// Scoop's is_in_dir, and the reason it exists: a manifest's `file` is a
// relative path, so "..\..\Windows\System32\something.exe" would
// otherwise resolve to a real program outside the app directory and be
// run with the manifest's arguments.
func isInDir(parent, child string) bool {
	p := strings.TrimRight(filepath.Clean(parent), `\/`)
	c := strings.TrimRight(filepath.Clean(child), `\/`)
	if strings.EqualFold(p, c) {
		return true
	}
	if !strings.HasSuffix(p, string(filepath.Separator)) {
		p += string(filepath.Separator)
	}
	return strings.HasPrefix(strings.ToLower(c), strings.ToLower(p))
}

// psSingleQuote wraps s as a PowerShell single-quoted literal, where the
// only escape is a doubled quote and nothing else expands.
func psSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
