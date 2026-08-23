// Package ui provides goop's terminal styling: colors, symbols, and
// aligned tables. No external dependency -- ANSI escape codes are
// simple enough to emit directly, and the one genuinely Windows-specific
// piece (a console won't interpret them without opting in) is a small,
// well-defined syscall.
package ui

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"golang.org/x/sys/windows"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
	ansiGray   = "\x1b[90m"
)

// Symbols used consistently across every command's output.
const (
	CheckMark = "✓"
	CrossMark = "✗"
	Arrow     = "→"
	Bang      = "!"
)

// Enabled reports whether color output is active. False when NO_COLOR
// is set, when GOOP_NO_COLOR is set, or when either stdout or stderr
// isn't a real console (redirected to a file or pipe) -- goop writes
// interleaved output to both, so both need to be real consoles for
// color to make sense.
var Enabled = detectColor()

func detectColor() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("GOOP_NO_COLOR") != "" {
		return false
	}
	return enableVirtualTerminal(os.Stdout) && enableVirtualTerminal(os.Stderr)
}

// enableVirtualTerminal opts a Windows console into interpreting ANSI
// escape codes (off by default even on modern Windows Terminal-backed
// consoles unless a process asks for it). Returns false if f isn't a
// real console at all (a redirected file or pipe), which doubles as
// goop's terminal-detection check.
func enableVirtualTerminal(f *os.File) bool {
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	return true
}

func paint(code, s string) string {
	if !Enabled || s == "" {
		return s
	}
	return code + s + ansiReset
}

func Bold(s string) string   { return paint(ansiBold, s) }
func Dim(s string) string    { return paint(ansiDim, s) }
func Red(s string) string    { return paint(ansiRed, s) }
func Green(s string) string  { return paint(ansiGreen, s) }
func Yellow(s string) string { return paint(ansiYellow, s) }
func Cyan(s string) string   { return paint(ansiCyan, s) }
func Gray(s string) string   { return paint(ansiGray, s) }

// Ok prints a green checkmark line to stdout, for a command's own
// successful, one-shot outcome (not per-app install progress, which
// goes through the Logf-driven line styling instead).
func Ok(format string, args ...any) {
	fmt.Printf("%s %s\n", Green(CheckMark), fmt.Sprintf(format, args...))
}

// Fail prints a red cross-mark line to stderr. Every command's error
// path goes through this, so failures look consistent regardless of
// which command produced them.
func Fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", Red(CrossMark), fmt.Sprintf(format, args...))
}

// ansiPattern matches the escape sequences Bold/Dim/Red/... emit, so
// table column widths can be computed from visible characters only --
// without this, a colored cell would be measured as wider than it
// displays (the escape bytes count toward len() but draw nothing) and
// throw off alignment against plain cells in the same column.
var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func visualLen(s string) int {
	if !strings.Contains(s, "\x1b") {
		return len(s)
	}
	return len(ansiPattern.ReplaceAllString(s, ""))
}

// Table renders header + rows as aligned columns with a bold header and
// a dim separator rule. Cells may already contain ANSI color codes;
// width is computed from visible characters regardless.
func Table(header []string, rows [][]string) string {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = visualLen(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				if l := visualLen(cell); l > widths[i] {
					widths[i] = l
				}
			}
		}
	}

	var b strings.Builder
	writeRow := func(cells []string, style func(string) string) {
		for i, cell := range cells {
			if i < len(widths) {
				cell = padRight(cell, widths[i])
			}
			if style != nil {
				cell = style(cell)
			}
			b.WriteString(cell)
			if i < len(cells)-1 {
				b.WriteString("  ")
			}
		}
		b.WriteString("\n")
	}

	writeRow(header, Bold)
	rule := make([]string, len(header))
	for i, w := range widths {
		rule[i] = strings.Repeat("-", w)
	}
	writeRow(rule, Dim)
	for _, row := range rows {
		writeRow(row, nil)
	}
	return b.String()
}

func padRight(s string, w int) string {
	n := visualLen(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}
