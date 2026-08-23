package vercmp

import "testing"

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.2.4", "1.2.3", 1},
		{"1.2", "1.2.1", -1},        // fewer components sorts first
		{"1.10.0", "1.9.0", 1},      // numeric compare, not lexicographic ("10" > "9")
		{"704", "705", -1},          // real less-Windows-style bare version
		{"15859902", "9000000", 1},  // real android-clt-style build number
		{"2026.1.3.7", "2026.1.4.0", -1}, // real android-studio-style version
		{"10.4.2", "10.4.10", -1},   // numeric, not string, comparison of "2" vs "10"
		{"1.8.2", "1.8.2", 0},
	}
	for _, tt := range tests {
		if got := Compare(tt.a, tt.b); got != tt.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSatisfies(t *testing.T) {
	tests := []struct {
		version, constraint string
		want                bool
	}{
		{version: "1.8.2", constraint: "1.8.2", want: true},
		{version: "1.8.2", constraint: "1.8.3", want: false},
		{version: "1.8.2", constraint: ">=1.5.0", want: true},
		{version: "1.4.0", constraint: ">=1.5.0", want: false},
		{version: "1.8.2", constraint: ">1.8.2", want: false},
		{version: "1.8.2", constraint: ">=1.8.2", want: true},
		{version: "1.8.2", constraint: "<2.0.0", want: true},
		{version: "2.0.0", constraint: "<2.0.0", want: false},
		{version: "1.8.2", constraint: "<=1.8.2", want: true},
		{version: "1.8.2", constraint: "!=1.8.3", want: true},
		{version: "1.8.2", constraint: "!=1.8.2", want: false},
		{version: "1.8.2", constraint: "==1.8.2", want: true},
		{version: "704", constraint: ">=700", want: true},
		{version: "1.8.2", constraint: ""},      // empty constraint -> error, handled below
		{version: "1.8.2", constraint: "~>1.0"}, // no recognized operator prefix -> exact-match parse, handled below
	}
	for _, tt := range tests {
		got, err := Satisfies(tt.version, tt.constraint)
		if tt.constraint == "" {
			if err == nil {
				t.Errorf("Satisfies(%q, \"\") expected error", tt.version)
			}
			continue
		}
		if tt.constraint == "~>1.0" {
			// "~>1.0" has no recognized operator prefix, so the whole
			// string is treated as the version to exact-match against --
			// documenting actual behavior, not asserting it's ideal.
			if got != false {
				t.Errorf("Satisfies(%q, %q) = %v, want false (no exact match)", tt.version, tt.constraint, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Satisfies(%q, %q) unexpected error: %v", tt.version, tt.constraint, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Satisfies(%q, %q) = %v, want %v", tt.version, tt.constraint, got, tt.want)
		}
	}
}
