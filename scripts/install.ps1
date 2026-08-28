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
    Defaults to the GOOP_HOME environment variable, else <home>\goop.

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
    [switch]$FromSource,
    [switch]$NoBucket,
    [switch]$RunAsAdmin,
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'

$Repo = 'TanguyBaudoin/goop'

function Write-Step($msg) { Write-Host $msg -ForegroundColor Cyan }
function Write-Ok($msg) { Write-Host $msg -ForegroundColor Green }

# Parameters are unbindable when the script arrives through iex, so every
# one of them falls back to an environment variable.
if (-not $GoopDir) {
    $GoopDir = if ($env:GOOP_HOME) { $env:GOOP_HOME } else { Join-Path $HOME 'goop' }
}
if (-not $Version -and $env:GOOP_VERSION) { $Version = $env:GOOP_VERSION }
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
$stagedExe = $null
$tempDir = $null

if ($FromSource) {
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
    if (-not $Version) {
        Write-Step "Resolving the latest release ..."
        try {
            $latest = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
        } catch {
            throw "Could not reach the GitHub releases API for $Repo. Pass -Version to name a release explicitly, or -FromSource to build from a checkout. Underlying error: $($_.Exception.Message)"
        }
        $Version = $latest.tag_name
    }
    $tag = if ($Version -match '^v') { $Version } else { "v$Version" }

    $tempDir = Join-Path ([IO.Path]::GetTempPath()) ("goop-install-" + [Guid]::NewGuid().ToString('N').Substring(0, 8))
    New-Item -ItemType Directory -Force -Path $tempDir | Out-Null
    $stagedExe = Join-Path $tempDir 'goop.exe'
    $checksums = Join-Path $tempDir 'checksums.txt'
    $base = "https://github.com/$Repo/releases/download/$tag"

    Write-Step "Downloading goop $tag ..."
    try {
        Invoke-WebRequest -Uri "$base/goop.exe" -OutFile $stagedExe -UseBasicParsing
    } catch {
        Remove-Item -Recurse -Force $tempDir -ErrorAction SilentlyContinue
        throw "Could not download $base/goop.exe -- check that release $tag exists, or use -FromSource. Underlying error: $($_.Exception.Message)"
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
        Write-Warning "No checksums.txt in release $tag; skipping checksum verification."
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

if (-not $NoBucket) {
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
