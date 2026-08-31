package downloader

import (
	"context"
	"fmt"
	"net/http"
)

// RedirectTarget reports where rawURL redirects to, without following
// the redirect or fetching a body.
//
// GitHub's `/releases/latest/download/<asset>` answers with a redirect
// to `/releases/download/<tag>/<asset>`, so the tag -- and with it the
// version about to be installed -- is in that one header. Reading it
// costs a request with no body and, unlike the releases API, is not rate
// limited: that endpoint allows 60 unauthenticated requests per hour per
// IP, shared by everyone behind a corporate NAT.
//
// Returns an empty string when rawURL does not redirect. A caller that
// only wants the information as a courtesy should treat any error the
// same way.
func RedirectTarget(rawURL string) (string, error) {
	if IsFileURL(rawURL) {
		return "", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), overallTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return "", err
	}

	// Same transport as everything else -- proxy and per-host credentials
	// apply -- but this one request must not follow the redirect, since
	// the redirect is the answer.
	noFollow := &http.Client{
		Transport:     httpClient.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := noFollow.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		if resp.StatusCode >= 400 {
			return "", fmt.Errorf("%s: %s", rawURL, resp.Status)
		}
		return "", nil
	}
	return resp.Header.Get("Location"), nil
}
