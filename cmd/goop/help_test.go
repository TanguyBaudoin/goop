package main

import (
	"bytes"
	"strings"
	"testing"
)

// The reference page was 137 lines on a terminal that shows 30 to 50, so
// opening it scrolled its own beginning off the screen. The short page
// has to stay short or it is the same problem again.
func TestPrintUsage_FitsOneScreen(t *testing.T) {
	_, output := isolateCLI(t)
	printUsage()
	lines := strings.Split(strings.TrimRight(output(), "\n"), "\n")
	if len(lines) > 34 {
		t.Errorf("the short help is %d lines; a terminal shows 30-50, so this must stay under ~34", len(lines))
	}
	// And it has to point at the rest, or the detail is unreachable.
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"goop help <command>", "goop help all"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the short help must mention %q", want)
		}
	}
}

// Every command the short page offers must actually dispatch. A front
// page that suggests something `run()` does not handle is worse than one
// that suggests nothing.
func TestPrintUsage_OffersOnlyRealCommands(t *testing.T) {
	_, output := isolateCLI(t)
	printUsage()
	out := output()

	entries := buildHelp()
	for _, line := range strings.Split(out, "\n") {
		// Command rows are the indented ones. The title line and the
		// section headings also begin with "goop", which is why this
		// matches on the indent rather than on the word.
		line = stripANSI(line)
		if !strings.HasPrefix(line, "  goop ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[1]
		if !commandExists(entries, name) {
			t.Errorf("the short help offers %q, which the reference page does not document", name)
		}
	}
}

func commandExists(entries []helpEntry, name string) bool {
	for _, e := range entries {
		if e.Kind == heCommand && matchesCommand(e.Usage, "goop "+name) {
			return true
		}
	}
	return false
}

// `goop help <command>` has to find the command and show the detail that
// used to clutter the front page.
func TestPrintHelp_PerCommand(t *testing.T) {
	entries := buildHelp()
	cases := map[string][]string{
		"install":   {"goop install", "--profile"},
		"update":    {"goop update", "EVERY installed package"},
		"reinstall": {"goop reinstall", "profile membership"},
		// A command with subcommands lists all of them.
		"machine": {"goop machine export", "goop machine restore", "goop machine audit"},
	}
	for topic, wants := range cases {
		t.Run(topic, func(t *testing.T) {
			var buf bytes.Buffer
			if !renderCommand(&buf, entries, topic) {
				t.Fatalf("goop help %s found nothing", topic)
			}
			got := stripANSI(buf.String())
			for _, w := range wants {
				if !strings.Contains(got, w) {
					t.Errorf("goop help %s should mention %q, got:\n%s", topic, w, got)
				}
			}
		})
	}
}

// A prefix must not match a longer name: "goop install" is not
// "goop installfoo".
func TestMatchesCommand(t *testing.T) {
	cases := []struct {
		usage, prefix string
		want          bool
	}{
		{"goop install <spec>...", "goop install", true},
		{"goop install", "goop install", true},
		{"goop installfoo <x>", "goop install", false},
		{"goop profile check <file>", "goop profile", true},
		{"goop profile check <file>", "goop check", false},
	}
	for _, tc := range cases {
		if got := matchesCommand(tc.usage, tc.prefix); got != tc.want {
			t.Errorf("matchesCommand(%q, %q) = %v, want %v", tc.usage, tc.prefix, got, tc.want)
		}
	}
}

// The names that moved in 0.4.0 are exactly what someone looks up after
// typing the old one, so the lookup has to lead them to the new place.
func TestCommandsContaining_PointsAtRenamedCommands(t *testing.T) {
	entries := buildHelp()
	cases := map[string]string{
		"check":   "goop profile check",
		"sync":    "goop profile sync",
		"audit":   "goop machine audit",
		"restore": "goop machine restore",
	}
	for word, want := range cases {
		got := commandsContaining(entries, word)
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("looking up %q should suggest %q, got %v", word, want, got)
		}
	}
	// Suggestions are command names, not usage lines with flags in them.
	for _, s := range commandsContaining(entries, "export") {
		if strings.ContainsAny(s, "<[-") {
			t.Errorf("suggestion %q should be a bare command name", s)
		}
	}
	if got := commandsContaining(entries, "nonsense-xyz"); len(got) != 0 {
		t.Errorf("an unknown word suggests nothing, got %v", got)
	}
}

// stripANSI removes the colour escapes so assertions compare text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
