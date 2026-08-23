package shim

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Kind identifies how a resolved target must be launched. TR-26 requires
// .exe, .bat, .cmd, .ps1, and .jar targets.
type Kind int

const (
	KindExe Kind = iota
	KindBatch
	KindPowerShell
	KindJar
)

// ErrUnsupportedTarget is returned for target extensions outside TR-26's list.
var ErrUnsupportedTarget = errors.New("unsupported shim target extension")

// Classify determines the launch strategy from a target's file extension.
func Classify(targetPath string) (Kind, error) {
	switch strings.ToLower(extOf(targetPath)) {
	case ".exe":
		return KindExe, nil
	case ".bat", ".cmd":
		return KindBatch, nil
	case ".ps1":
		return KindPowerShell, nil
	case ".jar":
		return KindJar, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnsupportedTarget, extOf(targetPath))
	}
}

func extOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		c := p[i]
		if c == '\\' || c == '/' {
			return ""
		}
		if c == '.' {
			return p[i:]
		}
	}
	return ""
}

// lookPath is overridden in tests so Classify/Plan-adjacent lookups don't
// depend on the host machine's installed toolchains.
var lookPath = exec.LookPath

// Plan describes the concrete process to launch for a target: the resolved
// program to run, and the command-line prefix (program + any interpreter
// flags) that goes before the sidecar's default args and the caller's own
// passthrough arguments.
type Plan struct {
	Program string
	Prefix  string // already quoted, ready to have " <args>" appended
}

// BuildPlan resolves how to invoke target, given its Kind.
func BuildPlan(kind Kind, target string) (Plan, error) {
	q := QuoteArg(target)
	switch kind {
	case KindExe:
		return Plan{Program: target, Prefix: q}, nil

	case KindBatch:
		comspec, err := lookPath("cmd.exe")
		if err != nil {
			comspec = "cmd.exe"
		}
		// /d skips AutoRun registry scripts; /c runs then exits.
		return Plan{Program: comspec, Prefix: QuoteArg(comspec) + " /d /c " + q}, nil

	case KindPowerShell:
		pwsh, err := resolvePowerShell()
		if err != nil {
			return Plan{}, err
		}
		prefix := QuoteArg(pwsh) + " -NoLogo -NoProfile -ExecutionPolicy Bypass -File " + q
		return Plan{Program: pwsh, Prefix: prefix}, nil

	case KindJar:
		java, err := lookPath("java.exe")
		if err != nil {
			return Plan{}, fmt.Errorf("locate java.exe for .jar target: %w", err)
		}
		return Plan{Program: java, Prefix: QuoteArg(java) + " -jar " + q}, nil

	default:
		return Plan{}, fmt.Errorf("unhandled shim kind %v", kind)
	}
}

func resolvePowerShell() (string, error) {
	if p, err := lookPath("pwsh.exe"); err == nil {
		return p, nil
	}
	if p, err := lookPath("powershell.exe"); err == nil {
		return p, nil
	}
	return "", errors.New("neither pwsh.exe nor powershell.exe found on PATH")
}

// QuoteArg quotes s as a single Windows command-line argument, following
// the escaping rules CommandLineToArgvW uses to parse it back losslessly
// (backslashes only escape when they immediately precede a quote).
func QuoteArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\n\v\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range s {
		switch r {
		case '\\':
			slashes++
			b.WriteRune(r)
		case '"':
			for ; slashes > 0; slashes-- {
				b.WriteByte('\\')
			}
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			slashes = 0
			b.WriteRune(r)
		}
	}
	for ; slashes > 0; slashes-- {
		b.WriteByte('\\')
	}
	b.WriteByte('"')
	return b.String()
}
