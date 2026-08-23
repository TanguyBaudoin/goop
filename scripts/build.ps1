# Builds goop: cmd/shim first (embedded into cmd/goop via
# internal/shimbin), then cmd/goop itself.
#
# Pass -Version to stamp a release build, e.g.
#   .\scripts\build.ps1 -Version 0.1.0
param(
    [string]$Version
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

Push-Location $root
try {
    # $ErrorActionPreference does not apply to native executables in
    # Windows PowerShell 5.1, so a failing `go build` has to be caught
    # by its exit code explicitly or the script would carry on and
    # report success over a stale binary.
    New-Item -ItemType Directory -Force -Path internal\shimbin | Out-Null
    go build -o internal\shimbin\shim.exe .\cmd\shim
    if ($LASTEXITCODE -ne 0) { throw "build of cmd\shim failed" }

    # goop's own version. Commit and build date are not stamped here:
    # the Go toolchain embeds them in build info automatically, so
    # `go install` produces the same detail without this script.
    if (-not $Version) {
        $Version = "0.1.0-dev"
    }

    New-Item -ItemType Directory -Force -Path build | Out-Null
    go build -ldflags "-X main.version=$Version" -o build\goop.exe .\cmd\goop
    if ($LASTEXITCODE -ne 0) { throw "build of cmd\goop failed" }

    Write-Output "built build\goop.exe ($Version)"
} finally {
    Pop-Location
}
