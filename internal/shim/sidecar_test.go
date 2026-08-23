package shim

import (
	"errors"
	"strings"
	"testing"
)

func TestParseSidecar(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    Sidecar
		wantErr error
	}{
		{
			name: "path only",
			body: `path = "C:\Users\me\scoop\apps\git\current\bin\git.exe"`,
			want: Sidecar{Path: `C:\Users\me\scoop\apps\git\current\bin\git.exe`},
		},
		{
			name: "path and args",
			body: "path = \"C:\\tools\\java.exe\"\nargs = \"-jar app.jar\"\n",
			want: Sidecar{Path: `C:\tools\java.exe`, Args: "-jar app.jar"},
		},
		{
			name: "blank lines and comments ignored",
			body: "\n# comment\npath = \"C:\\a.exe\"\n\n",
			want: Sidecar{Path: `C:\a.exe`},
		},
		{
			name: "unquoted value tolerated",
			body: `path = C:\a.exe`,
			want: Sidecar{Path: `C:\a.exe`},
		},
		{
			name:    "missing path",
			body:    `args = "-x"`,
			wantErr: ErrSidecarMissingPath,
		},
		{
			name:    "empty file",
			body:    ``,
			wantErr: ErrSidecarMissingPath,
		},
		{
			name:    "malformed line",
			body:    "not-an-assignment",
			wantErr: nil, // just check it's non-nil below
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSidecar(strings.NewReader(tt.body))
			if tt.name == "malformed line" {
				if err == nil {
					t.Fatalf("expected error for malformed line, got nil")
				}
				return
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("got err %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSidecarPath(t *testing.T) {
	tests := map[string]string{
		`C:\shims\git.exe`:    `C:\shims\git.shim`,
		`C:\shims\node`:       `C:\shims\node.shim`,
		`C:\shims\a.b.c.exe`:  `C:\shims\a.b.c.shim`,
		`git.exe`:             `git.shim`,
		`C:\dir.with.dots\x`:  `C:\dir.with.dots\x.shim`,
	}
	for in, want := range tests {
		if got := SidecarPath(in); got != want {
			t.Errorf("SidecarPath(%q) = %q, want %q", in, got, want)
		}
	}
}
