package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/installer"
	"github.com/TanguyBaudoin/goop/internal/ui"
)

const useUsage = "usage: goop use <name> [<version>]"

// cmdUse points an app at a version already on disk.
//
// With no version it lists what is installed instead of guessing, which
// is the common case: someone reaching for this rarely remembers the
// exact version string, and `goop use jq` answering with the choices is
// more useful than an error about a missing argument.
func cmdUse(args []string) int {
	var names []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			ui.Fail("goop use takes no flags (got %s)", a)
			fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim(useUsage))
			return 2
		}
		names = append(names, a)
	}
	if len(names) == 0 || len(names) > 2 {
		fmt.Fprintln(os.Stderr, useUsage)
		return 2
	}

	appName := names[0]
	if len(names) == 1 {
		return listVersions(appName)
	}

	if err := installer.Use(appName, names[1]); err != nil {
		ui.Fail("%v", err)
		return 1
	}
	return 0
}

func listVersions(appName string) int {
	versions, err := installer.InstalledVersions(appName)
	if err != nil {
		ui.Fail("%v", err)
		return 1
	}
	if len(versions) == 0 {
		ui.Fail("%s has no installed version with a usable receipt", appName)
		return 1
	}

	rows := make([][]string, len(versions))
	for i, v := range versions {
		mark := ""
		if v.Current {
			mark = "current"
		}
		rows[i] = []string{v.Version, mark}
	}
	fmt.Print(ui.Table([]string{"VERSION", ""}, rows))
	fmt.Fprintf(os.Stderr, "\n%s\n", ui.Dim(fmt.Sprintf("switch with `goop use %s <version>` -- nothing is downloaded", appName)))
	return 0
}
