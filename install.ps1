# Cortex installer for Windows (PowerShell 5.1 or 7).
#
#   irm https://raw.githubusercontent.com/marioolf/cortex/main/install.ps1 | iex
#
# Downloads the release zip for this machine, checks its SHA-256 against
# checksums.txt, and runs `cortex.exe install` from the unzipped copy. That
# command copies the program to %LOCALAPPDATA%\Programs\Cortex, adds a Start
# Menu shortcut, registers Cortex to start at login, and starts the installed
# copy (its control panel opens in the browser). If a Cortex is already
# running from another file, the new one asks it to quit and takes its place.
#
# Environment:
#   $env:CORTEX_VERSION        a tag such as v0.1.0 (default: the latest release)
#   $env:CORTEX_DOWNLOAD_BASE  (tests) replaces https://github.com/marioolf/cortex/releases/download;
#                              CORTEX_VERSION must then be set
#   $env:CORTEX_DOWNLOAD_ONLY  (tests) '1' stops after verify + extract and prints the path
#
# Note: a file downloaded by PowerShell carries no Mark-of-the-Web, so
# SmartScreen does not prompt for it. The binaries are still NOT code-signed;
# the SHA-256 check only proves the download matches the release's checksums.txt.

function Install-Cortex {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'   # the progress bar makes Invoke-WebRequest very slow on 5.1
    try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }

    $repo = 'https://github.com/marioolf/cortex'
    $base = if ($env:CORTEX_DOWNLOAD_BASE) { $env:CORTEX_DOWNLOAD_BASE.TrimEnd('/') } else { "$repo/releases/download" }

    # PROCESSOR_ARCHITEW6432 is set for a 32-bit process on a 64-bit system.
    $archName = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    switch ($archName) {
        'AMD64' { $arch = 'amd64' }
        'ARM64' { $arch = 'arm64' }
        default { throw "Unsupported processor '$archName': Cortex is built for AMD64 and ARM64." }
    }

    $tag = $env:CORTEX_VERSION
    if (-not $tag) {
        if ($env:CORTEX_DOWNLOAD_BASE) { throw 'CORTEX_VERSION must be set when CORTEX_DOWNLOAD_BASE is.' }
        Write-Host 'Looking up the latest release...'
        # /releases/latest redirects to /releases/tag/<tag>; reading the redirect
        # avoids the GitHub API and its rate limit. Windows PowerShell 5.1 throws
        # on the 302 with "maximum redirection exceeded" but still returns the
        # response, so the error is silenced and the Location header read from it;
        # PowerShell 7 returns it with -SkipHttpErrorCheck. The catch is a fallback.
        $loc = $null
        try {
            if ($PSVersionTable.PSVersion.Major -ge 7) {
                $r = Invoke-WebRequest -Uri "$repo/releases/latest" -MaximumRedirection 0 -SkipHttpErrorCheck -UseBasicParsing -ErrorAction SilentlyContinue
            } else {
                $r = Invoke-WebRequest -Uri "$repo/releases/latest" -MaximumRedirection 0 -UseBasicParsing -ErrorAction SilentlyContinue
            }
            $loc = $r.Headers['Location']
        } catch {
            $resp = $_.Exception.Response
            if ($resp) {
                if ($resp.Headers.Location) { $loc = $resp.Headers.Location }   # PS 7: HttpResponseHeaders
                else { $loc = $resp.Headers['Location'] }                       # PS 5.1: WebHeaderCollection
            }
        }
        if ($loc -is [array]) { $loc = $loc[0] }
        $loc = "$loc"
        if ($loc -notmatch '/tag/([^/?#]+)$') {
            throw "Could not find the latest release (is there one yet? see $repo/releases). Set `$env:CORTEX_VERSION = 'v0.1.0' to pick one."
        }
        $tag = $Matches[1]
    }
    if ($tag -notmatch '^v') { $tag = "v$tag" }
    $version = $tag.Substring(1)
    if ($version -notmatch '^[A-Za-z0-9._+-]+$') { throw "Invalid version '$tag'." }

    $asset = "cortex_${version}_windows_${arch}.zip"
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('cortex-install-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    $keep = $false
    try {
        Write-Host "Downloading Cortex $version for windows/$arch..."
        $zip = Join-Path $tmp $asset
        $sums = Join-Path $tmp 'checksums.txt'
        try { Invoke-WebRequest -Uri "$base/$tag/$asset" -OutFile $zip -UseBasicParsing }
        catch { throw "Could not download $base/$tag/$asset (does release $tag exist?): $($_.Exception.Message)" }
        try { Invoke-WebRequest -Uri "$base/$tag/checksums.txt" -OutFile $sums -UseBasicParsing }
        catch { throw "Could not download $base/$tag/checksums.txt: $($_.Exception.Message)" }

        $want = $null
        foreach ($line in Get-Content -LiteralPath $sums) {
            if ($line -match '^([0-9A-Fa-f]{64})\s+\*?(.+?)\s*$' -and $Matches[2] -eq $asset) { $want = $Matches[1].ToLower(); break }
        }
        if (-not $want) { throw "$asset is not listed in checksums.txt." }
        $got = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLower()
        if ($got -ne $want) { throw "Checksum mismatch for $asset (expected $want, got $got): nothing was installed." }
        Write-Host "Checksum OK (sha256 $got)."

        $dir = Join-Path $tmp 'cortex'
        Expand-Archive -LiteralPath $zip -DestinationPath $dir -Force
        $exe = Join-Path $dir 'cortex.exe'
        if (-not (Test-Path -LiteralPath $exe)) { throw "cortex.exe is not in $asset." }

        if ($env:CORTEX_DOWNLOAD_ONLY -eq '1') {
            $keep = $true
            Write-Host "Extracted to: $exe"
            return
        }

        # cortex.exe has no console (GUI subsystem), so run it through
        # Start-Process, which waits for it and can capture what it prints.
        Write-Host 'Installing...'
        $out = Join-Path $tmp 'install.out'
        $err = Join-Path $tmp 'install.err'
        $p = Start-Process -FilePath $exe -ArgumentList 'install' -Wait -PassThru -WindowStyle Hidden -RedirectStandardOutput $out -RedirectStandardError $err
        foreach ($f in @($out, $err)) {
            if (Test-Path -LiteralPath $f) { Get-Content -LiteralPath $f | ForEach-Object { Write-Host $_ } }
        }
        if ($p.ExitCode -ne 0) { throw "cortex.exe install failed with exit code $($p.ExitCode)." }

        Write-Host ''
        Write-Host "Cortex $version is installed. Its control panel is at http://127.0.0.1:47821/"
        Write-Host 'Next: open the vault folder (Cortex, in your user folder) in Obsidian and use the panel''s "Connect AI tools" tab.'
        Write-Host 'Uninstall: & "$env:LOCALAPPDATA\Programs\Cortex\cortex.exe" uninstall   (add -purge to remove settings and log too; the vault is never removed)'
    }
    finally {
        if (-not $keep) { Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue }
    }
}

Install-Cortex
