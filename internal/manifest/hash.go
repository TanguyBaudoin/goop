package manifest

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
	"strings"
)

// ParsedHash is a manifest hash entry split into its algorithm and hex
// digest, e.g. "sha256:abcd..." or a bare "abcd..." (FR-40).
type ParsedHash struct {
	Algo   string // "md5", "sha1", "sha256", or "sha512"
	Digest string // lowercase hex
}

// ParseHash decodes a manifest hash string. A bare hex string (no
// "algo:" prefix) defaults to sha256, matching current Scoop convention.
func ParseHash(s string) (ParsedHash, error) {
	algo, digest := "sha256", s
	if i := strings.IndexByte(s, ':'); i >= 0 {
		algo, digest = strings.ToLower(s[:i]), s[i+1:]
	}
	switch algo {
	case "md5", "sha1", "sha256", "sha512":
	default:
		return ParsedHash{}, fmt.Errorf("unsupported hash algorithm %q", algo)
	}
	digest = strings.ToLower(digest)
	for _, c := range digest {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return ParsedHash{}, fmt.Errorf("hash digest %q is not valid hex", digest)
		}
	}
	return ParsedHash{Algo: algo, Digest: digest}, nil
}

// NewHasher returns a hash.Hash implementing p's algorithm.
func (p ParsedHash) NewHasher() (hash.Hash, error) {
	switch p.Algo {
	case "md5":
		return md5.New(), nil
	case "sha1":
		return sha1.New(), nil
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("unsupported hash algorithm %q", p.Algo)
	}
}
