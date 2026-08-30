package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// maintainerOnlyFields are excluded from a manifest's digest. They drive
// how a bucket maintainer *produces* new manifest versions and have no
// effect on what installing a pinned version does, so a maintainer
// retouching an autoupdate template would otherwise turn every check red
// for no reason.
var maintainerOnlyFields = []string{"checkver", "autoupdate"}

// Digest is a stable fingerprint of what a manifest will actually do:
// its URLs and hashes, its bin entries, every install and uninstall
// script, shortcuts, environment changes, persisted paths, and the
// per-architecture overrides of all of those.
//
// The point is that a manifest is executable content. An artifact hash
// says the payload is unchanged; it says nothing about a post_install
// that has been edited since. This covers both, because the artifact
// hash is itself part of the manifest.
//
// Computed by decoding and re-encoding rather than hashing the file, so
// formatting cannot affect it. That matters concretely: a bucket cloned
// with core.autocrlf=true has CRLF on every line while the same bucket
// fetched as a zip has LF, and goop uses both -- hashing raw bytes would
// report drift between two machines holding identical manifests. Go
// marshals map keys sorted at every level and emits no whitespace, so
// indentation and key order fall out too.
//
// Safe against the usual round-trip hazard: across all 1640 manifests in
// ScoopInstaller/Main there is not one numeric value, so nothing depends
// on how a number is rendered.
func Digest(raw []byte) (string, error) {
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", fmt.Errorf("digest manifest: %w", err)
	}
	for _, f := range maintainerOnlyFields {
		delete(v, f)
	}
	canon, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("digest manifest: %w", err)
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
