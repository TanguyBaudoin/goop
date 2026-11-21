<#
.SYNOPSIS
    Measures acceptance criterion 6: does reaching a compiler through a
    goop shim make a CMake/Ninja build slower than Scoop's shim does?

.DESCRIPTION
    Generates a CMake/Ninja project of N translation units and builds it
    three times over, changing only how the C compiler is reached:

      direct      the compiler's own path, no shim at all
      goop-shim   a hardlink of goop's shim-master.exe plus a goop-format
                  sidecar (Go's %q, so separators are doubled)
      scoop-shim  a copy of Scoop's shim.exe plus a Scoop-format sidecar
                  (single separators)

    The compiler binary is byte-identical in all three arms; only the
    invocation path differs, so the difference in build time is the cost
    of the shim and nothing else.

    Two design decisions worth knowing before reading the numbers:

    The project is an OBJECT library, so there is no link step. Linking
    several hundred objects overflows the command line, gcc falls back to
    a response file, and at least one MinGW ld cannot read it. Dropping
    the link is right anyway -- the compiler invocations are what a shim
    sits in front of, and one serial link would only dilute the result.

    The build is deliberately shim-saturated: many small translation
    units, so a fixed per-invocation cost is a large fraction of the
    total. Heavier units would bury the shim and flatter goop. Read the
    percentages as an upper bound on what a shim can cost, not as what a
    real project pays.

.PARAMETER Gcc
    Path to gcc.exe. Defaults to a goop-installed gcc.

.PARAMETER Units
    Translation units to generate. Default 800.

.PARAMETER Reps
    Repetitions per arm. Default 5. Every repetition is reported, because
    the spread within one arm bounds what any between-arm claim can mean.

.EXAMPLE
    .\scripts\bench-shim.ps1
#>
[CmdletBinding()]
param(
    [string]$Gcc = "$env:USERPROFILE\goop\apps\gcc\current\bin\gcc.exe",
    [string]$GoopShimMaster = "$env:USERPROFILE\goop\shims\shim-master.exe",
    [string]$ScoopShimsDir = "$env:USERPROFILE\scoop\shims",
    [int]$Units = 800,
    [int]$Reps = 5,
    [int]$MicroCalls = 300,
    [string]$WorkDir = (Join-Path $env:TEMP 'goop-bench-shim')
)

$ErrorActionPreference = 'Stop'
$utf8 = New-Object Text.UTF8Encoding $false

function Resolve-Tool([string]$name) {
    $c = Get-Command $name -ErrorAction SilentlyContinue
    if ($c) { return $c.Source }
    throw "$name is not on PATH; install it (goop install $name) and retry"
}

$cmake = Resolve-Tool 'cmake'
$ninja = Resolve-Tool 'ninja'

if (-not (Test-Path $Gcc)) { throw "no compiler at $Gcc -- pass -Gcc" }
if (-not (Test-Path $GoopShimMaster)) { throw "no goop shim master at $GoopShimMaster" }

# Any Scoop shim will do: they are all the same binary under different
# names, and only its launch cost is being measured.
$scoopSample = Get-ChildItem $ScoopShimsDir -Filter *.exe -ErrorAction SilentlyContinue |
    Where-Object { Test-Path ([IO.Path]::ChangeExtension($_.FullName, '.shim')) } |
    Select-Object -First 1
if (-not $scoopSample) { throw "no Scoop shim found under $ScoopShimsDir; this script needs one to compare against" }

Write-Host ("machine      : {0} logical cores" -f [Environment]::ProcessorCount)
Write-Host ("compiler     : {0}" -f $Gcc)
Write-Host ("goop shim    : {0:N0} bytes" -f (Get-Item $GoopShimMaster).Length)
Write-Host ("scoop shim   : {0:N0} bytes ({1})" -f $scoopSample.Length, $scoopSample.Name)

# --- the three front-ends -------------------------------------------------
New-Item -ItemType Directory -Force $WorkDir | Out-Null
$gccDir = Split-Path $Gcc -Parent
$env:Path = "$gccDir;$env:Path"   # MinGW needs its own bin for runtime DLLs

$goopDir = Join-Path $WorkDir 'fe-goop'
New-Item -ItemType Directory -Force $goopDir | Out-Null
$goopGcc = Join-Path $goopDir 'gcc.exe'
if (Test-Path $goopGcc) { Remove-Item $goopGcc -Force }
New-Item -ItemType HardLink -Path $goopGcc -Target $GoopShimMaster | Out-Null
[IO.File]::WriteAllText((Join-Path $goopDir 'gcc.shim'),
    ('path = "' + $Gcc.Replace('\', '\\') + '"' + "`n"), $utf8)

$scoopDir = Join-Path $WorkDir 'fe-scoop'
New-Item -ItemType Directory -Force $scoopDir | Out-Null
$scoopGcc = Join-Path $scoopDir 'gcc.exe'
Copy-Item $scoopSample.FullName $scoopGcc -Force
[IO.File]::WriteAllText((Join-Path $scoopDir 'gcc.shim'),
    ('path = "' + $Gcc + '"' + "`n"), $utf8)

$arms = [ordered]@{ 'direct' = $Gcc; 'goop-shim' = $goopGcc; 'scoop-shim' = $scoopGcc }

# Every arm must actually reach the same compiler, or the comparison is
# measuring something else entirely.
Write-Host ''
$versions = @{}
foreach ($name in $arms.Keys) {
    $v = (& $arms[$name] --version 2>&1 | Select-Object -First 1)
    $versions[$name] = $v
    Write-Host ("  {0,-11} {1}" -f $name, $v)
}
if (($versions.Values | Select-Object -Unique).Count -ne 1) {
    throw "the arms do not reach the same compiler; the comparison would be meaningless"
}

# --- the project ----------------------------------------------------------
$srcDir = Join-Path $WorkDir 'proj'
if (Test-Path $srcDir) { Remove-Item -Recurse -Force $srcDir }
New-Item -ItemType Directory -Force $srcDir | Out-Null

foreach ($i in 1..$Units) {
    [IO.File]::WriteAllText((Join-Path $srcDir "u$i.c"), @"
#include <stdlib.h>
#include <string.h>
#include <math.h>

static double work$i(const double *v, size_t n) {
    double acc = 0.0;
    for (size_t k = 0; k < n; ++k) {
        acc += sqrt(fabs(v[k])) * (double)($i % 7 + 1);
        if (acc > 1e9) acc = fmod(acc, 1e9);
    }
    return acc;
}

int f$i(int x) {
    double buf[32];
    for (int k = 0; k < 32; ++k) buf[k] = (double)(x + k + $i);
    return (int)work$i(buf, 32) + $i;
}
"@, $utf8)
}
[IO.File]::WriteAllText((Join-Path $srcDir 'main.c'), "int main(void) { return 0; }`n", $utf8)
$sources = (1..$Units | ForEach-Object { "u$_.c" }) -join ' '
[IO.File]::WriteAllText((Join-Path $srcDir 'CMakeLists.txt'), @"
cmake_minimum_required(VERSION 3.20)
project(goopbench C)
set(CMAKE_C_FLAGS "-O2")
add_library(goopbench OBJECT main.c $sources)
"@, $utf8)

Write-Host ''
Write-Host ("project      : {0} translation units, -O2, OBJECT library (no link)" -f $Units)
Write-Host ''

# --- per-invocation cost --------------------------------------------------
# Measured first and separately because it is the only part that resolves
# cleanly. Each arm pays the same PowerShell invocation overhead, so the
# *difference* between arms is the shim's own cost even though the
# absolute figure is inflated. Arms are interleaved here too.
Write-Host "per-invocation cost ($MicroCalls calls x $Reps interleaved passes):"
$micro = [ordered]@{}
foreach ($name in $arms.Keys) { $micro[$name] = @() }
foreach ($name in $arms.Keys) { & $arms[$name] --version > $null 2>&1 }  # warm
foreach ($pass in 1..$Reps) {
    foreach ($name in ($arms.Keys | Sort-Object { Get-Random })) {
        $exe = $arms[$name]
        $sw = [Diagnostics.Stopwatch]::StartNew()
        for ($i = 0; $i -lt $MicroCalls; $i++) { & $exe --version > $null 2>&1 }
        $sw.Stop()
        $micro[$name] += $sw.Elapsed.TotalMilliseconds / $MicroCalls
    }
}
function Median($a) { $s = @($a) | Sort-Object; return $s[[int]($s.Count / 2)] }
$microBase = Median $micro['direct']
foreach ($name in $arms.Keys) {
    $m = Median $micro[$name]
    if ($name -eq 'direct') {
        Write-Host ("  {0,-11} {1,6:N2} ms / call   (baseline, includes PowerShell's own cost)" -f $name, $m)
    } else {
        Write-Host ("  {0,-11} {1,6:N2} ms / call   shim costs {2,6:N2} ms" -f $name, $m, ($m - $microBase))
    }
}

# --- full build -----------------------------------------------------------
# Arms are interleaved and randomly ordered within each pass. Run all
# repetitions of one arm back to back and the first arm pays for cold file
# caches and for the antivirus reading 800 freshly written sources: an
# earlier version of this script did exactly that and produced a result
# saying both shims were *faster* than calling the compiler directly,
# which cannot be true. Position was being measured, not the shim.
Write-Host ''
Write-Host "full build ($Reps interleaved passes):"
$results = [ordered]@{}
foreach ($name in $arms.Keys) { $results[$name] = @() }
foreach ($pass in 1..$Reps) {
    foreach ($name in ($arms.Keys | Sort-Object { Get-Random })) {
        $exe = $arms[$name]
        $buildDir = Join-Path $WorkDir ("build-" + $name)
        if (Test-Path $buildDir) { Remove-Item -Recurse -Force $buildDir }
        # Configure is excluded: it runs once per project, while the
        # criterion is about build duration.
        & $cmake -S $srcDir -B $buildDir -G Ninja `
            "-DCMAKE_C_COMPILER=$($exe.Replace('\','/'))" `
            "-DCMAKE_MAKE_PROGRAM=$($ninja.Replace('\','/'))" > $null 2>&1
        if ($LASTEXITCODE -ne 0) { throw "cmake configure failed for $name" }
        $sw = [Diagnostics.Stopwatch]::StartNew()
        & $cmake --build $buildDir > $null 2>&1
        $sw.Stop()
        if ($LASTEXITCODE -ne 0) { throw "build failed for $name" }
        $results[$name] += $sw.Elapsed.TotalMilliseconds
    }
}
foreach ($name in $arms.Keys) {
    $sorted = @($results[$name]) | Sort-Object
    Write-Host ("  {0,-11} min {1,7:N0}  med {2,7:N0}  max {3,7:N0} ms   [{4}]" -f `
        $name, $sorted[0], $sorted[[int]($Reps / 2)], $sorted[-1],
        (($results[$name] | ForEach-Object { "{0:N0}" -f $_ }) -join ', '))
}

# Medians, not minima: the minimum of a few runs is itself noisy.
$base = Median $results['direct']
Write-Host ''
Write-Host "medians against direct invocation:"
foreach ($name in 'goop-shim', 'scoop-shim') {
    $m = Median $results[$name]
    $d = $m - $base
    $sign = if ($d -ge 0) { '+' } else { '-' }
    Write-Host ("  {0,-11} {1,7:N0} ms   {2}{3,6:N0} ms   {4,6:N1} %" -f $name, $m, $sign, [Math]::Abs($d), (100.0 * $d / $base))
}
$g = Median $results['goop-shim']
$s = Median $results['scoop-shim']
Write-Host ''
Write-Host ("  goop vs scoop : {0,7:N0} ms  {1,6:N1} %   (negative = goop faster)" -f ($g - $s), (100.0 * ($g - $s) / $s))

# --- does the run resolve anything? --------------------------------------
# This guard exists because the first three runs of this benchmark gave
# three different orderings of the same three arms, including one that
# had both shims *faster* than calling the compiler directly. On a
# 24-core machine an 800-unit build takes about 6 s, and the run-to-run
# spread reached 4.4 s -- far larger than any shim could cost. Reporting
# a percentage out of data like that is worse than reporting nothing, so
# the script now says when its own numbers do not support a conclusion.
$spreads = @{}
foreach ($name in $arms.Keys) {
    $mm = $results[$name] | Measure-Object -Maximum -Minimum
    $spreads[$name] = $mm.Maximum - $mm.Minimum
}
$worstSpread = ($spreads.Values | Measure-Object -Maximum).Maximum
$effects = @()
foreach ($name in 'goop-shim', 'scoop-shim') { $effects += [Math]::Abs((Median $results[$name]) - $base) }
$effects += [Math]::Abs($g - $s)
$smallestEffect = ($effects | Measure-Object -Minimum).Minimum

Write-Host ''
Write-Host ("  worst intra-arm spread : {0,7:N0} ms" -f $worstSpread)
Write-Host ("  smallest effect claimed: {0,7:N0} ms" -f $smallestEffect)
if ($worstSpread -ge $smallestEffect) {
    Write-Host ''
    Write-Host "  INCONCLUSIVE: the noise on this machine is as large as the differences" -ForegroundColor Yellow
    Write-Host "  being measured. Do not quote the percentages above. Re-run on an idle" -ForegroundColor Yellow
    Write-Host "  machine, with more repetitions, or trust only the per-invocation figure," -ForegroundColor Yellow
    Write-Host "  which averages over $MicroCalls calls and has reproduced across runs." -ForegroundColor Yellow
    exit 2
}
Write-Host ''
Write-Host "  the spread is below the smallest effect claimed, so the ordering above holds"
