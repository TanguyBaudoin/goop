package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/envvars"
	"github.com/TanguyBaudoin/goop/internal/paths"
	"github.com/TanguyBaudoin/goop/internal/shim"
)

// Diagnose inspects an installation for the states that break it quietly.
//
// Every check here exists because the condition it looks for actually
// happened, on a real machine, and went unnoticed until something else
// failed:
//
//   - A `current` junction replaced by a real directory left zen-browser
//     permanently unupdatable (exit status 145), because the updater
//     rewrote what goop expected to be a link.
//   - `persist\vlc` survived `goop uninstall vlc` at 6.9 MB, with four
//     more orphaned persist directories alongside it, one of them 3.8 GB.
//   - `env_add_path` names a versioned directory, so PATH accumulated an
//     entry per update: 80 of 98 per-user entries were dead.
//   - A shim pointing at a missing target looks installed and reports no
//     deviation; it fails only when someone runs the command.
//
// Read-only by design. Each finding names the command that fixes it
// rather than acting: `doctor` is what you run when you do not yet know
// what is wrong, and that is the worst moment to start changing things.
func Diagnose() ([]Finding, error) {
	records, err := List()
	if err != nil {
		return nil, fmt.Errorf("reading installed packages: %w", err)
	}

	var out []Finding
	add := func(f *Finding) {
		if f != nil {
			out = append(out, *f)
		}
	}
	add(checkRoot())
	add(checkPendingInstalls(records))
	add(checkCurrentJunctions(records))
	add(checkShimTargets())
	for _, f := range checkMissingReceipts() {
		add(f)
	}
	add(checkOrphanedPersist(records))
	add(checkStalePathEntries())
	add(checkDeadPathEntries())
	return out, nil
}

// Severity orders findings by how much they warrant doing something.
type Severity int

const (
	// SevProblem is something already broken: a command that will not
	// run, an install that never finished.
	SevProblem Severity = iota
	// SevUntidy is waste or drift that works today: dead PATH entries,
	// persisted data for packages long gone.
	SevUntidy
)

func (s Severity) String() string {
	if s == SevProblem {
		return "problem"
	}
	return "untidy"
}

// Finding is one diagnosis.
type Finding struct {
	Check    string // stable identifier, so output can be grepped
	Severity Severity
	Summary  string   // one line, no trailing period
	Items    []string // the specific things found
	Fix      string   // the command that resolves it, empty if manual
}

func checkRoot() *Finding {
	root := paths.Root()
	st, err := os.Stat(root)
	if err != nil {
		return &Finding{
			Check:    "root-missing",
			Severity: SevProblem,
			Summary:  "the goop root does not exist",
			Items:    []string{fmt.Sprintf("%s: %v", root, err)},
			Fix:      "re-run the installer, or `goop config set-root <path>` if it moved",
		}
	}
	if !st.IsDir() {
		return &Finding{
			Check:    "root-not-a-directory",
			Severity: SevProblem,
			Summary:  "the goop root is not a directory",
			Items:    []string{root},
		}
	}
	// Writability is worth proving rather than assuming: a root on a
	// network share or under a directory someone re-permissioned reads
	// fine and fails on the first install.
	probe := filepath.Join(root, ".goop-doctor-probe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		return &Finding{
			Check:    "root-not-writable",
			Severity: SevProblem,
			Summary:  "the goop root is not writable",
			Items:    []string{fmt.Sprintf("%s: %v", root, err)},
		}
	}
	os.Remove(probe)
	return nil
}

// checkPendingInstalls finds installs that were interrupted between the
// commit rename and the shims being put in place.
//
// The files are there and the version directory looks complete, so
// nothing else notices; the package simply has no working commands.
func checkPendingInstalls(records []Record) *Finding {
	var items []string
	for _, r := range records {
		if !r.Ready() {
			items = append(items, fmt.Sprintf("%s %s", r.Name, r.Version))
		}
	}
	if len(items) == 0 {
		return nil
	}
	sort.Strings(items)
	return &Finding{
		Check:    "install-never-finished",
		Severity: SevProblem,
		Summary:  "install interrupted before its shims were in place",
		Items:    items,
		Fix:      "goop reset <name>",
	}
}

// checkCurrentJunctions verifies that every installed package still has
// `current` as a link.
//
// This is the zen-browser failure: an application that rewrites its own
// install directory can replace the junction with a real directory, and
// from then on every shim points into a version that the next update
// will move.
func checkCurrentJunctions(records []Record) *Finding {
	var items []string
	seen := map[string]bool{}
	for _, r := range records {
		if seen[r.Name] {
			continue
		}
		seen[r.Name] = true
		cur := paths.AppCurrent(r.Name)
		st, err := os.Lstat(cur)
		switch {
		case err != nil:
			items = append(items, fmt.Sprintf("%s: no `current` at all (%v)", r.Name, err))
		case !isReparsePoint(cur):
			kind := "a real directory"
			if !st.IsDir() {
				kind = "a file"
			}
			items = append(items, fmt.Sprintf("%s: `current` is %s, not a link", r.Name, kind))
		}
	}
	if len(items) == 0 {
		return nil
	}
	sort.Strings(items)
	return &Finding{
		Check:    "current-not-a-link",
		Severity: SevProblem,
		Summary:  "`current` is not a link, so updates cannot repoint it",
		Items:    items,
		Fix:      "goop reinstall <name>",
	}
}

// checkShimTargets finds shims whose target is gone.
//
// A shim that points nowhere is the failure mode that survives a green
// build and a clean `goop profile check`: everything reports installed,
// and the command fails only when a person runs it.
func checkShimTargets() *Finding {
	entries, err := os.ReadDir(paths.Shims())
	if err != nil {
		return nil // no shims directory is not itself a fault
	}
	var items []string
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".shim") {
			continue
		}
		sidecarPath := filepath.Join(paths.Shims(), e.Name())
		sc, err := shim.LoadSidecar(sidecarPath)
		if err != nil {
			items = append(items, fmt.Sprintf("%s: unreadable (%v)", e.Name(), err))
			continue
		}
		if _, err := os.Stat(sc.Path); err != nil {
			items = append(items, fmt.Sprintf("%s -> %s (missing)", e.Name(), sc.Path))
			continue
		}
		// A target still inside a staging directory means a commit that
		// never completed; the path exists now and will not survive.
		if strings.Contains(sc.Path, ".partial") {
			items = append(items, fmt.Sprintf("%s -> %s (still in staging)", e.Name(), sc.Path))
		}
	}
	if len(items) == 0 {
		return nil
	}
	sort.Strings(items)
	return &Finding{
		Check:    "shim-target-missing",
		Severity: SevProblem,
		Summary:  "shims pointing at files that are not there",
		Items:    items,
		Fix:      "goop reset <name>, or `goop uninstall <name>` if it is no longer wanted",
	}
}

// checkMissingReceipts finds version directories with no install record.
//
// Severity depends on which version it is, and getting that wrong was
// the first thing a real machine corrected. Zen Browser's own updater
// replaces `current` with a real directory and moves the app -- record
// included -- into it, leaving the previous version directories behind
// with no record of their own. Reporting those as "cannot be removed
// cleanly" was alarming and wrong: the record exists, just not where it
// used to, and the leftovers are only taking up space.
//
// So: the version `current` resolves to is the one that matters. Without
// its record goop cannot uninstall the package properly, because the
// shims, shortcuts and environment entries it created are recorded
// nowhere else. Any other version directory without a record is a
// leftover, and `goop cleanup` is what clears it.
func checkMissingReceipts() []*Finding {
	apps, err := os.ReadDir(paths.Apps())
	if err != nil {
		return nil
	}
	var active, leftover []string
	for _, app := range apps {
		if !app.IsDir() {
			continue
		}
		// Which version is live. An error here means goop itself cannot
		// find a record, which the current-junction and broken-record
		// checks already report -- so treat every version as a leftover
		// rather than claiming a second problem about the same thing.
		activeDir, activeErr := currentRecordDir(app.Name())
		versions, err := os.ReadDir(paths.App(app.Name()))
		if err != nil {
			continue
		}
		for _, v := range versions {
			if !v.IsDir() || v.Name() == "current" || strings.HasSuffix(v.Name(), ".partial") {
				continue
			}
			dir := filepath.Join(paths.App(app.Name()), v.Name())
			// A reparse point here is `current` under another name; it is
			// not a version directory of its own.
			if isReparsePoint(dir) {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, recordFileName)); err == nil {
				continue
			}
			label := fmt.Sprintf("%s %s", app.Name(), v.Name())
			if activeErr == nil && sameDir(dir, activeDir) {
				active = append(active, label)
			} else {
				leftover = append(leftover, label)
			}
		}
	}
	var out []*Finding
	if len(active) > 0 {
		sort.Strings(active)
		out = append(out, &Finding{
			Check:    "receipt-missing",
			Severity: SevProblem,
			Summary:  "the installed version has no install record, so it cannot be removed cleanly",
			Items:    active,
			Fix:      "goop reinstall <name>, which rewrites the record",
		})
	}
	if len(leftover) > 0 {
		sort.Strings(leftover)
		out = append(out, &Finding{
			Check:    "version-leftover",
			Severity: SevUntidy,
			Summary:  "old version directories with no install record, left by a self-updating app",
			Items:    leftover,
			Fix:      "goop cleanup",
		})
	}
	return out
}

// sameDir compares two paths as Windows would: case-insensitively, and
// without caring about a trailing separator.
func sameDir(a, b string) bool {
	return strings.EqualFold(
		strings.TrimRight(filepath.Clean(a), `\/`),
		strings.TrimRight(filepath.Clean(b), `\/`))
}

// checkOrphanedPersist finds persisted data for packages that are gone.
//
// `goop uninstall` keeps persisted data on purpose -- it is the user's,
// not the package's -- but nothing ever points out that it is still
// there. Measured on a real machine: five orphaned directories, the
// largest 3.8 GB.
func checkOrphanedPersist(records []Record) *Finding {
	persistRoot := filepath.Dir(paths.Persist("x"))
	entries, err := os.ReadDir(persistRoot)
	if err != nil {
		return nil
	}
	installed := map[string]bool{}
	for _, r := range records {
		installed[strings.ToLower(r.Name)] = true
	}
	var items []string
	for _, e := range entries {
		if !e.IsDir() || installed[strings.ToLower(e.Name())] {
			continue
		}
		size, files := treeSize(filepath.Join(persistRoot, e.Name()))
		items = append(items, fmt.Sprintf("%s (%s across %d file(s))", e.Name(), humanBytes(size), files))
	}
	if len(items) == 0 {
		return nil
	}
	sort.Strings(items)
	return &Finding{
		Check:    "persist-orphaned",
		Severity: SevUntidy,
		Summary:  "persisted data for packages that are no longer installed",
		Items:    items,
		Fix:      "`goop uninstall <name> --purge` removes it, or delete the directory if the package is already gone",
	}
}

func checkStalePathEntries() *Finding {
	stale, err := StalePathEntries()
	if err != nil || len(stale) == 0 {
		return nil
	}
	items := make([]string, 0, len(stale))
	for _, s := range stale {
		items = append(items, fmt.Sprintf("%s (%s)", s.Path, s.Reason))
	}
	sort.Strings(items)
	return &Finding{
		Check:    "path-superseded",
		Severity: SevUntidy,
		Summary:  "PATH entries left by versions an update superseded",
		Items:    items,
		Fix:      "goop cleanup",
	}
}

// checkDeadPathEntries reports PATH entries that point nowhere.
//
// Only those under the goop root: the rest of a person's PATH is not
// goop's business to audit, and saying so is the difference between a
// useful report and a lecture.
func checkDeadPathEntries() *Finding {
	entries, err := envvars.PathEntries()
	if err != nil {
		return nil
	}
	root := strings.ToLower(paths.Root())
	var items []string
	for _, e := range entries {
		if e == "" {
			continue
		}
		expanded := os.ExpandEnv(strings.ReplaceAll(e, "%", "$"))
		if !strings.HasPrefix(strings.ToLower(e), root) && !strings.HasPrefix(strings.ToLower(expanded), root) {
			continue
		}
		if _, err := os.Stat(expanded); err != nil {
			items = append(items, e)
		}
	}
	if len(items) == 0 {
		return nil
	}
	sort.Strings(items)
	return &Finding{
		Check:    "path-dead",
		Severity: SevUntidy,
		Summary:  "PATH entries under the goop root that point nowhere",
		Items:    items,
		Fix:      "goop cleanup, which prunes the ones it recognises",
	}
}

// treeSize totals a directory, returning bytes and file count.
//
// Separate from migrate.go's dirSize, which returns bytes only -- the
// file count is what distinguishes "a stray lock file" from "a package's
// whole data directory" in the report. Errors are ignored on purpose: a
// partial total is still worth reporting, and `doctor` must never fail
// because one file could not be read.
func treeSize(dir string) (int64, int) {
	var total int64
	var count int
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
			count++
		}
		return nil
	})
	return total, count
}
