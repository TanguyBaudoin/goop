// Package auth implements FR-30 through FR-35: per-host HTTP
// authentication, resolved env-var-first then Credential Manager
// (FR-33), injected only as an Authorization header on requests to
// that exact host -- never written into a manifest, never put in a URL
// (FR-30, FR-35). This is the architecture invariant from the spec:
// auth is a transport-layer concern the downloader knows nothing about.
package auth

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/credstore"
)

// EnvVarName maps a host to the environment variable that can override
// its stored credential, e.g. "github.com" -> "GOOP_AUTH_GITHUB_COM".
// Its value must be "bearer:<token>" or "basic:<user>:<password>".
func EnvVarName(host string) string {
	var b strings.Builder
	b.WriteString("GOOP_AUTH_")
	for _, r := range strings.ToUpper(host) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// resolve finds the Authorization header value for host, per FR-33:
// environment variable, then Credential Manager, then anonymous
// (ok=false, err=nil).
func resolve(host string) (headerValue string, ok bool, err error) {
	if v := os.Getenv(EnvVarName(host)); v != "" {
		return headerFromSpec(v)
	}

	authType, username, secret, found, err := credstore.Get(host)
	if err != nil {
		return "", false, fmt.Errorf("read stored credential for %s: %w", host, err)
	}
	if !found {
		return "", false, nil
	}
	return headerFromParts(authType, username, secret)
}

// headerFromSpec parses "bearer:<token>" or "basic:<user>:<password>".
func headerFromSpec(spec string) (string, bool, error) {
	kind, rest, hasRest := strings.Cut(spec, ":")
	switch strings.ToLower(kind) {
	case "bearer":
		if !hasRest || rest == "" {
			return "", false, fmt.Errorf("malformed bearer auth spec, want bearer:<token>")
		}
		return "Bearer " + rest, true, nil
	case "basic":
		user, pass, hasPass := strings.Cut(rest, ":")
		if !hasRest || !hasPass || user == "" {
			return "", false, fmt.Errorf("malformed basic auth spec, want basic:<user>:<password>")
		}
		return basicHeader(user, pass), true, nil
	default:
		return "", false, fmt.Errorf("unknown auth type %q (want bearer or basic)", kind)
	}
}

func headerFromParts(authType, username, secret string) (string, bool, error) {
	switch authType {
	case "bearer":
		return "Bearer " + secret, true, nil
	case "basic":
		return basicHeader(username, secret), true, nil
	default:
		return "", false, fmt.Errorf("stored credential has unknown auth type %q", authType)
	}
}

func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// Transport wraps Base, injecting a per-host Authorization header. It's
// the only place in goop a credential value is ever read; every error
// path below names the host, never the resolved header/secret (FR-35).
type Transport struct {
	Base http.RoundTripper
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	headerValue, ok, err := resolve(req.URL.Hostname())
	if err != nil {
		return nil, fmt.Errorf("resolve auth for host %s: %w", req.URL.Hostname(), err)
	}
	if ok {
		// Clone rather than mutate: req may be reused/retried by a
		// caller that doesn't expect us to have altered it in place.
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", headerValue)
	}
	return base.RoundTrip(req)
}
