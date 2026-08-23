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
    New-Item -ItemType Directory -Force -Path internal\shimbin | Out-Null
    go build -o internal\shimbin\shim.exe .\cmd\shim

    # goop's own version. Commit and build date are not stamped here:
    # the Go toolchain embeds them in build info automatically, so
    # `go install` produces the same detail without this script.
    if (-not $Version) {
        $Version = "0.1.0-dev"
    }

    New-Item -ItemType Directory -Force -Path build | Out-Null
    go build -ldflags "-X main.version=$Version" -o build\goop.exe .\cmd\goop

    Write-Output "built build\goop.exe ($Version)"
} finally {
    Pop-Location
}
