package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// FetchText downloads url's body as text through the same authenticated,
// proxied client as Get, for small text content (e.g. a Maven .sha1
// sidecar) that doesn't need to touch disk.
func FetchText(rawURL string) (string, error) {
	if strings.HasPrefix(rawURL, "file://") {
		src, err := fileURLToPath(rawURL)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), overallTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
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
