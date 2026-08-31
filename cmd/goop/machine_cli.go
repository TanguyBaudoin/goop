package main

import (
	"fmt"
	"os"

	"github.com/TanguyBaudoin/goop/internal/ui"
)

// cmdMachine groups the whole-machine plane: capture this machine,
// restore it elsewhere, check one against the other.
//
// It has nothing to do with any repository. A capture describes a machine
// and is stale tomorrow; a profile file describes a project and belongs
// in its history. Keeping them under different subjects is what stopped
// the two from being confused for each other.
func cmdMachine(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: goop machine <export|restore|audit> ...")
		return 2
	}
	switch args[0] {
	case "export":
		return cmdExport(args[1:])
	case "restore":
		return cmdImportSetup(args[1:])
	case "audit":
		return cmdAudit(args[1:])
	default:
		fmt.Fprintln(os.Stderr, "usage: goop machine <export|restore|audit> ...")
		return 2
	}
}

// movedCommand tells someone using a name from 0.3.x where it went.
// "unknown command" would be a poor way to discover a rename.
var movedNames = map[string]string{
	"export": "goop machine export",
	"import": "goop machine restore",
	"audit":  "goop machine audit",
	"check":  "goop profile check",
	"sync":   "goop profile sync",
}

func movedCommand(old string) int {
	ui.Fail("`goop %s` is now `%s`", old, movedNames[old])
	fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("The subject is part of the name now: `goop machine ...` describes this"))
	fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("machine, `goop profile ...` describes what a repository needs."))
	return 2
}
