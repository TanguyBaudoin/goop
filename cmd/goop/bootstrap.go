package main

import (
	"fmt"
	"os"

	"github.com/TanguyBaudoin/goop/internal/index"
	"github.com/TanguyBaudoin/goop/internal/installer"
	"github.com/TanguyBaudoin/goop/internal/paths"
	"github.com/TanguyBaudoin/goop/internal/profile"
	"github.com/TanguyBaudoin/goop/internal/repo"
	"github.com/TanguyBaudoin/goop/internal/ui"
)

// cmdBootstrap brings this machine in line with what the repository
// declares: the profiles it wants, and the toolchain its lockfile pins.
//
// Idempotent by design. Running it after a git pull is the same command
// as running it on day one, and running it twice in a row does nothing
// the second time -- including not reinstalling something the user
// deliberately removed, which is what the applied state is for.
func cmdBootstrap(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "usage: goop bootstrap")
		return 2
	}

	cwd, err := os.Getwd()
	if err != nil {
		ui.Fail("bootstrap: %v", err)
		return 1
	}
	cfg, found, err := repo.Find(cwd)
	if err != nil {
		ui.Fail("bootstrap: %v", err)
		return 1
	}
	if !found {
		ui.Fail("bootstrap: no %s here or in any parent directory", repo.FileName)
		fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("A repository declares what it needs in "+repo.FileName+", for example:"))
		fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim(`{"lockfile": "goop.lock.json", "profiles": ["baseline.tool"]}`))
		return 1
	}
	fmt.Println(ui.Dim("using " + cfg.Dir))

	acted := false

	// The index is a small document and the machine may have been away
	// for a while, so refresh it every time -- but never fail on it. A
	// stale index still resolves profiles, and the lockfile below does
	// not depend on it at all.
	if _, ok := paths.ConfiguredIndex(); ok {
		if _, err := index.Update(); err != nil {
			ui.Warn("could not refresh the profile index: %v", err)
			fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("continuing with the cached copy"))
		}
	}

	state := profile.LoadState()
	for _, name := range cfg.Profiles {
		if applyProfile(name, &state) {
			acted = true
		}
	}
	if err := profile.SaveState(state); err != nil {
		ui.Fail("bootstrap: saving state: %v", err)
		return 1
	}

	// The toolchain last: it is what the build actually needs, so a
	// failure here is the one that matters and should be the final word.
	lock := cfg.LockfilePath()
	if _, err := os.Stat(lock); err != nil {
		fmt.Println(ui.Dim("no lockfile at " + lock + " -- nothing pinned to install"))
		if !acted {
			fmt.Println(ui.Dim("nothing to do"))
		}
		return 0
	}
	res, err := installer.Sync(lock)
	if err != nil {
		ui.Fail("bootstrap: %v", err)
		return 1
	}
	for _, c := range res.Installed {
		acted = true
		if c.OldVersion == "" {
			fmt.Printf("%s installed %-28s %s\n", ui.Green(ui.CheckMark), c.Name, c.NewVersion)
		} else {
			fmt.Printf("%s synced    %-28s %s -> %s\n", ui.Green(ui.CheckMark), c.Name, ui.Dim(c.OldVersion), c.NewVersion)
		}
	}
	for _, n := range sortedKeys(res.Errors) {
		ui.Fail("bootstrap %s: %v", n, res.Errors[n])
	}
	if len(res.Errors) > 0 {
		return 1
	}
	if !acted {
		fmt.Println(ui.Dim("already up to date"))
	}
	return 0
}

// applyProfile installs a profile's members that are missing, skipping
// any the user has removed since bootstrap put them there. Reports
// whether it changed anything.
func applyProfile(name string, state *profile.State) bool {
	d, err := profile.Load(name)
	if err != nil {
		ui.Fail("profile %s: %v", name, err)
		return false
	}
	if len(d.Apps) == 0 {
		return false
	}
	installed := installedNames()

	acted := false
	for _, app := range d.Apps {
		if installed[app] {
			state.MarkApplied(name, app)
			continue
		}
		// Installed once by bootstrap, gone now: someone removed it on
		// purpose. Putting it back on every run would make `goop
		// uninstall` useless for anything a profile mentions.
		if state.WasApplied(name, app) {
			fmt.Println(ui.Dim(fmt.Sprintf("%s: %s was removed here, leaving it out", name, app)))
			continue
		}
		if _, err := installer.Install(app); err != nil {
			ui.Fail("%s: %v", app, err)
			continue
		}
		state.MarkApplied(name, app)
		acted = true
	}
	return acted
}

func installedNames() map[string]bool {
	out := map[string]bool{}
	recs, err := installer.List()
	if err != nil {
		return out
	}
	for _, r := range recs {
		out[r.Name] = true
	}
	return out
}
