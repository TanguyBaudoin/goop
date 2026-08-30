package installer

import (
	"testing"

	"github.com/TanguyBaudoin/goop/internal/manifest"
)

// The corroboration is the whole point: a digest is only recorded when
// every field the receipt kept still matches the bucket. These check the
// comparison itself, which is what decides whether a pin gets written.
func TestFirstFieldMismatch(t *testing.T) {
	base := func() (Record, manifest.Resolved) {
		rec := Record{
			Name: "jq", Version: "1.8.2",
			URLs:        []string{"https://example.com/jq.exe"},
			Hashes:      []string{"abc"},
			Bin:         []manifest.BinEntry{{Name: "jq", Exe: "jq.exe"}},
			ExtractDirs: []string{"inner"},
		}
		r := manifest.Resolved{
			Name: "jq", Version: "1.8.2",
			URLs:        []string{"https://example.com/jq.exe"},
			Hashes:      []string{"abc"},
			Bin:         manifest.BinList{{Name: "jq", Exe: "jq.exe"}},
			ExtractDirs: []string{"inner"},
		}
		return rec, r
	}

	if got := firstFieldMismatch(base()); got != "" {
		t.Errorf("identical records must match, got %q", got)
	}

	cases := []struct {
		name   string
		mutate func(*manifest.Resolved)
		want   string
	}{
		{"url swapped", func(r *manifest.Resolved) { r.URLs = []string{"https://evil.example/jq.exe"} }, "urls"},
		{"hash changed", func(r *manifest.Resolved) { r.Hashes = []string{"def"} }, "hashes"},
		{"bin renamed", func(r *manifest.Resolved) { r.Bin = manifest.BinList{{Name: "jaq", Exe: "jq.exe"}} }, "bin"},
		{"extract dir moved", func(r *manifest.Resolved) { r.ExtractDirs = []string{"other"} }, "extract_dir"},
		{"uninstaller added", func(r *manifest.Resolved) { r.PostUninstall = "rm -rf /" }, "post_uninstall"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, r := base()
			tc.mutate(&r)
			if got := firstFieldMismatch(rec, r); got != tc.want {
				t.Errorf("mismatch = %q, want %q", got, tc.want)
			}
		})
	}
}

// A manifest with no shortcuts and a receipt that round-tripped one into
// an empty slice describe the same thing. Refusing over that would block
// most packages for no reason.
func TestFirstFieldMismatch_NilAndEmptyAreTheSame(t *testing.T) {
	rec := Record{Shortcuts: nil, ExtractDirs: []string{}}
	r := manifest.Resolved{Shortcuts: []manifest.ShortcutEntry{}, ExtractDirs: nil}
	if got := firstFieldMismatch(rec, r); got != "" {
		t.Errorf("nil and empty must compare equal, got %q", got)
	}
}

// Corroboration covers what the receipt kept, and the receipt never kept
// pre_install/post_install. If that ever changes, this test should start
// failing and the command's wording has to change with it -- it tells
// users those scripts could not be checked.
func TestReceiptStillCannotCorroborateInstallScripts(t *testing.T) {
	rec := Record{URLs: []string{"u"}, Hashes: []string{"h"}}
	r := manifest.Resolved{
		URLs: []string{"u"}, Hashes: []string{"h"},
		PreInstall:  "Write-Host anything",
		PostInstall: "Write-Host anything else",
		Installer:   manifest.InstallHook{Script: "more"},
	}
	if got := firstFieldMismatch(rec, r); got != "" {
		t.Fatalf("unexpected mismatch %q -- if install scripts are now recorded, "+
			"add them to firstFieldMismatch and update the command's wording", got)
	}
}
