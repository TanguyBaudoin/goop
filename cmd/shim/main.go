// Command shim is the goop shim executable (J0). One compiled copy of
// this binary is hard-linked under every exposed command name; each link
// reads a sidecar ".shim" file next to itself to find its real target and
// execs it, propagating argv, stdio, exit code, and Ctrl-C faithfully.
package main

import (
	"fmt"
	"os"

	"github.com/TanguyBaudoin/goop/internal/shim"
)

func main() {
	os.Exit(run())
}

func run() int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "shim: cannot resolve own path:", err)
		return 1
	}

	sidecarPath := shim.SidecarPath(self)
	sc, err := shim.LoadSidecar(sidecarPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shim: cannot load", sidecarPath+":", err)
		return 1
	}

	kind, err := shim.Classify(sc.Path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shim: cannot launch", sc.Path+":", err)
		return 1
	}

	plan, err := shim.BuildPlan(kind, sc.Path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shim: cannot launch", sc.Path+":", err)
		return 1
	}

	cmdLine := plan.Prefix
	if sc.Args != "" {
		cmdLine += " " + sc.Args
	}
	if raw := shim.RawArgs(); raw != "" {
		cmdLine += " " + raw
	}

	exitCode, err := shim.Run(plan.Program, cmdLine)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shim:", err)
		return 1
	}
	return exitCode
}
