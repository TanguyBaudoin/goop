package main

import (
	"strings"
	"testing"
)

// A new command has three places to be wired, and missing any one makes
// it unreachable in a different way: run()'s switch, the help, and
// topLevelCommands for `goop <tab>`. The doctor command shipped with the
// third forgotten, so this checks all three from the start.
func TestUse_IsWiredEverywhere(t *testing.T) {
	func() {
		_, output := isolateCLI(t)
		run([]string{"use"})
		if out := stripANSI(output()); strings.Contains(out, "unknown command") {
			t.Errorf("run() does not handle \"use\":\n%s", out)
		}
	}()

	found := false
	for _, c := range topLevelCommands {
		if c == "use" {
			found = true
		}
	}
	if !found {
		t.Error("use must be in topLevelCommands, or `goop <tab>` will never offer it")
	}

	if !commandExists(buildHelp(), "use") {
		t.Error("goop use must appear in the reference help")
	}
	var buf strings.Builder
	if !renderCommand(&buf, buildHelp(), "use") {
		t.Fatal("goop help use must find something")
	}
	// The two things that make the command worth having.
	got := stripANSI(buf.String())
	for _, want := range []string{"nothing is downloaded", "current"} {
		if !strings.Contains(got, want) {
			t.Errorf("goop help use should mention %q, got:\n%s", want, got)
		}
	}
}

// No arguments is a usage error, not a silent success.
func TestCmdUse_NoArguments(t *testing.T) {
	_, output := isolateCLI(t)
	if code := cmdUse(nil); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if out := stripANSI(output()); !strings.Contains(out, "usage: goop use") {
		t.Errorf("output should show the usage, got:\n%s", out)
	}
}

// More than name + version is a typo, most likely a third package name
// from muscle memory for `goop install a b c`.
func TestCmdUse_TooManyArguments(t *testing.T) {
	_, output := isolateCLI(t)
	if code := cmdUse([]string{"jq", "1.7.1", "ripgrep"}); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if out := stripANSI(output()); !strings.Contains(out, "usage: goop use") {
		t.Errorf("output should show the usage, got:\n%s", out)
	}
}

// The command takes no flags, so one is a mistake worth naming rather
// than ignoring -- silently dropping -y would imply it had been honoured.
func TestCmdUse_RejectsFlags(t *testing.T) {
	_, output := isolateCLI(t)
	if code := cmdUse([]string{"jq", "-y"}); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if out := stripANSI(output()); !strings.Contains(out, "takes no flags") {
		t.Errorf("output should name the rejected flag, got:\n%s", out)
	}
}

// An app that is not installed is an error, not an empty table.
func TestCmdUse_NotInstalled(t *testing.T) {
	_, output := isolateCLI(t)
	if code := cmdUse([]string{"ghost"}); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if out := stripANSI(output()); !strings.Contains(out, "ghost") {
		t.Errorf("output should name the package, got:\n%s", out)
	}
}
