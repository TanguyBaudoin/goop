package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An empty root is a healthy root: nothing installed is not a fault, and
// doctor must say so and exit 0 rather than inventing something to
// report.
func TestCmdDoctor_CleanRootExitsZero(t *testing.T) {
	_, output := isolateCLI(t)
	if code := cmdDoctor(nil); code != 0 {
		t.Errorf("exit code = %d, want 0 on a healthy root", code)
	}
	if got := output(); !strings.Contains(stripANSI(got), "no problems found") {
		t.Errorf("output = %q, want it to say nothing is wrong", got)
	}
}

// Exit 3 is the contract `goop profile check` and `goop machine audit`
// already use for "something was found", so a scheduled run or a CI step
// can act on it without parsing the text.
func TestCmdDoctor_FindingsExitThree(t *testing.T) {
	root, output := isolateCLI(t)

	// Persisted data for a package that is not installed: untidy, not
	// broken, which is enough to make doctor report something.
	orphan := filepath.Join(root, "persist", "vlc")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "vlcrc"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := cmdDoctor(nil); code != 3 {
		t.Errorf("exit code = %d, want 3 when something was found", code)
	}
	out := stripANSI(output())
	for _, want := range []string{"vlc", "untidy", "fix:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should mention %q, got:\n%s", want, out)
		}
	}
	// Nothing is broken, so the summary must not claim a problem.
	if strings.Contains(out, "problem(s)") {
		t.Errorf("an untidy-only run must not report problems, got:\n%s", out)
	}
}

// -q is for the exit code alone: a scheduled check that only cares
// whether anything was found should not spill a report into a log.
func TestCmdDoctor_QuietSuppressesTheReport(t *testing.T) {
	root, output := isolateCLI(t)
	orphan := filepath.Join(root, "persist", "vlc")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}

	if code := cmdDoctor([]string{"-q"}); code != 3 {
		t.Errorf("exit code = %d, want 3; -q changes the output, not the verdict", code)
	}
	if out := stripANSI(output()); strings.Contains(out, "fix:") {
		t.Errorf("-q must not print the per-finding detail, got:\n%s", out)
	}
}

// doctor takes no package names. Accepting one silently would imply it
// had scoped the check to that package, which it never does.
func TestCmdDoctor_RejectsArguments(t *testing.T) {
	_, output := isolateCLI(t)
	if code := cmdDoctor([]string{"jq"}); code != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", code)
	}
	if out := stripANSI(output()); !strings.Contains(out, "takes no arguments") {
		t.Errorf("output should explain the usage error, got:\n%s", out)
	}
}

// A new command has three places to be wired, and missing any one of
// them makes it unreachable in a different way: run()'s switch, the help
// (or nobody finds it), and topLevelCommands (or `goop <tab>` never
// offers it). The first version of this test only checked the help --
// and the completion list had in fact been forgotten.
func TestDoctor_IsDocumentedAndDispatches(t *testing.T) {
	// It dispatches.
	func() {
		_, output := isolateCLI(t)
		run([]string{"doctor"})
		if out := stripANSI(output()); strings.Contains(out, "unknown command") {
			t.Errorf("run() does not handle \"doctor\":\n%s", out)
		}
	}()

	// Tab completion offers it.
	found := false
	for _, c := range topLevelCommands {
		if c == "doctor" {
			found = true
		}
	}
	if !found {
		t.Error("doctor must be in topLevelCommands, or `goop <tab>` will never offer it")
	}

	if !commandExists(buildHelp(), "doctor") {
		t.Error("goop doctor must appear in the reference help")
	}
	var buf strings.Builder
	if !renderCommand(&buf, buildHelp(), "doctor") {
		t.Fatal("goop help doctor must find something")
	}
	// The two things someone needs to know before running it.
	got := stripANSI(buf.String())
	for _, want := range []string{"changes nothing", "exit 3"} {
		if !strings.Contains(got, want) {
			t.Errorf("goop help doctor should mention %q, got:\n%s", want, got)
		}
	}
}
