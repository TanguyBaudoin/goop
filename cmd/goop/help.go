package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/ui"
)

// The help was one page of 137 lines covering 65 commands, each with up
// to four lines of rationale underneath. A terminal shows 30 to 50
// lines, so opening it scrolled the beginning off the screen and left
// you reading the middle of a reference manual -- which is why people
// said they could not understand it.
//
// Same words, three views. `goop help` is a single screen of the things
// people actually do. `goop help <command>` is that command with the
// rationale that used to clutter the front page. `goop help all` is the
// old page, unchanged to the byte, because it is what someone greps.
const (
	heSection  = iota // a group heading
	heCommand         // a command, with its one-line summary
	heContinue        // another line of detail under the command above
	heNote            // free-standing prose inside a section
	heBlank           // a blank line
)

type helpEntry struct {
	Kind  int
	Usage string
	Desc  string
}

// helpCollector gathers what buildHelp describes, so the same
// description can be rendered three ways instead of printed once.
type helpCollector struct{ entries []helpEntry }

func (c *helpCollector) section(title string) {
	c.entries = append(c.entries, helpEntry{Kind: heSection, Usage: title})
}

func (c *helpCollector) cmd(usage, desc string) {
	if usage == "" {
		c.entries = append(c.entries, helpEntry{Kind: heContinue, Desc: desc})
		return
	}
	c.entries = append(c.entries, helpEntry{Kind: heCommand, Usage: usage, Desc: desc})
}

func (c *helpCollector) note(text string) {
	c.entries = append(c.entries, helpEntry{Kind: heNote, Desc: text})
}

func (c *helpCollector) blank() {
	c.entries = append(c.entries, helpEntry{Kind: heBlank})
}

// renderAll reproduces the full reference page.
func renderAll(w io.Writer, entries []helpEntry) {
	for _, e := range entries {
		switch e.Kind {
		case heSection:
			fmt.Fprintln(w, ui.Bold(e.Usage))
		case heBlank:
			fmt.Fprintln(w)
		case heNote:
			fmt.Fprintf(w, "  %s\n", ui.Dim(e.Desc))
		case heContinue:
			fmt.Fprintf(w, "  %-46s %s\n", "", ui.Dim(e.Desc))
		case heCommand:
			switch {
			case e.Desc == "":
				fmt.Fprintf(w, "  %s\n", e.Usage)
			case len(e.Usage) >= 46:
				fmt.Fprintf(w, "  %s\n      %s\n", e.Usage, ui.Dim(e.Desc))
			default:
				fmt.Fprintf(w, "  %-46s %s\n", e.Usage, ui.Dim(e.Desc))
			}
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s 0 ok, 1 error, 2 usage, 3 deviation detected (`goop profile check`, `goop machine audit`)\n", ui.Bold("exit codes:"))
}

// renderCommand prints one command: its usage, its summary, and every
// line of detail under it. Matching is on the word after "goop", so
// `goop help profile` finds every `goop profile ...` subcommand and
// `goop help check` finds nothing -- which is correct, since that
// command is `goop profile check` now.
func renderCommand(w io.Writer, entries []helpEntry, name string) bool {
	prefix := "goop " + name
	found := false
	for i, e := range entries {
		if e.Kind != heCommand || !matchesCommand(e.Usage, prefix) {
			continue
		}
		if found {
			fmt.Fprintln(w)
		}
		found = true
		fmt.Fprintf(w, "  %s\n", ui.Bold(e.Usage))
		if e.Desc != "" {
			fmt.Fprintf(w, "      %s\n", e.Desc)
		}
		for _, cont := range entries[i+1:] {
			if cont.Kind != heContinue {
				break
			}
			fmt.Fprintf(w, "      %s\n", ui.Dim(cont.Desc))
		}
	}
	return found
}

// matchesCommand reports whether a usage line is for the named command.
// "goop install" must not match "goop installfoo", so the next character
// has to be a space or the end of the string.
func matchesCommand(usage, prefix string) bool {
	if !strings.HasPrefix(usage, prefix) {
		return false
	}
	rest := usage[len(prefix):]
	return rest == "" || strings.HasPrefix(rest, " ")
}

// printUsage is the one-screen view: what to type, not why.
//
// Everything here is a command somebody reaches for in the first week.
// The rest is a `goop help` away rather than in front of it, which is
// the whole point -- a reference you cannot scroll to the top of is not
// a reference.
func printUsage() {
	w := os.Stderr
	fmt.Fprintf(w, "%s  a package manager for Windows, reading Scoop's manifests\n\n", ui.Bold("goop"))

	group := func(title string, rows [][2]string) {
		fmt.Fprintln(w, ui.Bold(title))
		for _, r := range rows {
			fmt.Fprintf(w, "  %-34s %s\n", r[0], ui.Dim(r[1]))
		}
		fmt.Fprintln(w)
	}

	group("packages", [][2]string{
		{"goop install <name>...", "install, with its dependencies"},
		{"goop uninstall <name>...", "remove, keeping persisted data"},
		{"goop update [name]...", "no name updates every package"},
		{"goop reinstall <name>...", "lay the same version down again"},
		{"goop list", "what is installed"},
		{"goop info <name>", "where it came from, and when"},
		{"goop search <query>", "find a package across your buckets"},
	})
	group("where packages come from", [][2]string{
		{"goop bucket add <name> <url>", "add a catalogue of manifests"},
		{"goop bucket apps [name]", "everything a bucket offers"},
		{"goop bucket list", "the buckets you have, in search order"},
	})
	group("what a project needs", [][2]string{
		{"goop profile check <file>", "does this machine match the file"},
		{"goop profile sync <file>", "make it match"},
		{"goop profile export --out <file>", "write the file from this machine"},
	})
	group("this whole machine", [][2]string{
		{"goop machine export", "capture buckets and every package"},
		{"goop machine restore <file>", "replay that capture elsewhere"},
	})
	group("goop itself", [][2]string{
		{"goop self-update", "replace goop with the current release"},
		{"goop version", "the build you are running"},
	})

	fmt.Fprintf(w, "%s\n", ui.Dim("goop help <command>   what it does, and why it does it that way"))
	fmt.Fprintf(w, "%s\n", ui.Dim("goop help all         every command, grouped (long)"))
}

// printHelp dispatches `goop help [topic]`.
func printHelp(args []string) int {
	entries := buildHelp()
	if len(args) == 0 {
		printUsage()
		return 0
	}
	topic := args[0]
	if topic == "all" || topic == "--all" {
		renderAll(os.Stderr, entries)
		return 0
	}
	if renderCommand(os.Stderr, entries, topic) {
		return 0
	}
	ui.Fail("no command %q", topic)
	// The commands that moved are exactly the ones somebody looks up
	// after typing the old name, so pointing at the new one is the whole
	// job here. `goop help check` is the common case: that command is
	// `goop profile check` now.
	if suggestions := commandsContaining(entries, topic); len(suggestions) > 0 {
		fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("did you mean:"))
		for _, s := range suggestions {
			fmt.Fprintf(os.Stderr, "    %s\n", s)
		}
		return 2
	}
	fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("`goop help` lists the common ones, `goop help all` lists them all"))
	return 2
}

// commandsContaining finds commands whose name contains the word that
// was asked for -- so a subcommand looked up by its last word, which is
// what the old flat names were, leads to where it lives now.
func commandsContaining(entries []helpEntry, word string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Kind != heCommand || !strings.HasPrefix(e.Usage, "goop ") {
			continue
		}
		// The command itself, without its arguments or flags:
		// "goop profile export --out <file> ..." is "goop profile export".
		var words []string
		for _, w := range strings.Fields(e.Usage) {
			if strings.HasPrefix(w, "-") || strings.ContainsAny(w, "<[|") {
				break
			}
			words = append(words, w)
		}
		if len(words) < 2 {
			continue
		}
		name := strings.Join(words, " ")
		for _, part := range words[1:] {
			if part != word || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}
