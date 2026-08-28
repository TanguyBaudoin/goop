#Requires -Version 5.1
<#
.SYNOPSIS
    Reverses install.ps1: removes goop from PATH, deletes its directory
    tree, and cleans up persistent configuration. Optionally runs `goop
    uninstall --all` first to cleanly remove every managed app.

.PARAMETER GoopDir
    Where goop lives -- must match the directory the original install
    used. Same resolution as install.ps1 / paths.Root(): defaults to
    $env:GOOP_HOME if set, else "<home>\goop".

.PARAMETER Force
    Skip the confirmation prompt and the safety check that refuses to
    remove a GoopDir with apps still installed (implies --force for
    `goop uninstall --all`).

.PARAMETER DryRun
    Print what would be done without changing anything.

.PARAMETER PreserveApps
    Keep the apps\ directory and everything in it (version directories,
    persisted data). Useful when you only want to remove goop itself
    while preserving installed programs -- though they will no longer
    have working shims or PATH entries.

.PARAMETER SkipSelfdestruct
    Do not run `goop uninstall --all`. Use when you have already removed
    the apps yourself, or when you want to delete the tree directly
    without cleanly uninstalling each app first.
#>
[CmdletBinding()]
param(
    [string]$GoopDir = $(if ($env:GOOP_HOME) { $env:GOOP_HOME } else { Join-Path $HOME 'goop' }),
    [switch]$Force,
    [switch]$DryRun,
    [switch]$PreserveApps,
    [switch]$SkipSelfdestruct
)

$ErrorActionPreference = 'Stop'

function Write-Step($msg) { Write-Host $msg -ForegroundColor Cyan }
function Write-Ok($msg) { Write-Host $msg -ForegroundColor Green }
function Write-Warn($msg) { Write-Host $msg -ForegroundColor Yellow }

$repoRoot = Split-Path -Parent $PSScriptRoot

# ---------------------------------------------------------------------------
# 1. Verify the target looks like a goop install
# ---------------------------------------------------------------------------
if (-not (Test-Path $GoopDir)) {
    throw "GoopDir not found: $GoopDir"
}

$hasGoopExe = Test-Path (Join-Path $GoopDir 'bin\goop.exe')
$hasApps    = Test-Path (Join-Path $GoopDir 'apps')
$hasBuckets = Test-Path (Join-Path $GoopDir 'buckets')

if (-not ($hasGoopExe -or $hasApps -or $hasBuckets)) {
    throw "$GoopDir does not look like a goop install (none of bin\goop.exe, apps\, or buckets\ found)."
}

# ---------------------------------------------------------------------------
# 2. Safety check: refuse if apps are installed (unless -Force or
#    -PreserveApps or -SkipSelfdestruct, which imply the user knows what
#    they're doing).
# ---------------------------------------------------------------------------
$appCount = 0
if ($hasApps) {
    $appCount = @(Get-ChildItem (Join-Path $GoopDir 'apps') -Directory -ErrorAction SilentlyContinue).Count
}
if ($appCount -gt 0 -and -not $Force -and -not $PreserveApps -and -not $SkipSelfdestruct) {
    throw "$GoopDir\apps has $appCount app(s) installed. Run 'goop uninstall --all --force' first, or pass -Force (implies --force for the self-destruct), -PreserveApps (keep apps\), or -SkipSelfdestruct (delete everything directly)."
}

# ---------------------------------------------------------------------------
# 3. List what will be removed
# ---------------------------------------------------------------------------
Write-Step "The following will be removed:"
Write-Host "  Install directory: $GoopDir"
if ($hasGoopExe) { Write-Host "    $GoopDir\bin\goop.exe" }
if ($hasBuckets) { Write-Host "    $GoopDir\buckets\ (bucket data)" }
if ($hasApps -and -not $PreserveApps) { Write-Host "    $GoopDir\apps\ ($appCount app(s))" }
if ($hasApps -and $PreserveApps) { Write-Host "    $GoopDir\apps\ (preserved)" }

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$binDir   = Join-Path $GoopDir 'bin'
$shimsDir = Join-Path $GoopDir 'shims'
$pathEntries = @($userPath -split ';' | Where-Object { $_ })
$onPath = @($binDir, $shimsDir) | Where-Object { $pathEntries -contains $_ }
if ($onPath) {
    Write-Host "  PATH entries: $($onPath -join ', ')"
}

$hasGoopHome = [Environment]::GetEnvironmentVariable('GOOP_HOME', 'User')
if ($hasGoopHome -and $hasGoopHome -eq $GoopDir) {
    Write-Host "  Environment variable: GOOP_HOME=$GoopDir"
}

$localAppDataDir = Join-Path $env:LOCALAPPDATA 'goop'
if (Test-Path $localAppDataDir) {
    Write-Host "  Config directory: $localAppDataDir"
}

if ($DryRun) {
    Write-Host ""
    Write-Ok "DRYRUN: nothing changed"
    return
}

# ---------------------------------------------------------------------------
# 4. Confirmation prompt
# ---------------------------------------------------------------------------
if (-not $Force) {
    Write-Host ""
    $reply = Read-Host "Uninstall goop from '$GoopDir'? This will remove all installed apps, buckets, and configuration. [y/N]"
    if ($reply -notmatch '^\s*[yY]') {
        Write-Host "Cancelled."
        return
    }
}

# ---------------------------------------------------------------------------
# 5. Run `goop uninstall --all --force` to cleanly remove every managed
#    app (unless told to skip it).
# ---------------------------------------------------------------------------
if (-not $SkipSelfdestruct -and $hasGoopExe) {
    $goopExe = Join-Path $GoopDir 'bin\goop.exe'
    if (Test-Path $goopExe) {
        # We need goop on PATH or accessible. It sits in bin\ under GoopDir,
        # so resolve it by absolute path. If the user has also installed it
        # elsewhere, the absolute path is the right one.
        Write-Step "Running 'goop uninstall --all --force' to cleanly remove managed apps ..."
        if ($appCount -gt 0) {
            Write-Host "Uninstalling $appCount app(s) (this may take a while) ..."
        }
        & $goopExe uninstall --all --force --yes
        if ($LASTEXITCODE -ne 0) {
            Write-Warn "goop uninstall --all exited with code $LASTEXITCODE; some apps may not have been cleanly removed. Continuing with directory cleanup."
        } else {
            Write-Ok "All managed apps uninstalled."
        }
    }
}

# ---------------------------------------------------------------------------
# 6. Remove PATH entries
# ---------------------------------------------------------------------------
$toRemove = @($binDir, $shimsDir) | Where-Object { $pathEntries -contains $_ }
if ($toRemove) {
    $newPath = (@($pathEntries | Where-Object { $_ -notin $toRemove })) -join ';'
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    foreach ($p in $toRemove) { Write-Ok "removed $p from PATH" }
    # Also remove from current session
    $env:Path = (@($env:Path -split ';' | Where-Object { $_ -and $_ -notin $toRemove })) -join ';'
} else {
    Write-Ok "PATH entries already clean"
}

# ---------------------------------------------------------------------------
# 7. Remove GOOP_HOME environment variable if it points at GoopDir
# ---------------------------------------------------------------------------
if ($hasGoopHome -and $hasGoopHome -eq $GoopDir) {
    [Environment]::SetEnvironmentVariable('GOOP_HOME', $null, 'User')
    Write-Ok "removed GOOP_HOME environment variable"
    if ($env:GOOP_HOME -eq $GoopDir) { Remove-Item Env:\GOOP_HOME -ErrorAction SilentlyContinue }
}

# ---------------------------------------------------------------------------
# 8. Ask about %LOCALAPPDATA%\goop\ config directory
# ---------------------------------------------------------------------------
if (Test-Path $localAppDataDir) {
    if (-not $Force) {
        $reply = Read-Host "Remove config directory '$localAppDataDir' (holds set-root, proxy settings)? [y/N]"
        if ($reply -match '^\s*[yY]') {
            Remove-Item -Recurse -Force $localAppDataDir
            Write-Ok "removed config directory $localAppDataDir"
        } else {
            Write-Ok "kept config directory $localAppDataDir"
        }
    } else {
        Remove-Item -Recurse -Force $localAppDataDir
        Write-Ok "removed config directory $localAppDataDir"
    }
}

# ---------------------------------------------------------------------------
# 9. Remove GoopDir
# ---------------------------------------------------------------------------
if ($PreserveApps -and $hasApps) {
    # Remove everything under GoopDir except apps\
    $appsPath = Join-Path $GoopDir 'apps'
    Get-ChildItem $GoopDir -Directory | Where-Object { $_.FullName -ne $appsPath } | ForEach-Object {
        Remove-Item -Recurse -Force $_.FullName
        Write-Ok "removed $_"
    }
    Get-ChildItem $GoopDir -File | ForEach-Object {
        Remove-Item -Force $_.FullName
        Write-Ok "removed $_"
    }
} else {
    Remove-Item -Recurse -Force $GoopDir
    Write-Ok "removed $GoopDir"
}

# ---------------------------------------------------------------------------
# 10. Done
# ---------------------------------------------------------------------------
Write-Host ""
Write-Ok "goop has been uninstalled."
if ($PreserveApps -and $hasApps) {
    Write-Host "Apps under $GoopDir\apps were preserved."
}
Write-Host "Close and re-open any terminals that had goop on PATH."
