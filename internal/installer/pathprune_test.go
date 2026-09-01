package installer

import (
	"path/filepath"
	"testing"
)

// Only entries under <root>/apps/<name>/<version> are goop's to remove.
// Everything else in someone's PATH is theirs, and a false positive here
// deletes a working tool's directory from their environment.
func TestAppVersionUnder(t *testing.T) {
	apps := filepath.FromSlash(`C:/goop/apps`)

	cases := []struct {
		entry   string
		app     string
		version string
		ok      bool
	}{
		{filepath.FromSlash(`C:/goop/apps/vscode/1.134.0/bin`), "vscode", "1.134.0", true},
		{filepath.FromSlash(`C:/goop/apps/nodejs-lts/24.19.0`), "nodejs-lts", "24.19.0", true},
		// Through the junction: follows whatever is installed, so it never
		// goes stale and is none of this function's business.
		{filepath.FromSlash(`C:/goop/apps/vscode/current/bin`), "", "", false},
		// goop's own directories, set up once by the installer. Removing
		// either would take every shim off PATH.
		{filepath.FromSlash(`C:/goop/shims`), "", "", false},
		{filepath.FromSlash(`C:/goop/bin`), "", "", false},
		// The apps directory itself names no version.
		{filepath.FromSlash(`C:/goop/apps`), "", "", false},
		{filepath.FromSlash(`C:/goop/apps/vscode`), "", "", false},
		// Somebody else's PATH entry.
		{filepath.FromSlash(`C:/Program Files/Git/cmd`), "", "", false},
		{filepath.FromSlash(`C:/goop-elsewhere/apps/x/1.0`), "", "", false},
		// A sibling directory whose name merely starts the same way.
		{filepath.FromSlash(`C:/goopapps/x/1.0`), "", "", false},
	}
	for _, tc := range cases {
		app, version, ok := appVersionUnder(apps, tc.entry)
		if ok != tc.ok || app != tc.app || version != tc.version {
			t.Errorf("appVersionUnder(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.entry, app, version, ok, tc.app, tc.version, tc.ok)
		}
	}
}

// A PATH entry is compared as a path, not as text: trailing separators
// and mixed slashes are the same directory.
func TestAppVersionUnder_NormalizesSpelling(t *testing.T) {
	apps := filepath.FromSlash(`C:/goop/apps`)
	for _, entry := range []string{
		filepath.FromSlash(`C:/goop/apps/vscode/1.134.0/bin/`),
		filepath.FromSlash(`C:/goop/apps/./vscode/1.134.0/bin`),
		filepath.FromSlash(`C:/goop/apps/vscode/1.135.0/../1.134.0/bin`),
	} {
		app, version, ok := appVersionUnder(apps, entry)
		if !ok || app != "vscode" || version != "1.134.0" {
			t.Errorf("%q = (%q, %q, %v), want vscode/1.134.0", entry, app, version, ok)
		}
	}
}
