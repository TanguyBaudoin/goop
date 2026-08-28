package main

import (
	"fmt"
	"os"

	"github.com/TanguyBaudoin/goop/internal/selfupdate"
	"github.com/TanguyBaudoin/goop/internal/ui"
)

func cmdSelfUpdate(args []string) int {
	force := false
	for _, a := range args {
		if a == "--force" {
			force = true
			continue
		}
		fmt.Fprintln(os.Stderr, "usage: goop self-update [--force]")
		return 2
	}
	selfupdate.Logf = func(format string, a ...any) {
		fmt.Println(ui.Cyan(ui.Arrow) + " " + fmt.Sprintf(format, a...))
	}
	res, err := selfupdate.Update(version, force)
	if err != nil {
		ui.Fail("self-update: %v", err)
		return 1
	}
	if res.AlreadyCurrent {
		ui.Ok("goop %s is already the current release", res.OldVersion)
		return 0
	}
	if res.OldVersion == res.NewVersion {
		// Same version number, different bytes -- a locally built binary
		// replaced by the published build of the same release.
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
