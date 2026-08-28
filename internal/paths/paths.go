// Package paths centralizes goop's on-disk layout so every other package
// agrees on where things live (D4 in the spec is open; this picks a
// standalone root rather than reusing Scoop's, revisit at CPT-07/import
// time).
package paths

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Root is goop's install root, resolved in order:
//  1. the GOOP_HOME environment variable (highest priority -- scripting,
//     CI, or a one-off override without touching persistent config)
//  2. a root persisted via `goop config set-root` (ConfigFilePath)
//  3. "<user home>\goop"
func Root() string {
	if v := os.Getenv("GOOP_HOME"); v != "" {
		return v
	}
	if r, ok := ConfiguredRoot(); ok {
		return r
	}
	return defaultRoot()
}

func defaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "goop")
}

// ConfigFilePath is where goop's own persistent settings live -- fixed
// and independent of Root() (it has to be: it's what tells goop where
// Root() is, so it can't itself live under Root()).
// %LOCALAPPDATA%, not %APPDATA%: which drive/folder holds package data
// is inherently a per-machine choice (a roamed profile can't assume the
// same drive layout on another machine), so this shouldn't roam either.
func ConfigFilePath() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = defaultRoot() // pathological fallback; LOCALAPPDATA is always set on real Windows
	}
	return filepath.Join(base, "goop", "config.json")
}

type config struct {
	Root    string   `json:"root,omitempty"`
	Proxy   string   `json:"proxy,omitempty"`
	NoProxy []string `json:"no_proxy,omitempty"`
	// CacheLimit caps the download cache, in bytes. A pointer so the
	// three states stay distinct in JSON: absent (unlimited, the
	// default), 0 (keep nothing), or a real ceiling. Downloads are big
	// and never expire on their own -- a real install here reached 9.3
	// GB across 20 files -- so without this the cache only ever grows.
	CacheLimit *int64 `json:"cache_limit,omitempty"`
	// LastBucketUpdate is when buckets were last refreshed, so install
	// can refresh them when they've gone stale (see BucketsStale).
	LastBucketUpdate string `json:"last_bucket_update,omitempty"`
	// BucketTTL, in seconds, overrides how long buckets stay fresh.
	BucketTTL *int64 `json:"bucket_ttl_seconds,omitempty"`
}

// defaultBucketTTL matches real Scoop's own threshold
// (is_scoop_outdated, lib/core.ps1: 3 hours since LAST_UPDATE). Used
// when the user hasn't set one of their own.
const defaultBucketTTL = 3 * time.Hour

// ConfiguredBucketTTL returns how long buckets stay fresh before an
// install refreshes them. Zero disables the automatic refresh outright
// (nothing is ever considered stale), for anyone who would rather run
// `goop bucket update` on their own schedule.
func ConfiguredBucketTTL() time.Duration {
	c, err := loadConfig()
	if err != nil || c.BucketTTL == nil {
		return defaultBucketTTL
	}
	return time.Duration(*c.BucketTTL) * time.Second
}

// SetConfiguredBucketTTL persists the freshness window in seconds.
func SetConfiguredBucketTTL(d time.Duration) error {
	if d < 0 {
		return fmt.Errorf("bucket TTL must not be negative (use 0 to disable the automatic refresh)")
	}
	secs := int64(d / time.Second)
	return updateConfig(func(c *config) { c.BucketTTL = &secs })
}

// UnsetConfiguredBucketTTL reverts to the Scoop-matching default.
func UnsetConfiguredBucketTTL() error {
	return updateConfig(func(c *config) { c.BucketTTL = nil })
}

// BucketsStale reports whether buckets haven't been refreshed recently
// enough. A manifest pins an exact hash, but plenty of real apps ship
// from a rolling URL that always serves the latest build (Spotify's
// SpotifyFullSetupX64.exe, for one) -- against a stale manifest the
// download then fails hash verification, which looks like corruption
// but is really just an out-of-date bucket.
func BucketsStale() bool {
	c, err := loadConfig()
	if err != nil || c.LastBucketUpdate == "" {
		return ConfiguredBucketTTL() != 0
	}
	last, err := time.Parse(time.RFC3339, c.LastBucketUpdate)
	if err != nil {
		return true
	}
	ttl := ConfiguredBucketTTL()
	if ttl == 0 {
		return false // automatic refresh disabled
	}
	return time.Since(last) >= ttl
}

// MarkBucketsUpdated records that buckets were just refreshed.
func MarkBucketsUpdated() error {
	return updateConfig(func(c *config) { c.LastBucketUpdate = time.Now().Format(time.RFC3339) })
}

// CacheUnlimited is what ConfiguredCacheLimit reports when no ceiling
// is set: keep every cached download, goop's original behavior.
const CacheUnlimited int64 = -1

// ConfiguredCacheLimit returns the cache ceiling in bytes:
// CacheUnlimited when unset, 0 to keep nothing, else the limit.
func ConfiguredCacheLimit() int64 {
	c, err := loadConfig()
	if err != nil || c.CacheLimit == nil {
		return CacheUnlimited
	}
	return *c.CacheLimit
}

// SetConfiguredCacheLimit persists a ceiling in bytes (0 = keep
// nothing). Enforcement is the caller's job -- see installer.PruneCache.
func SetConfiguredCacheLimit(bytes int64) error {
	if bytes < 0 {
		return fmt.Errorf("cache limit must not be negative (use `goop config unset-cache-limit` for unlimited)")
	}
	return updateConfig(func(c *config) { c.CacheLimit = &bytes })
}

// UnsetConfiguredCacheLimit reverts to keeping everything.
func UnsetConfiguredCacheLimit() error {
	return updateConfig(func(c *config) { c.CacheLimit = nil })
}

func loadConfig() (config, error) {
	data, err := os.ReadFile(ConfigFilePath())
	if os.IsNotExist(err) {
		return config{}, nil
	}
	if err != nil {
		return config{}, fmt.Errorf("read %s: %w", ConfigFilePath(), err)
	}
	var c config
	if err := json.Unmarshal(data, &c); err != nil {
		return config{}, fmt.Errorf("parse %s: %w", ConfigFilePath(), err)
	}
	return c, nil
}

func saveConfig(c config) error {
	path := ConfigFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// updateConfig loads the current config, lets mutate change just the
// field(s) it cares about, and saves the result -- so setting one field
// (e.g. Proxy) never clobbers another already-persisted one (e.g. Root)
// the way constructing a fresh zero-value config{} and saving it would.
func updateConfig(mutate func(*config)) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	mutate(&c)
	return saveConfig(c)
}

// ConfiguredRoot returns the root persisted via SetConfiguredRoot, if
// any.
func ConfiguredRoot() (string, bool) {
	c, err := loadConfig()
	if err != nil || c.Root == "" {
		return "", false
	}
	return c.Root, true
}

// SetConfiguredRoot persists root as goop's install root for future
// commands. This only ever changes where goop *looks* -- it never
// moves, copies, or deletes anything at the old or new location, so
// it's always safe to change back with UnsetConfiguredRoot or another
// SetConfiguredRoot call. If apps are already installed at the current
// root, the caller (the CLI) is expected to warn about that, since
// they'll become invisible to goop until moved there manually or the
// config is reverted.
func SetConfiguredRoot(root string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("root must be an absolute path, got %q", root)
	}
	root = filepath.Clean(root)
	return updateConfig(func(c *config) { c.Root = root })
}

// UnsetConfiguredRoot reverts to the default root (or GOOP_HOME, if
// set).
func UnsetConfiguredRoot() error {
	return updateConfig(func(c *config) { c.Root = "" })
}

// ConfiguredProxy returns the proxy URL persisted via SetConfiguredProxy,
// if any.
func ConfiguredProxy() (string, bool) {
	c, err := loadConfig()
	if err != nil || c.Proxy == "" {
		return "", false
	}
	return c.Proxy, true
}

// SetConfiguredProxy persists proxyURL as goop's proxy, used for both
// http:// and https:// targets alike -- one setting for both, same as
// git's http.proxy, rather than separate http/https keys. The standard
// HTTP_PROXY/HTTPS_PROXY environment variables always take priority when
// set (EnvProxyConfigured), same precedence pattern as GOOP_HOME vs. the
// persisted root.
func SetConfiguredProxy(proxyURL string) error {
	if _, err := url.Parse(proxyURL); err != nil {
		return fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)
	}
	return updateConfig(func(c *config) { c.Proxy = proxyURL })
}

// UnsetConfiguredProxy removes the persisted proxy.
func UnsetConfiguredProxy() error {
	return updateConfig(func(c *config) { c.Proxy = "" })
}

// ConfiguredNoProxy returns the hosts/domains that bypass the persisted
// proxy (SetConfiguredNoProxy), if any.
func ConfiguredNoProxy() []string {
	c, err := loadConfig()
	if err != nil {
		return nil
	}
	return c.NoProxy
}

// SetConfiguredNoProxy persists hosts as the bypass list: entries can be
// an exact hostname ("localhost"), a domain suffix (".corp.internal",
// matching any subdomain), or "*" (bypass the proxy for everything).
func SetConfiguredNoProxy(hosts []string) error {
	return updateConfig(func(c *config) { c.NoProxy = hosts })
}

// UnsetConfiguredNoProxy clears the bypass list.
func UnsetConfiguredNoProxy() error {
	return updateConfig(func(c *config) { c.NoProxy = nil })
}

// EnvProxyConfigured reports whether any of the standard HTTP_PROXY/
// HTTPS_PROXY environment variables (upper or lower case) are set --
// when true, callers should defer entirely to their own environment-
// aware proxy resolution (e.g. http.ProxyFromEnvironment, or git's own
// env handling) instead of goop's persisted config.
func EnvProxyConfigured() bool {
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

// ProxyFor returns goop's own persisted proxy to use for a request to
// host, or "" if none is configured or host matches a configured
// no-proxy entry. Callers should only consult this once
// EnvProxyConfigured is false.
func ProxyFor(host string) string {
	proxyURL, ok := ConfiguredProxy()
	if !ok {
		return ""
	}
	host = strings.ToLower(host)
	for _, entry := range ConfiguredNoProxy() {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if entry == "*" {
			return ""
		}
		entry = strings.TrimPrefix(entry, "*.")
		entry = strings.TrimPrefix(entry, ".")
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return ""
		}
	}
	return proxyURL
}

func Buckets() string           { return filepath.Join(Root(), "buckets") }
func Bucket(name string) string { return filepath.Join(Buckets(), name) }
func BucketsConfig() string     { return filepath.Join(Root(), "buckets.json") }
func MavenReposConfig() string  { return filepath.Join(Root(), "maven-repos.json") }

func Apps() string                           { return filepath.Join(Root(), "apps") }
func App(name string) string                 { return filepath.Join(Apps(), name) }
func AppVersion(name, version string) string { return filepath.Join(App(name), version) }
func AppVersionStaging(name, version string) string {
	return filepath.Join(App(name), version+".partial")
}
func AppCurrent(name string) string { return filepath.Join(App(name), "current") }

func Shims() string      { return filepath.Join(Root(), "shims") }
func ShimMaster() string { return filepath.Join(Shims(), "shim-master.exe") }
func Cache() string      { return filepath.Join(Root(), "cache") }

// Modules is where a manifest's `psmodule.name` gets junctioned to,
// mirroring real Scoop's own modulesdir -- not part of EnsureLayout
// since only a minority of manifests (31 in the real corpus) ever need
// it; created on demand instead, same as Persist.
func Modules() string { return filepath.Join(Root(), "modules") }

// Persist is the stable, version-independent store backing an app's
// `persist` entries (NR-04).
func Persist(name string) string { return filepath.Join(Root(), "persist", name) }
func PersistEntry(name, target string) string {
	return filepath.Join(Persist(name), filepath.FromSlash(target))
}

// StartMenu is where goop creates Start Menu shortcuts, namespaced under
// its own folder so uninstall can remove exactly what it created.
func StartMenu() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		appData = filepath.Join(Root(), "startmenu-fallback")
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "goop")
}

// EnsureLayout creates the top-level directories goop needs.
func EnsureLayout() error {
	for _, d := range []string{Buckets(), Apps(), Shims(), Cache()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
