// Package shimbin embeds the compiled shim binary (cmd/shim) so cmd/goop
// can hard-link fresh shims at install time without needing a Go
// toolchain present on the target machine (TR-01).
//
// shim.exe here is a build artifact, not source: run scripts/build.ps1,
// which builds cmd/shim into this directory before building cmd/goop.
package shimbin

import _ "embed"

//go:embed shim.exe
var Bytes []byte
