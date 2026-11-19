# PTZ controller installer for Windows.
#
# Run in PowerShell as Administrator:
#   irm https://github.com/__REPO__/releases/latest/download/install.ps1 | iex
#
# Downloads the latest release, checks its SHA-256 against the release's
# SHA256SUMS, installs it as a Windows service that starts with the PC,
# opens the firewall on private networks only, and opens the setup
# wizard. Run it again to update.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Error 'Run PowerShell as Administrator (right-click PowerShell, "Run as administrator"), then run this again.'
    return
}

$repo = if ($env:PTZ_REPO) { $env:PTZ_REPO } else { '__REPO__' }
$base = "https://github.com/$repo/releases/latest/download"
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$asset = "ptz-windows-$arch.exe"
$dir = Join-Path $env:ProgramFiles 'PTZ'
$exe = Join-Path $dir 'ptz.exe'
$tmp = Join-Path $env:TEMP ("ptz-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
    Write-Host "Downloading $asset from $repo ..."
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile (Join-Path $tmp $asset)
    Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile (Join-Path $tmp 'SHA256SUMS')

    $line = Get-Content (Join-Path $tmp 'SHA256SUMS') | Where-Object { $_ -match "\s\*?$([regex]::Escape($asset))$" } | Select-Object -First 1
    if (-not $line) { throw "$asset is not listed in SHA256SUMS - not installing." }
    $want = ($line -split '\s+')[0].ToLower()
    $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $asset)).Hash.ToLower()
    if ($want -ne $got) { throw "Checksum mismatch for $asset - not installing." }
    Write-Host 'Checksum OK.'

    if (Test-Path $exe) {
        Write-Host "Updating the existing install in $dir"
        & $exe service stop 2>$null | Out-Null
        Start-Sleep -Seconds 2
    }
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    Copy-Item -Force (Join-Path $tmp $asset) $exe

    # data\ holds camera logins: only the service (SYSTEM) and admins may read it.
    $data = Join-Path $dir 'data'
    New-Item -ItemType Directory -Force -Path $data | Out-Null
    icacls $data /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null

    & $exe service install 2>$null | Out-Null   # already installed is fine
    & $exe service start
    if ($LASTEXITCODE -ne 0) { throw 'The service did not start.' }

    # Phones on the venue network reach the app and its live video.
    # Private networks only; nothing opens on public Wi-Fi.
    $rules = @(
        @{ Name = 'PTZ Controller (web)';        Proto = 'TCP'; Ports = '8080' },
        @{ Name = 'PTZ Controller (video)';      Proto = 'TCP'; Ports = '8888,8889' },
        @{ Name = 'PTZ Controller (video UDP)';  Proto = 'UDP'; Ports = '8189,8890' }
    )
    foreach ($r in $rules) {
        if (-not (Get-NetFirewallRule -DisplayName $r.Name -ErrorAction SilentlyContinue)) {
            New-NetFirewallRule -DisplayName $r.Name -Direction Inbound -Action Allow -Profile Private,Domain `
                -Protocol $r.Proto -LocalPort ($r.Ports -split ',') | Out-Null
        }
    }

    $ver = & $exe version
    Write-Host ''
    Write-Host "PTZ controller $ver is running."
    Write-Host 'Finish setup at: http://localhost:8080/setup'
    Start-Process 'http://localhost:8080/setup'
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
