package ui

import (
	"strings"
	"testing"
)

func TestVisualLen_IgnoresANSICodes(t *testing.T) {
	plain := "ripgrep"
	colored := ansiGreen + "ripgrep" + ansiReset
	if visualLen(colored) != visualLen(plain) {
		t.Errorf("visualLen(colored) = %d, visualLen(plain) = %d, want equal", visualLen(colored), visualLen(plain))
	}
	if visualLen(plain) != 7 {
		t.Errorf("visualLen(%q) = %d, want 7", plain, visualLen(plain))
	}
}

func TestPadRight_AccountsForColorCodes(t *testing.T) {
	colored := ansiGreen + "hi" + ansiReset // visually 2 chars
	got := padRight(colored, 5)
	if visualLen(got) != 5 {
		t.Errorf("padRight result has visual length %d, want 5", visualLen(got))
	}
}

func TestTable_AlignsColoredAndPlainCellsTogether(t *testing.T) {
	rows := [][]string{
		{"jq", Green("1.8.2"), "main"},
		{"ripgrep", "15.2.0", "main"},
	}
	out := Table([]string{"NAME", "VERSION", "BUCKET"}, rows)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 { // header + rule + 2 rows
		t.Fatalf("got %d lines, want 4:\n%s", len(lines), out)
	}
	// Every line's visible content up to (but not including) the final
	// column should line up: check the "BUCKET" column starts at the
	// same visual offset on every line despite one cell being colored.
	col := func(line string) int {
		// find visual offset of "main"/"BUCKET"/dashes by measuring
		// everything before the last field's first non-space rune
		// after stripping ANSI codes.
		stripped := ansiPattern.ReplaceAllString(line, "")
		fields := strings.Fields(stripped)
		idx := strings.LastIndex(stripped, fields[len(fields)-1])
		return idx
	}
	first := col(lines[2])
	for _, l := range lines[2:] {
		if col(l) != first {
			t.Errorf("column misaligned: %q (offset %d) vs first row offset %d", l, col(l), first)
		}
	}
}

func TestPaint_ProducesCorrectANSIBytes(t *testing.T) {
	old := Enabled
	Enabled = true
	defer func() { Enabled = old }()

	tests := []struct {
		fn   func(string) string
		want string
	}{
		{Green, "\x1b[32mok\x1b[0m"},
		{Red, "\x1b[31mok\x1b[0m"},
		{Yellow, "\x1b[33mok\x1b[0m"},
		{Cyan, "\x1b[36mok\x1b[0m"},
		{Gray, "\x1b[90mok\x1b[0m"},
		{Bold, "\x1b[1mok\x1b[0m"},
		{Dim, "\x1b[2mok\x1b[0m"},
	}
	for _, tt := range tests {
		if got := tt.fn("ok"); got != tt.want {
			t.Errorf("got %q (%v), want %q (%v)", got, []byte(got), tt.want, []byte(tt.want))
		}
	}
}

func TestPaint_EmptyStringStaysEmpty(t *testing.T) {
	old := Enabled
	Enabled = true
	defer func() { Enabled = old }()
	if got := Green(""); got != "" {
		t.Errorf("Green(\"\") = %q, want empty (no dangling escape codes around nothing)", got)
	}
}

func TestTable_NoColorIsPlainText(t *testing.T) {
	old := Enabled
	Enabled = false
	defer func() { Enabled = old }()

	out := Table([]string{"NAME"}, [][]string{{"jq"}})
	if strings.Contains(out, "\x1b") {
		t.Errorf("expected no ANSI codes with Enabled=false, got: %q", out)
	}
}
