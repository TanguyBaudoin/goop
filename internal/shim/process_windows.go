//go:build windows

package shim

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procSetConsoleCtrlHandler = modkernel32.NewProc("SetConsoleCtrlHandler")
)

// RawArgs returns the raw, unmodified argument portion of the current
// process's command line -- everything after the program-name token --
// exactly as Windows delivered it, with no re-parsing or re-quoting.
// Reconstructing this from os.Args would normalize away escaping edge
// cases the original caller used; TR-21 requires byte-for-byte fidelity.
func RawArgs() string {
	cmdLine := windows.UTF16PtrToString(windows.GetCommandLine())
	return stripProgramName(cmdLine)
}

// stripProgramName removes the leading program-name token from a raw
// Windows command line, following the same (unescaped) quoting rule
// CommandLineToArgvW applies specifically to argv[0].
func stripProgramName(cmdLine string) string {
	i, n := 0, len(cmdLine)
	for i < n && (cmdLine[i] == ' ' || cmdLine[i] == '\t') {
		i++
	}
	if i < n && cmdLine[i] == '"' {
		i++
		for i < n && cmdLine[i] != '"' {
			i++
		}
		if i < n {
			i++ // skip closing quote
		}
	} else {
		for i < n && cmdLine[i] != ' ' && cmdLine[i] != '\t' {
			i++
		}
	}
	for i < n && (cmdLine[i] == ' ' || cmdLine[i] == '\t') {
		i++
	}
	return cmdLine[i:]
}

// suppressCtrlC installs a console control handler that swallows Ctrl-C
// (and friends) in the shim process itself, so it survives to relay the
// child's exit code (TR-20). The target process receives the signal
// independently: it shares our console and we never pass
// CREATE_NEW_PROCESS_GROUP, so Windows broadcasts CTRL_C_EVENT to it too
// (TR-23).
func suppressCtrlC() error {
	handler := syscall.NewCallback(func(ctrlType uint32) uintptr { return 1 })
	r1, _, err := procSetConsoleCtrlHandler.Call(handler, 1)
	if r1 == 0 {
		return fmt.Errorf("SetConsoleCtrlHandler: %w", err)
	}
	return nil
}

// Run launches program with the given full command line, sharing this
// process's console and standard handles untouched (TR-22: no added
// buffering), waits for it to exit, and returns its exit code (TR-20).
func Run(program, commandLine string) (int, error) {
	if err := suppressCtrlC(); err != nil {
		return 0, err
	}

	programPtr, err := windows.UTF16PtrFromString(program)
	if err != nil {
		return 0, fmt.Errorf("encode program path: %w", err)
	}
	cmdLinePtr, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return 0, fmt.Errorf("encode command line: %w", err)
	}

	si := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	var pi windows.ProcessInformation

	if err := windows.CreateProcess(
		programPtr,
		cmdLinePtr,
		nil,
		nil,
		true, // inherit handles: stdin/stdout/stderr pass straight through
		0,    // no CREATE_NEW_PROCESS_GROUP/CREATE_NEW_CONSOLE: shares our console
		nil,
		nil,
		&si,
		&pi,
	); err != nil {
		return 0, fmt.Errorf("launch %s: %w", program, err)
	}
	defer windows.CloseHandle(pi.Thread)
	defer windows.CloseHandle(pi.Process)

	if _, err := windows.WaitForSingleObject(pi.Process, windows.INFINITE); err != nil {
		return 0, fmt.Errorf("wait for %s: %w", program, err)
	}

	var exitCode uint32
	if err := windows.GetExitCodeProcess(pi.Process, &exitCode); err != nil {
		return 0, fmt.Errorf("get exit code for %s: %w", program, err)
	}
	return int(exitCode), nil
}
