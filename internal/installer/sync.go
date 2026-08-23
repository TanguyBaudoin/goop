package installer

import (
	"fmt"
	"strings"
	"sync"

	"github.com/TanguyBaudoin/goop/internal/lockfile"
	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/paths"
	"github.com/TanguyBaudoin/goop/internal/profile"
)

// Lock snapshots every currently-installed app into profileName's
// membership file (EXF-10) -- profileName defaults to the active profile
// (profile.Active()) when empty, so plain `goop lock` still targets
// whatever profile is currently in use (profile.Default,
// "<root>/goop.lock.json", if none has ever been activated -- unchanged
// from before profiles existed). Apps whose record can't be read (broken
// `current`) are skipped -- there's nothing trustworthy to lock.
func Lock(profileName string) (lockfile.File, error) {
	if profileName == "" {
		profileName = profile.Active()
	}
	records, err := List()
	if err != nil {
		return lockfile.File{}, err
	}
	var f lockfile.File
	for _, r := range records {
		if strings.HasPrefix(r.Version, "(broken") {
			Logf("%s: skipped (unreadable record)", r.Name)
			continue
		}
		f.Entries = append(f.Entries, lockfile.Entry{
			Name:         r.Name,
			Version:      r.Version,
			Bucket:       r.Bucket,
			Architecture: r.Architecture,
			URLs:         r.URLs,
			Hashes:       r.Hashes,
			Bin:          r.Bin,
			ExtractDirs:  r.ExtractDirs,
			ExtractTos:   r.ExtractTos,
		})
	}
	if err := lockfile.Save(profile.Path(profileName), f); err != nil {
		return lockfile.File{}, err
	}
	return f, nil
}

// SyncResult tallies what Sync did.
type SyncResult struct {
	Installed []SyncChange // brought in line with the lock
	AlreadyOK []SyncChange // already installed at the locked version
	Errors    map[string]error
}

// SyncChange is what sync did to one app. OldVersion is empty when the
// app wasn't installed at all beforehand, so a caller can tell a fresh
// install apart from a version change -- "what actually changed" being
// the point of a sync report, not just which names were touched.
type SyncChange struct {
	Name       string
	OldVersion string
	NewVersion string
}

// Sync installs every entry in profileName's membership file that isn't
// already installed at exactly that version -- profileName defaults to
// the active profile when empty, same as Lock. A pinned entry (from a
// snapshot via Lock) uses ONLY its own frozen fields, never a bucket
// (EXF-11: deterministic, no resolution step). A bare entry (from
// `goop profile add`, no pinned version/URLs/hashes -- declarative
// authoring, not a snapshot) has nothing frozen to install from, so it
// resolves live via the normal bucket path instead, same as
// `goop install <name>`. An entry whose locked version differs from
// what's currently `current` gets the locked version installed and made
// current, leaving the previously current version's files in place
// (consistent with how any other version switch works). Entries sync
// concurrently (A1), bounded the same way InstallAll is.
func Sync(profileName string) (SyncResult, error) {
	if profileName == "" {
		profileName = profile.Active()
	}
	path := profile.Path(profileName)
	f, err := lockfile.Load(path)
	if err != nil {
		return SyncResult{}, fmt.Errorf("load profile %q (%s): %w", profileName, path, err)
	}

	concurrency := defaultConcurrency()
	if concurrency > len(f.Entries) {
		concurrency = len(f.Entries)
	}
	if concurrency < 1 {
		concurrency = 1
	}

	res := SyncResult{Errors: map[string]error{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	for _, e := range f.Entries {
		wg.Add(1)
		sem <- struct{}{}
		go func(e lockfile.Entry) {
			defer wg.Done()
			defer func() { <-sem }()

			// Captured before anything is installed, so the report can
			// say what the app moved *from* (empty when it wasn't
			// installed at all).
			var before string
			if rec, ok := readCurrentRecord(e.Name); ok {
				before = rec.Version
			}

			if e.Version == "" {
				// installSpec directly, not the public Install() -- this
				// entry is already a member of profileName (that's why
				// it's being synced); Install()'s profile-registration
				// side effect would incorrectly also add it to whatever
				// profile happens to be active right now.
				//
				// quiet: this entry is reported in res.Installed and
				// printed by the caller's own summary, so the pipeline's
				// "already installed" line would just duplicate it. A
				// genuinely new install still logs its download and
				// hooks normally -- quiet only silences that one line.
				newRec, err := installSpec(e.Name, nil, true)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					res.Errors[e.Name] = err
					return
				}
				change := SyncChange{Name: e.Name, OldVersion: before, NewVersion: newRec.Version}
				// A bare entry resolves live, so "nothing changed" is a
				// perfectly normal outcome -- report it as in sync
				// rather than as work done.
				if before == newRec.Version {
					res.AlreadyOK = append(res.AlreadyOK, change)
				} else {
					res.Installed = append(res.Installed, change)
				}
				return
			}

			versionDir := paths.AppVersion(e.Name, e.Version)
			if _, ok := readRecord(versionDir); ok {
				err := relinkCurrent(e.Name, versionDir)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					res.Errors[e.Name] = err
					return
				}
				// Deliberately not logged here: the entry is already
				// reported in res.AlreadyOK, and every caller prints
				// that as its own sorted summary -- logging as well
				// meant a real `goop sync` listed each unchanged app
				// twice, once unsorted here and once in the summary.
				//
				// `before` can differ from e.Version here: the locked
				// version was already on disk but `current` pointed at
				// another one, so relinkCurrent above just moved it --
				// a real change worth reporting as such.
				change := SyncChange{Name: e.Name, OldVersion: before, NewVersion: e.Version}
				if before == e.Version {
					res.AlreadyOK = append(res.AlreadyOK, change)
				} else {
					res.Installed = append(res.Installed, change)
				}
				return
			}

			resolved := manifest.Resolved{
				Name:        e.Name,
				Version:     e.Version,
				URLs:        e.URLs,
				Hashes:      e.Hashes,
				Bin:         e.Bin,
				ExtractDirs: e.ExtractDirs,
				ExtractTos:  e.ExtractTos,
			}
			_, err := installResolved(e.Name, e.Bucket, e.Architecture, resolved, false)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				res.Errors[e.Name] = err
				return
			}
			res.Installed = append(res.Installed, SyncChange{Name: e.Name, OldVersion: before, NewVersion: e.Version})
		}(e)
	}
	wg.Wait()
	return res, nil
}

// DriftEntry is one locked app whose installed state doesn't match the
// lock: either a different version is current, or it isn't installed
// at all (Current == "").
type DriftEntry struct {
	Name    string
	Locked  string
	Current string
}

// Status compares profileName's membership file against installed state
// without changing anything (EXF-12), for CI drift detection.
// profileName defaults to the active profile when empty, same as Lock.
func Status(profileName string) ([]DriftEntry, error) {
	if profileName == "" {
		profileName = profile.Active()
	}
	path := profile.Path(profileName)
	f, err := lockfile.Load(path)
	if err != nil {
		return nil, fmt.Errorf("load profile %q (%s): %w", profileName, path, err)
	}

	var drift []DriftEntry
	for _, e := range f.Entries {
		rec, ok := readCurrentRecord(e.Name)
		if !ok {
			drift = append(drift, DriftEntry{Name: e.Name, Locked: e.Version})
			continue
		}
		if rec.Version != e.Version {
			drift = append(drift, DriftEntry{Name: e.Name, Locked: e.Version, Current: rec.Version})
		}
	}
	return drift, nil
}
