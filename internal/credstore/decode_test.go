package credstore

import (
	"syscall"
	"testing"
)

// utf16LE encodes the way Windows' Credential Manager stores a secret it
// wrote itself.
func utf16LE(s string, bom bool) []byte {
	units, err := syscall.UTF16FromString(s)
	if err != nil {
		panic(err)
	}
	units = units[:len(units)-1] // drop the terminating NUL
	var out []byte
	if bom {
		out = append(out, 0xFF, 0xFE)
	}
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// These run without touching the real Credential Manager, so the rule
// that decides UTF-8 from UTF-16 is covered on every test run rather
// than only under GOOP_TEST_CREDSTORE.
func TestDecodeSecret(t *testing.T) {
	cases := []struct {
		name string
		blob []byte
		want string
	}{
		// What goop writes. Must pass through untouched.
		{"utf8 ascii", []byte("hunter2"), "hunter2"},
		{"utf8 punctuation", []byte(`p@ss:w/rd!#$%&`), `p@ss:w/rd!#$%&`},
		{"utf8 accented", []byte("mot-de-passe-éàü"), "mot-de-passe-éàü"},
		{"utf8 cjk", []byte("密码token"), "密码token"},
		{"utf8 single byte", []byte("a"), "a"},
		{"empty", []byte{}, ""},

		// What the Credential Manager writes after somebody edits the
		// password by hand. Interleaved NULs for anything with an ASCII
		// character in it.
		{"utf16 ascii", utf16LE("changed-password", false), "changed-password"},
		{"utf16 with BOM", utf16LE("changed-password", true), "changed-password"},
		{"utf16 accented", utf16LE("mot-de-passe-éàü", false), "mot-de-passe-éàü"},
		{"utf16 single char", utf16LE("a", false), "a"},
		// No NUL bytes at all: caught because the byte pairs are not
		// valid UTF-8, which is the half a NUL-only test would miss.
		{"utf16 cjk only", utf16LE("密码", false), "密码"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decodeSecret(tc.blob); got != tc.want {
				t.Errorf("decodeSecret(%q) = %q, want %q", tc.blob, got, tc.want)
			}
		})
	}
}

// An odd-length blob is neither UTF-8 text nor UTF-16. Returning the raw
// bytes beats inventing a decode: whoever reports the failed login is
// then holding what is actually stored.
func TestDecodeSecret_OddLengthIsHandedBack(t *testing.T) {
	blob := []byte{0x61, 0x00, 0x62} // "a\0b": has a NUL, odd length
	if got := decodeSecret(blob); got != string(blob) {
		t.Errorf("decodeSecret = %q, want the raw bytes back", got)
	}
}

// The type lives in Comment, which an external edit erases. A username
// is the distinguishing fact, and the UI keeps it -- goop only ever sets
// one for "basic".
func TestInferAuthType(t *testing.T) {
	if got := inferAuthType("alice"); got != "basic" {
		t.Errorf("with a username: %q, want basic", got)
	}
	if got := inferAuthType(""); got != "bearer" {
		t.Errorf("without a username: %q, want bearer", got)
	}
}
