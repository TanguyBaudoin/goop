package manifest

import "testing"

func TestParseSpec(t *testing.T) {
	tests := []struct {
		in   string
		want Spec
	}{
		{"jq", Spec{Name: "jq"}},
		{"extras/86box", Spec{Bucket: "extras", Name: "86box"}},
		{"jq@1.8.2", Spec{Name: "jq", Constraint: "1.8.2"}},
		{"jq@>=1.5.0", Spec{Name: "jq", Constraint: ">=1.5.0"}},
		{"extras/mpv@>=0.40", Spec{Bucket: "extras", Name: "mpv", Constraint: ">=0.40"}},
	}
	for _, tt := range tests {
		got := ParseSpec(tt.in)
		if got != tt.want {
			t.Errorf("ParseSpec(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestSpec_String(t *testing.T) {
	tests := []struct {
		in   Spec
		want string
	}{
		{Spec{Name: "jq"}, "jq"},
		{Spec{Bucket: "extras", Name: "86box"}, "extras/86box"},
		{Spec{Name: "jq", Constraint: "1.8.2"}, "jq@1.8.2"},
		{Spec{Bucket: "extras", Name: "mpv", Constraint: ">=0.40"}, "extras/mpv@>=0.40"},
	}
	for _, tt := range tests {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("%+v.String() = %q, want %q", tt.in, got, tt.want)
		}
	}
}
