# Copyright Monte Carlo AI, Inc.
# SPDX-License-Identifier: Apache-2.0
#
# Installs the montecarlo CLI on Windows from a GitHub release of monte-carlo-data/mc-cli:
# downloads the archive for this machine, checks it against the release's checksums, installs
# the binary and adds its directory to the user PATH. Works in Windows PowerShell 5.1 and
# PowerShell 7.
#
#   MONTECARLO_VERSION        the release to install, 0.1.3 or v0.1.3; the latest by default
#   MONTECARLO_INSTALL_DIR    where to install; %LOCALAPPDATA%\Programs\montecarlo by default
#   MONTECARLO_BIN_NAME       the installed name, montecarlo by default
#   MONTECARLO_FORCE=1        replace a montecarlo at the target that is not this CLI
#   MONTECARLO_DOWNLOAD_BASE  where releases are served from, for a mirror; the GitHub
#                             releases page by default
#
# With gh installed and logged in, the archive's provenance is verified too.
#
# Everything runs from Install-MonteCarlo, called on the last line, so a download cut short runs
# nothing. Errors are thrown, never exit, so a session running it through Invoke-Expression
# stays open.

function Install-MonteCarlo {
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingWriteHost', '', Justification = 'An installer talks to the person running it.')]
    param()
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    $repo = 'monte-carlo-data/mc-cli'

    function Say([string]$message) { Write-Host "install.ps1: $message" }
    function Fail([string]$message) { throw "install.ps1: $message" }

    # Follows redirects by hand, the same way in both PowerShell versions, so that every hop can
    # be held to HTTPS. Returns the final URL, and saves the body to $OutFile when one is given.
    function Get-Url([string]$Url, [string]$OutFile, [switch]$Head) {
        for ($hop = 0; $hop -lt 10; $hop++) {
            if ($strict -and -not $Url.StartsWith('https://')) { Fail "refused to follow $Url, which is not HTTPS" }
            $request = [System.Net.HttpWebRequest]::Create($Url)
            $request.AllowAutoRedirect = $false
            $request.UserAgent = 'montecarlo-install.ps1'
            if ($Head) { $request.Method = 'HEAD' }
            try {
                $response = $request.GetResponse()
            } catch {
                # A status of 400 or more arrives as a WebException, which PowerShell may wrap.
                $e = $_.Exception
                while ($e -and -not ($e -is [System.Net.WebException])) { $e = $e.InnerException }
                if ($null -eq $e -or $null -eq $e.Response) { throw }
                $response = $e.Response
            }
            try {
                $status = [int]$response.StatusCode
                if ($status -ge 300 -and $status -lt 400) {
                    $Url = [Uri]::new([Uri]$Url, $response.Headers['Location']).AbsoluteUri
                    continue
                }
                if ($status -ne 200) { return $null }
                if ($OutFile) {
                    $file = [System.IO.File]::Create($OutFile)
                    try { $response.GetResponseStream().CopyTo($file) } finally { $file.Dispose() }
                }
                return $Url
            } finally {
                $response.Dispose()
            }
        }
        Fail "too many redirects from $Url"
    }

    # This CLI's binary records its module path in its build info. Reading it, not running the
    # file, guards against replacing another program by accident; it is not a tamper check.
    function Test-OurBinary([string]$Path) {
        $text = [System.Text.Encoding]::ASCII.GetString([System.IO.File]::ReadAllBytes($Path))
        return $text.Contains("github.com/$repo")
    }

    if ($PSVersionTable.PSVersion.Major -ge 6 -and -not $IsWindows) { Fail 'this installs on Windows; on macOS or Linux, use install.sh' }
    [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor [System.Net.SecurityProtocolType]::Tls12

    $base = $env:MONTECARLO_DOWNLOAD_BASE
    if (-not $base) { $base = "https://github.com/$repo/releases" }
    # HTTPS on every hop, unless a mirror outside HTTPS was asked for by name.
    $strict = $base.StartsWith('https://')

    $name = $env:MONTECARLO_BIN_NAME
    if (-not $name) { $name = 'montecarlo' }
    if ($name.EndsWith('.exe')) { $name = $name.Substring(0, $name.Length - 4) }
    if ($name -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]*$') { Fail "MONTECARLO_BIN_NAME must be a plain file name, got '$name'" }

    # The OS's architecture, not this process's, which is x64 under emulation on Arm. Windows
    # PowerShell may lack RuntimeInformation or read its OSArchitecture as null, so it is read
    # as a string and an empty one falls back.
    $os = $null
    $runtime = 'System.Runtime.InteropServices.RuntimeInformation' -as [type]
    if ($runtime) { $os = "$($runtime::OSArchitecture)" }
    if (-not $os) { $os = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE } }
    switch ($os) {
        { $_ -in 'X64', 'AMD64' } { $arch = 'amd64' }
        'Arm64' { $arch = 'arm64' }
        default { Fail "unsupported architecture $os" }
    }

    if ($env:MONTECARLO_VERSION) {
        $tag = 'v' + $env:MONTECARLO_VERSION.TrimStart('v')
    } else {
        # Before a first release, GitHub redirects latest to the releases page instead of a tag.
        $location = Get-Url "$base/latest" -Head
        if (-not $location -or $location -notmatch '/tag/([^/]+)$') { Fail "found no release at $base/latest" }
        $tag = $Matches[1]
    }
    if ($tag -cnotmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') { Fail "'$tag' is not a release version like v0.1.3" }
    $version = $tag.Substring(1)

    $archive = "montecarlo_${version}_windows_$arch.zip"
    $sums = "montecarlo_${version}_checksums.txt"
    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Say "downloading $archive ($tag)"
        if (-not (Get-Url "$base/download/$tag/$archive" (Join-Path $tmp $archive))) { Fail "could not download $base/download/$tag/$archive" }
        if (-not (Get-Url "$base/download/$tag/$sums" (Join-Path $tmp $sums))) { Fail "could not download $base/download/$tag/$sums" }

        $want = $null
        foreach ($line in [System.IO.File]::ReadAllLines((Join-Path $tmp $sums))) {
            if ($line -cmatch '^([0-9a-f]{64})  (.+)$' -and $Matches[2] -ceq $archive) { $want = $Matches[1] }
        }
        if (-not $want) { Fail "$sums has no checksum for $archive" }
        $got = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmp $archive)).Hash.ToLowerInvariant()
        if ($got -cne $want) { Fail "$archive does not match its checksum: got $got, want $want" }

        # Under Stop, Windows PowerShell 5.1 turns a native command's stderr into an error, so gh
        # runs under Continue and only its exit code decides.
        $loggedIn = $false
        if (Get-Command gh -ErrorAction SilentlyContinue) {
            & { $ErrorActionPreference = 'Continue'; & gh auth status *> $null }
            $loggedIn = $LASTEXITCODE -eq 0
        }
        if ($loggedIn) {
            # gh before 2.49 has no attestation command, which is not a verdict on the release.
            $help = & { $ErrorActionPreference = 'Continue'; & gh attestation verify --help *>&1 | Out-String }
            if ($help -notlike '*--source-ref*') {
                Say 'gh is too old to verify the provenance (it needs gh 2.49 or later); checked the checksum only'
            } else {
                & {
                    $ErrorActionPreference = 'Continue'
                    & gh attestation verify (Join-Path $tmp $archive) --repo $repo `
                        --signer-workflow "$repo/.github/workflows/ci.yml" --source-ref refs/heads/main `
                        --deny-self-hosted-runners *> $null
                }
                if ($LASTEXITCODE -ne 0) { Fail "$archive has no provenance from $repo's release workflow on main" }
                Say "verified the provenance of $archive"
            }
        } else {
            Say 'checked the checksum; with gh installed and logged in, the provenance is verified too'
        }

        Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath (Join-Path $tmp 'x')

        $dir = $env:MONTECARLO_INSTALL_DIR
        if (-not $dir) { $dir = Join-Path $env:LOCALAPPDATA 'Programs\montecarlo' }
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        $dir = (Resolve-Path $dir).Path
        $target = Join-Path $dir "$name.exe"

        if ((Test-Path $target) -and -not (Test-OurBinary $target) -and $env:MONTECARLO_FORCE -ne '1') {
            Fail "$target is another program, not this CLI, so it was left alone. Install under another name with MONTECARLO_BIN_NAME, for example `$env:MONTECARLO_BIN_NAME = 'mc', or replace it with `$env:MONTECARLO_FORCE = '1'."
        }

        # Written beside the target and renamed over it, so the target is never half-written.
        $staged = Join-Path $dir ".$name.$PID.exe"
        Copy-Item (Join-Path $tmp 'x\montecarlo.exe') $staged
        Unblock-File $staged
        Move-Item -Force $staged $target
    } finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }
    Say "installed $(& $target version --output table) at $target"

    # Appended once to the user PATH's registry value, with its %VAR% entries unexpanded, never
    # rebuilt from this session's PATH.
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
    try {
        $userPath = $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $entries = @($userPath.Split(';') | Where-Object { $_ })
        $listed = $entries | Where-Object { [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\') -ieq $dir.TrimEnd('\') }
        if (-not $listed) {
            $key.SetValue('Path', (($entries + $dir) -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString)
            # Setting and clearing a user variable broadcasts the change to running programs.
            [Environment]::SetEnvironmentVariable('MONTECARLO_INSTALL_PATH_REFRESH', '1', 'User')
            [Environment]::SetEnvironmentVariable('MONTECARLO_INSTALL_PATH_REFRESH', $null, 'User')
            $env:Path = "$env:Path;$dir"
            Say "added $dir to your user PATH; open a new terminal to pick it up"
        }
    } finally {
        $key.Dispose()
    }

    $first = $null
    foreach ($d in $env:Path.Split(';')) {
        if (-not $d) { continue }
        $candidate = Join-Path $d "$name.exe"
        if (-not (Test-Path $candidate -PathType Leaf)) { continue }
        if (-not $first) { $first = $candidate }
        if ($candidate -ine $target -and -not (Test-OurBinary $candidate)) {
            Say "warning: $candidate is another program with the same name; whichever comes first on PATH runs."
            Say '  Install under another name with MONTECARLO_BIN_NAME to keep both.'
        }
    }
    if ($first -and $first -ine $target) { Say "warning: $first comes before $target on PATH, so `"$name`" runs that one" }
}

Install-MonteCarlo
