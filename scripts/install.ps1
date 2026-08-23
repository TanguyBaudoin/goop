#Requires -Version 5.1
<#
.SYNOPSIS
    Bootstraps goop: builds it from source, lays out its directory
    structure, and puts it on PATH -- the equivalent of what Scoop's own
    `irm get.scoop.sh | iex` does, adapted to goop's reality (a single Go
    binary, not published anywhere yet, so this builds from a local
    clone rather than fetching a hosted release).

.PARAMETER GoopDir
    Where goop lives (apps/buckets/shims/cache/goop.exe itself). Same
    precedence goop's own `paths.Root()` uses: defaults to
    $env:GOOP_HOME if set, else "<home>\goop". Passing this only affects
    this install -- it does not itself set GOOP_HOME or `goop config
    set-root`, so a later `goop` invocation must agree (this script
    persists GOOP_HOME for you when GoopDir differs from the plain
    default, so they always do).

.PARAMETER NoBucket
    Skip adding the main bucket after install.

.PARAMETER RunAsAdmin
    goop never installs system-wide (NR-01) and its own directories are
    fully user-owned, so there's no reason to run this elevated -- same
    reasoning as Scoop's own installer, which refuses an elevated
    session unless this is passed explicitly.

.PARAMETER DryRun
    Build and lay out directories for real, but only print what would be
    persisted to GOOP_HOME/PATH instead of actually changing them --
    lets you preview the install (e.g. against a scratch -GoopDir)
    without touching your real environment.
#>
[CmdletBinding()]
param(
    [string]$GoopDir = $(if ($env:GOOP_HOME) { $env:GOOP_HOME } else { Join-Path $HOME 'goop' }),
    [switch]$NoBucket,
    [switch]$RunAsAdmin,
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'

function Write-Step($msg) { Write-Host $msg -ForegroundColor Cyan }
function Write-Ok($msg) { Write-Host $msg -ForegroundColor Green }

$isElevated = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if ($isElevated -and -not $RunAsAdmin) {
    throw "Running as administrator. goop installs per-user only (NR-01) -- re-run from a normal, non-elevated PowerShell, or pass -RunAsAdmin if you really mean it."
}

$repoRoot = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path (Join-Path $repoRoot 'cmd\goop\main.go'))) {
    throw "This script expects to run from inside a goop source checkout (found no cmd\goop\main.go above $PSScriptRoot). goop isn't published as a release yet, so there's nothing else to fetch -- clone the repo and run scripts\install.ps1 from there."
}

if (-not (Get-Command go -CommandType Application -ErrorAction SilentlyContinue)) {
    throw "Go toolchain not found on PATH; required to build goop from source (https://go.dev/dl/)."
}

Write-Step "Building goop from $repoRoot ..."
# Delegated to build.ps1 rather than repeating `go build` here: that
# script owns the shim-then-goop ordering and the -ldflags version
# stamp, and a second copy of it would silently drift -- an install done
# this way would report a different `goop version` than one built the
# normal way.
& (Join-Path $PSScriptRoot 'build.ps1')
if ($LASTEXITCODE -ne 0) { throw "build failed" }
Write-Ok "built $repoRoot\build\goop.exe"

Write-Step "Setting up $GoopDir ..."
$binDir = Join-Path $GoopDir 'bin'
$shimsDir = Join-Path $GoopDir 'shims'
foreach ($d in @($GoopDir, $binDir, $shimsDir, (Join-Path $GoopDir 'apps'), (Join-Path $GoopDir 'buckets'), (Join-Path $GoopDir 'cache'))) {
    New-Item -ItemType Directory -Force -Path $d | Out-Null
}

# goop.exe itself lives in bin\, deliberately separate from shims\ (which
# holds *managed app* shims goop creates/removes on install/uninstall) --
# keeping the manager's own binary out of that directory means nothing
# in goop's own shim-reconciliation logic can ever touch it.
Copy-Item -Path (Join-Path $repoRoot 'build\goop.exe') -Destination (Join-Path $binDir 'goop.exe') -Force
Write-Ok "installed $binDir\goop.exe"

# Persist GOOP_HOME only if it needs to differ from what paths.Root()
# would already resolve to unprompted (the plain "<home>\goop" default),
# so a vanilla install doesn't clutter the user's environment with a
# redundant variable.
$defaultDir = Join-Path $HOME 'goop'
if ($GoopDir -ne $defaultDir -and $env:GOOP_HOME -ne $GoopDir) {
    if ($DryRun) {
        Write-Ok "DRYRUN: would persist GOOP_HOME=$GoopDir"
    } else {
        [Environment]::SetEnvironmentVariable('GOOP_HOME', $GoopDir, 'User')
        Write-Ok "persisted GOOP_HOME=$GoopDir"
    }
    $env:GOOP_HOME = $GoopDir
}

Write-Step "Adding to PATH ..."
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$pathEntries = @($userPath -split ';' | Where-Object { $_ })
$toAdd = @($binDir, $shimsDir) | Where-Object { $pathEntries -notcontains $_ }
if ($toAdd) {
    if ($DryRun) {
        Write-Ok "DRYRUN: would persist Path += $($toAdd -join ', ')"
    } else {
        $newPath = (@($toAdd) + $pathEntries) -join ';'
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        foreach ($p in $toAdd) { Write-Ok "added $p to PATH" }
    }
    foreach ($p in $toAdd) { $env:Path = "$p;$env:Path" }
} else {
    Write-Ok "PATH already up to date"
}

if (-not $NoBucket) {
    $bucketsFile = Join-Path $GoopDir 'buckets.json'
    $hasMain = (Test-Path $bucketsFile) -and ((Get-Content $bucketsFile -Raw) -match '"main"')
    if (-not $hasMain) {
        Write-Step "Adding main bucket ..."
        & (Join-Path $binDir 'goop.exe') bucket add main https://github.com/ScoopInstaller/Main
    }
}

Write-Host ""
Write-Ok "goop is installed."
Write-Host "Open a new terminal (so the updated PATH takes effect), then run 'goop help' to get started."
