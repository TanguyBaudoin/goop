package downloader

import (
	"net/http"
	"net/url"

	"github.com/TanguyBaudoin/goop/internal/paths"
)

// resolveProxy picks the proxy for req, in order: the standard
// HTTP_PROXY/HTTPS_PROXY/NO_PROXY environment variables (via Go's own
// http.ProxyFromEnvironment, so CIDR/wildcard NO_PROXY entries and
// proxy-embedded credentials all work exactly as they would for any
// other Go program); else goop's own persisted proxy (`goop config
// set-proxy`), applied to both http:// and https:// targets alike and
// skipped for hosts matching `goop config set-no-proxy`; else no proxy.
func resolveProxy(req *http.Request) (*url.URL, error) {
	if paths.EnvProxyConfigured() {
		return http.ProxyFromEnvironment(req)
	}
	if p := paths.ProxyFor(req.URL.Hostname()); p != "" {
		return url.Parse(p)
	}
	return nil, nil
}
