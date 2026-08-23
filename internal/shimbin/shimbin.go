// Package shimbin embeds the compiled shim binary (cmd/shim) so cmd/goop
// can hard-link fresh shims at install time without needing a Go
// toolchain present on the target machine (TR-01).
//
// shim.exe here is a build artifact, not source: run scripts/build.ps1,
// which builds cmd/shim into this directory before building cmd/goop.
package shimbin

import _ "embed"

// The generate directive below is the machine-readable form of that
// bootstrap: `go generate ./...` produces shim.exe so the embed below
// resolves. It exists because shim.exe is a build artifact and is
// therefore gitignored -- a fresh clone has no such file, and the embed
// fails loudly at compile time rather than shipping a broken shim.
//go:generate go build -o shim.exe ../../cmd/shim

//go:embed shim.exe
var Bytes []byte
