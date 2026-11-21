package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/installer"
	"github.com/TanguyBaudoin/goop/internal/ui"
)

// cmdDoctor reports what is wrong with an installation without changing
// anything.
//
// Read-only on purpose. `doctor` is what you reach for when you do not
// yet know what is wrong, which is the worst possible moment to start
// changing things -- so each finding names the command that fixes it and
// leaves the decision alone.
//
// Exit 3 when something was found, matching `goop profile check` and
// `goop machine audit`, so a scheduled run or a CI step can act on it.
func cmdDoctor(args []string) int {
	quiet := false
	var unknown []string
	for _, a := range args {
		switch a {
		case "-q", "--quiet":
			quiet = true
		default:
			unknown = append(unknown, a)
		}
	}
	if len(unknown) > 0 {
		ui.Fail("goop doctor takes no arguments (got %s)", strings.Join(unknown, " "))
		fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("usage: goop doctor [-q]"))
		return 2
	}

	findings, err := installer.Diagnose()
	if err != nil {
		ui.Fail("%v", err)
		return 1
	}

	if len(findings) == 0 {
		ui.Ok("no problems found")
		return 0
	}

	problems, untidy := 0, 0
	for _, f := range findings {
		if f.Severity == installer.SevProblem {
			problems++
		} else {
			untidy++
		}
	}

	if !quiet {
		for _, f := range findings {
			fmt.Println()
			// The marker carries the severity, so a run piped into a file
			// without colour still distinguishes the two.
			marker := "untidy "
			if f.Severity == installer.SevProblem {
				marker = "problem"
			}
			fmt.Printf("%s  %s\n", ui.Bold(marker), ui.Bold(f.Summary))
			for _, item := range f.Items {
				fmt.Printf("    %s\n", item)
			}
			if f.Fix != "" {
				fmt.Printf("    %s %s\n", ui.Dim("fix:"), f.Fix)
			}
		}
		fmt.Println()
	}

	// The summary has to distinguish the two, because they call for
	// different reactions: a problem means something is already broken, a
	// untidy finding means disk and PATH are carrying things nobody needs.
	switch {
	case problems > 0 && untidy > 0:
		fmt.Printf("%s\n", ui.Bold(fmt.Sprintf("%d problem(s), %d untidy finding(s)", problems, untidy)))
	case problems > 0:
		fmt.Printf("%s\n", ui.Bold(fmt.Sprintf("%d problem(s)", problems)))
	default:
		fmt.Printf("%s\n", ui.Bold(fmt.Sprintf("%d untidy finding(s), nothing broken", untidy)))
	}
	return 3
}
