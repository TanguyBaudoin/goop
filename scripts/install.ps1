#Requires -Version 5.1
<#
.SYNOPSIS
    Installs goop: downloads a published release binary, lays out its
    directory structure, and puts it on PATH. The equivalent of Scoop's
    irm get.scoop.sh | iex.

    Nothing needs to be installed first -- no Go toolchain, and no git.
    goop adds its buckets over HTTPS when git is absent.

.DESCRIPTION
    Run without a checkout:

        irm https://raw.githubusercontent.com/TanguyBaudoin/goop/main/scripts/install.ps1 | iex

    Piped into iex there is nothing to bind parameters to, so the options
    below are also read from the environment -- set them first, e.g.
    setting GOOP_HOME before invoking.

.PARAMETER GoopDir
    Where goop lives (apps/buckets/shims/cache and goop.exe itself).

    When neither this parameter nor GOOP_HOME is set, an interactive run
    asks, offering the right default: the root of an existing install if
    there is one, else <home>\goop. Pressing Enter accepts it.

    Nothing is asked when stdin is not a console, so unattended installs
    behave exactly as before. To choose the location without being asked
    -- including through iex, where parameters cannot bind -- set
    GOOP_HOME first:

        $env:GOOP_HOME = 'D:\goop'; irm <url> | iex

.PARAMETER Version
    Release to install, e.g. 0.1.0 or v0.1.0. Defaults to the latest
    published release. Environment: GOOP_VERSION.

.PARAMETER FromSource
    Build from a source checkout instead of downloading a release --
    requires a Go toolchain and this script to be run from inside the
    repository. For development; the released binary is what users want.
    Environment: GOOP_FROM_SOURCE=1.

.PARAMETER NoBucket
    Skip adding the main bucket after install. Environment: GOOP_NO_BUCKET=1.

.PARAMETER RunAsAdmin
    goop never installs system-wide (NR-01) and its directories are fully
    user-owned, so there is no reason to run this elevated -- same
    reasoning as Scoop's own installer, which also refuses.

.PARAMETER DryRun
    Report what would be persisted to GOOP_HOME/PATH without changing it.
#>
[CmdletBinding()]
param(
    [string]$GoopDir,
    [string]$Version,
    [string]$From,
    [switch]$FromSource,
    [switch]$NoBucket,
    [switch]$RunAsAdmin,
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'

$Repo = 'TanguyBaudoin/goop'

function Write-Step($msg) { Write-Host $msg -ForegroundColor Cyan }
function Write-Ok($msg) { Write-Host $msg -ForegroundColor Green }

# Where an existing install already lives, so re-running the installer
# offers that instead of sending goop to <home>\goop and orphaning the
# packages already installed elsewhere. goop persists a non-default root
# in %LOCALAPPDATA%\goop\config.json (paths.ConfiguredRoot); this reads
# the same file rather than guessing.
function Get-ExistingRoot {
    $cfg = Join-Path $env:LOCALAPPDATA 'goop\config.json'
    if (-not (Test-Path $cfg)) { return $null }
    try {
        $root = (Get-Content $cfg -Raw | ConvertFrom-Json).root
    } catch {
        return $null   # a corrupt config is not this script's problem to fix
    }
    if ($root -and (Test-Path (Join-Path $root 'bin\goop.exe'))) { return $root }
    return $null
}

# Prompting is only ever a convenience: it must never be the reason an
# unattended install hangs. UserInteractive alone is not enough -- it
# stays true in plenty of contexts with no console attached -- so the
# redirected-stdin check is what actually decides.
function Test-CanPrompt {
    try {
        return [Environment]::UserInteractive -and -not [Console]::IsInputRedirected
    } catch {
        return $false
    }
}

function Resolve-InstallDir([string]$raw) {
    $v = [Environment]::ExpandEnvironmentVariables($raw.Trim().Trim('"'))
    if ($v.StartsWith('~')) { $v = Join-Path $HOME $v.Substring(1).TrimStart('\', '/') }
    # [IO.Path]::GetFullPath, not Resolve-Path: the directory does not
    # exist yet, and Resolve-Path fails on paths that do not.
    $full = [IO.Path]::GetFullPath($v)
    # GetFullPath keeps a trailing separator, and "<home>\goop\" compares
    # unequal to "<home>\goop" -- which would make the installer persist a
    # GOOP_HOME it does not need, for a root that is in fact the default.
    # A drive root has to keep its separator: "D:" is not "D:\".
    if ($full.Length -gt 3) { $full = $full.TrimEnd('\', '/') }
    return $full
}

function Read-InstallDir([string]$default) {
    if (-not (Test-CanPrompt)) { return $default }
    for ($attempt = 0; $attempt -lt 3; $attempt++) {
        $answer = Read-Host "Where should goop live? [$default]"
        if ([string]::IsNullOrWhiteSpace($answer)) { return $default }
        try {
            $resolved = Resolve-InstallDir $answer
        } catch {
            Write-Host "  not a usable path: $($_.Exception.Message)" -ForegroundColor Yellow
            continue
        }
        # A relative answer is resolved against the current directory,
        # which through iex is wherever the user happened to be standing.
        # Showing the result is the difference between a convenience and a
        # surprise.
        if ($resolved -ne $answer.Trim().Trim('"')) {
            Write-Host "  -> $resolved"
        }
        if (Test-Path $resolved -PathType Leaf) {
            Write-Host "  that is a file, not a directory" -ForegroundColor Yellow
            continue
        }
        # goop creates apps\, bin\, shims\, buckets\ and cache\ under this
        # directory. Pointing it at something that already holds unrelated
        # files (a home directory, a drive root) is almost always a typo,
        # and one that is tedious to undo, so it gets a confirmation.
        if ((Test-Path $resolved) -and
            -not (Test-Path (Join-Path $resolved 'bin\goop.exe')) -and
            @(Get-ChildItem -LiteralPath $resolved -Force -ErrorAction SilentlyContinue).Count -gt 0) {
            Write-Host "  $resolved already exists and is not a goop install." -ForegroundColor Yellow
            Write-Host "  goop would create apps\, bin\, shims\, buckets\ and cache\ inside it." -ForegroundColor Yellow
            if ((Read-Host "  use it anyway? [y/N]") -notmatch '^(y|yes|o|oui)$') { continue }
        }
        return $resolved
    }
    Write-Host "Keeping the default: $default" -ForegroundColor Yellow
    return $default
}

# Parameters are unbindable when the script arrives through iex, so every
# one of them falls back to an environment variable.
#
# An explicit choice -- the parameter, or GOOP_HOME -- is taken as final
# and never second-guessed with a prompt. Only a run that expressed no
# preference gets asked.
if (-not $GoopDir) {
    if ($env:GOOP_HOME) {
        $GoopDir = $env:GOOP_HOME
    } else {
        $existing = Get-ExistingRoot
        $suggested = if ($existing) { $existing } else { Join-Path $HOME 'goop' }
        if ($existing) { Write-Host "Found an existing install at $existing" }
        $GoopDir = Read-InstallDir $suggested
    }
}
$GoopDir = Resolve-InstallDir $GoopDir
if (-not $Version -and $env:GOOP_VERSION) { $Version = $env:GOOP_VERSION }
if (-not $From -and $env:GOOP_FROM) { $From = $env:GOOP_FROM }
if (-not $FromSource -and $env:GOOP_FROM_SOURCE -eq '1') { $FromSource = $true }
if (-not $NoBucket -and $env:GOOP_NO_BUCKET -eq '1') { $NoBucket = $true }

# Windows PowerShell 5.1 still negotiates TLS 1.0 by default on some
# systems, which GitHub refuses outright -- without this the download
# fails with a bare "could not create SSL/TLS secure channel".
if ([Net.ServicePointManager]::SecurityProtocol -notmatch 'Tls12') {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
}

$isElevated = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if ($isElevated -and -not $RunAsAdmin) {
    throw "Running as administrator. goop installs per-user only (NR-01) -- re-run from a normal, non-elevated PowerShell, or pass -RunAsAdmin if you really mean it."
}

# ---------------------------------------------------------------------------
# Obtain goop.exe, either from a release or from a local build.
# ---------------------------------------------------------------------------
# An offline bundle is a directory holding goop.exe next to this script,
# and optionally a bucket archive. Detected rather than declared: if the
# binary is sitting there, downloading a copy of it would be absurd.
$bundleDir = $PSScriptRoot
if ($From) {
    if (-not (Test-Path $From)) { throw "-From: no such file: $From" }
    $bundleExe = (Resolve-Path $From).Path
    $bundleDir = Split-Path -Parent $bundleExe
} elseif ($bundleDir -and (Test-Path (Join-Path $bundleDir 'goop.exe'))) {
    $bundleExe = (Join-Path $bundleDir 'goop.exe')
} else {
    $bundleExe = $null
}

$stagedExe = $null
$tempDir = $null

if ($From -and $FromSource) {
    throw "-From and -FromSource are mutually exclusive: one installs a binary you already have, the other builds one."
}

if ($FromSource) {
    $bundleExe = $null
}

if ($bundleExe) {
    Write-Step "Using the bundled goop.exe (offline) ..."
    # Same checksum check as a download, when the bundle carries one --
    # a USB stick is not more trustworthy than HTTPS, just closer.
    $bundleSums = Join-Path $bundleDir 'checksums.txt'
    if (Test-Path $bundleSums) {
        $expected = ((Get-Content $bundleSums -Raw).Trim() -split '\s+')[0].ToLower()
        $actual = (Get-FileHash $bundleExe -Algorithm SHA256).Hash.ToLower()
        if ($expected -ne $actual) {
            throw "Checksum mismatch for the bundled goop.exe: expected $expected, got $actual. Nothing was installed."
        }
        Write-Ok "checksum verified (sha256 $actual)"
    }
    $stagedExe = $bundleExe
} elseif ($FromSource) {
    if (-not $PSScriptRoot) {
        throw "-FromSource needs the script to run from a checkout; it cannot work when piped into iex. Clone the repository and run scripts\install.ps1 from there."
    }
    $repoRoot = Split-Path -Parent $PSScriptRoot
    if (-not (Test-Path (Join-Path $repoRoot 'cmd\goop\main.go'))) {
        throw "-FromSource expects a goop source checkout (found no cmd\goop\main.go above $PSScriptRoot)."
    }
    if (-not (Get-Command go -CommandType Application -ErrorAction SilentlyContinue)) {
        throw "Go toolchain not found on PATH; required to build from source (https://go.dev/dl/). Drop -FromSource to install a published release instead."
    }
    Write-Step "Building goop from $repoRoot ..."
    # Delegated to build.ps1: that script owns the shim-then-goop ordering
    # and the version stamp, and a second copy would silently drift.
    & (Join-Path $PSScriptRoot 'build.ps1')
    if ($LASTEXITCODE -ne 0) { throw "build failed" }
    $stagedExe = Join-Path $repoRoot 'build\goop.exe'
    Write-Ok "built $stagedExe"
} else {
    # No call to api.github.com. Its unauthenticated limit is 60 requests
    # per hour *per IP*, which a corporate NAT shares across everyone
    # behind it -- the install would fail with an opaque 403 through no
    # fault of the person running it. GitHub's /releases/latest/download/
    # redirect serves the same asset and is not rate limited.
    if ($Version) {
        $tag = if ($Version -match '^v') { $Version } else { "v$Version" }
        $base = "https://github.com/$Repo/releases/download/$tag"
        $label = $tag
    } else {
        $base = "https://github.com/$Repo/releases/latest/download"
        $label = 'the latest release'
    }

    $tempDir = Join-Path ([IO.Path]::GetTempPath()) ("goop-install-" + [Guid]::NewGuid().ToString('N').Substring(0, 8))
    New-Item -ItemType Directory -Force -Path $tempDir | Out-Null
    $stagedExe = Join-Path $tempDir 'goop.exe'
    $checksums = Join-Path $tempDir 'checksums.txt'

    Write-Step "Downloading goop ($label) ..."
    try {
        Invoke-WebRequest -Uri "$base/goop.exe" -OutFile $stagedExe -UseBasicParsing
    } catch {
        Remove-Item -Recurse -Force $tempDir -ErrorAction SilentlyContinue
        throw "Could not download $base/goop.exe -- check that the release exists, or use -FromSource. Underlying error: $($_.Exception.Message)"
    }

    # The checksum travels in the same release, so it guards against a
    # truncated or corrupted download rather than against a compromised
    # release -- HTTPS and GitHub are the trust anchor here. Verifying a
    # release signature with the binary being installed would be circular;
    # goop verify is for checking later upgrades from an already-trusted
    # goop.
    $haveChecksums = $true
    try {
        Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $checksums -UseBasicParsing
    } catch {
        $haveChecksums = $false
        Write-Warning "No checksums.txt published alongside this release; skipping checksum verification."
    }
    if ($haveChecksums) {
        $expected = ((Get-Content $checksums -Raw).Trim() -split '\s+')[0].ToLower()
        $actual = (Get-FileHash $stagedExe -Algorithm SHA256).Hash.ToLower()
        if ($expected -ne $actual) {
            Remove-Item -Recurse -Force $tempDir -ErrorAction SilentlyContinue
            throw "Checksum mismatch for goop.exe: expected $expected, got $actual. Nothing was installed."
        }
        Write-Ok "checksum verified (sha256 $actual)"
    }
}

# ---------------------------------------------------------------------------
# Lay out the directory tree and install the binary.
# ---------------------------------------------------------------------------
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
Copy-Item -Path $stagedExe -Destination (Join-Path $binDir 'goop.exe') -Force
Write-Ok "installed $binDir\goop.exe"
if ($tempDir) { Remove-Item -Recurse -Force $tempDir -ErrorAction SilentlyContinue }

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

if ($bundleExe -and -not $NoBucket) {
    # Installed from a bundle, so there is probably no network -- and on an
    # internal network the public main bucket is not what you want anyway.
    # Say what to do instead of failing to reach GitHub.
    Write-Host ""
    Write-Host "No bucket was added. Add the one you use, for example:"
    Write-Host "    goop bucket add main https://github.com/ScoopInstaller/Main"
    Write-Host "    goop bucket add internal file://fileserver/goop/our-bucket.zip"
} elseif (-not $NoBucket) {
    $bucketsFile = Join-Path $GoopDir 'buckets.json'
    $hasMain = (Test-Path $bucketsFile) -and ((Get-Content $bucketsFile -Raw) -match '"main"')
    if (-not $hasMain) {
        # goop falls back to a codeload archive when git is absent, so this
        # works on a machine with no git installed.
        Write-Step "Adding main bucket ..."
        & (Join-Path $binDir 'goop.exe') bucket add main https://github.com/ScoopInstaller/Main
    }
}

Write-Host ""
Write-Ok "goop is installed."
& (Join-Path $binDir 'goop.exe') version
Write-Host "Open a new terminal (so the updated PATH takes effect), then run 'goop help' to get started."
