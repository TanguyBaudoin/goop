package downloader

import (
	"path/filepath"
	"testing"
)

// Expectations go through filepath.FromSlash so the test states the
// shape of the path rather than a pile of backslash escapes.
func TestFileURLToPath(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		// Drive-letter local path.
		{"file:///C:/tools/jdk.zip", filepath.FromSlash("C:/tools/jdk.zip")},
		// Standard UNC form: the authority is the server, not a path
		// segment. Slicing the string turned this into a relative path.
		{"file://server/share/jdk.zip", filepath.FromSlash("//server/share/jdk.zip")},
		// Percent-encoding is decoded, so shares with spaces work.
		{"file:///C:/Program%20Files/jdk.zip", filepath.FromSlash("C:/Program Files/jdk.zip")},
		{"file://build-srv/Tool%20Chain/gcc.zip", filepath.FromSlash("//build-srv/Tool Chain/gcc.zip")},
		// Four-slash spelling some tools emit still resolves to UNC.
		{"file:////server/share/jdk.zip", filepath.FromSlash("//server/share/jdk.zip")},
		// "localhost" means here, not a server of that name.
		{"file://localhost/C:/tools/jdk.zip", filepath.FromSlash("C:/tools/jdk.zip")},
	}
	for _, c := range cases {
		got, err := fileURLToPath(c.url)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.url, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.url, got, c.want)
		}
	}
}

func TestFileURLToPath_RejectsNonFile(t *testing.T) {
	if _, err := fileURLToPath("https://example.com/x.zip"); err == nil {
		t.Error("expected an error for a non-file URL")
	}
}

// A drive-letter path exists only on the machine that wrote it; a UNC
// path resolves identically from any host that can reach the share.
// Both export paths warn on the former and stay quiet on the latter.
func TestIsMachineLocalFileURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"file:///C:/tools/jdk.zip", true},
		{"file://localhost/C:/tools/jdk.zip", true},
		{"file://server/share/jdk.zip", false},
		{"file:////server/share/jdk.zip", false},
		{"https://example.com/jdk.zip", false},
	}
	for _, c := range cases {
		if got := IsMachineLocalFileURL(c.url); got != c.want {
			t.Errorf("IsMachineLocalFileURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}
