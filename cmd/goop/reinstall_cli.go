package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/installer"
	"github.com/TanguyBaudoin/goop/internal/ui"
)

// cmdReinstall lays an installed version down again from scratch.
//
// `goop reset` repairs the wiring -- shims, shortcuts, environment --
// from the record, without touching a file. This is for when the files
// are what is wrong, and it exists because the alternative was uninstall
// then install, which drops the app out of every profile that named it
// and into `default`.
func cmdReinstall(args []string) int {
	var names []string
	assumeYes := false
	for _, a := range args {
		switch a {
		case "-y", "--yes":
			assumeYes = true
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintln(os.Stderr, "usage: goop reinstall <name>... [-y]")
				return 2
			}
			names = append(names, a)
		}
	}
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "usage: goop reinstall <name>... [-y]")
		fmt.Fprintln(os.Stderr, ui.Dim("  reinstalls the version you have; `goop update` is what moves a version"))
		return 2
	}

	// What will be rebuilt and what will not. The point of the command is
	// that profile membership and persisted data survive, so the plan
	// shows them: a confirmation that does not say what is preserved
	// leaves the caller guessing at exactly the thing they chose this
	// command for.
	type target struct {
		name      string
		version   string
		profiles  []string
		persisted string
	}
	var plan []target
	exit := 0
	for _, n := range names {
		rec, ok := installer.CurrentRecord(n)
		if !ok {
			ui.Fail("reinstall %s: not installed", n)
			exit = 1
			continue
		}
		t := target{name: n, version: rec.Version, profiles: installer.ProfilesOf(n)}
		if size, has := installer.PersistedSize(n); has {
			t.persisted = formatSize(size)
		}
		plan = append(plan, t)
	}
	if len(plan) == 0 {
		return 1
	}

	fmt.Printf("\n%s\n", ui.Bold(fmt.Sprintf("%d package(s) to reinstall", len(plan))))
	rows := make([][]string, len(plan))
	for i, t := range plan {
		profiles := strings.Join(t.profiles, ", ")
		if profiles == "" {
			profiles = ui.Dim("-")
		}
		persisted := t.persisted
		if persisted == "" {
			persisted = ui.Dim("-")
		}
		rows[i] = []string{t.name, t.version, profiles, persisted}
	}
	fmt.Print(ui.Table([]string{"PACKAGE", "VERSION", "STAYS IN PROFILE", "DATA KEPT"}, rows))
	fmt.Println(ui.Dim("  same version, re-extracted; profile membership and persisted data are kept"))
	fmt.Println(ui.Dim("  staged and committed by a rename, so a failure leaves the current install in place"))

	if !assumeYes && !confirm("Reinstall them?") {
		return cancelled()
	}
	fmt.Println()

	for _, t := range plan {
		res, err := installer.Reinstall(t.name)
		if err != nil {
			ui.Fail("reinstall %s: %v", t.name, err)
			exit = 1
			continue
		}
		if len(res.Profiles) > 0 {
			ui.Ok("reinstalled %s %s (still in %s)", res.Name, res.Version, strings.Join(res.Profiles, ", "))
			continue
		}
		ui.Ok("reinstalled %s %s", res.Name, res.Version)
	}
	return exit
}
