package selfupdate

import "testing"

func TestParseChecksum(t *testing.T) {
	// The shape the release workflow writes.
	const good = "cd952d94395ce5deca78a1958f9658e997a3ad232656adf24cbf138dcb197ab2  goop.exe\n"
	got, err := parseChecksum(good)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "cd952d94395ce5deca78a1958f9658e997a3ad232656adf24cbf138dcb197ab2"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseChecksum_PicksTheRightLine(t *testing.T) {
	body := "1111111111111111111111111111111111111111111111111111111111111111  other.zip\n" +
		"2222222222222222222222222222222222222222222222222222222222222222  goop.exe\n"
	got, err := parseChecksum(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0] != '2' {
		t.Errorf("picked the wrong line: %q", got)
	}
}

func TestParseChecksum_Rejects(t *testing.T) {
	cases := map[string]string{
		"no goop.exe line": "abc  something-else.zip\n",
		"truncated hash":   "abc123  goop.exe\n",
		"empty":            "",
		"hash but no name": "cd952d94395ce5deca78a1958f9658e997a3ad232656adf24cbf138dcb197ab2\n",
	}
	for name, body := range cases {
		if _, err := parseChecksum(body); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
