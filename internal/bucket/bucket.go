// Package bucket manages Scoop-compatible manifest buckets: Git
// repositories cloned locally (FR-20, the default), or plain archives
// served by an artifact host with no Git involved (FR-21) -- searched
// in priority order to resolve an app name to a manifest (FR-22).
package bucket

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/TanguyBaudoin/goop/internal/archive"
	"github.com/TanguyBaudoin/goop/internal/downloader"
	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/paths"
)

// gitOnPath reports whether git is available on PATH.
func gitOnPath() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// githubArchiveURL converts a GitHub git remote URL to a codeload archive
// URL that can be downloaded without git. Returns "" if the URL is not
// a recognizable GitHub URL.
//
// Handles:
//
//	https://github.com/org/repo
//	https://github.com/org/repo.git
func githubArchiveURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if u.Hostname() != "github.com" {
		return ""
	}
	path := strings.TrimSuffix(u.Path, ".git")
	path = strings.TrimSuffix(path, "/")
	parts := strings.SplitN(path, "/", 4)
	if len(parts) < 3 || parts[0] != "" || parts[1] == "" || parts[2] == "" {
		return ""
	}
	return fmt.Sprintf("https://codeload.github.com/%s/%s/zip/HEAD", parts[1], parts[2])
}

// gitProxyArgs returns extra "git -c http.proxy=..." args for a git
// invocation targeting rawURL, drawn from goop's own persisted proxy
// (`goop config set-proxy`) -- only when neither HTTP_PROXY nor
// HTTPS_PROXY is set in the environment, since git already honors those
// itself. Bypassed for hosts matching `goop config set-no-proxy`, same
// as downloader's HTTP client.
func gitProxyArgs(rawURL string) []string {
	if paths.EnvProxyConfigured() {
		return nil
	}
	host := ""
	if u, err := url.Parse(rawURL); err == nil {
		host = u.Hostname()
	}
	if p := paths.ProxyFor(host); p != "" {
		return []string{"-c", "http.proxy=" + p}
	}
	return nil
}

// Kind identifies how a bucket's content is fetched/updated.
type Kind string

const (
	KindGit     Kind = "git"
	KindArchive Kind = "archive"
)

// Entry is one configured bucket: a name, its source URL, and how it
// was fetched. Priority is the order entries appear in Config.Buckets --
// earlier wins on name collisions (FR-22).
type Entry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Kind Kind   `json:"kind,omitempty"` // empty means "git", for entries written before Kind existed
}

func (e Entry) kind() Kind {
	if e.Kind == "" {
		return KindGit
	}
	return e.Kind
}

type Config struct {
	Buckets []Entry `json:"buckets"`
}

func loadConfig() (Config, error) {
	data, err := os.ReadFile(paths.BucketsConfig())
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read bucket config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse bucket config: %w", err)
	}
	return cfg, nil
}

func saveConfig(cfg Config) error {
	if err := paths.EnsureLayout(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(paths.BucketsConfig(), data, 0o644)
}

// List returns the configured buckets in priority order.
func List() ([]Entry, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	return cfg.Buckets, nil
}

// archiveExt reports the archive extension recognized for a Git-less
// bucket URL, or "" if it doesn't look like a plain archive.
func archiveExt(rawURL string) string {
	lower := strings.ToLower(rawURL)
	lower, _, _ = strings.Cut(lower, "?")
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return ".tar.gz"
	case strings.HasSuffix(lower, ".zip"):
		return ".zip"
	case strings.HasSuffix(lower, ".tar"):
		return ".tar"
	}
	// GitHub (and GitHub-alike forges') codeload archive URLs carry the
	// format as a path *segment*, not a suffix, e.g.
	// codeload.github.com/org/repo/zip/refs/heads/main -- probably the
	// single most common way someone actually reaches for a Git-less
	// bucket, so worth recognizing explicitly.
	segments := strings.Split(lower, "/")
	for _, s := range segments {
		switch s {
		case "zip":
			return ".zip"
		case "tar.gz":
			return ".tar.gz"
		}
	}
	return ""
}

// Add fetches url as a new bucket named name and appends it (lowest
// priority) to the config. kind selects git (FR-20, the default -- Git
// is delegated to, consistent with how Scoop itself depends on it) or
// archive (FR-21, no Git required); pass "" to auto-detect from url's
// extension, defaulting to git.
//
// When kind is git (or auto-detected as git) and git is not on PATH, Add
// falls back to archive mode if the URL is a recognizable GitHub URL
// (githubArchiveURL) -- this lets a user bootstrap goop without having
// git installed first. The bucket is stored as KindArchive so future
// updates also use the archive path.
func Add(name, url string, kind Kind) error {
	if name == "" {
		return fmt.Errorf("bucket name must not be empty")
	}
	if kind == "" {
		if archiveExt(url) != "" {
			kind = KindArchive
		} else {
			kind = KindGit
		}
	}

	if kind == KindGit && !gitOnPath() {
		if archiveURL := githubArchiveURL(url); archiveURL != "" {
			url = archiveURL
			kind = KindArchive
		}
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	for _, e := range cfg.Buckets {
		if e.Name == name {
			return fmt.Errorf("bucket %q already added (%s)", name, e.URL)
		}
	}

	if err := paths.EnsureLayout(); err != nil {
		return err
	}
	dir := paths.Bucket(name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("bucket directory %s already exists", dir)
	}

	switch kind {
	case KindGit:
		args := append([]string{}, gitProxyArgs(url)...)
		args = append(args, "clone", "--depth", "1", url, dir)
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git clone %s: %w\n%s", url, err, out)
		}
	case KindArchive:
		if err := fetchArchiveBucket(url, dir); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown bucket kind %q", kind)
	}

	cfg.Buckets = append(cfg.Buckets, Entry{Name: name, URL: url, Kind: kind})
	return saveConfig(cfg)
}

// SetPriority moves name to 1-based position pos in the search order,
// keeping every other bucket's relative order. Position 1 makes it win
// every name collision (Find returns the first bucket that has the
// app), which is the point: an app carried by several buckets, like
// `flux` in both main and extras, otherwise always resolves to
// whichever bucket happens to have been added first.
//
// Real Scoop has no equivalent -- its order is alphabetical with
// "known" buckets hardcoded to the front (lib/buckets.ps1's
// Get-LocalBucket), not something a user can influence -- so this is
// goop's own. Out-of-range positions clamp rather than erroring: the
// intent of "priority 99" on a five-bucket list is unambiguous.
func SetPriority(name string, pos int) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	idx := -1
	for i, e := range cfg.Buckets {
		if e.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("bucket %q is not configured", name)
	}

	if pos < 1 {
		pos = 1
	}
	if pos > len(cfg.Buckets) {
		pos = len(cfg.Buckets)
	}
	target := pos - 1

	entry := cfg.Buckets[idx]
	rest := append(cfg.Buckets[:idx:idx], cfg.Buckets[idx+1:]...)
	reordered := make([]Entry, 0, len(cfg.Buckets))
	reordered = append(reordered, rest[:target]...)
	reordered = append(reordered, entry)
	reordered = append(reordered, rest[target:]...)
	cfg.Buckets = reordered

	return saveConfig(cfg)
}

// Remove drops name from the bucket config and deletes its local clone
// (mirrors real Scoop's `scoop bucket rm`, libexec/scoop-bucket.ps1's
// rm_bucket). An app already installed from name is unaffected -- its
// own files and record live under paths.App, entirely independent of
// the bucket directory being removed here.
func Remove(name string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	idx := -1
	for i, e := range cfg.Buckets {
		if e.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("bucket %q is not configured", name)
	}
	cfg.Buckets = append(cfg.Buckets[:idx], cfg.Buckets[idx+1:]...)
	if err := saveConfig(cfg); err != nil {
		return err
	}
	if err := os.RemoveAll(paths.Bucket(name)); err != nil {
		return fmt.Errorf("remove bucket directory: %w", err)
	}
	return nil
}

// Register adds name to the bucket config pointing at url, without
// fetching anything -- the caller is responsible for paths.Bucket(name)
// already existing and being ready to use. For a KindGit entry this is
// safe even with url == "": Update drives a git pull from the
// directory's own already-configured remote, never from the registry's
// URL field (that field is purely informational there). Used by migrate
// to register a bucket copied in from an existing Scoop installation
// without re-cloning it.
func Register(name, url string, kind Kind) error {
	if name == "" {
		return fmt.Errorf("bucket name must not be empty")
	}
	if kind == "" {
		kind = KindGit
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	for _, e := range cfg.Buckets {
		if e.Name == name {
			return fmt.Errorf("bucket %q already added (%s)", name, e.URL)
		}
	}
	cfg.Buckets = append(cfg.Buckets, Entry{Name: name, URL: url, Kind: kind})
	return saveConfig(cfg)
}

// fetchArchiveBucket downloads and extracts a Git-less bucket into dir.
// A GitHub-style archive download (and most artifact-host archives)
// wraps everything in one top-level directory (e.g. "Main-master/");
// that wrapper is hoisted away so manifests land directly under dir,
// matching a git clone's layout.
func fetchArchiveBucket(url, dir string) error {
	tmp, err := os.CreateTemp("", "goop-bucket-*"+archiveExt(url))
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := downloader.FetchUnverified(url, tmpPath); err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	switch archiveExt(url) {
	case ".zip":
		err = archive.ExtractZip(tmpPath, dir, "")
	case ".tar.gz":
		err = archive.ExtractTarGz(tmpPath, dir, "")
	case ".tar":
		err = archive.ExtractTar(tmpPath, dir, "")
	default:
		err = fmt.Errorf("unrecognized bucket archive format for %s (want .zip, .tar.gz, or .tar)", url)
	}
	if err != nil {
		os.RemoveAll(dir)
		return err
	}

	if err := hoistSingleSubdir(dir); err != nil {
		os.RemoveAll(dir)
		return err
	}
	return nil
}

// hoistSingleSubdir moves a lone subdirectory's contents up into dir
// and removes the now-empty wrapper, if dir contains exactly one entry
// and that entry is a directory.
func hoistSingleSubdir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return nil
	}
	wrapper := filepath.Join(dir, entries[0].Name())
	inner, err := os.ReadDir(wrapper)
	if err != nil {
		return err
	}
	for _, e := range inner {
		if err := os.Rename(filepath.Join(wrapper, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return os.Remove(wrapper)
}

// Update refreshes bucket name (FR-23, incremental for git; a fresh
// download+extract for archive buckets, which have no incremental
// mechanism of their own).
func Update(name string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	var entry *Entry
	for i := range cfg.Buckets {
		if cfg.Buckets[i].Name == name {
			entry = &cfg.Buckets[i]
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("bucket %q not configured", name)
	}

	dir := paths.Bucket(name)
	switch entry.kind() {
	case KindGit:
		if !gitOnPath() {
			if archiveURL := githubArchiveURL(entry.URL); archiveURL != "" {
				entry.Kind = KindArchive
				entry.URL = archiveURL
				if err := saveConfig(cfg); err != nil {
					return err
				}
				return updateArchive(entry.URL, dir)
			}
			return fmt.Errorf("git not found on PATH; cannot update bucket %q (and %q is not a GitHub URL that can fall back to archive mode)", name, entry.URL)
		}
		if _, err := os.Stat(dir); err != nil {
			return fmt.Errorf("bucket %q not found: %w", name, err)
		}
		// Buckets are disposable upstream mirrors -- fetch + hard-reset
		// avoids the "untracked working tree files would be overwritten"
		// error that plain pull --ff-only chokes on whenever upstream
		// adds files whose paths happen to exist unstaged in the clone.
		fetchArgs := append([]string{"-C", dir}, gitProxyArgs(entry.URL)...)
		fetchArgs = append(fetchArgs, "fetch")
		cmd := exec.Command("git", fetchArgs...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git fetch %s: %w\n%s", name, err, out)
		}
		cmd = exec.Command("git", "-C", dir, "reset", "--hard", "FETCH_HEAD")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git reset %s: %w\n%s", name, err, out)
		}
		cmd = exec.Command("git", "-C", dir, "clean", "-fd")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git clean %s: %w\n%s", name, err, out)
		}
		return nil
	case KindArchive:
		return updateArchive(entry.URL, dir)
	default:
		return fmt.Errorf("bucket %q has unknown kind %q", name, entry.Kind)
	}
}

// updateArchive replaces dir's contents with a fresh download+extract of
// url (a Git-less archive bucket).
func updateArchive(url, dir string) error {
	staging := dir + ".update"
	os.RemoveAll(staging)
	if err := fetchArchiveBucket(url, staging); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		os.RemoveAll(staging)
		return err
	}
	return os.Rename(staging, dir)
}

// UpdateAll updates every configured bucket, returning the first error
// encountered (after attempting the rest) so one broken bucket doesn't
// block reporting on the others.
func UpdateAll() error {
	entries, err := List()
	if err != nil {
		return err
	}
	// Concurrently: each Update is a `git pull` against a different
	// remote, so this is almost entirely network wait -- serially it
	// meant five round trips back to back, which became painful once
	// install started refreshing stale buckets on its own. Each bucket
	// is a separate directory and a separate git process, so there is
	// no shared state to guard beyond collecting the error.
	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	for _, e := range entries {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			if err := Update(name); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(e.Name)
	}
	wg.Wait()
	return firstErr
}

// manifestDir returns where .json manifests live inside a bucket:
// "<bucket>/bucket/" if that subdirectory exists (the layout every
// public Scoop bucket uses), else the bucket root itself.
func manifestDir(bucketDir string) string {
	sub := filepath.Join(bucketDir, "bucket")
	if info, err := os.Stat(sub); err == nil && info.IsDir() {
		return sub
	}
	return bucketDir
}

// ManifestNames lists the app names available in bucket name (manifest
// filenames with ".json" stripped), without decoding any of them --
// just a directory listing, fast enough to call on every shell-completion
// keystroke.
func ManifestNames(name string) ([]string, error) {
	entries, err := os.ReadDir(manifestDir(paths.Bucket(name)))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	return names, nil
}

// Find locates appName across configured buckets in priority order and
// decodes its manifest. NotFoundError is returned (wrapped) if no bucket
// has it.
func Find(appName string) (bucketName string, m manifest.Manifest, err error) {
	entries, err := List()
	if err != nil {
		return "", manifest.Manifest{}, err
	}
	if len(entries) == 0 {
		return "", manifest.Manifest{}, fmt.Errorf("no buckets configured; add one with `goop bucket add <name> <url>`")
	}

	var tried []string
	var malformed []error
	for _, e := range entries {
		m, err := readManifest(e.Name, appName)
		if err != nil {
			// A manifest that exists but won't decode is a different
			// problem from one that isn't there, and saying "not found"
			// for it sends you looking in entirely the wrong place --
			// so keep those errors and surface them instead.
			if !os.IsNotExist(err) {
				malformed = append(malformed, err)
			}
			tried = append(tried, e.Name)
			continue
		}
		return e.Name, m, nil
	}
	if len(malformed) > 0 {
		return "", manifest.Manifest{}, malformed[0]
	}
	return "", manifest.Manifest{}, fmt.Errorf("%q not found in bucket(s) %s", appName, strings.Join(tried, ", "))
}

// FindIn locates appName in exactly bucketName, for a bucket-qualified
// reference (e.g. a "extras/86box" depends entry, or a "extras/mpv"
// install spec) -- other configured buckets are not consulted, even if
// they also happen to have an app by that name.
func FindIn(bucketName, appName string) (manifest.Manifest, error) {
	entries, err := List()
	if err != nil {
		return manifest.Manifest{}, err
	}
	found := false
	for _, e := range entries {
		if e.Name == bucketName {
			found = true
			break
		}
	}
	if !found {
		return manifest.Manifest{}, fmt.Errorf("bucket %q is not configured", bucketName)
	}
	m, err := readManifest(bucketName, appName)
	if err != nil {
		// Same distinction as Find: only a genuinely missing file is
		// "not found"; anything else (a manifest that won't decode,
		// a permission problem) is reported as itself.
		if !os.IsNotExist(err) {
			return manifest.Manifest{}, err
		}
		return manifest.Manifest{}, fmt.Errorf("%q not found in bucket %q", appName, bucketName)
	}
	return m, nil
}

// Resolve locates spec's manifest: FindIn if spec names a bucket, Find
// (priority search) otherwise.
func Resolve(spec manifest.Spec) (bucketName string, m manifest.Manifest, err error) {
	if spec.Bucket != "" {
		m, err := FindIn(spec.Bucket, spec.Name)
		if err != nil {
			return "", manifest.Manifest{}, err
		}
		return spec.Bucket, m, nil
	}
	return Find(spec.Name)
}

func readManifest(bucketName, appName string) (manifest.Manifest, error) {
	dir := manifestDir(paths.Bucket(bucketName))
	file := filepath.Join(dir, appName+".json")
	data, err := os.ReadFile(file)
	if err != nil {
		return manifest.Manifest{}, err
	}
	m, err := manifest.Decode(data)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("bucket %q: manifest %s: %w", bucketName, appName, err)
	}
	return m, nil
}
