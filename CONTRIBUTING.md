# Contributing

## Platform

goop is Windows-only by design: it creates NTFS hardlinks, reads the
Windows registry, and runs manifest hooks through `powershell.exe`. The
PowerShell target is **Windows PowerShell 5.1**, not just PowerShell 7 —
that is what ships with Windows and what most users will actually run
manifests under. Two 5.1 quirks have already caused real bugs here:

- `Set-Content -Encoding utf8` writes a **BOM**. Anything Go reads back
  must either be written BOM-less (`New-Object System.Text.UTF8Encoding
  $false`) or strip `﻿` on read.
- Redirected stdout encoding is controlled by `[Console]::OutputEncoding`,
  not `$OutputEncoding`.

## Build

```powershell
.\scripts\build.ps1
```

`go build ./...` **on its own fails on a fresh clone**, with
`pattern shim.exe: no matching files found`. That is expected, not a
broken checkout: `cmd/goop` embeds the compiled shim via
`internal/shimbin/shim.exe`, which is a build artifact and therefore
gitignored. `scripts/build.ps1` builds `cmd/shim` into place first, then
`cmd/goop`. `go generate ./...` produces the same file if you prefer.

CI runs `scripts/build.ps1` before anything else for this reason.

## Test

```powershell
go test ./...
```

To also decode the real upstream manifest corpus (a few thousand files,
where parser regressions actually show up), point `GOOP_MAIN_BUCKET` at
a checkout of [ScoopInstaller/Main](https://github.com/ScoopInstaller/Main):

```powershell
$env:GOOP_MAIN_BUCKET = "$env:USERPROFILE\goop\buckets\main\bucket"
go test ./internal/manifest/ -run Corpus -v
```

The test skips itself when that variable is unset, so it never fails on
a machine without the checkout.

`go test -race` needs a C compiler (`CGO_ENABLED=1`).

### Isolating tests from your real machine

**Setting `GOOP_HOME` is not enough.** `paths.StartMenu()` deliberately
ignores it, because Scoop itself always writes shortcuts to the real
Start Menu — that fidelity is correct behaviour, not a bug. The
consequence is that an install test run carelessly will create *and
delete* shortcuts in your actual Start Menu. This has already destroyed
real user shortcuts during development.

Any test that installs anything must isolate `$env:APPDATA` as well:

```powershell
$env:GOOP_HOME = "$env:TEMP\goop-test"
$env:APPDATA   = "$env:TEMP\goop-test-appdata"
```

Clean both up afterwards.

## Verifying changes

goop's contract is behavioural compatibility with Scoop, so the standard
of proof is real data, not just a green build:

- compare against the actual Scoop source (`lib/*.ps1`, `libexec/*.ps1`)
  when changing anything that mirrors a Scoop behaviour, and say which
  file you checked;
- exercise changes against real manifests and real installs, not only
  fixtures. Most bugs found in this codebase — staging-path leaks into
  shim sidecars, case-sensitivity in `extract_dir`, BOMs in log files —
  compiled cleanly and passed unit tests.

If a change intentionally diverges from Scoop, document *why* in a
comment at the divergence.

## Changelog and versioning

User-visible changes go in [CHANGELOG.md](CHANGELOG.md) under
`Unreleased`, in the same change you make them — reconstructing them at
release time from git history loses the *why*, which is the part users
need. Purely internal work (refactors, test additions) does not need an
entry.

goop's own version lives in `cmd/goop/version.go` and is stamped at link
time:

```powershell
.\scripts\build.ps1 -Version 0.1.0
```

An unstamped build reports `0.1.0-dev`, and `go install` builds report
`dev`. Commit and build date are not stamped by hand — the Go toolchain
embeds them in build info, so `goop version` reports them for any build
made from a git working tree.

## Code style

Match the surrounding code. Comments explain *why* a thing is done, and
in particular record the real-world failure a piece of defensive code
exists to prevent — that context is the most valuable thing in this
codebase and the easiest to lose.

## The install harness

`TestInstallHarness` installs real packages, checks what landed, removes
them and checks for residue. It is the only test that exercises the
install pipeline end to end, and it is opt-in because it downloads from
real upstream URLs:

```powershell
$env:GOOP_HARNESS = '1'
go test ./internal/installer/ -run Harness -v -timeout 30m
```

The default set is chosen for *shape* coverage — one package per
extraction or hook mechanism — rather than popularity. Adding a package
whose shape nothing covers yet is the most useful contribution to it;
`$env:GOOP_HARNESS_APPS = 'name1,name2'` tries a set without editing
code.

It runs weekly in CI. A failure there may mean an upstream release was
retired rather than that goop broke — read the log before assuming a
regression.
