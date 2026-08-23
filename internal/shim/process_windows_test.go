//go:build windows

package shim

import "testing"

func TestStripProgramName(t *testing.T) {
	tests := map[string]string{
		`git.exe status --short`:                `status --short`,
		`"C:\Program Files\git\git.exe" status`: `status`,
		`"C:\Program Files\git\git.exe"`:        ``,
		`git.exe`:                               ``,
		`git.exe  --two-spaces`:                 `--two-spaces`,
		`git.exe "arg with spaces" next`:        `"arg with spaces" next`,
		`"unterminated`:                         ``,
	}
	for in, want := range tests {
		if got := stripProgramName(in); got != want {
			t.Errorf("stripProgramName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuoteArgRoundTripsThroughStripProgramName(t *testing.T) {
	// A quoted program name followed by an argument containing embedded
	// quotes and backslashes must survive verbatim in the remainder, since
	// RawArgs forwards it byte-for-byte without re-parsing.
	arg := QuoteArg(`weird\"arg`)
	line := QuoteArg(`C:\shims\tool.exe`) + " " + arg
	if got := stripProgramName(line); got != arg {
		t.Errorf("stripProgramName(%q) = %q, want %q", line, got, arg)
	}
}
