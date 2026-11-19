package installer

import (
	"strings"
	"testing"
)

// normalizeRoot decides, in the two-root reproducibility check, whether a
// file that differs between two roots differs *only* by where it was
// installed. That makes it the one function in that test which can turn a
// real failure into a pass: too eager, and it would launder genuine
// nondeterminism into "expected". So it gets tested on its own, without
// the network, rather than trusted because the big test went green.

func TestNormalizeRoot_EqualisesTheSamePathUnderTwoRoots(t *testing.T) {
	rootA := `C:\Temp\TestA001\root`
	rootB := `D:\other\TestB002\root`

	cases := []struct {
		name     string
		a, b     string
		wantSame bool
	}{
		{
			name:     "a shim sidecar naming its target",
			a:        "path = \"" + rootA + "\\apps\\jq\\1.7.1\\jq.exe\"\n",
			b:        "path = \"" + rootB + "\\apps\\jq\\1.7.1\\jq.exe\"\n",
			wantSame: true,
		},
		{
			name:     "forward slashes on one side",
			a:        rootA + "/apps/jq/current",
			b:        strings.ReplaceAll(rootB, `\`, "/") + "/apps/jq/current",
			wantSame: true,
		},
		{
			name:     "a script echoed the path back in a different case",
			a:        strings.ToUpper(rootA) + `\apps\7zip\current`,
			b:        strings.ToLower(rootB) + `\apps\7zip\current`,
			wantSame: true,
		},
		{
			name:     "a JSON-encoded path, separators doubled",
			a:        `{"dir":"` + strings.ReplaceAll(rootA, `\`, `\\`) + `\\apps"}`,
			b:        `{"dir":"` + strings.ReplaceAll(rootB, `\`, `\\`) + `\\apps"}`,
			wantSame: true,
		},

		// The cases that matter: a difference that is NOT the root must
		// survive normalisation.
		{
			name:     "different versions under the same root shape",
			a:        rootA + `\apps\jq\1.7.1\jq.exe`,
			b:        rootB + `\apps\jq\1.6\jq.exe`,
			wantSame: false,
		},
		{
			name:     "a generated config with a timestamp in it",
			a:        "installed=2026-01-01\npath=" + rootA,
			b:        "installed=2026-06-30\npath=" + rootB,
			wantSame: false,
		},
		{
			name:     "same text, one byte of payload different",
			a:        rootA + `\apps\x` + "\nseed=41",
			b:        rootB + `\apps\x` + "\nseed=42",
			wantSame: false,
		},
		{
			name:     "a path that merely starts like the root",
			a:        rootA + `-backup\apps\jq`,
			b:        rootB + `\apps\jq`,
			wantSame: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			na := string(normalizeRoot([]byte(tc.a), rootA))
			nb := string(normalizeRoot([]byte(tc.b), rootB))
			if got := na == nb; got != tc.wantSame {
				t.Errorf("normalised equality = %v, want %v\n  A: %q -> %q\n  B: %q -> %q",
					got, tc.wantSame, tc.a, na, tc.b, nb)
			}
		})
	}
}

// PowerShell 5.1 writes text files as UTF-16LE, and it is PowerShell that
// writes shim sidecars and whatever a post_install script produces. A
// normaliser that only understood UTF-8 would report every one of those
// as genuinely different, which would bury the real signal.
func TestNormalizeRoot_HandlesUTF16LE(t *testing.T) {
	rootA := `C:\Temp\A\root`
	rootB := `C:\Temp\BBBB\root`

	a := utf16le("path = \"" + rootA + "\\apps\\jq\\jq.exe\"")
	b := utf16le("path = \"" + rootB + "\\apps\\jq\\jq.exe\"")
	if string(normalizeRoot(a, rootA)) != string(normalizeRoot(b, rootB)) {
		t.Error("two UTF-16LE sidecars naming the same relative target must normalise equal")
	}

	// And it must still distinguish a real difference in UTF-16LE.
	c := utf16le("path = \"" + rootB + "\\apps\\jq\\other.exe\"")
	if string(normalizeRoot(a, rootA)) == string(normalizeRoot(c, rootB)) {
		t.Error("UTF-16LE sidecars naming different targets must not normalise equal")
	}
}

// Nothing may be replaced when the root does not occur at all: a file
// with no path in it must come through byte-identical, or the normaliser
// is corrupting content it was only meant to inspect.
func TestNormalizeRoot_LeavesUnrelatedContentAlone(t *testing.T) {
	root := `C:\Temp\A\root`
	for _, body := range []string{
		"",
		"plain text with no path",
		`{"version":"1.7.1","hash":"abc"}`,
		`C:\Windows\System32\cmd.exe`,
	} {
		if got := string(normalizeRoot([]byte(body), root)); got != body {
			t.Errorf("normalizeRoot changed unrelated content:\n  in:  %q\n  out: %q", body, got)
		}
	}
}

// rootSpellings must try the longest form first. If a shorter spelling
// ran first it could consume the prefix of a longer one and leave a tail
// behind, producing two different normalisations of the same path.
func TestRootSpellings_LongestFirst(t *testing.T) {
	got := rootSpellings(`C:\Temp\A\root`)
	if len(got) < 2 {
		t.Fatalf("expected several spellings, got %v", got)
	}
	for i := 1; i < len(got); i++ {
		if len(got[i]) > len(got[i-1]) {
			t.Errorf("spelling %d (%q, %d bytes) is longer than the one before it (%q, %d bytes)",
				i, got[i], len(got[i]), got[i-1], len(got[i-1]))
		}
	}
	// The doubled-separator form is what makes a receipt comparable, so
	// it must be among them.
	wantDoubled := `C:\\Temp\\A\\root`
	found := false
	for _, s := range got {
		if s == wantDoubled {
			found = true
		}
	}
	if !found {
		t.Errorf("the JSON-escaped spelling %q is missing from %v", wantDoubled, got)
	}
}
