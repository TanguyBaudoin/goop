// Package minisign verifies minisign signatures (FR-41: signature
// verification when a manifest or bucket provides one; A5 provenance).
// Verification only -- goop never holds a secret key, only checks
// signatures third parties (a bucket maintainer, or goop's own future
// release process) already produced. minisign was chosen over
// Authenticode because it needs no CA/purchased certificate to start
// using today, and over Sigstore because it needs no supporting
// infrastructure (transparency log, OIDC) -- a keypair and a signed
// file are enough. Format: https://jedisct1.github.io/minisign/
package minisign

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// PublicKey is a decoded minisign public key.
type PublicKey struct {
	KeyID [8]byte
	Key   ed25519.PublicKey
}

// ParsePublicKey decodes a minisign public key file's contents (the
// "untrusted comment:" line followed by a base64 line).
func ParsePublicKey(data string) (PublicKey, error) {
	line, err := lastNonEmptyLine(data)
	if err != nil {
		return PublicKey{}, fmt.Errorf("parse public key: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil {
		return PublicKey{}, fmt.Errorf("parse public key: decode base64: %w", err)
	}
	if len(raw) != 42 {
		return PublicKey{}, fmt.Errorf("parse public key: want 42 bytes, got %d", len(raw))
	}
	if string(raw[0:2]) != "Ed" {
		return PublicKey{}, fmt.Errorf("parse public key: unsupported algorithm %q (only Ed25519 \"Ed\" is supported)", raw[0:2])
	}
	var pk PublicKey
	copy(pk.KeyID[:], raw[2:10])
	pk.Key = ed25519.PublicKey(append([]byte(nil), raw[10:42]...))
	return pk, nil
}

// Signature is a decoded minisign .minisig file.
type Signature struct {
	Algorithm      [2]byte // "Ed" (legacy, signs the raw message) or "ED" (signs a BLAKE2b-512 hash of it)
	KeyID          [8]byte
	Bytes          [64]byte // the Ed25519 signature itself
	TrustedComment string
	GlobalSig      [64]byte // signs (Algorithm+KeyID+Bytes) || TrustedComment, binding the comment to the signature
}

// ParseSignature decodes a .minisig file's contents: an untrusted
// comment line, a base64 signature line, a trusted comment line, and a
// base64 global-signature line.
func ParseSignature(data string) (Signature, error) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimRight(data, "\n"), "\r\n", "\n"), "\n")
	if len(lines) != 4 {
		return Signature{}, fmt.Errorf("parse signature: expected 4 lines, got %d", len(lines))
	}
	if !strings.HasPrefix(lines[0], "untrusted comment:") {
		return Signature{}, fmt.Errorf("parse signature: line 1 must start with \"untrusted comment:\"")
	}
	sigRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil {
		return Signature{}, fmt.Errorf("parse signature: decode signature base64: %w", err)
	}
	if len(sigRaw) != 74 {
		return Signature{}, fmt.Errorf("parse signature: want 74 bytes, got %d", len(sigRaw))
	}
	const trustedPrefix = "trusted comment:"
	if !strings.HasPrefix(lines[2], trustedPrefix) {
		return Signature{}, fmt.Errorf("parse signature: line 3 must start with %q", trustedPrefix)
	}
	globalRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3]))
	if err != nil {
		return Signature{}, fmt.Errorf("parse signature: decode global signature base64: %w", err)
	}
	if len(globalRaw) != 64 {
		return Signature{}, fmt.Errorf("parse signature: want global signature of 64 bytes, got %d", len(globalRaw))
	}

	var sig Signature
	copy(sig.Algorithm[:], sigRaw[0:2])
	copy(sig.KeyID[:], sigRaw[2:10])
	copy(sig.Bytes[:], sigRaw[10:74])
	sig.TrustedComment = strings.TrimPrefix(lines[2], trustedPrefix)
	sig.TrustedComment = strings.TrimPrefix(sig.TrustedComment, " ")
	copy(sig.GlobalSig[:], globalRaw)
	return sig, nil
}

// VerifyFile checks that fileData was signed by pub according to sig,
// including that sig's trusted comment wasn't tampered with.
func VerifyFile(pub PublicKey, fileData []byte, sig Signature) error {
	if sig.KeyID != pub.KeyID {
		return fmt.Errorf("signature was made with a different key (key ID mismatch)")
	}

	var msg []byte
	switch string(sig.Algorithm[:]) {
	case "ED":
		h := blake2b.Sum512(fileData)
		msg = h[:]
	case "Ed":
		msg = fileData
	default:
		return fmt.Errorf("unsupported signature algorithm %q", sig.Algorithm)
	}
	if !ed25519.Verify(pub.Key, msg, sig.Bytes[:]) {
		return fmt.Errorf("signature verification failed: file does not match")
	}

	// global_signature = Ed25519(<signature> || <trusted_comment>) --
	// just the raw 64-byte signature, not the algorithm+keyID prefix.
	globalMsg := make([]byte, 0, 64+len(sig.TrustedComment))
	globalMsg = append(globalMsg, sig.Bytes[:]...)
	globalMsg = append(globalMsg, []byte(sig.TrustedComment)...)
	if !ed25519.Verify(pub.Key, globalMsg, sig.GlobalSig[:]) {
		return fmt.Errorf("signature verification failed: trusted comment does not match (possible tampering)")
	}
	return nil
}

func lastNonEmptyLine(data string) (string, error) {
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line, nil
		}
	}
	return "", fmt.Errorf("empty input")
}
