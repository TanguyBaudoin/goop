package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/index"
	"github.com/TanguyBaudoin/goop/internal/installer"
	"github.com/TanguyBaudoin/goop/internal/paths"
	"github.com/TanguyBaudoin/goop/internal/profile"
	"github.com/TanguyBaudoin/goop/internal/repo"
	"github.com/TanguyBaudoin/goop/internal/ui"
)

// ideProfile is the one profile treated as a choice rather than a set to
// install wholesale. Nothing in a build may depend on an editor, so this
// is the only place goop asks a question -- and the only place a repo's
// declaration is a suggestion rather than a requirement.
const ideProfile = "ide"

// cmdBootstrap brings this machine in line with what the repository
// declares: the profiles it wants, and the toolchain its lockfile pins.
//
// Idempotent by design. Running it after a git pull is the same command
// as running it on day one, and running it twice in a row does nothing
// the second time -- including not reinstalling something the user
// deliberately removed, which is what the applied state is for.
func cmdBootstrap(args []string) int {
	nonInteractive := false
	for _, a := range args {
		if a == "--non-interactive" {
			nonInteractive = true
			continue
		}
		fmt.Fprintln(os.Stderr, "usage: goop bootstrap [--non-interactive]")
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
		fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim(`{"lockfile": "goop.lock.json", "profiles": ["baseline.tool", "ide"]}`))
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
		if name == ideProfile {
			if applyIDE(&state, nonInteractive) {
				acted = true
			}
			continue
		}
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

// applyIDE handles the one interactive step. The question is asked once
// and the answer remembered -- including a refusal, so someone who wants
// no editor from goop is not asked again on every pull.
func applyIDE(state *profile.State, nonInteractive bool) bool {
	if state.IDEAsked {
		return false
	}
	d, err := profile.Load(ideProfile)
	if err != nil || len(d.Apps) == 0 {
		return false
	}

	choice := d.Apps[0] // the default, first as listed
	if nonInteractive || !ui.IsTerminal(os.Stdin) {
		fmt.Println(ui.Dim("choosing " + choice + " (no terminal to ask on)"))
	} else {
		fmt.Println("Which editor would you like? Nothing in a build depends on this.")
		for i, a := range d.Apps {
			marker := " "
			if i == 0 {
				marker = "*"
			}
			fmt.Printf("  %s %d) %s\n", marker, i+1, a)
		}
		answer, err := ui.Ask(fmt.Sprintf("Pick 1-%d, or 'n' for none [%s]: ", len(d.Apps), choice))
		if err != nil {
			return false
		}
		switch {
		case answer == "":
		case strings.EqualFold(answer, "n"), strings.EqualFold(answer, "none"):
			state.IDEAsked = true
			fmt.Println(ui.Dim("no editor installed; goop will not ask again"))
			return false
		default:
			n, err := strconv.Atoi(answer)
			if err != nil || n < 1 || n > len(d.Apps) {
				ui.Warn("%q is not one of the choices; taking %s", answer, choice)
			} else {
				choice = d.Apps[n-1]
			}
		}
	}

	state.IDEAsked = true
	state.IDE = choice
	if installedNames()[choice] {
		return false
	}
	if _, err := installer.Install(choice); err != nil {
		ui.Fail("%s: %v", choice, err)
		return false
	}
	return true
}
