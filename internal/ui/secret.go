package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// ReadSecret asks for a secret without echoing it, the way sudo does.
//
// A secret passed as a command-line argument leaks twice over: into the
// shell's history file, and into the process list where any other
// process on the machine can read it while the command runs. Neither is
// recoverable after the fact, which is why goop asks instead.
//
// When stdin is not a console -- a pipe, a here-string, CI -- the line is
// read plainly, so `echo $TOKEN | goop auth add ...` still works. There
// is nothing to hide from in that case: the value never reaches a
// terminal, and the caller has already decided how to protect it.
func ReadSecret(prompt string) (string, error) {
	in := os.Stdin
	handle := windows.Handle(in.Fd())

	var mode uint32
	interactive := windows.GetConsoleMode(handle, &mode) == nil

	if interactive {
		fmt.Fprint(os.Stderr, prompt)
		// Turning off ENABLE_ECHO_INPUT is what stops the console
		// painting the characters; the line is still read normally.
		if err := windows.SetConsoleMode(handle, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
			return "", fmt.Errorf("could not disable echo on the console: %w", err)
		}
		// Restore whatever the console had before, even on a read error,
		// so a failure here never leaves the user typing blind.
		defer func() {
			_ = windows.SetConsoleMode(handle, mode)
			fmt.Fprintln(os.Stderr)
		}()
	}

	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// Ask prints a prompt and reads one line, echoed normally. Returns "" on
// a bare Enter, which callers use to mean "take the default".
//
// Callers must check IsTerminal first and decide what to do without a
// console: reading from a pipe here would silently consume whatever the
// caller's stdin happened to hold.
func Ask(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
