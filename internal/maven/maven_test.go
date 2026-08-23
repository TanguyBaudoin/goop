package maven

import "testing"

func TestParseCoordinate(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Coordinate
		wantErr bool
	}{
		{
			name: "with classifier",
			in:   "org.apache.maven:apache-maven:3.9.9:bin:zip",
			want: Coordinate{GroupID: "org.apache.maven", ArtifactID: "apache-maven", Version: "3.9.9", Classifier: "bin", Packaging: "zip"},
		},
		{
			name: "empty classifier",
			in:   "org.foo:tool:1.0::zip",
			want: Coordinate{GroupID: "org.foo", ArtifactID: "tool", Version: "1.0", Classifier: "", Packaging: "zip"},
		},
		{name: "too few fields", in: "org.foo:tool:1.0", wantErr: true},
		{name: "too many fields", in: "org.foo:tool:1.0:a:b:c", wantErr: true},
		{name: "empty groupId", in: ":tool:1.0::zip", wantErr: true},
		{name: "empty packaging", in: "org.foo:tool:1.0::", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCoordinate(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseCoordinate(%q) = %+v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCoordinate(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseCoordinate(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestCoordinate_Filename(t *testing.T) {
	c := Coordinate{GroupID: "org.apache.maven", ArtifactID: "apache-maven", Version: "3.9.9", Classifier: "bin", Packaging: "zip"}
	if got, want := c.Filename(), "apache-maven-3.9.9-bin.zip"; got != want {
		t.Errorf("Filename() = %q, want %q", got, want)
	}

	noClassifier := Coordinate{GroupID: "org.foo", ArtifactID: "tool", Version: "1.0", Packaging: "jar"}
	if got, want := noClassifier.Filename(), "tool-1.0.jar"; got != want {
		t.Errorf("Filename() (no classifier) = %q, want %q", got, want)
	}
}

func TestCoordinate_URL(t *testing.T) {
	c := Coordinate{GroupID: "org.apache.maven", ArtifactID: "apache-maven", Version: "3.9.9", Classifier: "bin", Packaging: "zip"}
	want := "https://repo1.maven.org/maven2/org/apache/maven/apache-maven/3.9.9/apache-maven-3.9.9-bin.zip"
	if got := c.URL("https://repo1.maven.org/maven2"); got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
	// A trailing slash on repoBase must not produce a doubled slash.
	if got := c.URL("https://repo1.maven.org/maven2/"); got != want {
		t.Errorf("URL() with trailing slash = %q, want %q", got, want)
	}
}

func TestSplitSpec(t *testing.T) {
	tests := []struct {
		in        string
		wantRepo  string
		wantCoord string
	}{
		{in: "internal/org.foo:tool:1.0::zip", wantRepo: "internal", wantCoord: "org.foo:tool:1.0::zip"},
		{in: "org.foo:tool:1.0::zip", wantRepo: "", wantCoord: "org.foo:tool:1.0::zip"},
	}
	for _, tt := range tests {
		repo, coord := SplitSpec(tt.in)
		if repo != tt.wantRepo || coord != tt.wantCoord {
			t.Errorf("SplitSpec(%q) = (%q, %q), want (%q, %q)", tt.in, repo, coord, tt.wantRepo, tt.wantCoord)
		}
	}
}
