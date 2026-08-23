package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// FetchText downloads url's body as text through the same authenticated,
// proxied client as Get, for small text content (e.g. a Maven .sha1
// sidecar) that doesn't need to touch disk.
func FetchText(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), overallTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
