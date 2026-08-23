package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/TanguyBaudoin/goop/internal/manifest"
)

// writeLog writes content to a temp file and returns its path, so the
// log readers can be exercised against real bytes (BOMs included)
// rather than a stubbed reader.
func writeLog(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "log.txt")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadShimLog(t *testing.T) {
	// The BOM case is the regression that actually shipped: PowerShell
	// 5.1's Set-Content -Encoding utf8 prefixes one, and it used to glue
	// itself onto the first shim's name.
	const bom = "\uFEFF"

	tests := []struct {
		name    string
		content string
		want    []ExtraShim
	}{
		{"empty", "", nil},
		{
			"name and target",
			"rg\tC:\\apps\\ripgrep\\rg.exe\n",
			[]ExtraShim{{Name: "rg", Path: "C:\\apps\\ripgrep\\rg.exe"}},
		},
		{
			"leading BOM is not part of the first name",
			bom + "a\tC:\\a.exe\nb\tC:\\b.exe\n",
			[]ExtraShim{{Name: "a", Path: "C:\\a.exe"}, {Name: "b", Path: "C:\\b.exe"}},
		},
		{
			"tab-less line predates target tracking",
			"legacy\n",
			[]ExtraShim{{Name: "legacy"}},
		},
		{
			"duplicates collapse, output is sorted",
			"z\tC:\\z.exe\na\tC:\\a.exe\nz\tC:\\other.exe\n",
			[]ExtraShim{{Name: "a", Path: "C:\\a.exe"}, {Name: "z", Path: "C:\\z.exe"}},
		},
		{"blank lines ignored", "\n\n  \n", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readShimLog(writeLog(t, tt.content))
			if len(got) != len(tt.want) {
				t.Fatalf("got %d entries %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("entry %d: got %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestReadShimLog_MissingFile(t *testing.T) {
	if got := readShimLog(filepath.Join(t.TempDir(), "nope.txt")); got != nil {
		t.Errorf("a missing log should read as no shims, got %v", got)
	}
}

func TestReadShortcutLog(t *testing.T) {
	got := readShortcutLog(writeLog(t,
		"\uFEFFFreeCAD\tC:\\apps\\freecad\\bin\\FreeCAD.exe\t\t\n"+
			"Other\tC:\\o.exe\t--flag\tC:\\icon.ico\n"))
	want := []ExtraShortcut{
		{Name: "FreeCAD", Target: "C:\\apps\\freecad\\bin\\FreeCAD.exe"},
		{Name: "Other", Target: "C:\\o.exe", Args: "--flag", Icon: "C:\\icon.ico"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// ExtraShim's decoder has to accept both shapes: records written before
// targets were tracked stored a bare string, and they must keep loading
// rather than failing the whole record.
func TestExtraShim_UnmarshalBothShapes(t *testing.T) {
	var rec struct {
		ExtraShims []ExtraShim `json:"extra_shims"`
	}
	const in = `{"extra_shims":["legacy",{"name":"modern","path":"C:\\m.exe"}]}`
	if err := json.Unmarshal([]byte(in), &rec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []ExtraShim{{Name: "legacy"}, {Name: "modern", Path: "C:\\m.exe"}}
	for i := range want {
		if rec.ExtraShims[i] != want[i] {
			t.Errorf("entry %d: got %+v, want %+v", i, rec.ExtraShims[i], want[i])
		}
	}
}

func TestArchiveExt(t *testing.T) {
	// Compound extensions must win over filepath.Ext's last-dot rule,
	// or a .tar.gz gets treated as a bare .gz.
	tests := map[string]string{
		"x.zip":            ".zip",
		"x.tar.gz":         ".tar.gz",
		"x.tar.xz":         ".tar.xz",
		"x.TAR.GZ":         ".tar.gz",
		"x.7z":             ".7z",
		"Setup.exe":        ".exe",
		"no-extension":     "",
		"a.b.c.tar.bz2":    ".tar.bz2",
		"weird.tar.gz.txt": ".txt",
	}
	for in, want := range tests {
		if got := archiveExt(in); got != want {
			t.Errorf("archiveExt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBasenameWithoutQuery(t *testing.T) {
	tests := map[string]string{
		"https://h/a/b/file.zip":            "file.zip",
		"https://h/download?channel=stable": "download",
		"https://h/x/y/setup.exe?v=2":       "setup.exe",
	}
	for in, want := range tests {
		if got := basenameWithoutQuery(in); got != want {
			t.Errorf("basenameWithoutQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

// implicitHelpers is what makes goop bootstrap its own extraction
// tools. The .zip case is the one worth pinning: goop extracts those
// natively, so pulling in 7zip for them would be wrong.
func TestImplicitHelpers(t *testing.T) {
	tests := []struct {
		name string
		r    manifest.Resolved
		want []string
	}{
		{"plain zip needs nothing", manifest.Resolved{URLs: []string{"https://h/a.zip"}}, nil},
		{"tar.gz is native too", manifest.Resolved{URLs: []string{"https://h/a.tar.gz"}}, nil},
		{"tar.xz needs 7zip", manifest.Resolved{URLs: []string{"https://h/a.tar.xz"}}, []string{"7zip"}},
		{"7z needs 7zip", manifest.Resolved{URLs: []string{"https://h/a.7z"}}, []string{"7zip"}},
		{"innosetup needs innounp", manifest.Resolved{URLs: []string{"https://h/a.exe"}, InnoSetup: true}, []string{"innounp"}},
		{
			"script calling Expand-DarkArchive needs dark",
			manifest.Resolved{URLs: []string{"https://h/a.exe"}, Installer: manifest.InstallHook{Script: "Expand-DarkArchive $dir"}},
			[]string{"dark"},
		},
		{
			"script calling Expand-7zipArchive needs 7zip even for a zip url",
			manifest.Resolved{URLs: []string{"https://h/a.zip"}, PostInstall: "Expand-7zipArchive x"},
			[]string{"7zip"},
		},
		{
			"url fragment decides, not the url path",
			manifest.Resolved{URLs: []string{"https://h/download#/dl.7z"}},
			[]string{"7zip"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := implicitHelpers(tt.r)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestContainsString(t *testing.T) {
	stack := []string{"a", "b"}
	if !containsString(stack, "b") {
		t.Error("b is in the stack")
	}
	if containsString(stack, "c") {
		t.Error("c is not in the stack")
	}
	if containsString(nil, "a") {
		t.Error("nothing is in an empty stack")
	}
}

func TestHumanBytes(t *testing.T) {
	tests := map[int64]string{
		// Sub-KB sizes render as "0KB". Accepted rather than fixed: this
		// only ever labels an app version being cleaned up, which is
		// megabytes at minimum -- pinned here so the rounding is a
		// documented choice and not a silent surprise.
		512:             "0KB",
		1023:            "1KB",
		2 * 1024:        "2KB",
		5 * 1024 * 1024: "5MB",
		3 * 1073741824:  "3.0GB",
		1610612736:      "1.5GB",
	}
	for in, want := range tests {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
