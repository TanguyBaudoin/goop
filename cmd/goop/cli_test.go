package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateCLI points goop at an empty tree and silences the command's
// output, returning what it printed.
//
// run() writes to stdout and stderr directly, so a test that does not
// capture them fills the test log with real command output and hides its
// own failures in it.
func isolateCLI(t *testing.T) (root string, output func() string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("GOOP_HOME", root)
	t.Setenv("APPDATA", filepath.Join(root, "appdata"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "localappdata"))

	oldOut, oldErr := os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = w, w

	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()

	var captured string
	var closed bool
	t.Cleanup(func() {
		if !closed {
			w.Close()
			<-done
		}
		os.Stdout, os.Stderr = oldOut, oldErr
	})
	return root, func() string {
		if !closed {
			w.Close()
			captured = <-done
			closed = true
			os.Stdout, os.Stderr = oldOut, oldErr
		}
		return captured
	}
}

// Exit codes are the contract CI depends on: 0 ok, 1 error, 2 usage,
// 3 deviation. Getting 2 where 1 belongs turns a real failure into
// "you typed it wrong", and vice versa.
func TestRun_ExitCodes(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no arguments", nil, 2},
		{"unknown command", []string{"nonsense"}, 2},
		{"version", []string{"version"}, 0},
		{"help", []string{"help"}, 0},
		{"install with no spec", []string{"install"}, 2},
		{"uninstall with no name", []string{"uninstall"}, 2},
		{"profile with no subcommand", []string{"profile"}, 2},
		{"profile unknown subcommand", []string{"profile", "nonsense"}, 2},
		{"machine with no subcommand", []string{"machine"}, 2},
		{"machine unknown subcommand", []string{"machine", "nonsense"}, 2},
		{"profile check with no file", []string{"profile", "check"}, 2},
		{"profile sync with no file", []string{"profile", "sync"}, 2},
		{"machine audit with no file", []string{"machine", "audit"}, 2},
		{"digest with no target", []string{"digest"}, 2},
		{"profile delete with no name", []string{"profile", "delete"}, 2},
		// A file that does not exist is an error, not a usage mistake.
		{"profile check on a missing file", []string{"profile", "check", "no-such-file.json"}, 1},
		{"machine audit on a missing file", []string{"machine", "audit", "no-such-file.json"}, 1},
		{"unknown flag", []string{"update", "--nonsense"}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateCLI(t)
			if got := run(tc.args); got != tc.want {
				t.Errorf("run(%v) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

// The 0.3.x names were removed rather than aliased, so someone using one
// has to be told where it went -- "unknown command" would be a poor way
// to discover a rename.
func TestRun_MovedCommandsSayWhereTheyWent(t *testing.T) {
	moved := map[string]string{
		"export": "goop machine export",
		"import": "goop machine restore",
		"audit":  "goop machine audit",
		"check":  "goop profile check",
		"sync":   "goop profile sync",
	}
	for old, want := range moved {
		t.Run(old, func(t *testing.T) {
			_, output := isolateCLI(t)
			code := run([]string{old, "whatever"})
			out := output()
			if code != 2 {
				t.Errorf("run(%s) = %d, want 2", old, code)
			}
			if !strings.Contains(out, want) {
				t.Errorf("output should name %q, got:\n%s", want, out)
			}
		})
	}
}

// Every command that changes the machine takes -y. One that does not
// makes the flag unusable in the scripts it exists for -- which is how
// `machine restore -y` was found to reject it.
func TestRun_AcceptsAssumeYes(t *testing.T) {
	for _, args := range [][]string{
		{"install", "jq", "-y"},
		{"uninstall", "jq", "-y"},
		{"update", "-y"},
		{"self-update", "-y", "--dry-run"},
		{"profile", "sync", "some-file.json", "-y"},
		{"machine", "restore", "some-file.json", "-y"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, output := isolateCLI(t)
			// Not asserting success -- there are no buckets and no such
			// files. Only that -y is not rejected as a usage error.
			code := run(args)
			out := output()
			if code == 2 && strings.Contains(out, "usage:") {
				t.Errorf("-y was rejected as a usage error:\n%s", out)
			}
		})
	}
}

// --dry-run must not touch anything, which is the entire promise.
func TestRun_DryRunChangesNothing(t *testing.T) {
	root, output := isolateCLI(t)
	code := run([]string{"update", "--dry-run", "--no-update"})
	_ = output()
	if code != 0 {
		t.Errorf("update --dry-run on an empty machine = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(root, "apps")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(root, "apps"))
		if len(entries) > 0 {
			t.Errorf("--dry-run installed something: %v", entries)
		}
	}
}

// `goop profile show` on a name that does not exist must be an error.
// Printing it with "no members" reads as an empty profile, which is the
// silence the whole design refuses.
func TestRun_ProfileShowUnknownIsAnError(t *testing.T) {
	_, output := isolateCLI(t)
	code := run([]string{"profile", "show", "ghost"})
	out := output()
	if code != 1 {
		t.Errorf("profile show ghost = %d, want 1", code)
	}
	if !strings.Contains(out, "ghost") {
		t.Errorf("the error should name the profile, got:\n%s", out)
	}
}

// The default profile is the fallback, so deleting it would leave
// nowhere for anything to fall back to.
func TestRun_ProfileDeleteRefusesDefault(t *testing.T) {
	_, output := isolateCLI(t)
	code := run([]string{"profile", "delete", "default"})
	out := output()
	if code != 1 {
		t.Errorf("profile delete default = %d, want 1", code)
	}
	if !strings.Contains(out, "default") {
		t.Errorf("the refusal should say why, got:\n%s", out)
	}
}

// completion.go's list is what `goop <tab>` offers, and a name in it
// that run() does not handle sends people to a command that reports
// "unknown".
func TestTopLevelCommands_AllDispatch(t *testing.T) {
	for _, name := range topLevelCommands {
		t.Run(name, func(t *testing.T) {
			_, output := isolateCLI(t)
			code := run([]string{name})
			out := output()
			if strings.Contains(out, "unknown command") {
				t.Errorf("%q is offered by completion but run() does not handle it (exit %d):\n%s", name, code, out)
			}
		})
	}
}
