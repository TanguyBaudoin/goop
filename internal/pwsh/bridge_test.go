package pwsh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testVars(t *testing.T) Vars {
	t.Helper()
	dir := t.TempDir()
	return Vars{
		Dir:          dir,
		Version:      "1.2.3",
		Architecture: "64bit",
		PersistDir:   filepath.Join(dir, "persist"),
		Fname:        "asset.zip",
		Cmd:          "install",
		Bucket:       "main",
		BucketsDir:   filepath.Join(dir, "buckets"),
		ShimsDir:     filepath.Join(dir, "shims"),
		ShimMaster:   filepath.Join(dir, "shims", "shim-master.exe"),
	}
}

func TestRun_PreludeLoadsAndVarsBind(t *testing.T) {
	out, err := Run(`
if ($dir -eq '') { throw "dir not bound" }
if ($version -ne '1.2.3') { throw "version = $version" }
if ($architecture -ne '64bit') { throw "arch = $architecture" }
Write-Host "OK $dir $version $architecture"
`, testVars(t))
	if err != nil {
		t.Fatalf("script failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "OK") {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestRun_ScriptErrorPropagates(t *testing.T) {
	_, err := Run(`throw "boom"`, testVars(t))
	if err == nil {
		t.Fatal("expected error from failing script")
	}
}

func TestRun_EmptyScriptIsNoop(t *testing.T) {
	out, err := Run("   \n  ", testVars(t))
	if err != nil || out != "" {
		t.Fatalf("expected no-op, got out=%q err=%v", out, err)
	}
}

func TestRun_InfoWarnErrorAbortHelpers(t *testing.T) {
	out, err := Run(`
info "hello"
warn "careful"
error "uh oh"
`, testVars(t))
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	for _, want := range []string{"INFO", "WARN", "ERROR"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
}

func TestRun_Ensure(t *testing.T) {
	vars := testVars(t)
	target := filepath.Join(vars.Dir, "sub", "nested")
	out, err := Run(`ensure "`+strings.ReplaceAll(target, `\`, `\\`)+`" | Out-Null`, vars)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("ensure did not create %s: %v", target, statErr)
	}
}

func TestRun_InvokeExternalCommand(t *testing.T) {
	vars := testVars(t)
	out, err := Run(`
$ok = Invoke-ExternalCommand -FilePath 'cmd.exe' -ArgumentList @('/c', 'echo', 'hi-from-child')
if (-not $ok) { throw "command reported failure" }
`, vars)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "hi-from-child") {
		t.Errorf("child output missing: %s", out)
	}
}

func TestRun_InvokeExternalCommand_LargeStderrDoesNotDeadlock(t *testing.T) {
	// Regression test: Invoke-ExternalCommand used to ReadToEnd() stdout
	// fully before touching stderr. A child that writes enough to
	// stderr while stdout stays empty (msiexec routinely does) fills
	// stderr's pipe buffer and blocks on it while the parent is still
	// blocked reading stdout to completion -- a genuine deadlock. This
	// writes ~1MB to stderr (far past the OS pipe buffer, typically a
	// few KB) with nothing on stdout, and must complete well within the
	// script timeout rather than hang.
	vars := testVars(t)
	done := make(chan struct{})
	var out string
	var err error
	go func() {
		out, err = Run(`
$ok = Invoke-ExternalCommand -FilePath 'powershell.exe' -ArgumentList @('-NoProfile', '-Command', '1..5000 | ForEach-Object { [Console]::Error.WriteLine("x" * 200) }')
if (-not $ok) { throw "command reported failure" }
`, vars)
		close(done)
	}()

	select {
	case <-done:
		if err != nil {
			t.Fatalf("script failed: %v\n%s", err, out)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Invoke-ExternalCommand deadlocked on large stderr output with empty stdout")
	}
}

func TestRun_NonASCIIRoundTrips(t *testing.T) {
	// Regression test: Windows PowerShell 5.1 reads a BOM-less script
	// file using the system code page, not UTF-8, silently mangling
	// non-ASCII text (e.g. "Less MSIérables" -> "Less MSIÃ©rables")
	// unless the temp script is written with a UTF-8 BOM.
	out, err := Run(`Write-Host "Less MSIérables"`, testVars(t))
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Less MSIérables") {
		t.Errorf("non-ASCII text was mangled, got: %q", out)
	}
}

func TestRun_ShimHelper(t *testing.T) {
	vars := testVars(t)
	if err := os.MkdirAll(vars.ShimsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	master := []byte("fake-shim-binary-bytes")
	if err := os.WriteFile(vars.ShimMaster, master, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(vars.Dir, "tool.exe")
	if err := os.WriteFile(target, []byte("fake-exe"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := Run(`shim "`+strings.ReplaceAll(target, `\`, `\\`)+`" $false "mytool" "--default-arg"`, vars)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}

	shimExe := filepath.Join(vars.ShimsDir, "mytool.exe")
	if _, err := os.Stat(shimExe); err != nil {
		t.Errorf("shim exe not created: %v", err)
	}
	sidecar, err := os.ReadFile(filepath.Join(vars.ShimsDir, "mytool.shim"))
	if err != nil {
		t.Fatalf("sidecar not created: %v", err)
	}
	if !strings.Contains(string(sidecar), "tool.exe") || !strings.Contains(string(sidecar), "--default-arg") {
		t.Errorf("sidecar content unexpected: %s", sidecar)
	}
}
