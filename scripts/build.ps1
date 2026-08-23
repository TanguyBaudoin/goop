# Builds goop: cmd/shim first (embedded into cmd/goop via
# internal/shimbin), then cmd/goop itself.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

Push-Location $root
try {
    New-Item -ItemType Directory -Force -Path internal\shimbin | Out-Null
    go build -o internal\shimbin\shim.exe .\cmd\shim

    New-Item -ItemType Directory -Force -Path build | Out-Null
    go build -o build\goop.exe .\cmd\goop

    Write-Output "built build\goop.exe"
} finally {
    Pop-Location
}
