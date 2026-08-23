package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/ui"
)

// version is goop's own release version, as opposed to the versions of
// the packages it manages. It is overridden at link time by
// scripts/build.ps1:
//
//	go build -ldflags "-X main.version=0.1.0" ./cmd/goop
//
// The "dev" default is deliberately not a plausible version number: a
// build that reports "dev" is one nobody stamped, and a bug report
// quoting it tells you exactly that.
var version = "dev"

// versionInfo renders the version block. Commit and build date are read
// from the embedded build info rather than stamped: `go install` fills
// the vcs.* keys in automatically, so a user who installed that way gets
// a precise commit without goop's build script having been involved at
// all. A plain `go build` from a working tree fills them in too; only
// builds from an unpacked source archive leave them empty, which is why
// each line is emitted only when it has a value.
func versionInfo() string {
	var b strings.Builder
	fmt.Fprintf(&b, "goop %s\n", version)

	if info, ok := debug.ReadBuildInfo(); ok {
		var revision, buildTime string
		dirty := false
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				revision = s.Value
			case "vcs.time":
				buildTime = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if revision != "" {
			// Short hash, but keep the dirty marker: a commit id alone
			// is misleading when the tree had uncommitted changes.
			short := revision
			if len(short) > 12 {
				short = short[:12]
			}
			if dirty {
				short += " (modified)"
			}
			fmt.Fprintf(&b, "commit %s\n", short)
		}
		if buildTime != "" {
			fmt.Fprintf(&b, "built  %s\n", buildTime)
		}
	}

	fmt.Fprintf(&b, "%s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return b.String()
}

// cmdVersion prints the version block. It takes no arguments; anything
// passed is a mistake worth naming rather than ignoring.
func cmdVersion(args []string) int {
	if len(args) > 0 {
		ui.Fail("version takes no arguments")
		return 2
	}
	fmt.Print(versionInfo())
	return 0
}
