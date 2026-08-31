package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/TanguyBaudoin/goop/internal/bucket"
	"github.com/TanguyBaudoin/goop/internal/installer"
	"github.com/TanguyBaudoin/goop/internal/paths"
	"github.com/TanguyBaudoin/goop/internal/ui"
)

// cmdUpdate resolves what would change, shows it, and asks before doing
// it.
//
// It used to run every install first and report afterwards. On a
// maintained machine the answer is "nothing" -- 41 packages, none with a
// newer version -- and finding that out cost a full pass plus 41 lines
// of roll-call. Resolving first is a manifest read per app and turns the
// whole thing into a question that can be answered.
func cmdUpdate(args []string) int {
	var names []string
	noUpdate, assumeYes, dryRun, verbose := false, false, false, false
	for _, a := range args {
		switch a {
		case "--no-update":
			noUpdate = true
		case "-y", "--yes":
			assumeYes = true
		case "--dry-run":
			dryRun = true
		case "-v", "--verbose":
			verbose = true
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintln(os.Stderr, "usage: goop update [name]... [--no-update] [--dry-run] [-y] [-v]")
				return 2
			}
			names = append(names, a)
		}
	}

	// Refresh stale buckets first -- more important here than for
	// install, since update's whole job is to find versions newer than
	// what's installed, and it looks for them *in* the buckets. Against
	// a stale bucket it cheerfully reports "up to date" for apps that
	// have a newer release waiting, which is worse than being slow.
	if !noUpdate && paths.BucketsStale() {
		refreshBuckets()
	}

	plans, err := installer.PlanUpdates(names)
	if err != nil {
		ui.Fail("update: %v", err)
		return 1
	}
	if len(plans) == 0 {
		fmt.Println(ui.Dim("no apps installed"))
		return 0
	}

	var changing, held, current, broken []installer.UpdatePlan
	for _, p := range plans {
		switch {
		case p.Err != nil:
			broken = append(broken, p)
		case p.Held:
			held = append(held, p)
		case p.Changes():
			changing = append(changing, p)
		default:
			current = append(current, p)
		}
	}

	// A package goop could not resolve is a failure to answer the
	// question, not a package with nothing to do -- so it sets the exit
	// code whether or not anything else changed.
	failed := len(broken) > 0

	if len(changing) == 0 {
		ui.Ok("everything is up to date (%d package(s))", len(current)+len(held))
		reportSkipped(current, held, broken, verbose)
		if failed {
			return 1
		}
		return 0
	}

	fmt.Printf("\n%s\n", ui.Bold(fmt.Sprintf("%d package(s) to update", len(changing))))
	rows := make([][]string, len(changing))
	for i, p := range changing {
		rows[i] = []string{p.Name, ui.Dim(p.Have), ui.Green(p.Available), ui.Gray(p.Bucket)}
	}
	fmt.Print(ui.Table([]string{"PACKAGE", "FROM", "TO", "BUCKET"}, rows))
	reportSkipped(current, held, broken, verbose)

	if dryRun {
		fmt.Println()
		fmt.Println(ui.Dim("--dry-run: nothing was changed"))
		if failed {
			return 1
		}
		return 0
	}
	if !assumeYes && !confirmUpdate(len(changing)) {
		fmt.Println(ui.Dim("nothing was changed"))
		return 1
	}
	fmt.Println()

	// Only the ones that actually change. Re-resolving the rest is what
	// produced a screen of "already installed" for no work.
	only := make([]string, len(changing))
	for i, p := range changing {
		only[i] = p.Name
	}
	results, errs, err := installer.UpdateAll(only)
	if err != nil {
		ui.Fail("update: %v", err)
		return 1
	}

	fmt.Println()
	fmt.Println(ui.Bold("update summary"))
	if len(errs) > 0 {
		// First, because it is the part that needs acting on. Buried
		// under a list of successes is how it gets missed.
		fmt.Printf("\n%s\n", ui.Bold(fmt.Sprintf("failed (%d)", len(errs))))
		for _, name := range sortedKeys(errs) {
			ui.Fail("%s: %v", name, errs[name])
		}
	}
	var updated []string
	for name, r := range results {
		if r.Updated {
			updated = append(updated, name)
		}
	}
	sort.Strings(updated)
	if len(updated) > 0 {
		fmt.Printf("\n%s\n", ui.Bold(fmt.Sprintf("updated (%d)", len(updated))))
		for _, name := range updated {
			r := results[name]
			fmt.Printf("%s %-28s %s -> %s\n", ui.Green(ui.CheckMark), name, ui.Dim(r.OldVersion), r.NewVersion)
		}
	}
	if len(errs) > 0 || failed {
		return 1
	}
	return 0
}

// confirmUpdate asks, but only where there is someone to ask. A
// non-interactive run -- CI, a pipe, a scheduled task -- proceeds: this
// is a routine operation, not a destructive one, and refusing there
// would make `goop update` unusable unattended.
func confirmUpdate(n int) bool {
	if !ui.IsTerminal(os.Stdin) {
		return true
	}
	fmt.Printf("\nUpdate %s? [Y/n] ", ui.Bold(fmt.Sprintf("%d package(s)", n)))
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Println()
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes", "o", "oui":
		return true
	default:
		return false
	}
}

// refreshBuckets updates every bucket, naming each and how long it took.
// On a slow link a silent multi-second pause is indistinguishable from a
// hang, which is the complaint this answers.
func refreshBuckets() {
	fmt.Println(ui.Dim("refreshing buckets (skip with --no-update)"))
	start := time.Now()
	results, err := bucket.UpdateAllReport(nil)
	if err != nil {
		ui.Fail("bucket update: %v", err) // non-fatal: updating from cache still beats refusing
		return
	}
	for _, r := range results {
		if r.Err != nil {
			ui.Fail("  %-20s %v", r.Name, r.Err)
			continue
		}
		fmt.Printf("  %s %-20s %s\n", ui.Green(ui.CheckMark), r.Name,
			ui.Dim(r.Duration.Round(time.Millisecond).String()))
	}
	if len(results) > 1 {
		fmt.Println(ui.Dim(fmt.Sprintf("  %d bucket(s) in %s",
			len(results), time.Since(start).Round(time.Millisecond))))
	}
	if err := paths.MarkBucketsUpdated(); err != nil {
		ui.Fail("bucket update: %v", err)
	}
}

// reportSkipped accounts for what will not be touched.
//
// Held packages and errors are always named: a held app that quietly did
// not update looks exactly like one with nothing to update, and an
// unresolvable one looks like a healthy one. Packages already current
// are a count, because listing all of them is the noise this replaces.
func reportSkipped(current, held, broken []installer.UpdatePlan, verbose bool) {
	if len(held) > 0 {
		fmt.Printf("\n%s\n", ui.Bold(fmt.Sprintf("held, not updated (%d)", len(held))))
		for _, p := range held {
			fmt.Println(ui.Gray(fmt.Sprintf("%s %-28s %s", ui.CheckMark, p.Name, p.Have)))
		}
	}
	if len(broken) > 0 {
		fmt.Printf("\n%s\n", ui.Bold(fmt.Sprintf("could not be checked (%d)", len(broken))))
		for _, p := range broken {
			ui.Fail("%s: %v", p.Name, p.Err)
		}
	}
	if len(current) == 0 {
		return
	}
	if verbose {
		fmt.Printf("\n%s\n", ui.Bold(fmt.Sprintf("already up to date (%d)", len(current))))
		for _, p := range current {
			fmt.Println(ui.Gray(fmt.Sprintf("%s %-28s %s", ui.CheckMark, p.Name, p.Have)))
		}
		return
	}
	fmt.Println(ui.Dim(fmt.Sprintf("  %d already up to date (-v to list)", len(current))))
}
