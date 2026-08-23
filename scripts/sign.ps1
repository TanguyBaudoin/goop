# Signs release artifacts with minisign (A5 provenance). Requires
# minisign.exe on PATH (e.g. `goop install minisign` or `scoop install
# minisign`) and an existing keypair (`minisign -G` to create one --
# keep the .key file secret, publish the .pub key's contents in the
# release notes / a well-known URL so users can verify with
# `goop verify`).
#
# Usage:
#   .\scripts\sign.ps1 -SecretKey C:\path\to\release.key
#
# This does NOT create or manage the keypair itself -- that's a
# one-time, deliberately manual step (`minisign -G`), not something a
# build script should do unattended.
param(
    [Parameter(Mandatory = $true)]
    [string]$SecretKey
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

$minisign = Get-Command minisign.exe -ErrorAction SilentlyContinue
if (-not $minisign) {
    Write-Error "minisign.exe not found on PATH. Install it first (e.g. 'goop install minisign' or 'scoop install minisign')."
    exit 1
}
if (-not (Test-Path $SecretKey)) {
    Write-Error "Secret key not found: $SecretKey"
    exit 1
}

$targets = @(
    (Join-Path $root "build\goop.exe"),
    (Join-Path $root "build\shim.exe")
) | Where-Object { Test-Path $_ }

if ($targets.Count -eq 0) {
    Write-Error "No build artifacts found under build\. Run scripts\build.ps1 first."
    exit 1
}

foreach ($target in $targets) {
    Write-Output "Signing $target ..."
    & $minisign.Source -S -s $SecretKey -m $target
    if ($LASTEXITCODE -ne 0) {
        Write-Error "Signing failed for $target"
        exit 1
    }
    Write-Output "  -> $target.minisig"
}

Write-Output ""
Write-Output "Done. Publish the .minisig file(s) alongside the release, and make sure"
Write-Output "the public key is available somewhere users trust (release notes, README)."
Write-Output "Users verify with:"
Write-Output "  goop verify build\goop.exe build\goop.exe.minisig <published-public-key>"
