package maven

import (
	"fmt"
	"strings"

	"goop/internal/downloader"
)

// Resolve fetches coord's .sha1 sidecar from repoBase (published next to
// every real artifact on Maven Central and Artifactory alike) and returns
// the artifact's download URL plus its hash in the "algo:hexdigest"
// format downloader.Get expects.
func Resolve(repoBase string, coord Coordinate) (url, hash string, err error) {
	url = coord.URL(repoBase)
	sidecar, err := downloader.FetchText(url + ".sha1")
	if err != nil {
		return "", "", fmt.Errorf("fetch %s.sha1: %w", url, err)
	}

	// Some hosts (notably some Artifactory configs) format the sidecar as
	// "<hex>  <filename>" (BSD/GNU sha1sum(1) style) rather than bare hex
	// -- the digest is always the first whitespace-delimited field either
	// way.
	fields := strings.Fields(sidecar)
	if len(fields) == 0 {
		return "", "", fmt.Errorf("%s.sha1: empty sidecar", url)
	}
	return url, "sha1:" + fields[0], nil
}
