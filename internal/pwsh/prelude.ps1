# goop's Scoop-compatible PowerShell compat library. Real manifest
# scripts (pre_install, post_install, installer.script, uninstaller.script)
# call these functions directly by name, expecting them to already be in
# scope -- Scoop itself defines them in lib/*.ps1. Signatures here mirror
# the real ones closely enough for the common call patterns actually seen
# across the ScoopInstaller/Main and /Extras corpora; internals differ
# (e.g. 7z.exe stands in for Scoop's bundled helper, msiexec for lessmsi).

function info($msg) { Write-Host "INFO  $msg" -ForegroundColor DarkGray }
function warn($msg) { Write-Host "WARN  $msg" -ForegroundColor DarkYellow }
function error($msg) { Write-Host "ERROR $msg" -ForegroundColor DarkRed }
function abort($msg, [int]$exit_code = 1) { Write-Host $msg -ForegroundColor Red; exit $exit_code }
function debug($obj) { }

function ensure($dir) {
    if (!(Test-Path -Path $dir)) {
        New-Item -Path $dir -ItemType Directory -Force | Out-Null
    }
    Convert-Path -Path $dir
}

function fname($path) { Split-Path $path -Leaf }
function strip_ext($str) { $str -replace '\.[^.\\/]*$', '' }

# Absolute-path resolution that works even for a path that doesn't exist
# yet (Resolve-Path errors in that case) -- a direct port of Scoop's own
# implementation, no goop-specific behavior involved.
function Get-AbsolutePath {
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory = $true, ValueFromPipeline = $true)]
        [string]$Path
    )
    process {
        return $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Path)
    }
}

# Cosmetic path shortening for a script's own console output -- replaces
# the user's home directory prefix with "~\", same as Scoop's own
# friendly_path (goop's CLI has an equivalent for its own log lines,
# separately, in internal/installer/manage.go).
function friendly_path($path) {
    $h = (Get-PSProvider 'FileSystem').Home
    if (!$h.EndsWith('\')) { $h += '\' }
    if ($h -eq '\') { return $path }
    return $path -replace ([Regex]::Escape($h)), '~\'
}

# Real manifest scripts call this to warn/branch on elevation (e.g. an
# installer that behaves differently, or refuses to run at all, without
# admin rights) -- the standard WindowsPrincipal check, same approach
# Scoop's own lib/core.ps1 uses.
function is_admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    (New-Object Security.Principal.WindowsPrincipal $id).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

# Scripts sometimes reference a *different* app (e.g. an optional
# companion package) to decide what to do, not just $dir/$version for the
# one being installed -- these mirror Scoop's real path helpers closely
# enough for that read-only lookup ("current" as $version naturally lands
# on the current-junction path, same as Scoop's).
function appdir($app, $global) { Join-Path $goop_apps_dir $app }
function versiondir($app, $version, $global) { Join-Path (appdir $app $global) $version }
function currentdir($app, $global) { versiondir $app 'current' $global }
function appsdir($global) { $goop_apps_dir }
function persistdir($app, $global) { Join-Path (Split-Path $goop_apps_dir -Parent) "persist\$app" }

# A minority of real manifests (confirmed: extras/hwinfo.json,
# main/cygwin.json -- exactly 2 in the whole corpus) call Scoop's own
# internal persist machinery directly from a script, passing a
# synthetic @{ persist = ... } object standing in for a manifest, on
# top of (not instead of) whatever their own top-level `persist` field
# already declares -- goop's normal top-level `persist` handling is
# native Go (internal/installer/persist.go's linkPersist), entirely
# separate from this; these are ports of real Scoop's own
# lib/install.ps1/lib/core.ps1 functions for the rarer case of a
# script invoking the mechanism itself.
function is_directory([string]$path) {
    return (Test-Path $path) -and (Get-Item $path) -is [System.IO.DirectoryInfo]
}

function persist_def($persist) {
    if ($persist -is [Array]) {
        $source = $persist[0]
        $target = $persist[1]
    } else {
        $source = $persist
        $target = $null
    }
    if (!$target) { $target = $source }
    return $source, $target
}

function New-DirectoryJunction($source, $target) {
    if (Get-Service -Name cexecsvc -ErrorAction SilentlyContinue) {
        cmd.exe /d /c "mklink /j `"$source`" `"$target`""
    } else {
        New-Item -Path $source -ItemType Junction -Value $target
    }
}

function persist_data($manifest, $original_dir, $persist_dir) {
    $persist = $manifest.persist
    if (-not $persist) { return }
    $persist_dir = ensure $persist_dir
    if ($persist -is [String]) { $persist = @($persist) }

    $persist | ForEach-Object {
        $source, $target = persist_def $_
        Write-Host "Persisting $source"
        $source = $source.TrimEnd('/').TrimEnd('\')
        $source = "$original_dir\$source"
        $target = "$persist_dir\$target"

        if (Test-Path $target) {
            if (Test-Path $source) {
                Move-Item -Force $source "$source.original"
            }
        } elseif (Test-Path $source) {
            New-Item -ItemType Directory -Force -Path (Split-Path -Path $target) | Out-Null
            Move-Item $source $target
        } else {
            New-Item -ItemType Directory -Force -Path $target | Out-Null
        }

        if (is_directory $target) {
            New-DirectoryJunction $source $target | Out-Null
            attrib $source +R /L
        } else {
            New-Item -Path $source -ItemType HardLink -Value $target | Out-Null
        }
    }
}

function unlink_persist_data($manifest, $dir) {
    $persist = $manifest.persist
    if (-not $persist) { return }
    @($persist) | ForEach-Object {
        $source, $null = persist_def $_
        $source = Get-Item "$dir\$source" -ErrorAction SilentlyContinue
        if ($source.LinkType) {
            $source_path = $source.FullName
            if ($source -is [System.IO.DirectoryInfo]) {
                attrib -R /L $source_path
                Remove-Item -Path $source_path -Recurse -Force -ErrorAction SilentlyContinue
            } else {
                Remove-Item -Path $source_path -Force -ErrorAction SilentlyContinue
            }
        }
    }
}

# goop is always per-user (NR-01: $global is hardcoded $false, see
# bridge.go's header) -- real Scoop only ever touches ACLs here when
# $global is true, so this stays a correct, faithful no-op in goop
# rather than needing its own separate stub.
function persist_permission($manifest, $global) {
    if ($global -and $manifest.persist -and (is_admin)) {
        $path = persistdir $null $global
        $user = New-Object System.Security.Principal.SecurityIdentifier 'S-1-5-32-545'
        $target_rule = New-Object System.Security.AccessControl.FileSystemAccessRule($user, 'Write', 'ObjectInherit', 'none', 'Allow')
        $acl = Get-Acl -Path $path
        $acl.SetAccessRule($target_rule)
        $acl | Set-Acl -Path $path
    }
}

# Whether another app (not necessarily the one this script belongs to)
# is currently installed -- $global is accepted for call-signature
# compatibility but ignored, same as elsewhere in this file (goop has no
# global/per-user distinction).
function installed($app, $global) {
    $app = ($app -split '/|\\')[-1]
    return Test-Path (Join-Path (appdir $app $global) 'current')
}

# Recursive move via robocopy (/e /move), used to flatten a nested
# extraction directory into the app dir -- direct port of Scoop's own
# implementation. Exit codes 0-7 are robocopy successes (files
# copied/skipped, no failures); 8+ means real failure.
function movedir($from, $to) {
    $from = $from.TrimEnd('\')
    $to = $to.TrimEnd('\')

    $proc = New-Object System.Diagnostics.Process
    $proc.StartInfo.FileName = 'robocopy.exe'
    $proc.StartInfo.Arguments = "`"$from`" `"$to`" /e /move"
    $proc.StartInfo.RedirectStandardOutput = $true
    $proc.StartInfo.RedirectStandardError = $true
    $proc.StartInfo.UseShellExecute = $false
    $proc.StartInfo.WindowStyle = [System.Diagnostics.ProcessWindowStyle]::Hidden
    [void]$proc.Start()
    $stdoutTask = $proc.StandardOutput.ReadToEndAsync()
    $proc.WaitForExit()

    if ($proc.ExitCode -ge 8) {
        throw "Could not find '$(fname $from)'! (error $($proc.ExitCode))"
    }
    1..10 | ForEach-Object {
        if (Test-Path $from) { Start-Sleep -Milliseconds 100 }
    }
}

# Where a script's own manual download would land in goop's cache --
# mirrors Scoop's exact underscored-URL + truncated-SHA256 naming
# scheme, even though it doesn't line up with goop's own downloader's
# content-hash-addressed cache entries (internal/downloader.Get) --
# scripts using this just need a stable, writable scratch path, not an
# entry goop's own downloader already created.
function cache_path($app, $version, $url) {
    $underscoredUrl = $url -replace '[^\w\.\-]+', '_'
    $filePath = Join-Path $goop_cache_dir "$app#$version#$underscoredUrl"
    $urlStream = [System.IO.MemoryStream]::new([System.Text.Encoding]::UTF8.GetBytes($url))
    $sha = (Get-FileHash -Algorithm SHA256 -InputStream $urlStream).Hash.ToLower().Substring(0, 7)
    $extension = [System.IO.Path]::GetExtension($url)
    return ($filePath -replace [Regex]::Escape($underscoredUrl), "$sha$extension")
}

# Resolves the path to a helper tool goop itself would shell out to for
# the same purpose -- unlike real Scoop (which resolves these against
# its own separately-installed Git/7zip/Lessmsi/Innounp/Dark/Aria2 apps),
# goop has no equivalent for Lessmsi (uses msiexec.exe directly) or
# Aria2 (uses its own native chunked downloader), so those two always
# return $null; the rest resolve via PATH, same as goop's own
# Expand-*Archive functions already do.
function Get-HelperPath {
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory = $true, Position = 0, ValueFromPipeline = $true)]
        [ValidateSet('Git', '7zip', 'Lessmsi', 'Innounp', 'Dark', 'Aria2')]
        [string]$Helper
    )
    process {
        $cmd = switch ($Helper) {
            'Git' { 'git.exe' }
            '7zip' { '7z.exe' }
            'Innounp' { 'innounp.exe' }
            'Dark' { 'dark.exe' }
            default { $null }
        }
        if (-not $cmd) { return $null }
        $found = Get-Command $cmd -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($found) { return $found.Source }
        return $null
    }
}

# Resolves what a command on PATH actually points to -- if it's one of
# goop's own shims, reads the real target out of the .shim sidecar
# instead of returning the shim.exe stub's own path.
function Get-CommandPath {
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory = $true, ValueFromPipeline = $true)]
        [string]$Command
    )
    process {
        $comm = Get-Command $Command -ErrorAction SilentlyContinue
        if (-not $comm) { return $null }
        if ($comm.Source -like "$goop_shims_dir\*") {
            $shimFile = Join-Path $goop_shims_dir ((strip_ext (fname $comm.Source)) + '.shim')
            if (Test-Path $shimFile) {
                $line = Get-Content $shimFile | Select-Object -First 1
                return ($line -replace '^path\s*=\s*"?([^"]*?)"?$', '$1')
            }
        }
        if ($comm.CommandType -eq 'Application') { return $comm.Source }
        return $null
    }
}

# Standard Win32 argv quoting (CommandLineToArgvW-compatible). Used
# instead of ProcessStartInfo.ArgumentList, which is unreliable on
# Windows PowerShell 5.1 (.NET Framework) -- goop targets 5.1 since it
# ships in Windows with no separate install, matching what a real user's
# machine has without requiring PowerShell 7.
function goop_quote_arg($s) {
    if ($null -eq $s -or $s -eq '') { return '""' }
    if ($s -notmatch '[\s"]') { return $s }
    $result = '"'
    $slashes = 0
    foreach ($ch in $s.ToCharArray()) {
        if ($ch -eq '\') {
            $slashes++
            $result += $ch
        } elseif ($ch -eq '"') {
            $result += ('\' * $slashes) + '\"'
            $slashes = 0
        } else {
            $slashes = 0
            $result += $ch
        }
    }
    $result += ('\' * $slashes) + '"'
    return $result
}

function Invoke-ExternalCommand {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true, Position = 0)]
        [Alias('Path')]
        [string]$FilePath,
        [Parameter(Position = 1)]
        [Alias('Args')]
        [string[]]$ArgumentList,
        [switch]$RunAs,
        [switch]$Quiet,
        [Alias('Msg')]
        [string]$Activity,
        [Hashtable]$ContinueExitCodes,
        [string]$LogPath
    )
    if ($Activity) { Write-Host "$Activity " -NoNewline }
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $FilePath
    if ($ArgumentList) {
        $psi.Arguments = (($ArgumentList | ForEach-Object { goop_quote_arg $_ }) -join ' ')
    }
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $p = [System.Diagnostics.Process]::Start($psi)
    # Classic .NET Process deadlock avoided: read both streams
    # asynchronously *before* WaitForExit, not ReadToEnd() on one then
    # the other. A child that writes enough to stderr while stdout sits
    # empty (msiexec routinely does) fills stderr's pipe buffer and
    # blocks on it while we're still blocked reading stdout to
    # completion -- neither side ever unblocks the other. Starting both
    # reads up front drains both pipes concurrently, so WaitForExit is
    # safe.
    $outTask = $p.StandardOutput.ReadToEndAsync()
    $errTask = $p.StandardError.ReadToEndAsync()
    $p.WaitForExit()
    $out = $outTask.GetAwaiter().GetResult()
    $errOut = $errTask.GetAwaiter().GetResult()
    if ($out) { Write-Host $out }
    if ($errOut) { Write-Host $errOut -ForegroundColor DarkRed }
    if ($Activity) { Write-Host "done." }
    $ok = ($p.ExitCode -eq 0)
    if (-not $ok -and $ContinueExitCodes -and $ContinueExitCodes.ContainsKey($p.ExitCode)) {
        Write-Host $ContinueExitCodes[$p.ExitCode]
        $ok = $true
    }
    return $ok
}

function Expand-7zipArchive {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true, Position = 0)] [string]$Path,
        [Parameter(Position = 1)] [string]$DestinationPath = (Split-Path $Path),
        [string]$ExtractDir,
        [Parameter(ValueFromRemainingArguments = $true)] [string[]]$Switches,
        [ValidateSet('All', 'Skip', 'Rename')] [string]$Overwrite,
        [switch]$Removal
    )
    $sevenZip = Get-Command '7z.exe' -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $sevenZip) {
        abort "7z.exe not found on PATH; install 7-Zip (e.g. with an existing package manager) so goop can delegate .7z/.msi/installer extraction to it (CPT-05/EXT-06)."
    }
    $dest = $DestinationPath.TrimEnd('\')

    # A compressed tar (.tar.xz, .tbz2, .txz, .tar.zst, ...) takes 7z.exe
    # two passes to fully unpack: the first only strips the outer
    # compression, leaving a plain .tar file that itself still needs its
    # own extraction pass -- same detection Scoop's own Expand-7zipArchive
    # uses. Missing this left the produced .tar silently discarded as
    # "temp folder cleanup" (real bug, caught against a real .tar.xz
    # manifest -- main/curl.json -- once goop started routing more
    # formats through this function).
    $isTar = ((strip_ext $Path) -match '\.tar$') -or ($Path -match '\.t[abgpx]z2?$')
    if ($isTar) {
        $unpackDir = Join-Path $dest '_goop_untar'
        New-Item -ItemType Directory -Force -Path $unpackDir | Out-Null
        $ok = Invoke-ExternalCommand -FilePath $sevenZip.Source -ArgumentList @('x', $Path, "-o$unpackDir", '-y')
        if (-not $ok) { abort "7z extraction of '$Path' failed." }
        $tarFile = Get-ChildItem -Path $unpackDir -Filter '*.tar' -File | Select-Object -First 1
        if (-not $tarFile) { abort "7z extraction of '$Path' didn't produce a .tar file to unpack." }
        Expand-7zipArchive -Path $tarFile.FullName -DestinationPath $dest -ExtractDir $ExtractDir
        Remove-Item $unpackDir -Recurse -Force
        if ($Removal) { Remove-Item $Path -Force }
        return
    }

    $target = $dest
    if ($ExtractDir) { $target = Join-Path $dest '_goop_tmp' }
    New-Item -ItemType Directory -Force -Path $target | Out-Null

    $argList = @('x', $Path, "-o$target", '-y')
    if ($Switches) { $argList += $Switches }
    $ok = Invoke-ExternalCommand -FilePath $sevenZip.Source -ArgumentList $argList
    if (-not $ok) { abort "7z extraction of '$Path' failed." }

    if ($ExtractDir) {
        $src = Join-Path $target $ExtractDir
        Get-ChildItem -Path $src -Force | Move-Item -Destination $dest -Force
        Remove-Item $target -Recurse -Force
    }
    if ($Removal) { Remove-Item $Path -Force }
}

function Expand-MsiArchive {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true, Position = 0)] [string]$Path,
        [Parameter(Position = 1)] [string]$DestinationPath = (Split-Path $Path),
        [string]$ExtractDir,
        [Parameter(ValueFromRemainingArguments = $true)] [string[]]$Switches,
        [switch]$Removal
    )
    $dest = $DestinationPath.TrimEnd('\')
    New-Item -ItemType Directory -Force -Path $dest | Out-Null
    # Administrative install: extracts without installing or needing admin
    # rights, matching what Scoop itself does when use_lessmsi is off.
    # msiexec stays silent on stdout/stderr in /qn mode even on failure
    # (exit code alone doesn't say why) -- /lwe writes the real reason to
    # a log file, which we read back on failure so the error is
    # actionable (TR-08) instead of a bare "extraction failed".
    $msiLog = Join-Path $dest 'msiexec.log'
    $argList = @('/a', $Path, '/qn', "TARGETDIR=$dest\SourceDir", '/lwe', $msiLog)
    if ($Switches) { $argList += $Switches }
    $ok = Invoke-ExternalCommand -FilePath 'msiexec.exe' -ArgumentList $argList
    if (-not $ok) {
        $reason = ""
        if (Test-Path $msiLog) {
            $reason = (Get-Content $msiLog | Where-Object { $_ -match '^Error \d+\.' } | Select-Object -Last 1)
        }
        if ($reason -match 'Error writing to file') {
            abort "msiexec extraction of '$Path' failed: $reason`nThis usually means a path under '$dest' is too long for Windows (MAX_PATH); see TR-31 -- try a shorter goop root (GOOP_HOME) or enable Windows long path support."
        } elseif ($reason) {
            abort "msiexec extraction of '$Path' failed: $reason"
        } else {
            abort "msiexec extraction of '$Path' failed (see $msiLog for details)."
        }
    }
    Remove-Item $msiLog -Force -ErrorAction SilentlyContinue

    $source = "$dest\SourceDir"
    if ($ExtractDir) { $source = Join-Path $source $ExtractDir }
    if (Test-Path $source) {
        Get-ChildItem -Path $source -Force | Move-Item -Destination $dest -Force
    }
    Remove-Item "$dest\SourceDir" -Recurse -Force -ErrorAction SilentlyContinue
    if ($Removal) { Remove-Item $Path -Force }
}

function Expand-ZipArchive {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true, Position = 0)] [string]$Path,
        [Parameter(Position = 1)] [string]$DestinationPath = (Split-Path $Path),
        [string]$ExtractDir,
        [switch]$Removal
    )
    $dest = $DestinationPath.TrimEnd('\')
    if ($ExtractDir) {
        $tmp = Join-Path $dest '_goop_tmp'
        Expand-Archive -Path $Path -DestinationPath $tmp -Force
        Get-ChildItem -Path (Join-Path $tmp $ExtractDir) -Force | Move-Item -Destination $dest -Force
        Remove-Item $tmp -Recurse -Force
    } else {
        Expand-Archive -Path $Path -DestinationPath $dest -Force
    }
    if ($Removal) { Remove-Item $Path -Force }
}

function Expand-InnoArchive {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true, Position = 0)] [string]$Path,
        [Parameter(Position = 1)] [string]$DestinationPath = (Split-Path $Path),
        [string]$ExtractDir,
        [Parameter(ValueFromRemainingArguments = $true)] [string[]]$Switches,
        [switch]$Removal
    )
    # innounp isn't bundled; goop's own shims dir is on PATH (see
    # bridge.go), so this resolves for real once the user has
    # `goop install innounp` (a normal Scoop main-bucket package).
    #
    # Deliberately no 7z fallback: a plain `7z x` against an Inno Setup
    # installer only unpacks the wrapper .exe's own PE sections (.text,
    # .rsrc, a numbered payload stub, ...), not the actual embedded
    # install tree -- confirmed against a real keepass.json install,
    # which "succeeded" but left no KeePass.exe anywhere in the result.
    # Aborting here (same pattern Expand-7zipArchive uses for a missing
    # 7z.exe) is safer than silently committing a broken install that
    # reports success; real Scoop never falls back either -- it treats
    # innounp as a hard implicit dependency (lib/depends.ps1's
    # `$Manifest.innosetup -> $helper += 'innounp'`) installed before
    # extraction ever runs.
    $innounp = Get-Command 'innounp.exe' -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $innounp) {
        abort "innounp.exe not found on PATH; install it with ``goop install innounp`` so goop can extract this Inno Setup installer."
    }

    $dest = $DestinationPath.TrimEnd('\')
    New-Item -ItemType Directory -Force -Path $dest | Out-Null
    $argList = @('-x', "-d$dest", $Path, '-y')
    if ($ExtractDir) {
        if ($ExtractDir -match '^\{') { $argList += "-c$ExtractDir" }
        else { $argList += "-c{app}\$ExtractDir" }
    } else {
        $argList += '-c{app}'
    }
    if ($Switches) { $argList += $Switches }
    $ok = Invoke-ExternalCommand -FilePath $innounp.Source -ArgumentList $argList
    if (-not $ok) { abort "innounp extraction of '$Path' failed." }
    if ($Removal) { Remove-Item $Path -Force }
}

function Expand-DarkArchive {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true, Position = 0)] [string]$Path,
        [Parameter(Position = 1)] [string]$DestinationPath = (Split-Path $Path),
        [Parameter(ValueFromRemainingArguments = $true)] [string[]]$Switches,
        [switch]$Removal
    )
    # WiX-bundle extraction (MSI burn bundles): needs dark.exe, itself a
    # normal Scoop package (`goop install dark`), resolved via PATH.
    $dark = Get-Command 'dark.exe' -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $dark) {
        abort "dark.exe not found; install it first with ``goop install dark`` (CPT-05/EXT-06)."
    }
    $dest = $DestinationPath.TrimEnd('\')
    New-Item -ItemType Directory -Force -Path $dest | Out-Null
    $argList = @('-nologo', '-x', $dest, $Path)
    if ($Switches) { $argList += $Switches }
    $ok = Invoke-ExternalCommand -FilePath $dark.Source -ArgumentList $argList
    if (-not $ok) { abort "dark extraction of '$Path' failed." }

    # dark.exe's newer behavior extracts a burn bundle's attached-container
    # payloads under anonymous names (a0, a1, a2, ...) instead of their
    # real filenames -- confirmed against a real WiX 5 bundle (PowerToys),
    # where AttachedContainer\a2 was the actual MSI, unusable under that
    # name by a manifest script expecting to find it by its real filename
    # (e.g. "PowerToysUserSetup*.msi"). UX\manifest.xml's own burn
    # manifest records each payload's real FilePath keyed by its anonymous
    # SourcePath, so restore it the same way real Scoop's own
    # Expand-DarkArchive does (lib/decompress.ps1) -- older dark.exe
    # versions instead name the directory itself "WixAttachedContainer",
    # handled by the simpler rename in that branch.
    if (Test-Path "$dest\WixAttachedContainer") {
        Rename-Item "$dest\WixAttachedContainer" 'AttachedContainer' -ErrorAction Ignore
    } elseif (Test-Path "$dest\AttachedContainer\a0") {
        $xml = [xml](Get-Content -Raw "$dest\UX\manifest.xml" -Encoding utf8)
        $xml.BurnManifest.UX.Payload | ForEach-Object {
            Rename-Item "$dest\UX\$($_.SourcePath)" $_.FilePath -ErrorAction Ignore
        }
        $xml.BurnManifest.Payload | ForEach-Object {
            Rename-Item "$dest\AttachedContainer\$($_.SourcePath)" $_.FilePath -ErrorAction Ignore
        }
    }

    if ($Removal) { Remove-Item $Path -Force }
}

# Hooks run against the staging directory, before the commit rename
# (TR-04), so any path handed to us still says "<version>.partial" --
# which stops existing the moment the install finishes. Anything we
# *persist* (a shim sidecar, a .lnk) must record the committed path
# instead, or it points into the void: seen for real as a blank-icon
# FreeCAD shortcut and a FreeCADCmd shim pointing at nothing.
function goop_final_path($p) {
    if (-not $p) { return $p }
    return $p -replace '\.partial(?=\\|/|$)', ''
}
function shim($path, $global, $name, $arg) {
    if (!(Test-Path $path)) { abort "Can't shim '$path': couldn't find it." }
    if (!$name) { $name = [System.IO.Path]::GetFileNameWithoutExtension($path) }
    $shimExe = Join-Path $goop_shims_dir "$name.exe"
    $shimFile = Join-Path $goop_shims_dir "$name.shim"
    Copy-Item $goop_shim_master $shimExe -Force
    $resolved = goop_final_path (Resolve-Path $path).Path
    # [IO.File]::WriteAllText with a BOM-less encoder, not Set-Content
    # -Encoding utf8: PowerShell 5.1's utf8 writes a BOM, and the shim
    # binary's sidecar parser reads the first key literally -- a BOM
    # turned every script-created shim into "missing required path key".
    $enc = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllText($shimFile, "path = `"$resolved`"`n", $enc)
    if ($arg) { [System.IO.File]::AppendAllText($shimFile, "args = $arg`n", $enc) }
    # $goop_shim_log (if set) is a per-install scratch file the Go side
    # reads back after the hooks finish, so it knows exactly which shims
    # *this* script created (ExtraShims) -- installs run concurrently
    # (A1), so paths.Shims() is a directory shared by every app
    # installing at once; recording precisely here instead of diffing
    # that shared directory before/after is what keeps one app's
    # imperative shims from being misattributed to whichever other app
    # happens to be creating its own declarative shims in the same
    # window.
    # "<nom>`t<cible>" : le nom seul suffisait a la desinstallation, mais pas
    # a reconstruire le shim (goop reset) sans reexecuter le script.
    if ($goop_shim_log) { [System.IO.File]::AppendAllText($goop_shim_log, "$name`t$resolved`n", (New-Object System.Text.UTF8Encoding $false)) }
}

# HKCU\Environment accessors + the Add-Path/Remove-Path pair real
# manifests call directly (main/go.json's installer.script does, to put
# %USERPROFILE%\goin on PATH). $Global is accepted for
# call-signature compatibility but always treated as per-user: goop
# never writes HKLM (NR-01).
function Get-EnvVar($Name, [switch]$Global) {
    [Environment]::GetEnvironmentVariable($Name, 'User')
}

function Set-EnvVar($Name, $Value, [switch]$Global) {
    [Environment]::SetEnvironmentVariable($Name, $Value, 'User')
    Set-Item -Path "Env:$Name" -Value $Value -ErrorAction SilentlyContinue
}

function Add-Path {
    param(
        [string[]]$Path,
        [string]$TargetEnvVar = 'PATH',
        [switch]$Global,
        [switch]$Force,
        [switch]$Quiet
    )
    $current = Get-EnvVar -Name $TargetEnvVar
    $parts = @($current -split ';' | Where-Object { $_ })
    $added = @()
    foreach ($p in $Path) {
        $trimmed = $p.TrimEnd('')
        if ($Force -or -not ($parts | Where-Object { $_.TrimEnd('') -ieq $trimmed })) {
            if (!$Quiet) { Write-Host "Adding $(friendly_path $p) to your path." }
            $parts = @($p) + $parts
            $added += $p
        }
    }
    if ($added) { Set-EnvVar -Name $TargetEnvVar -Value ($parts -join ';') }
}

function Remove-Path {
    param(
        [string[]]$Path,
        [string]$TargetEnvVar = 'PATH',
        [switch]$Global,
        [switch]$Quiet,
        [switch]$PassThru
    )
    $current = Get-EnvVar -Name $TargetEnvVar
    $parts = @($current -split ';' | Where-Object { $_ })
    $kept = @()
    $removed = $false
    foreach ($e in $parts) {
        $match = $false
        foreach ($p in $Path) {
            if ($e.TrimEnd('') -ieq $p.TrimEnd('')) { $match = $true; break }
        }
        if ($match) {
            $removed = $true
            if (!$Quiet) { Write-Host "Removing $(friendly_path $e) from your path." }
        } else { $kept += $e }
    }
    if ($removed) { Set-EnvVar -Name $TargetEnvVar -Value ($kept -join ';') }
}

function unshim($name) {
    Remove-Item (Join-Path $goop_shims_dir "$name.exe") -Force -ErrorAction SilentlyContinue
    Remove-Item (Join-Path $goop_shims_dir "$name.shim") -Force -ErrorAction SilentlyContinue
}

# Real manifests' own uninstaller scripts sometimes call these directly
# (looping over what they shimmed, mirroring how they looped to shim it
# in the first place) instead of relying on goop's own Record-driven
# cleanup. $shimdir/$global are accepted for call-signature
# compatibility but effectively ignored -- goop has exactly one shims
# directory, not Scoop's separate global/per-user ones.
function rm_shim($name, $shimdir) { unshim $name }
function shimdir($global) { $goop_shims_dir }

# Some manifests create their own Start Menu shortcut from a script
# (rather than -- or in addition to -- the manifest-level `shortcuts`
# field goop's own createShortcuts already handles) by calling this
# directly. $global is accepted for call-signature compatibility but
# deliberately ignored -- goop never writes shortcuts machine-wide
# (ProgramData), same principle as env vars only ever going to
# HKCU\Environment, never HKLM (NR-01).
# Where goop puts Start Menu shortcuts. Real manifests call this from
# their uninstaller scripts to find and delete what they created (real
# Scoop's lib/shortcuts.ps1 returns its own "Scoop Apps" folder the same
# way). goop namespaces under "goop" instead, so uninstall only ever
# touches shortcuts it owns.
function shortcut_folder($global) {
    return ensure ([System.IO.Path]::Combine([Environment]::GetFolderPath('StartMenu'), 'Programs', 'goop'))
}
function startmenu_shortcut($targetPath, $shortcutName, $shortcutArguments, $icon, $global) {
    $linkPath = Join-Path "$env:APPDATA\Microsoft\Windows\Start Menu\Programs\goop" "$shortcutName.lnk"
    $linkDir = Split-Path $linkPath -Parent
    if (!(Test-Path $linkDir)) { New-Item -Path $linkDir -ItemType Directory -Force | Out-Null }

    $wsh = New-Object -ComObject WScript.Shell
    # Hooks run against the staging directory, before the commit
    # rename (TR-04), so a path a script hands us still says
    # "<version>.partial" -- which stops existing the moment the install
    # finishes. A .lnk pointing there shows a blank icon and launches
    # nothing (seen for real with extras/freecad.json). Rewrite it to
    # the committed path the file will actually live at.
    $targetPath = goop_final_path $targetPath
    $lnk = $wsh.CreateShortcut($linkPath)
    $lnk.TargetPath = $targetPath
    if ($shortcutArguments) { $lnk.Arguments = $shortcutArguments }
    if ($icon) { $lnk.IconLocation = $icon }
    $lnk.WorkingDirectory = Split-Path $targetPath -Parent
    $lnk.Save()

    # Same bookkeeping as the shim polyfill: without it a script-created
    # shortcut is invisible to goop, so uninstall leaves it behind
    # pointing at a deleted directory (NR-02) and reset can't rebuild it.
    # "<name>`t<target>`t<args>`t<icon>".
    if ($goop_shortcut_log) {
        [System.IO.File]::AppendAllText($goop_shortcut_log,
            "$shortcutName`t$targetPath`t$shortcutArguments`t$icon`n",
            (New-Object System.Text.UTF8Encoding $false))
    }
}
