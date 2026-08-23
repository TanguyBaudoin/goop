package archive

import "testing"

// trimDirPrefix is what makes extract_dir tolerate the case mismatches
// real manifests carry: extras/bleachbit.json asks for
// "BleachBit-portable" while the zip actually ships
// "BleachBit-Portable", and a byte-exact comparison rejected the whole
// archive with "matched no entries".
func TestTrimDirPrefix(t *testing.T) {
	tests := []struct {
		name    string
		entry   string
		prefix  string
		want    string
		wantHit bool
	}{
		{"no prefix passes everything through", "a/b.txt", "", "a/b.txt", true},
		{"exact match", "dir/b.txt", "dir/", "b.txt", true},
		{"case-insensitive match", "BleachBit-Portable/b.txt", "BleachBit-portable/", "b.txt", true},
		{"entry outside the prefix", "other/b.txt", "dir/", "", false},
		{"the prefix entry itself yields nothing", "dir", "dir/", "", true},
		{"a longer sibling name is not a match", "dirextra/b.txt", "dir/", "", false},
		{"nested path keeps its own casing", "Dir/Sub/File.TXT", "dir/", "Sub/File.TXT", true},
		{"entry shorter than the prefix", "d", "dir/", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := trimDirPrefix(tt.entry, tt.prefix)
			if ok != tt.wantHit {
				t.Fatalf("trimDirPrefix(%q, %q) matched = %v, want %v", tt.entry, tt.prefix, ok, tt.wantHit)
			}
			if ok && got != tt.want {
				t.Errorf("trimDirPrefix(%q, %q) = %q, want %q", tt.entry, tt.prefix, got, tt.want)
			}
		})
	}
}
