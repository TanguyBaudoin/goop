package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/selfupdate"
	"github.com/TanguyBaudoin/goop/internal/ui"
)

func cmdSelfUpdate(args []string) int {
	force, assumeYes, dryRun := false, false, false
	for _, a := range args {
		switch a {
		case "--force":
			force = true
		case "-y", "--yes":
			assumeYes = true
		case "--dry-run":
			dryRun = true
		default:
			fmt.Fprintln(os.Stderr, "usage: goop self-update [--dry-run] [-y] [--force]")
			return 2
		}
	}
	selfupdate.Logf = func(format string, a ...any) {
		fmt.Println(ui.Cyan(ui.Arrow) + " " + fmt.Sprintf(format, a...))
	}

	// Checking costs a checksum file and a redirect -- a few dozen bytes
	// -- so the versions can be shown and confirmed before spending
	// someone's bandwidth on 14 MB they may not want.
	plan, err := selfupdate.Check(version)
	if err != nil {
		ui.Fail("self-update: %v", err)
		return 1
	}
	if plan.AlreadyCurrent {
		ui.Ok("goop %s is already the current release", plan.CurrentVersion)
		return 0
	}

	available := plan.Available
	if available == "" {
		// The redirect is a courtesy, not a requirement: the binary is
		// run and its own version checked before any swap. Say so rather
		// than print a blank.
		available = ui.Dim("unknown until downloaded")
	} else if available == plan.CurrentVersion {
		// Same number, different bytes -- typically a locally built
		// binary against the published build of the same release.
		available = plan.Available + ui.Dim(" (same version, different build)")
	} else {
		available = ui.Green(plan.Available)
	}
	fmt.Println()
	fmt.Print(ui.Table([]string{"", "VERSION"}, [][]string{
		{"running", ui.Dim(plan.CurrentVersion)},
		{"available", available},
	}))
	fmt.Println(ui.Dim("  " + strings.TrimSpace(binaryPath(plan))))

	if dryRun {
		fmt.Println()
		fmt.Println(ui.Dim("--dry-run: nothing was changed"))
		return 0
	}
	if !assumeYes && !confirm("Replace the running goop?") {
		return cancelled()
	}
	fmt.Println()

	res, err := plan.Apply(force)
	if err != nil {
		ui.Fail("self-update: %v", err)
		return 1
	}
	if res.OldVersion == res.NewVersion {
		ui.Ok("replaced goop %s with the published %s build", res.OldVersion, res.NewVersion)
	} else {
		ui.Ok("updated goop %s -> %s", res.OldVersion, res.NewVersion)
	}
	// Naming the path matters: someone who ran this from a checkout has
	// just had their locally built binary replaced by a release, and
	// should see that at a glance rather than discover it later.
	fmt.Println(ui.Dim("  " + res.Path))
	fmt.Println(ui.Dim("Open a new shell if one still has the old goop running."))
	return 0
}

// binaryPath is what would be replaced -- worth showing before the
// question, since running this from a checkout replaces a locally built
// binary with a release.
func binaryPath(plan selfupdate.Plan) string {
	if p := plan.TargetPath(); p != "" {
		return p
	}
	return ""
}
