# Copyright Monte Carlo AI, Inc.
# SPDX-License-Identifier: Apache-2.0
#
# Tests install.ps1 against a snapshot dist/ served by mirror.py, laid out like GitHub releases,
# running the installer in -Shell: powershell (Windows PowerShell 5.1) or pwsh (PowerShell 7).
# Needs Windows: install.ps1 refuses to run elsewhere. Against a copy without that guard (-Installer), the cases that don't run the binary or touch PATH also run on macOS and Linux.

param(
    [Parameter(Mandatory)] [string]$Dist,
    [Parameter(Mandatory)] [string]$Tag,
    [string]$Shell = 'pwsh',
    [string]$Installer = (Join-Path $PSScriptRoot '../../install.ps1')
)

$ErrorActionPreference = 'Stop'
$onWindows = [System.Environment]::OSVersion.Platform -eq 'Win32NT'
$Dist = (Resolve-Path $Dist).Path
$Installer = (Resolve-Path $Installer).Path
$root = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $root | Out-Null
$version = $Tag.TrimStart('v')
$arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') { 'arm64' } else { 'amd64' }
$archive = "montecarlo_${version}_windows_$arch.zip"
$sums = "montecarlo_${version}_checksums.txt"
$failures = 0
$mirrors = @()
# On Windows, python3 can be the Microsoft Store's stand-in, so python comes first there.
$candidates = if ($onWindows) { 'python', 'python3' } else { 'python3', 'python' }
$python = (Get-Command $candidates -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1).Source
Get-ChildItem env: | Where-Object Name -like 'MONTECARLO_*' | ForEach-Object { Remove-Item "env:$($_.Name)" }

# Starts a mirror of $Directory and returns its base URL.
function Start-Mirror([string]$Directory, [switch]$NoRelease) {
    $out = Join-Path $root "mirror-$([System.Guid]::NewGuid()).txt"
    $arguments = @((Join-Path $PSScriptRoot 'mirror.py'), '--dist', $Directory, '--tag', $Tag)
    if ($NoRelease) { $arguments += '--no-release' }
    $script:mirrors += Start-Process $python -ArgumentList $arguments -RedirectStandardOutput $out -PassThru -NoNewWindow
    for ($i = 0; $i -lt 300; $i++) {
        if ((Test-Path $out -PathType Leaf) -and (Get-Content $out -Raw)) { break }
        Start-Sleep -Milliseconds 100
    }
    # An empty base would send the installer to the real releases.
    $url = Get-Content $out -Raw -ErrorAction SilentlyContinue
    if (-not $url) { throw 'mirror.py did not start' }
    return $url.Trim()
}

# A gh that is installed but not logged in, unless a case says otherwise, so a developer's own
# gh never verifies a snapshot that has no attestation.
$stubs = Join-Path $root 'stubs'
New-Item -ItemType Directory -Path $stubs | Out-Null
# Every call writes to stderr, as the real gh does. The stub answers the --source-ref probe like a
# gh that can verify, or with -Old like one that has no attestation command. The arguments of a
# real verify call are recorded in $ghArgs.
$ghArgs = Join-Path $root 'gh-args.txt'
function Set-Gh([int]$AuthStatus, [int]$Verify, [switch]$Old) {
    if ($onWindows) {
        $lines = @(
            '@echo off',
            'echo some progress 1>&2',
            'if "%1"=="auth" exit /b @AUTH@',
            'if "%1"=="attestation" if "@OLD@"=="1" echo unknown command "attestation" 1>&2 & exit /b 1',
            'if "%3"=="--help" echo   --source-ref string & exit /b 0',
            'if "%1"=="attestation" echo %* > "@ARGS@"',
            'exit /b @VERIFY@'
        )
        $path = Join-Path $stubs 'gh.cmd'
        $eol = "`r`n"
    } else {
        $lines = @(
            '#!/bin/sh',
            'echo some progress >&2',
            '[ "$1" = auth ] && exit @AUTH@',
            '[ "$1" = attestation ] && [ "@OLD@" = 1 ] && { echo ''unknown command "attestation"'' >&2; exit 1; }',
            '[ "$3" = --help ] && { echo ''  --source-ref string''; exit 0; }',
            '[ "$1" = attestation ] && printf ''%s\n'' "$@" > "@ARGS@"',
            'exit @VERIFY@'
        )
        $path = Join-Path $stubs 'gh'
        $eol = "`n"
    }
    $text = (($lines -join $eol) + $eol).Replace('@AUTH@', "$AuthStatus").Replace('@VERIFY@', "$Verify").Replace('@ARGS@', $ghArgs).Replace('@OLD@', $(if ($Old) { '1' } else { '0' }))
    [System.IO.File]::WriteAllText($path, $text)
    if (-not $onWindows) { chmod +x $path }
}
Set-Gh -AuthStatus 1 -Verify 1

$base = Start-Mirror $Dist
$sep = [System.IO.Path]::PathSeparator

# Runs the installer in a fresh directory with the variables given.
function Invoke-Installer([string]$Name, [hashtable]$Vars = @{}, [switch]$Piped) {
    $bin = Join-Path (Join-Path $root $Name) 'bin'
    New-Item -ItemType Directory -Force -Path $bin | Out-Null
    $all = @{ MONTECARLO_DOWNLOAD_BASE = $base; MONTECARLO_INSTALL_DIR = $bin; PATH = "$stubs$sep$env:PATH" }
    foreach ($k in $Vars.Keys) { $all[$k] = $Vars[$k] }
    $saved = @{}
    foreach ($k in $all.Keys) {
        $saved[$k] = [Environment]::GetEnvironmentVariable($k)
        [Environment]::SetEnvironmentVariable($k, $all[$k])
    }
    try {
        if ($Piped) {
            $out = & $Shell -NoProfile -Command "Get-Content -Raw '$Installer' | Invoke-Expression" 2>&1 | Out-String
        } else {
            $out = & $Shell -NoProfile -ExecutionPolicy Bypass -File $Installer 2>&1 | Out-String
        }
        $code = $LASTEXITCODE
    } finally {
        foreach ($k in $saved.Keys) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) }
    }
    # PowerShell 7 wraps an error at the console width, behind a | gutter, so matching is done on
    # the output flattened onto one line.
    $flat = $out -replace '\s*\r?\n\s*(\|\s*)?', ' '
    return [pscustomobject]@{ Code = $code; Out = $out; Flat = $flat; Bin = $bin }
}

function Test-Case([string]$Description, [object]$Result, [bool]$Passed) {
    if ($Passed) {
        Write-Output "ok   $Description"
    } else {
        Write-Output "FAIL $Description"
        Write-Output (($Result.Out -split "`n" | ForEach-Object { "       $_" }) -join "`n")
        $script:failures++
    }
}

# The installed binary's version; off Windows (a guard-free -Installer copy), only that it is there.
function Test-Installed([object]$Result, [string]$Name = 'montecarlo') {
    $exe = Join-Path $Result.Bin "$Name.exe"
    if (-not (Test-Path $exe)) { return $false }
    if (-not $onWindows) { return $true }
    return ((& $exe version --output json | ConvertFrom-Json).version -eq $Tag)
}

function Open-UserEnvironment { [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true) }

# The user PATH as found, raw and with its type, to put back at the end.
if ($onWindows) {
    $key = Open-UserEnvironment
    $origPath = $key.GetValue('Path', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    if ($null -ne $origPath) { $origKind = $key.GetValueKind('Path') }
    $key.Dispose()
}

function New-Foreign([string]$Path) {
    New-Item -ItemType Directory -Force -Path (Split-Path $Path) | Out-Null
    Set-Content $Path 'another montecarlo'
}

try {
    $r = Invoke-Installer latest
    Test-Case 'the latest release is found through the redirect' $r ($r.Code -eq 0 -and (Test-Installed $r))

    $r = Invoke-Installer pinned @{ MONTECARLO_VERSION = $Tag }
    Test-Case 'a pinned version installs' $r ($r.Code -eq 0 -and (Test-Installed $r))

    $r = Invoke-Installer pinned-no-v @{ MONTECARLO_VERSION = $version }
    Test-Case 'a pinned version without the v installs' $r ($r.Code -eq 0 -and (Test-Installed $r))

    $r = Invoke-Installer piped -Piped
    Test-Case 'the script runs through Invoke-Expression' $r ($r.Code -eq 0 -and (Test-Installed $r))

    foreach ($bad in '1.2', 'v0.1', 'v0.1.0;true', 'v0.1.0-rc1') {
        $r = Invoke-Installer "bad-version-$($bad -replace '[^a-z0-9]', '_')" @{ MONTECARLO_VERSION = $bad }
        Test-Case "version '$bad' is refused" $r ($r.Code -ne 0 -and $r.Flat -like '*is not a release version*')
    }

    $r = Invoke-Installer bad-name @{ MONTECARLO_BIN_NAME = '..\mc' }
    Test-Case 'a bin name with a path is refused' $r ($r.Code -ne 0 -and $r.Flat -like '*plain file name*')

    $r = Invoke-Installer no-release @{ MONTECARLO_DOWNLOAD_BASE = (Start-Mirror $Dist -NoRelease) }
    Test-Case 'no release yet is said so' $r ($r.Code -ne 0 -and $r.Flat -like '*found no release*')

    $tampered = Join-Path $root 'tampered-dist'
    Copy-Item -Recurse $Dist $tampered
    Add-Content -Path (Join-Path $tampered $archive) -Value 'x' -NoNewline
    $r = Invoke-Installer tampered @{ MONTECARLO_DOWNLOAD_BASE = (Start-Mirror $tampered) }
    Test-Case 'a tampered archive is refused' $r ($r.Code -ne 0 -and $r.Flat -like '*does not match its checksum*' -and -not (Test-Path (Join-Path $r.Bin 'montecarlo.exe')))

    $unlisted = Join-Path $root 'unlisted-dist'
    Copy-Item -Recurse $Dist $unlisted
    Get-Content (Join-Path $Dist $sums) | Where-Object { $_ -notlike "*  $archive" } | Set-Content (Join-Path $unlisted $sums)
    $r = Invoke-Installer unlisted @{ MONTECARLO_DOWNLOAD_BASE = (Start-Mirror $unlisted) }
    Test-Case 'an archive missing from the checksums is refused' $r ($r.Code -ne 0 -and $r.Flat -like '*has no checksum*')

    $null = Invoke-Installer upgrade
    $r = Invoke-Installer upgrade
    Test-Case 'a second install replaces our own without force' $r ($r.Code -eq 0 -and (Test-Installed $r))

    $foreign = Join-Path (Join-Path (Join-Path $root 'foreign') 'bin') 'montecarlo.exe'
    New-Foreign $foreign
    $r = Invoke-Installer foreign
    Test-Case 'another program at the target is left alone' $r ($r.Code -ne 0 -and $r.Flat -like '*is another program*' -and (Get-Content $foreign -Raw) -like 'another montecarlo*')

    $r = Invoke-Installer foreign @{ MONTECARLO_BIN_NAME = 'mc' }
    Test-Case 'MONTECARLO_BIN_NAME installs beside it' $r ($r.Code -eq 0 -and (Test-Installed $r 'mc') -and (Get-Content $foreign -Raw) -like 'another montecarlo*')

    $r = Invoke-Installer foreign @{ MONTECARLO_FORCE = '1' }
    Test-Case 'MONTECARLO_FORCE replaces it' $r ($r.Code -eq 0 -and (Test-Installed $r))

    Set-Gh -AuthStatus 0 -Verify 1
    $r = Invoke-Installer gh-rejects
    Test-Case 'a provenance gh rejects is refused' $r ($r.Code -ne 0 -and $r.Flat -like '*has no provenance*' -and -not (Test-Path (Join-Path $r.Bin 'montecarlo.exe')))
    Set-Gh -AuthStatus 0 -Verify 1 -Old
    $r = Invoke-Installer gh-old
    Test-Case 'a gh too old to verify provenance installs on the checksum alone' $r ($r.Code -eq 0 -and (Test-Installed $r) -and $r.Flat -like '*too old*')
    Set-Gh -AuthStatus 0 -Verify 0
    $r = Invoke-Installer gh-accepts
    $recorded = Get-Content $ghArgs -Raw -ErrorAction SilentlyContinue
    $pins = '--repo\s+monte-carlo-data/mc-cli', '--signer-workflow\s+monte-carlo-data/mc-cli/\.github/workflows/ci\.yml',
        '--source-ref\s+refs/heads/main', '--deny-self-hosted-runners', [regex]::Escape($archive)
    $pinned = [bool]$recorded -and @($pins | Where-Object { $recorded -notmatch $_ }).Count -eq 0
    Test-Case 'a provenance gh accepts is reported' $r ($r.Code -eq 0 -and $r.Flat -like '*verified the provenance*' -and $pinned)
    Set-Gh -AuthStatus 1 -Verify 1

    if ($onWindows) {
        $shadow = Join-Path $root 'shadow'
        New-Foreign (Join-Path $shadow 'montecarlo.exe')
        $r = Invoke-Installer shadowed @{ PATH = "$stubs;$shadow;$env:PATH" }
        Test-Case 'another montecarlo earlier on PATH is warned about' $r ($r.Code -eq 0 -and $r.Flat -like "*$shadow\montecarlo.exe is another program*" -and $r.Flat -like '*comes before*')

        $keep = '%USERPROFILE%\montecarlo-test-keep'
        $key = Open-UserEnvironment
        $raw = $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $key.SetValue('Path', ((@($raw.Split(';') | Where-Object { $_ }) + $keep) -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString)
        $key.Dispose()

        $r = Invoke-Installer path-once
        $r = Invoke-Installer path-once
        $entries = @([Environment]::GetEnvironmentVariable('Path', 'User').Split(';') | Where-Object { $_.TrimEnd('\') -ieq $r.Bin.TrimEnd('\') })
        Test-Case 'the install dir is added to the user PATH once' $r ($r.Code -eq 0 -and $entries.Count -eq 1)

        $key = Open-UserEnvironment
        $raw = $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $kind = $key.GetValueKind('Path')
        $key.Dispose()
        Test-Case 'the user PATH keeps its %VAR% entries and its type' $r ($raw.Contains($keep) -and $kind -eq [Microsoft.Win32.RegistryValueKind]::ExpandString)
    } else {
        Write-Output 'skip the PATH cases: not Windows'
    }
} finally {
    $mirrors | ForEach-Object { Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue }
    if ($onWindows) {
        # Leave the user PATH as it was found, with its value and type.
        $key = Open-UserEnvironment
        if ($null -eq $origPath) { $key.DeleteValue('Path', $false) } else { $key.SetValue('Path', $origPath, $origKind) }
        $key.Dispose()
    }
    Remove-Item -Recurse -Force $root -ErrorAction SilentlyContinue
}

exit [int]($failures -gt 0)
