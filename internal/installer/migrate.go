package installer

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"github.com/TanguyBaudoin/goop/internal/bucket"
	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/paths"
)

// Migrate (unlike Import, which junctions straight at Scoop's own
// directories so it depends on the Scoop installation staying in place)
// copies everything -- buckets and app versions -- into goop's own tree,
// re-deriving env vars/shortcuts/shims against goop's copy. The result no
// longer depends on Scoop at all, so it's safe to uninstall Scoop
// afterward. No network access is needed: the bits Scoop already
// downloaded and verified are reused as-is via a plain file copy.

// MigrationBucket is one Scoop bucket detected under a Scoop
// installation, as reported by PlanMigration.
type MigrationBucket struct {
	Name          string
	URL           string // best-effort, read from the bucket's own .git/config; "" if undeterminable
	AlreadyInGoop bool
}

// MigrationApp is one importable Scoop app, as reported by PlanMigration.
type MigrationApp struct {
	Name          string
	Version       string
	Bucket        string
	SizeBytes     int64
	AlreadyInGoop bool
}

// MigrationPlan is what Migrate would do, computed with no side effects
// so it can be shown before anything happens.
type MigrationPlan struct {
	ScoopRoot string
	Buckets   []MigrationBucket
	Apps      []MigrationApp
}

// PlanMigration inspects a real Scoop installation and reports every
// bucket and app it finds, without changing anything.
func PlanMigration() (MigrationPlan, error) {
	scoopRoot, ok := DetectScoopRoot()
	if !ok {
		return MigrationPlan{}, fmt.Errorf("no Scoop installation found (checked $SCOOP and <home>\\scoop)")
	}
	plan := MigrationPlan{ScoopRoot: scoopRoot}

	existingBuckets := map[string]bool{}
	if entries, err := bucket.List(); err == nil {
		for _, e := range entries {
			existingBuckets[e.Name] = true
		}
	}
	bucketEntries, _ := os.ReadDir(filepath.Join(scoopRoot, "buckets"))
	for _, e := range bucketEntries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(scoopRoot, "buckets", e.Name())
		plan.Buckets = append(plan.Buckets, MigrationBucket{
			Name:          e.Name(),
			URL:           gitRemoteURL(dir),
			AlreadyInGoop: existingBuckets[e.Name()],
		})
	}

	existingApps := map[string]bool{}
	if recs, err := List(); err == nil {
		for _, r := range recs {
			existingApps[r.Name] = true
		}
	}
	appNames, err := ImportableApps(scoopRoot)
	if err != nil {
		return MigrationPlan{}, err
	}
	sort.Strings(appNames)
	for _, name := range appNames {
		realVersionDir, version, ok := scoopCurrentVersion(scoopRoot, name)
		if !ok {
			continue
		}
		var sij scoopInstallJSON
		if data, err := os.ReadFile(filepath.Join(realVersionDir, "install.json")); err == nil {
			_ = json.Unmarshal(data, &sij)
		}
		plan.Apps = append(plan.Apps, MigrationApp{
			Name:          name,
			Version:       version,
			Bucket:        sij.Bucket,
			SizeBytes:     dirSize(realVersionDir),
			AlreadyInGoop: existingApps[name],
		})
	}
	return plan, nil
}

func scoopCurrentVersion(scoopRoot, appName string) (realVersionDir, version string, ok bool) {
	link := filepath.Join(scoopRoot, "apps", appName, "current")
	target, err := os.Readlink(link)
	if err != nil {
		return "", "", false
	}
	return target, filepath.Base(target), true
}

// MigrateBucket copies bucket b's files (including .git, so `goop bucket
// update` keeps working via a plain git pull afterward, needing no
// network fetch here) from scoopRoot into goop's own bucket tree, and
// registers it. A no-op if it's already registered.
func MigrateBucket(scoopRoot string, b MigrationBucket) error {
	if b.AlreadyInGoop {
		Logf("bucket %s already migrated", b.Name)
		return nil
	}
	if err := paths.EnsureLayout(); err != nil {
		return err
	}
	src := filepath.Join(scoopRoot, "buckets", b.Name)
	dst := paths.Bucket(b.Name)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("bucket directory %s already exists", dst)
	}
	if err := copyTree(src, dst); err != nil {
		os.RemoveAll(dst)
		return fmt.Errorf("copy bucket %s: %w", b.Name, err)
	}
	if err := bucket.Register(b.Name, b.URL, bucket.KindGit); err != nil {
		return fmt.Errorf("register bucket %s: %w", b.Name, err)
	}
	Logf("migrated bucket %s", b.Name)
	return nil
}

// MigrateApp copies appName's currently-active Scoop version into goop's
// own tree and takes full ownership of it: env vars, shortcuts, and
// shims are all re-derived against the copy (same pipeline installResolved
// uses), rather than reused from Scoop's already-applied ones -- so the
// result is correct even after Scoop is later uninstalled.
func MigrateApp(scoopRoot, appName string) (Record, error) {
	if err := paths.EnsureLayout(); err != nil {
		return Record{}, err
	}

	realVersionDir, version, ok := scoopCurrentVersion(scoopRoot, appName)
	if !ok {
		return Record{}, fmt.Errorf("%s: not found under Scoop root %s", appName, scoopRoot)
	}

	unlock := lockInstall(appName)
	defer unlock()

	versionDir := paths.AppVersion(appName, version)
	if rec, ok := readRecord(versionDir); ok {
		Logf("%s %s already migrated", appName, version)
		if err := relinkCurrent(appName, versionDir); err != nil {
			return Record{}, err
		}
		return rec, nil
	}

	var sij scoopInstallJSON
	installData, err := os.ReadFile(filepath.Join(realVersionDir, "install.json"))
	if err != nil {
		return Record{}, fmt.Errorf("%s: read Scoop install.json: %w", appName, err)
	}
	if err := json.Unmarshal(installData, &sij); err != nil {
		return Record{}, fmt.Errorf("%s: parse Scoop install.json: %w", appName, err)
	}

	manifestData, err := os.ReadFile(filepath.Join(realVersionDir, "manifest.json"))
	if err != nil {
		return Record{}, fmt.Errorf("%s: read Scoop manifest.json: %w", appName, err)
	}
	m, err := manifest.Decode(manifestData)
	if err != nil {
		return Record{}, fmt.Errorf("%s: decode Scoop manifest.json: %w", appName, err)
	}
	resolved, err := m.Resolve(appName, sij.Architecture)
	if err != nil {
		return Record{}, fmt.Errorf("%s: %w", appName, err)
	}

	// Copy Scoop's own persisted data for this app first, so linkPersist
	// below (run against the staged copy, same as a normal install) finds
	// it already seeded and re-creates the junctions against goop's own
	// copy -- never against Scoop's, which won't exist once Scoop is gone.
	if err := copyPersistStore(scoopRoot, appName); err != nil {
		return Record{}, fmt.Errorf("%s: copy persisted data: %w", appName, err)
	}

	staging := paths.AppVersionStaging(appName, version)
	os.RemoveAll(staging) // leftover from a previous failed attempt
	Logf("%s: copying %s from Scoop", appName, version)
	if err := copyTree(realVersionDir, staging); err != nil {
		os.RemoveAll(staging)
		return Record{}, fmt.Errorf("%s: copy from Scoop: %w", appName, err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			os.RemoveAll(staging)
		}
	}()

	// Scoop's own bookkeeping has no meaning inside goop's tree.
	os.Remove(filepath.Join(staging, "install.json"))
	os.Remove(filepath.Join(staging, "manifest.json"))

	if err := linkPersist(appName, resolved.Persist, staging); err != nil {
		return Record{}, err
	}

	// $dir here means the app's final, stable path -- versionDir, not
	// staging -- same reasoning as a normal install (installResolved).
	envSet := expandEnvSet(resolved.EnvSet, versionDir, resolved.Version, paths.Persist(appName))
	envAddedPaths := expandEnvAddPath(resolved.EnvAddPath, versionDir)

	rec := Record{
		Name:         appName,
		Version:      version,
		Bucket:       sij.Bucket,
		Architecture: sij.Architecture,
		URLs:         resolved.URLs,
		Hashes:       resolved.Hashes,
		Bin:          resolved.Bin,
		ExtractDirs:  resolved.ExtractDirs,
		ExtractTos:   resolved.ExtractTos,
		Shortcuts:    resolved.Shortcuts,
		// Uninstaller is deliberately omitted, same reasoning as Import:
		// it could carry Scoop-specific side effects goop has no business
		// triggering against its own copy.
		EnvSet:        envSet,
		EnvAddedPaths: envAddedPaths,
		InstalledAt:   time.Now().UTC(),
	}
	if err := writeRecord(staging, rec); err != nil {
		return Record{}, err
	}

	if err := os.Rename(staging, versionDir); err != nil {
		return Record{}, fmt.Errorf("finalize migration of %s: %w", appName, err)
	}
	succeeded = true

	if err := relinkCurrent(appName, versionDir); err != nil {
		return Record{}, err
	}
	if err := createShims(appName, resolved.Bin); err != nil {
		return Record{}, err
	}
	if err := createShortcuts(appName, resolved.Shortcuts); err != nil {
		Logf("%s: shortcuts: %v", appName, err) // non-fatal, same tier as a normal install
	}
	applyEnv(appName, envSet, envAddedPaths)

	Logf("migrated %s %s (now independent of Scoop)", appName, version)
	return rec, nil
}

// MigrateAllApps migrates every app in plan concurrently (bounded, same
// pattern as InstallAll/UpdateAll), skipping ones already migrated.
func MigrateAllApps(scoopRoot string, apps []MigrationApp) map[string]error {
	var names []string
	for _, a := range apps {
		if a.AlreadyInGoop {
			Logf("%s already migrated", a.Name)
			continue
		}
		names = append(names, a.Name)
	}
	return runConcurrent(names, defaultConcurrency(), func(name string) error {
		_, err := MigrateApp(scoopRoot, name)
		return err
	})
}

// copyPersistStore copies scoopRoot's persist\<appName> directory (if it
// exists and goop doesn't already have one for this app) wholesale into
// goop's own persist store.
func copyPersistStore(scoopRoot, appName string) error {
	src := filepath.Join(scoopRoot, "persist", appName)
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return nil // nothing persisted for this app
	}
	dst := paths.Persist(appName)
	if _, err := os.Stat(dst); err == nil {
		return nil // already migrated
	}
	return copyTree(src, dst)
}

// copyTree recursively copies src into dst, creating dst as needed.
// Reparse points (NTFS junctions/symlinks) found inside src are skipped
// rather than followed -- inside a Scoop version directory these are
// almost always persist junctions pointing back into Scoop's own persist
// store, which migrate handles separately (copyPersistStore, copying the
// real data rather than a link that would dangle once Scoop is gone).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if isReparsePoint(path) {
			return nil // junction/symlink: skip, don't follow
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

// isReparsePoint reports whether path is an NTFS reparse point (a
// junction or symlink) by checking the Windows FILE_ATTRIBUTE_REPARSE_
// POINT bit directly, rather than relying on Go's fs.FileMode symlink
// detection: empirically (confirmed against Scoop's own real persist
// junctions), Go reports some reparse points as fs.ModeIrregular, not
// fs.ModeSymlink, so checking the portable bit alone misses them and
// copyTree would try to open and read a directory-shaped reparse point
// as if it were a regular file.
func isReparsePoint(path string) bool {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return false
	}
	return attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// dirSize sums file sizes under dir (best-effort: errors and reparse
// points are silently skipped, since this is only used for a
// human-facing size estimate in the migration report).
func dirSize(dir string) int64 {
	var total int64
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || isReparsePoint(path) {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// gitRemoteURL best-effort reads a git repository's "origin" remote URL
// straight out of .git/config, without shelling out to git or requiring
// network access.
func gitRemoteURL(repoDir string) string {
	data, err := os.ReadFile(filepath.Join(repoDir, ".git", "config"))
	if err != nil {
		return ""
	}
	inOrigin := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inOrigin = strings.HasPrefix(line, `[remote "origin"]`)
			continue
		}
		if !inOrigin {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "url"); ok {
			rest = strings.TrimSpace(rest)
			if val, ok := strings.CutPrefix(rest, "="); ok {
				return strings.TrimSpace(val)
			}
		}
	}
	return ""
}
