package pwsh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Invoke-ExternalCommand declared -RunAs, -Quiet and -LogPath and
// ignored all three. These pin the ported behaviour.
//
// What is NOT covered here, stated rather than implied: -RunAs itself.
// Honouring it means ShellExecute with the RunAs verb, which raises a UAC
// prompt, and a test that needs someone to click Yes is not a test. Its
// correctness rests on matching real Scoop's lib/core.ps1 line for line
// -- UseShellExecute = $true, Verb = 'RunAs' -- and on the parameter-set
// exclusivity pinned below, which is the structure that makes it
// possible at all.

// A failing child used to return $false and say nothing, which is how a
// package gets recorded as installed when its installer did not run.
// Scoop prints the exit code; so does this now.
func TestInvokeExternalCommand_NonZeroExitIsReported(t *testing.T) {
	vars := testVars(t)
	out, err := Run(`
$ok = Invoke-ExternalCommand -FilePath 'cmd.exe' -ArgumentList @('/c', 'exit', '3')
if ($ok) { throw "a failing command must report failure" }
`, vars)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Exit code was 3") {
		t.Errorf("the exit code must be reported, got:\n%s", out)
	}
}

// -ContinueExitCodes turns a known non-zero code into success with a
// warning. 11 manifest scripts rely on it -- typically an installer that
// returns 3010 for "reboot required".
func TestInvokeExternalCommand_ContinueExitCodes(t *testing.T) {
	vars := testVars(t)
	out, err := Run(`
$ok = Invoke-ExternalCommand -FilePath 'cmd.exe' -ArgumentList @('/c', 'exit', '3010') -ContinueExitCodes @{ 3010 = 'a reboot is needed' }
if (-not $ok) { throw "a continuable exit code must report success" }
`, vars)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "a reboot is needed") {
		t.Errorf("the continuation message must be shown, got:\n%s", out)
	}
	if strings.Contains(out, "Exit code was") {
		t.Errorf("a continuable code must not also be reported as an error, got:\n%s", out)
	}
}

// -LogPath sends the child's output to a file instead of the console.
// 11 manifest scripts pass it; goop ignored it, so the output went to
// the console and the file was never created -- a manifest that then
// read the log back found nothing.
func TestInvokeExternalCommand_LogPathWritesTheFile(t *testing.T) {
	vars := testVars(t)
	log := filepath.Join(t.TempDir(), "install.log")
	script := `
$ok = Invoke-ExternalCommand -FilePath 'cmd.exe' -ArgumentList @('/c', 'echo', 'into-the-log') -LogPath '` + strings.ReplaceAll(log, `'`, `''`) + `'
if (-not $ok) { throw "command reported failure" }
`
	out, err := Run(script, vars)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	data, rerr := os.ReadFile(log)
	if rerr != nil {
		t.Fatalf("the log was never written: %v", rerr)
	}
	if !strings.Contains(string(data), "into-the-log") {
		t.Errorf("log = %q, want the child's output in it", string(data))
	}
	// And it went to the log instead of the console.
	if strings.Contains(out, "into-the-log") {
		t.Errorf("with -LogPath the output must not also go to the console, got:\n%s", out)
	}
}

// -Quiet means ShellExecute with a hidden window, and ShellExecute
// cannot redirect the child's streams. The observable consequence is
// that the output does not come back -- which is what distinguishes the
// switch being honoured from being ignored.
func TestInvokeExternalCommand_QuietDoesNotCaptureOutput(t *testing.T) {
	vars := testVars(t)
	out, err := Run(`
$ok = Invoke-ExternalCommand -FilePath 'cmd.exe' -ArgumentList @('/c', 'echo', 'should-not-be-captured') -Quiet
if (-not $ok) { throw "command reported failure" }
`, vars)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "should-not-be-captured") {
		t.Errorf("-Quiet uses ShellExecute, which cannot redirect; output must not be captured, got:\n%s", out)
	}
}

// -RunAs/-Quiet and -LogPath are mutually exclusive, as they are in
// Scoop: the first two require ShellExecute and the third requires
// redirection, and Windows does not allow both. Keeping the parameter
// sets means a manifest combining them fails loudly instead of having
// one switch silently win.
func TestInvokeExternalCommand_RunAsAndLogPathAreExclusive(t *testing.T) {
	vars := testVars(t)
	out, err := Run(`
Invoke-ExternalCommand -FilePath 'cmd.exe' -ArgumentList @('/c', 'echo', 'x') -RunAs -LogPath 'C:\nope.log'
`, vars)
	if err == nil {
		t.Errorf("combining -RunAs and -LogPath must be refused, got no error:\n%s", out)
	}
}

// The deadlock guard has to survive the rewrite: the capture path is
// still the default one, and it is where the two-stream deadlock lives.
func TestInvokeExternalCommand_StillCapturesByDefault(t *testing.T) {
	vars := testVars(t)
	out, err := Run(`
$ok = Invoke-ExternalCommand -FilePath 'cmd.exe' -ArgumentList @('/c', 'echo', 'captured-by-default')
if (-not $ok) { throw "command reported failure" }
`, vars)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "captured-by-default") {
		t.Errorf("the default path must still capture output, got:\n%s", out)
	}
}
