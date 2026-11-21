package installer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/pwsh"
)

// Scoop substitutes $dir, $version and $global into installer arguments.
// goop did not, and 5 manifests in the real corpus were consequently run
// with the literal string "$dir" as an argument -- worse than being
// skipped, because the installer did run, against a path that does not
// exist.
func TestSubstituteHookArgs(t *testing.T) {
	dir := `C:\goop\apps\vulkan\1.4.321.0`
	got := substituteHookArgs([]string{"/S", `/D=$dir`, "--v=$version", "$global"}, dir, "1.4.321.0")
	want := []string{"/S", `/D=C:\goop\apps\vulkan\1.4.321.0`, "--v=1.4.321.0", "False"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, got[i], want[i])
		}
	}
	if substituteHookArgs(nil, dir, "1") != nil {
		t.Error("no arguments must stay no arguments, not an empty slice")
	}
}

// A single pass, so a replacement value containing "$dir" is not itself
// rescanned. An install path with a dollar sign in it is legal on
// Windows, and rescanning would corrupt it.
func TestSubstituteHookArgs_DoesNotRescanReplacements(t *testing.T) {
	dir := `C:\weird\$dir\apps\x`
	got := substituteHookArgs([]string{`/D=$dir`}, dir, "1")
	if want := `/D=` + dir; got[0] != want {
		t.Errorf("got %q, want %q -- the replacement must not be substituted again", got[0], want)
	}
}

// isInDir is Scoop's is_in_dir, and it is a security check rather than a
// tidiness one: `file` is a relative path, so without it a manifest
// could name "..\..\Windows\System32\..." and have goop run a real
// program with the manifest's own arguments.
func TestIsInDir(t *testing.T) {
	parent := `C:\goop\apps\thing\1.0`
	cases := []struct {
		child string
		want  bool
	}{
		{`C:\goop\apps\thing\1.0\setup.exe`, true},
		{`C:\goop\apps\thing\1.0\bin\setup.exe`, true},
		{`C:\goop\apps\thing\1.0`, true},  // the directory itself
		{`C:\goop\apps\thing\1.0\`, true}, // trailing separator
		{`c:\GOOP\apps\thing\1.0\setup.exe`, true},
		{`C:\goop\apps\thing\1.0-other\setup.exe`, false}, // prefix, not child
		{`C:\goop\apps\thing`, false},
		{`C:\Windows\System32\cmd.exe`, false},
		{filepath.Join(parent, `..\..\..\..\Windows\System32\cmd.exe`), false},
	}
	for _, tc := range cases {
		if got := isInDir(parent, tc.child); got != tc.want {
			t.Errorf("isInDir(%q, %q) = %v, want %v", parent, tc.child, got, tc.want)
		}
	}
}

// The traversal isInDir exists to stop, exercised through the hook
// itself rather than only against the helper.
func TestRunInstallHook_RefusesATargetOutsideTheAppDirectory(t *testing.T) {
	dir := t.TempDir()
	hook := manifest.InstallHook{File: `..\..\Windows\System32\cmd.exe`, Args: []string{"/c", "echo"}}
	err := runInstallHook("evil", "installer", hook, dir, pwsh.Vars{Dir: dir})
	if err == nil {
		t.Fatal("a target outside the app directory must be refused")
	}
	if !strings.Contains(err.Error(), "outside the app directory") {
		t.Errorf("error should say why, got: %v", err)
	}
}

// Omitting `file` is legal and means the downloaded file. goop skipped
// the hook entirely, so 19 manifests -- vulkan, latex and miktex among
// them -- downloaded, installed nothing, and reported success.
func TestRunInstallHook_ArgsWithNoFileUsesTheDownloadedFile(t *testing.T) {
	dir := t.TempDir()
	// No such file, so the hook must get as far as resolving the name and
	// then fail on the file being missing -- which proves it resolved
	// vars.Fname rather than silently returning nil.
	hook := manifest.InstallHook{Args: []string{"/S"}}
	err := runInstallHook("vulkan", "installer", hook, dir, pwsh.Vars{Dir: dir, Fname: "installer.exe"})
	if err == nil {
		t.Fatal("args with no file must run the downloaded file, not be skipped silently")
	}
	if !strings.Contains(err.Error(), "installer.exe") || !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should name the defaulted file and say it is missing, got: %v", err)
	}
}

// With neither file, args, nor script there is nothing to run, and that
// is not an error -- most manifests have no installer at all.
func TestRunInstallHook_EmptyHookDoesNothing(t *testing.T) {
	dir := t.TempDir()
	if err := runInstallHook("x", "installer", manifest.InstallHook{}, dir, pwsh.Vars{Dir: dir}); err != nil {
		t.Errorf("an empty hook must be a no-op, got: %v", err)
	}
}

// Args but nothing downloaded is a manifest goop cannot honour; saying
// so beats running something arbitrary.
func TestRunInstallHook_ArgsWithNothingToRunIsAnError(t *testing.T) {
	dir := t.TempDir()
	hook := manifest.InstallHook{Args: []string{"/S"}}
	err := runInstallHook("x", "installer", hook, dir, pwsh.Vars{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "no file") {
		t.Errorf("expected a clear refusal, got: %v", err)
	}
}

// A .ps1 target is a script. Spawning it hands the path to whatever is
// registered for the extension, which is not PowerShell everywhere and
// is not what Scoop does. main's pshazz is one of four manifests that
// need this.
func TestRunInstallHook_Ps1TargetRunsThroughPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell hook execution is Windows-only")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.txt")
	script := "param($a, $b)\n" +
		"Set-Content -LiteralPath '" + strings.ReplaceAll(marker, `'`, `''`) + "' -Value \"$a|$b\"\n"
	if err := os.WriteFile(filepath.Join(dir, "install.ps1"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	hook := manifest.InstallHook{File: "install.ps1", Args: []string{"$dir", "second"}}
	if err := runInstallHook("pshazz", "installer", hook, dir, pwsh.Vars{Dir: dir, Version: "1.0"}); err != nil {
		t.Fatalf("running a .ps1 installer: %v", err)
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the script did not run: %v", err)
	}
	// It ran as PowerShell, received its arguments positionally, and $dir
	// was substituted before being handed over.
	want := dir + "|second"
	if strings.TrimSpace(string(got)) != want {
		t.Errorf("script saw %q, want %q", strings.TrimSpace(string(got)), want)
	}
}

// Quoting a PowerShell single-quoted literal: the only escape is a
// doubled quote, and nothing inside expands.
func TestPsSingleQuote(t *testing.T) {
	cases := map[string]string{
		`plain`:        `'plain'`,
		`with space`:   `'with space'`,
		`it's`:         `'it''s'`,
		`$dir\x`:       `'$dir\x'`,
		`a'b'c`:        `'a''b''c'`,
		`C:\Program F`: `'C:\Program F'`,
	}
	for in, want := range cases {
		if got := psSingleQuote(in); got != want {
			t.Errorf("psSingleQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
