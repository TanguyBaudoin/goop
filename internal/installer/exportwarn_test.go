package installer

import (
	"fmt"
	"strings"
	"testing"
)

// captureLog redirects installer progress messages for the duration of a
// test.
func captureLog(t *testing.T) *[]string {
	t.Helper()
	var lines []string
	prev := Logf
	Logf = func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() { Logf = prev })
	return &lines
}

// A pin nobody else can install is worse than no pin: the file looks
// complete and fails on the next machine. Drive-letter file:// URLs are
// the case that happens by accident.
func TestExport_WarnsOnMachineLocalSource(t *testing.T) {
	cases := []struct {
		name string
		url  string
		warn bool
	}{
		{"drive letter", "file:///C:/tools/jdk.zip", true},
		{"UNC share", "file://fileserver/share/jdk.zip", false},
		{"https", "https://example.com/jdk.zip", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateRoot(t)
			fakeInstall(t, Record{
				Name: "jdk", Version: "21", State: "ready",
				URLs: []string{tc.url},
			})
			lines := captureLog(t)

			members := func(string) ([]string, error) { return []string{"jdk"}, nil }
			if _, err := ExportProfiles([]string{"chipa"}, members); err != nil {
				t.Fatal(err)
			}

			warned := false
			for _, l := range *lines {
				if strings.Contains(l, "only on this machine") {
					warned = true
				}
			}
			if warned != tc.warn {
				t.Errorf("warned = %v, want %v (lines: %v)", warned, tc.warn, *lines)
			}
		})
	}
}

// The same file leaves a machine capture, so the same warning applies.
func TestExportSetup_WarnsOnMachineLocalSource(t *testing.T) {
	isolateRoot(t)
	fakeInstall(t, Record{
		Name: "jdk", Version: "21", State: "ready",
		URLs: []string{"file:///D:/tools/jdk.zip"},
	})
	lines := captureLog(t)

	if _, err := ExportSetup(); err != nil {
		t.Fatal(err)
	}
	for _, l := range *lines {
		if strings.Contains(l, "only on this machine") {
			return
		}
	}
	t.Errorf("a capture pinning a drive-letter path must warn, got %v", *lines)
}
