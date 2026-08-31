package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/installer"
	"github.com/TanguyBaudoin/goop/internal/manifest"
	"github.com/TanguyBaudoin/goop/internal/ui"
)

// confirm asks a yes/no question, defaulting to yes.
//
// It only asks where there is someone to answer. A pipe, CI or a
// scheduled task proceeds: these are commands someone ran on purpose,
// and refusing without a terminal would make them unusable unattended.
// `goop uninstall --all` is the deliberate exception -- it refuses
// outright without a terminal, because "remove everything" triggered by
// accident has no undo.
//
// Reading from stdin fails or returns nothing on EOF, which is the
// answer to a question nobody heard: do not proceed.
func confirm(question string) bool {
	if !ui.IsTerminal(os.Stdin) {
		return true
	}
	fmt.Printf("\n%s [Y/n] ", question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Println()
		return false
	}
	return isYes(line)
}

// confirmDestructive is confirm for something with no undo: the default
// is no, and it has to be typed.
func confirmDestructive(question string) bool {
	if !ui.IsTerminal(os.Stdin) {
		return true
	}
	fmt.Printf("\n%s [y/N] ", question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Println()
		return false
	}
	switch normalize(line) {
	case "y", "yes", "o", "oui":
		return true
	default:
		return false
	}
}

func isYes(line string) bool {
	switch normalize(line) {
	case "", "y", "yes", "o", "oui":
		return true
	default:
		return false
	}
}

// cancelled prints the one line every abandoned command should end on,
// so "I said no" never looks like "something went wrong".
func cancelled() int {
	fmt.Println(ui.Dim("nothing was changed"))
	return 1
}

func normalize(line string) string {
	return strings.ToLower(strings.TrimSpace(line))
}

// confirmExtraInstalls asks only when an install would bring in
// something nobody named.
//
// Installing exactly what was asked for needs no question -- prompting
// on `goop install jq` would be friction for nothing. But a manifest can
// declare dependencies, and an archive format can pull in an extraction
// helper (7zip, innounp, dark), so one name can become four. That is
// worth seeing, and it is what apt does for the same reason.
//
// Resolution is a bucket read per spec, no downloads. If it fails --
// an unknown package, an unreachable bucket -- this says nothing and
// lets the install report the real error rather than a vague refusal.
func confirmExtraInstalls(specs []string) bool {
	asked := map[string]bool{}
	for _, s := range specs {
		asked[manifest.ParseSpec(s).Name] = true
	}

	var extra []installer.DependencyEntry
	seen := map[string]bool{}
	for _, s := range specs {
		deps, err := installer.ResolveDependencies(s)
		if err != nil {
			return true // let the install itself say what is wrong
		}
		for _, d := range deps {
			if asked[d.Name] || seen[d.Name] {
				continue
			}
			if installer.IsInstalled(d.Name) {
				continue
			}
			seen[d.Name] = true
			extra = append(extra, d)
		}
	}
	if len(extra) == 0 {
		return true
	}

	fmt.Printf("\n%s\n", ui.Bold(fmt.Sprintf("%d additional package(s) will be installed", len(extra))))
	rows := make([][]string, len(extra))
	for i, d := range extra {
		rows[i] = []string{d.Name, ui.Gray(d.Bucket)}
	}
	fmt.Print(ui.Table([]string{"PACKAGE", "BUCKET"}, rows))
	fmt.Println(ui.Dim("  required by what you asked for, or needed to unpack it"))
	return confirm("Continue?")
}
