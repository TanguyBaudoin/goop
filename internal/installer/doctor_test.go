package installer

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/paths"
)

// realInstall lays down what a finished install actually leaves behind:
// a version directory holding the record, and `current` as a junction
// pointing at it.
//
// Deliberately not conform_test.go's fakeInstall, which writes the
// record straight into `current` as a real directory. That shape is fine
// for tests that only read receipts, but it is indistinguishable from
// the zen-browser breakage doctor exists to report -- so using it here
// would make the clean-install baseline fail for the wrong reason.
//
// goop creates the link with `mklink /J`, a directory junction, which
// needs no privilege; this does the same, so the fixture matches
// production rather than approximating it.
func realInstall(t *testing.T, name, version string, mutate ...func(*Record)) {
	t.Helper()
	dir := paths.AppVersion(name, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := Record{Name: name, Version: version} // empty State means ready
	for _, m := range mutate {
		m(&rec)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, recordFileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	linkCurrent(t, name, dir)
}

func linkCurrent(t *testing.T, name, target string) {
	t.Helper()
	link := paths.AppCurrent(name)
	os.RemoveAll(link)
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, out)
	}
}

func findingFor(findings []Finding, check string) *Finding {
	for i := range findings {
		if findings[i].Check == check {
			return &findings[i]
		}
	}
	return nil
}

func diagnose(t *testing.T) []Finding {
	t.Helper()
	findings, err := Diagnose()
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	return findings
}

// The baseline, and the test that matters most: a healthy install must
// produce nothing at all. A diagnostic that cries wolf on a clean
// machine is worse than no diagnostic, because it teaches people to
// ignore it.
func TestDiagnose_CleanInstallIsSilent(t *testing.T) {
	isolateRoot(t)
	isolateEnvRegistry(t)
	realInstall(t, "jq", "1.7.1")
	exe := filepath.Join(paths.AppVersion("jq", "1.7.1"), "jq.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeShim(t, "jq", exe)

	if findings := diagnose(t); len(findings) != 0 {
		for _, f := range findings {
			t.Errorf("clean install reported %q: %s %v", f.Check, f.Summary, f.Items)
		}
	}
}

// An install interrupted between the commit rename and the shims being
// created leaves files that look complete and a package with no working
// commands.
func TestDiagnose_InstallNeverFinished(t *testing.T) {
	isolateRoot(t)
	isolateEnvRegistry(t)
	realInstall(t, "jq", "1.7.1", func(r *Record) { r.State = recordPending })

	f := findingFor(diagnose(t), "install-never-finished")
	if f == nil {
		t.Fatal("a pending install must be reported")
	}
	if f.Severity != SevProblem {
		t.Errorf("severity = %v, want problem", f.Severity)
	}
	if len(f.Items) != 1 || !strings.Contains(f.Items[0], "jq") {
		t.Errorf("items = %v, want the pending package named", f.Items)
	}
	if f.Fix == "" {
		t.Error("a finding that can be fixed must say with what")
	}
}

// Zen Browser's updater replaces the junction with a real directory and
// moves the app -- record included -- into it. Everything still runs,
// and the next update cannot repoint `current`, which is how a real
// install got stuck at exit status 145.
func TestDiagnose_CurrentReplacedByRealDirectory(t *testing.T) {
	isolateRoot(t)
	isolateEnvRegistry(t)
	realInstall(t, "zen-browser", "1.21.16b")

	cur := paths.AppCurrent("zen-browser")
	recorded := filepath.Join(paths.AppVersion("zen-browser", "1.21.16b"), recordFileName)
	body, err := os.ReadFile(recorded)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cur); err != nil {
		t.Fatalf("removing the junction: %v", err)
	}
	if err := os.MkdirAll(cur, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cur, recordFileName), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(recorded); err != nil {
		t.Fatal(err)
	}

	findings := diagnose(t)
	f := findingFor(findings, "current-not-a-link")
	if f == nil {
		t.Fatal("a `current` that is a real directory must be reported")
	}
	if f.Severity != SevProblem {
		t.Errorf("severity = %v, want problem", f.Severity)
	}
	if len(f.Items) != 1 || !strings.Contains(f.Items[0], "real directory") {
		t.Errorf("items = %v, want it to say `current` is a real directory", f.Items)
	}

	// The version directory left without a record is a leftover, not an
	// install that cannot be removed: the record still exists, it just
	// moved. Getting this wrong against a real machine is what prompted
	// splitting the check by severity.
	if l := findingFor(findings, "version-leftover"); l == nil {
		t.Error("the version directory left with no record must be reported")
	} else if l.Severity != SevUntidy {
		t.Errorf("a leftover version is untidy, not a problem (got %v)", l.Severity)
	}
	if findingFor(findings, "receipt-missing") != nil {
		t.Error("the live record is readable, so this must not be reported as receipt-missing")
	}
}

// The version `current` points at, with no record, is the serious case:
// goop cannot uninstall it cleanly, because the shims, shortcuts and
// environment entries it created are recorded nowhere else.
func TestDiagnose_ActiveVersionWithNoRecordIsAProblem(t *testing.T) {
	isolateRoot(t)
	isolateEnvRegistry(t)
	realInstall(t, "jq", "1.7.1")
	if err := os.Remove(filepath.Join(paths.AppVersion("jq", "1.7.1"), recordFileName)); err != nil {
		t.Fatal(err)
	}

	f := findingFor(diagnose(t), "receipt-missing")
	if f == nil {
		t.Fatal("the active version having no record must be reported as a problem")
	}
	if f.Severity != SevProblem {
		t.Errorf("severity = %v, want problem", f.Severity)
	}
}

// A shim pointing nowhere survives a green build and a clean
// `goop profile check`: everything reports installed, and it fails only
// when a person runs the command.
func TestDiagnose_ShimPointingNowhere(t *testing.T) {
	isolateRoot(t)
	isolateEnvRegistry(t)
	realInstall(t, "jq", "1.7.1")
	writeShim(t, "jq", filepath.Join(paths.AppVersion("jq", "1.7.1"), "jq.exe")) // never created

	f := findingFor(diagnose(t), "shim-target-missing")
	if f == nil {
		t.Fatal("a shim whose target does not exist must be reported")
	}
	if f.Severity != SevProblem {
		t.Errorf("severity = %v, want problem", f.Severity)
	}
	if len(f.Items) != 1 || !strings.Contains(f.Items[0], "jq.shim") {
		t.Errorf("items = %v, want the shim named", f.Items)
	}
}

// A shim still pointing into staging means a commit that never
// completed: the path exists right now and will not survive.
func TestDiagnose_ShimStillPointingIntoStaging(t *testing.T) {
	isolateRoot(t)
	isolateEnvRegistry(t)
	realInstall(t, "jq", "1.7.1")
	staging := paths.AppVersionStaging("jq", "1.7.1")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(staging, "jq.exe")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeShim(t, "jq", target)

	f := findingFor(diagnose(t), "shim-target-missing")
	if f == nil {
		t.Fatal("a shim pointing into staging must be reported even though the file exists")
	}
	if len(f.Items) != 1 || !strings.Contains(f.Items[0], "staging") {
		t.Errorf("items = %v, want it to say the target is still in staging", f.Items)
	}
}

// `goop uninstall` keeps persisted data on purpose -- it is the user's,
// not the package's -- but nothing ever pointed out that it was still
// there. Measured on a real machine: five orphaned directories, the
// largest 3.8 GB.
func TestDiagnose_OrphanedPersist(t *testing.T) {
	root := isolateRoot(t)
	isolateEnvRegistry(t)
	realInstall(t, "jq", "1.7.1")

	// vlc was uninstalled; its persisted data was not.
	gone := filepath.Join(root, "persist", "vlc")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gone, "vlcrc"), []byte(strings.Repeat("x", 2048)), 0o644); err != nil {
		t.Fatal(err)
	}
	// jq is installed, so its persisted data is not orphaned.
	if err := os.MkdirAll(filepath.Join(root, "persist", "jq"), 0o755); err != nil {
		t.Fatal(err)
	}

	f := findingFor(diagnose(t), "persist-orphaned")
	if f == nil {
		t.Fatal("persisted data for an uninstalled package must be reported")
	}
	if f.Severity != SevUntidy {
		t.Errorf("severity = %v, want untidy -- nothing is broken", f.Severity)
	}
	if len(f.Items) != 1 {
		t.Fatalf("items = %v, want only vlc (jq is installed)", f.Items)
	}
	if !strings.Contains(f.Items[0], "vlc") {
		t.Errorf("items = %v, want vlc named", f.Items)
	}
	// The size is the whole point: it is what tells someone whether a
	// stray directory is worth caring about.
	if !strings.Contains(f.Items[0], "KB") {
		t.Errorf("items = %v, want the size reported", f.Items)
	}
}

// sameDir has to compare the way Windows does, or the severity split
// above lands on the wrong side.
func TestSameDir(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{`C:\apps\jq\1.7.1`, `C:\apps\jq\1.7.1`, true},
		{`C:\apps\jq\1.7.1`, `c:\APPS\jq\1.7.1`, true},
		{`C:\apps\jq\1.7.1\`, `C:\apps\jq\1.7.1`, true},
		{`C:\apps\jq\1.7.1`, `C:\apps\jq\1.6`, false},
		{`C:\apps\jq`, `C:\apps\jq2`, false},
	}
	for _, tc := range cases {
		if got := sameDir(tc.a, tc.b); got != tc.want {
			t.Errorf("sameDir(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
