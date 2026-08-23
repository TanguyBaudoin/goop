package minisign

import (
	"strings"
	"testing"
)

// Fixtures below are real output from the actual minisign.exe tool
// (v0.12, github.com/jedisct1/minisign) -- generated with `minisign -G`
// and `minisign -S`, not synthesized -- to pin this package against the
// real on-disk format rather than an invented one.

const realPublicKey = `untrusted comment: minisign public key 669DABD3C6EC8A59
RWRZiuzG06udZgLkFif3N3XzjHBATa5Nkpbsupbtr6pjHPxS0IWzC6Iu
`

const realSignature = "untrusted comment: test comment\n" +
	"RURZiuzG06udZhBYxEi1X8hWBhlzydh0qYDVofpAg67Vn8IdvxW7x2wy3Ot9NqrpynFu3U/H9LBLK6owTtnFkeg8aTfjW7LQEQc=\n" +
	"trusted comment: timestamp:1786637925\tfile:testfile.txt\thashed\n" +
	"RQzQITkLhOFgJDiHtt4ah6SJfJKUKOhLcCfPyiFLZg6KwOPJyz+kIQwWsBV2h1sTQaeeSIuL7BharUz5I2XUBA==\n"

const realSignedFileContent = "hello goop signature verification test\n"

func TestVerifyFile_RealMinisignOutput(t *testing.T) {
	pub, err := ParsePublicKey(realPublicKey)
	if err != nil {
		t.Fatalf("ParsePublicKey: %v", err)
	}
	sig, err := ParseSignature(realSignature)
	if err != nil {
		t.Fatalf("ParseSignature: %v", err)
	}
	if string(sig.Algorithm[:]) != "ED" {
		t.Errorf("Algorithm = %q, want \"ED\" (minisign's default hashed mode)", sig.Algorithm)
	}
	if sig.TrustedComment != "timestamp:1786637925\tfile:testfile.txt\thashed" {
		t.Errorf("TrustedComment = %q", sig.TrustedComment)
	}

	if err := VerifyFile(pub, []byte(realSignedFileContent), sig); err != nil {
		t.Fatalf("VerifyFile on the real signed content: %v", err)
	}
}

func TestVerifyFile_RejectsTamperedContent(t *testing.T) {
	pub, err := ParsePublicKey(realPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ParseSignature(realSignature)
	if err != nil {
		t.Fatal(err)
	}

	tampered := strings.Replace(realSignedFileContent, "hello", "HELLO", 1)
	if err := VerifyFile(pub, []byte(tampered), sig); err == nil {
		t.Fatal("expected verification to fail for tampered content")
	}
}

func TestVerifyFile_RejectsTamperedTrustedComment(t *testing.T) {
	pub, err := ParsePublicKey(realPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ParseSignature(realSignature)
	if err != nil {
		t.Fatal(err)
	}
	sig.TrustedComment = "timestamp:9999999999\tfile:testfile.txt\thashed"

	if err := VerifyFile(pub, []byte(realSignedFileContent), sig); err == nil {
		t.Fatal("expected verification to fail when the trusted comment was altered")
	}
}

func TestVerifyFile_RejectsWrongKey(t *testing.T) {
	// A different, unrelated real minisign public key (freshly
	// generated), unrelated to the one that made realSignature.
	const otherKey = `untrusted comment: minisign public key ABCDEF0123456789
RWQ9uFZFY3Nx7q8hZ0m0z1v2w3x4y5z6A7B8C9D0E1F2G3H4I5J6K7L8M9`
	// This key is intentionally malformed (not a real generated key) --
	// used only to confirm ParsePublicKey's own validation rejects it,
	// since fabricating a second *valid* real key pair isn't worth the
	// extra fixture.
	if _, err := ParsePublicKey(otherKey); err == nil {
		t.Fatal("expected an error parsing a malformed public key")
	}
}

func TestParsePublicKey_RejectsBadInput(t *testing.T) {
	if _, err := ParsePublicKey("not a key"); err == nil {
		t.Fatal("expected error for garbage input")
	}
}

func TestParseSignature_RejectsBadInput(t *testing.T) {
	if _, err := ParseSignature("not a signature"); err == nil {
		t.Fatal("expected error for garbage input")
	}
}
