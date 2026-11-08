<#
.SYNOPSIS
  Install and manage LibreDCM on a Windows workstation (Docker Desktop).

.DESCRIPTION
  LibreDCM - the DICOM module of Libre RWS (Radiology Workflow System)
  Developed by Patrick Mulcahy, Mulcahy.io - dev@mulcahy.io

  Commands:
    install    First time: ask the install-time questions (storage, TLS,
               patient IDs), build the image, start, verify
    configure  Ask the install-time questions again and apply them
    upgrade    Rebuild from the current source, back up the index, restart, verify
               (also moves a 0.1.x "rws-dicom" install to the LibreDCM names)
    verify     Health check, admin endpoint, host port, DICOM C-ECHO
    status     Container state and version
    logs       Follow the module log (Ctrl+C to stop)
    start | stop | restart
    backup     Index snapshot + images to .\backups\<timestamp> (safe while running)
    license    License status (official builds)
    activate KEY            Activate this PC online with a license key
    license-request [KEY]   Write an activation request (PC without internet)
    license-install FILE    Install a license file from the LibreKey portal
    uninstall  Remove containers and image. Data is kept unless -RemoveData.

  -Yes takes the values in .env without asking (unattended installs).

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File .\LibreDCM.ps1 install
  .\LibreDCM.ps1 install -AETitle LIBREDCM_WS1 -DicomPort 104 -Yes
  .\LibreDCM.ps1 uninstall -RemoveData
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('install', 'configure', 'upgrade', 'verify', 'status', 'logs', 'start', 'stop', 'restart', 'backup', 'uninstall', 'license', 'activate', 'license-request', 'license-install')]
    [string]$Command = 'install',
    [Parameter(Position = 1)]
    [string]$Arg,
    [string]$AETitle,
    [int]$DicomPort,
    [string]$DataDir,
    [switch]$Yes,
    [switch]$RemoveData
)

# 'Continue' so native docker stderr never throws in Windows PowerShell 5.1;
# failures are checked through `$LASTEXITCODE and Fail.
$ErrorActionPreference = 'Continue'
Set-Location -LiteralPath $PSScriptRoot
$Container = 'libredcm'
$DataPath = '/var/lib/libredcm'

function Info($m) { Write-Host "  $m" }
function Ok($m)   { Write-Host "  [ OK ] $m" -ForegroundColor Green }
function Warn($m) { Write-Host "  [WARN] $m" -ForegroundColor Yellow }
function Fail($m) { Write-Host "  [FAIL] $m" -ForegroundColor Red; exit 1 }

function Read-Env {
    $h = @{}
    if (Test-Path .env) {
        foreach ($line in Get-Content .env) {
            if ($line -match '^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*?)\s*$') { $h[$Matches[1]] = $Matches[2] }
        }
    }
    return $h
}

function Write-EnvLines([string[]]$Lines) {
    # UTF-8 without BOM so docker compose reads the first line correctly
    [IO.File]::WriteAllLines((Join-Path $PSScriptRoot '.env'), $Lines, (New-Object Text.UTF8Encoding $false))
}

function Set-EnvValue([string]$Key, [string]$Value) {
    $lines = @(Get-Content .env)
    $found = $false
    for ($i = 0; $i -lt $lines.Count; $i++) {
        if ($lines[$i] -match "^\s*$Key\s*=") { $lines[$i] = "$Key=$Value"; $found = $true }
    }
    if (-not $found) { $lines += "$Key=$Value" }
    Write-EnvLines ([string[]]$lines)
}

function Test-TlsOn { $m = (Read-Env)['LIBRE_DCM_TLS_MODE']; return ($m -eq 'self-signed' -or $m -eq 'files') }

function Get-ProfileArgs($envs) {
    if ($envs['LIBRE_DCM_EVENTS_DRIVER'] -eq 'nats') { return @('--profile', 'events') }
    return @()
}

function Invoke-Compose {
    $envs = Read-Env
    $p = Get-ProfileArgs $envs
    & docker compose -f docker-compose.yml @p @args
    if ($LASTEXITCODE -ne 0) { Fail "docker compose $($args -join ' ') failed (exit $LASTEXITCODE)" }
}

function Test-Docker {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        Fail 'Docker is not installed. Install Docker Desktop (WSL 2 backend) from https://www.docker.com/products/docker-desktop/ and run this again.'
    }
    & docker info *> $null
    if ($LASTEXITCODE -ne 0) { Fail 'Docker is installed but not running. Start Docker Desktop, wait for "Engine running", then try again.' }
    & docker compose version *> $null
    if ($LASTEXITCODE -ne 0) { Fail 'Docker Compose v2 is missing. Update Docker Desktop.' }
    Ok 'Docker is running'
}

function Test-Interactive { return (-not $Yes) -and [Environment]::UserInteractive -and -not [Console]::IsInputRedirected }

function Ask([string]$Question, [string]$Default) {
    $a = Read-Host "  $Question [$Default]"
    if ([string]::IsNullOrWhiteSpace($a)) { return $Default }
    return $a.Trim()
}

# Move a 0.1.x (rws-dicom) install to the LibreDCM names without touching
# stored images or the index. Only an install whose .env still has RWS_*
# settings is migrated, so a fresh install never touches another copy.
function Move-Legacy {
    $lines = @(Get-Content .env)
    if (-not ($lines | Where-Object { $_ -match '^\s*RWS_' })) { return }
    $bak = ".env.bak-" + (Get-Date -Format 'yyyyMMdd-HHmmss')
    Copy-Item .env $bak
    $lines = $lines | ForEach-Object {
        $_ -replace '^(\s*)RWS_DICOM_PORT=', '$1LIBRE_DCM_PORT=' -replace '^(\s*)RWS_DICOM_', '$1LIBRE_DCM_' -replace '^(\s*)RWS_', '$1LIBRE_DCM_'
    }
    Write-EnvLines ([string[]]$lines)
    Ok "Renamed RWS_* settings to LIBRE_DCM_* (old file kept as $bak)"
    foreach ($old in @('rws-dicom', 'rws-dicom-nats')) {
        & docker container inspect $old *> $null
        if ($LASTEXITCODE -eq 0) { & docker rm -f $old *> $null; Ok "Removed old container $old (its data volume is kept)" }
    }
    # 0.1.x compose prefixed volume names with its project name (rws-dicom_...).
    $e = Read-Env
    if (-not $e['LIBRE_DCM_VOLUME']) {
        foreach ($v in @('rws-dicom_rws-dicom-data', 'rws-dicom-data')) {
            & docker volume inspect $v *> $null
            if ($LASTEXITCODE -eq 0) { Set-EnvValue 'LIBRE_DCM_VOLUME' $v; Ok "Keeping your existing data volume $v"; break }
        }
    }
    if (-not $e['LIBRE_DCM_NATS_VOLUME']) {
        foreach ($v in @('rws-dicom_rws-dicom-nats', 'rws-dicom-nats')) {
            & docker volume inspect $v *> $null
            if ($LASTEXITCODE -eq 0) { Set-EnvValue 'LIBRE_DCM_NATS_VOLUME' $v; break }
        }
    }
}

# Add settings new in this version; move the image version to this package's.
function Add-MissingSettings {
    $have = Read-Env
    $add = @()
    foreach ($line in Get-Content .env.example) {
        if ($line -match '^([A-Z_][A-Z0-9_]*)=' -and -not $have.ContainsKey($Matches[1])) { $add += $line }
    }
    if ($add.Count) { Write-EnvLines ([string[]](@(Get-Content .env) + $add)) }
    $ver = ((Get-Content .env.example) | Where-Object { $_ -match '^LIBRE_DCM_VERSION=' }) -replace '^LIBRE_DCM_VERSION=', ''
    Set-EnvValue 'LIBRE_DCM_VERSION' $ver
}

function Invoke-Configure {
    $e = Read-Env
    Write-Host ''
    Info 'Install-time settings (Enter keeps the value in brackets)'
    Set-EnvValue 'LIBRE_DCM_AE_TITLE' (Ask 'AE title of this node' $e['LIBRE_DCM_AE_TITLE']).ToUpper()

    Write-Host ''
    Info 'Where should images be stored?'
    Info '  1) This PC, Docker volume (recommended)   2) This PC, a folder I choose   3) S3 / S3-compatible store'
    $cur = '1'; if ($e['LIBRE_DCM_DATA']) { $cur = '2' }; if ($e['LIBRE_DCM_STORAGE_DRIVER'] -eq 's3') { $cur = '3' }
    switch (Ask 'Storage' $cur) {
        '1' { Set-EnvValue 'LIBRE_DCM_STORAGE_DRIVER' 'fs'; Set-EnvValue 'LIBRE_DCM_DATA' '' }
        '2' { Set-EnvValue 'LIBRE_DCM_STORAGE_DRIVER' 'fs'; Set-EnvValue 'LIBRE_DCM_DATA' ((Ask 'Local folder (e.g. C:/LibreDCM/data)' $e['LIBRE_DCM_DATA']) -replace '\\', '/') }
        '3' {
            Set-EnvValue 'LIBRE_DCM_STORAGE_DRIVER' 's3'
            Set-EnvValue 'LIBRE_DCM_STORAGE_S3_ENDPOINT' (Ask 'S3 endpoint (host[:port])' $e['LIBRE_DCM_STORAGE_S3_ENDPOINT'])
            Set-EnvValue 'LIBRE_DCM_STORAGE_S3_BUCKET' (Ask 'Bucket' $e['LIBRE_DCM_STORAGE_S3_BUCKET'])
            Set-EnvValue 'LIBRE_DCM_STORAGE_S3_REGION' (Ask 'Region (blank for MinIO/NAS)' $e['LIBRE_DCM_STORAGE_S3_REGION'])
            Set-EnvValue 'LIBRE_DCM_STORAGE_S3_ACCESS_KEY' (Ask 'Access key' $e['LIBRE_DCM_STORAGE_S3_ACCESS_KEY'])
            $sec = Read-Host '  Secret key [keep current]' -AsSecureString
            $plain = [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec))
            if ($plain) { Set-EnvValue 'LIBRE_DCM_STORAGE_S3_SECRET_KEY' $plain }
            Set-EnvValue 'LIBRE_DCM_STORAGE_S3_CREATE_BUCKET' (Ask 'Create the bucket if missing (true/false)' $e['LIBRE_DCM_STORAGE_S3_CREATE_BUCKET'])
        }
        default { Fail 'Choose 1, 2 or 3' }
    }

    Write-Host ''
    Info 'TLS for DICOM and the admin endpoint:'
    Info '  1) Off   2) Self-signed certificate (created now)   3) My own certificate files'
    $cur = '1'; if ($e['LIBRE_DCM_TLS_MODE'] -eq 'self-signed') { $cur = '2' }; if ($e['LIBRE_DCM_TLS_MODE'] -eq 'files') { $cur = '3' }
    switch (Ask 'TLS' $cur) {
        '1' { Set-EnvValue 'LIBRE_DCM_TLS_MODE' 'off' }
        '2' { Set-EnvValue 'LIBRE_DCM_TLS_MODE' 'self-signed'; Set-EnvValue 'LIBRE_DCM_TLS_HOSTS' (Ask 'Extra DNS names / IPs for the certificate (comma-separated)' $e['LIBRE_DCM_TLS_HOSTS']) }
        '3' {
            Set-EnvValue 'LIBRE_DCM_TLS_MODE' 'files'
            $crt = Ask 'Certificate file (PEM, full chain)' ''
            $key = Ask 'Private key file (PEM)' ''
            if (-not (Test-Path -LiteralPath $crt) -or -not (Test-Path -LiteralPath $key)) { Fail 'Certificate or key file not found' }
            New-Item -ItemType Directory -Path tls -Force | Out-Null
            Copy-Item -LiteralPath $crt tls\tls.crt -Force; Copy-Item -LiteralPath $key tls\tls.key -Force
            Set-EnvValue 'LIBRE_DCM_TLS_DIR' './tls'; Ok 'Copied certificate and key into .\tls'
        }
        default { Fail 'Choose 1, 2 or 3' }
    }

    Write-Host ''
    $d = if ($e['LIBRE_DCM_EVENTS_OMIT_PATIENT_ID'] -eq 'true') { 'n' } else { 'y' }
    Set-EnvValue 'LIBRE_DCM_EVENTS_OMIT_PATIENT_ID' $(if ((Ask 'Include patient IDs in event-bus messages? (y/n)' $d) -match '^[Nn]') { 'true' } else { 'false' })
    $d = if ($e['LIBRE_DCM_AUDIT_OMIT_PATIENT_ID'] -eq 'true') { 'n' } else { 'y' }
    Set-EnvValue 'LIBRE_DCM_AUDIT_OMIT_PATIENT_ID' $(if ((Ask 'Include patient IDs in the audit log? (y/n)' $d) -match '^[Nn]') { 'true' } else { 'false' })
    Set-EnvValue 'LIBRE_DCM_CONFIGURED' '1'
}

# Turn LIBRE_DCM_TLS_MODE into the certificate paths the module reads.
function Set-TlsSettings {
    $e = Read-Env
    $crt = ''; $key = ''
    switch ($e['LIBRE_DCM_TLS_MODE']) {
        { $_ -in @('', 'off') } { }
        'self-signed' { $crt = "$DataPath/tls/tls.crt"; $key = "$DataPath/tls/tls.key" }
        'files' {
            $dir = if ($e['LIBRE_DCM_TLS_DIR']) { $e['LIBRE_DCM_TLS_DIR'] } else { './tls' }
            if (-not (Test-Path (Join-Path $dir 'tls.crt')) -or -not (Test-Path (Join-Path $dir 'tls.key'))) { Fail "TLS mode 'files' needs tls.crt and tls.key in $dir" }
            $crt = '/etc/libredcm/tls/tls.crt'; $key = '/etc/libredcm/tls/tls.key'
        }
        default { Fail 'LIBRE_DCM_TLS_MODE must be off, self-signed or files' }
    }
    New-Item -ItemType Directory -Path tls -Force | Out-Null
    Set-EnvValue 'LIBRE_DCM_TLS_CERT' $crt; Set-EnvValue 'LIBRE_DCM_TLS_KEY' $key
    Set-EnvValue 'LIBRE_DCM_ADMIN_TLS_CERT' $crt; Set-EnvValue 'LIBRE_DCM_ADMIN_TLS_KEY' $key
}

function Initialize-Config {
    $fresh = $false
    if (-not (Test-Path .env)) { Copy-Item .env.example .env -ErrorAction Stop; Ok 'Created .env from .env.example'; $fresh = $true }
    Move-Legacy
    Add-MissingSettings
    if ($AETitle)   { Set-EnvValue 'LIBRE_DCM_AE_TITLE' $AETitle.ToUpper() }
    if ($DicomPort) { Set-EnvValue 'LIBRE_DCM_PORT' $DicomPort }
    if ($DataDir)   { Set-EnvValue 'LIBRE_DCM_DATA' ($DataDir -replace '\\', '/') }
    if ($Command -eq 'configure' -or ((Read-Env)['LIBRE_DCM_CONFIGURED'] -ne '1' -and (Test-Interactive))) {
        Invoke-Configure
    } elseif ($fresh) { Info 'Unattended install: using the settings in .env' }
    if (-not (Read-Env)['LIBRE_DCM_ADMIN_TOKEN']) {
        $b = New-Object byte[] 24; [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b)
        Set-EnvValue 'LIBRE_DCM_ADMIN_TOKEN' (($b | ForEach-Object { $_.ToString('x2') }) -join '')
        Ok 'Generated an admin token (in .env)'
    }
    Set-TlsSettings
}

function Test-Settings {
    $e = Read-Env
    $ae = $e['LIBRE_DCM_AE_TITLE']
    if (-not $ae -or $ae.Length -gt 16 -or $ae -notmatch '^[A-Za-z0-9_\-\. ]+$') {
        Fail "LIBRE_DCM_AE_TITLE '$ae' is invalid: 1-16 characters, letters/digits/_ recommended."
    }
    $data = $e['LIBRE_DCM_DATA']
    if ($data) {
        if ($data -match '^(\\\\|//)') { Fail "LIBRE_DCM_DATA '$data' is a network path. Use a local folder - the SQLite index needs local disk." }
        if ($data -match '^([A-Za-z]):') {
            $disk = Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='$($Matches[1].ToUpper()):'" -ErrorAction SilentlyContinue
            if ($disk -and $disk.DriveType -eq 4) { Fail "LIBRE_DCM_DATA '$data' is on a mapped network drive. Use a local disk (e.g. C:/LibreDCM/data)." }
        }
        if (-not (Test-Path -LiteralPath $data)) { New-Item -ItemType Directory -Path $data -Force | Out-Null; Ok "Created data folder $data" }
    }
    if ($e['LIBRE_DCM_STORAGE_DRIVER'] -eq 's3' -and (-not $e['LIBRE_DCM_STORAGE_S3_ENDPOINT'] -or -not $e['LIBRE_DCM_STORAGE_S3_BUCKET'])) {
        Fail 'S3 storage needs LIBRE_DCM_STORAGE_S3_ENDPOINT and LIBRE_DCM_STORAGE_S3_BUCKET'
    }
    # Port check - skip when our own container already owns it.
    $running = & docker ps -q -f "name=^$Container$" 2>$null
    if (-not $running) {
        foreach ($port in @($e['LIBRE_DCM_PORT'], $e['LIBRE_DCM_ADMIN_PORT'])) {
            if ($port -and (Get-NetTCPConnection -LocalPort ([int]$port) -State Listen -ErrorAction SilentlyContinue)) {
                Fail "Port $port is already in use on this PC. Change it in .env and run again."
            }
        }
    }
    Ok "Settings valid (AE $ae, DICOM port $($e['LIBRE_DCM_PORT']), storage $($e['LIBRE_DCM_STORAGE_DRIVER']), TLS $($e['LIBRE_DCM_TLS_MODE']))"
}

# Create the self-signed certificate in the data volume (kept on later runs).
function New-Certificate {
    $e = Read-Env
    if ($e['LIBRE_DCM_TLS_MODE'] -ne 'self-signed') { return }
    Invoke-Compose run --rm --no-deps -T dicom -gen-cert "$DataPath/tls" -gen-cert-hosts "$($e['LIBRE_DCM_TLS_HOSTS'])"
}

function Wait-Healthy([int]$Seconds = 120) {
    Info 'Waiting for the module to report healthy...'
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        $s = (& docker inspect -f '{{.State.Health.Status}}' $Container 2>$null)
        if ($s -eq 'healthy') { Ok 'Container healthy'; return }
        if ($s -eq 'unhealthy') { break }
        Start-Sleep -Seconds 3
    }
    & docker logs --tail 40 $Container
    Fail 'Module did not become healthy. Last 40 log lines are above.'
}

# Authorization header for the admin endpoint.
function Get-AdminHeaders($e) {
    if ($e['LIBRE_DCM_ADMIN_TOKEN']) { return @{ Authorization = "Bearer $($e['LIBRE_DCM_ADMIN_TOKEN'])" } }
    return @{}
}

# Read /version; over HTTPS the certificate isn't checked (localhost only,
# often self-signed). Uses curl.exe (built into Windows 10+) so it works the
# same in Windows PowerShell 5.1 and PowerShell 7.
function Get-AdminVersion($e, [int]$Timeout = 5) {
    $scheme = if (Test-TlsOn) { 'https' } else { 'http' }
    $curlArgs = @('-fsS', '--max-time', "$Timeout")
    if (Test-TlsOn) { $curlArgs += '-k' }
    if ($e['LIBRE_DCM_ADMIN_TOKEN']) { $curlArgs += @('-H', "Authorization: Bearer $($e['LIBRE_DCM_ADMIN_TOKEN'])") }
    $out = & curl.exe @curlArgs "${scheme}://127.0.0.1:$($e['LIBRE_DCM_ADMIN_PORT'])/version" 2>$null
    if ($LASTEXITCODE -ne 0) { return $null }
    return $out
}

function Invoke-Verify {
    $e = Read-Env
    Wait-Healthy
    $v = Get-AdminVersion $e
    if (-not $v) { Fail "Admin endpoint on 127.0.0.1:$($e['LIBRE_DCM_ADMIN_PORT']) not reachable" }
    Ok "Admin endpoint answering$(if (Test-TlsOn) { ' (HTTPS)' }): $v"

    $tcp = New-Object Net.Sockets.TcpClient
    try {
        $tcp.ConnectAsync('127.0.0.1', [int]$e['LIBRE_DCM_PORT']).Wait(5000) | Out-Null
        if ($tcp.Connected) { Ok "DICOM port $($e['LIBRE_DCM_PORT']) reachable from this PC" } else { Fail "DICOM port $($e['LIBRE_DCM_PORT']) not reachable from this PC" }
    } finally { $tcp.Dispose() }

    & docker exec $Container /LibreDCM -echo 127.0.0.1:11112 -echo-ae $e['LIBRE_DCM_AE_TITLE']
    if ($LASTEXITCODE -ne 0) { Fail 'DICOM C-ECHO failed' }
    Ok "DICOM C-ECHO succeeded$(if (Test-TlsOn) { ' (TLS)' })"
}

function Show-Summary {
    $e = Read-Env
    $ips = @(Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.IPAddress -notmatch '^(127\.|169\.254\.)' -and $_.InterfaceAlias -notmatch 'vEthernet|WSL|Loopback' } |
        Select-Object -ExpandProperty IPAddress)
    $vol = if ($e['LIBRE_DCM_VOLUME']) { $e['LIBRE_DCM_VOLUME'] } else { 'libredcm-data' }
    $storage = if ($e['LIBRE_DCM_STORAGE_DRIVER'] -eq 's3') { "S3 $($e['LIBRE_DCM_STORAGE_S3_ENDPOINT'])/$($e['LIBRE_DCM_STORAGE_S3_BUCKET']) (index on this PC)" }
               elseif ($e['LIBRE_DCM_DATA']) { "this PC ($($e['LIBRE_DCM_DATA']))" } else { "this PC (Docker volume $vol)" }
    $scheme = if (Test-TlsOn) { 'https' } else { 'http' }
    Write-Host ''
    Write-Host '  LibreDCM is running' -ForegroundColor Cyan
    Info "AE title     : $($e['LIBRE_DCM_AE_TITLE'])"
    Info "DICOM port   : $($e['LIBRE_DCM_PORT'])$(if (Test-TlsOn) { ' (DICOM TLS)' })"
    Info "This PC's IP : $(if ($ips) { $ips -join ', ' } else { 'see ipconfig' })"
    Info "Storage      : $storage"
    Info "TLS          : $($e['LIBRE_DCM_TLS_MODE'])"
    Info "Health       : ${scheme}://127.0.0.1:$($e['LIBRE_DCM_ADMIN_PORT'])/readyz   Metrics: /metrics (token in .env)"
    Info 'Configure modalities/viewers to send to the AE title, IP and port above.'
    Info 'If other machines cannot connect, allow the DICOM port through Windows Defender Firewall.'
    Write-Host ''
}

function Invoke-Backup {
    $e = Read-Env
    $dest = Join-Path 'backups' (Get-Date -Format 'yyyyMMdd-HHmmss')
    New-Item -ItemType Directory -Path $dest -Force | Out-Null
    # Snapshot the index first (consistent while running), then copy images.
    # Images are write-once, so every indexed image is in the copy.
    & docker exec $Container /LibreDCM -backup "$DataPath/backup/index.db" | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail 'Index snapshot failed' }
    & docker cp "${Container}:$DataPath/backup/index.db" (Join-Path $dest 'index.db')
    if ($LASTEXITCODE -ne 0) { Fail 'Copying the index snapshot failed' }
    if ($e['LIBRE_DCM_STORAGE_DRIVER'] -eq 's3') {
        Ok "Backed up the index to $dest (images are in S3: use the store's own versioning/replication)"
        return
    }
    & docker cp "${Container}:$DataPath/blobs" (Join-Path $dest 'blobs')
    if ($LASTEXITCODE -ne 0) { Fail 'Copying images failed' }
    Ok "Backed up to $dest (index snapshot + images)"
}

# ---- License (official builds) ---------------------------------------------
function Get-HostId {
    try { return (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Cryptography' -Name MachineGuid -ErrorAction Stop).MachineGuid } catch { return '' }
}

# Official builds need a license for this PC before starting; source builds
# report "no license needed" (exit 3) and skip this.
function Confirm-License {
    if (-not (Read-Env)['LIBREKEY_HOST_ID']) { Set-EnvValue 'LIBREKEY_HOST_ID' (Get-HostId) }
    & docker compose -f docker-compose.yml run --rm --no-deps -T dicom -license-status *> $null
    $rc = $LASTEXITCODE
    if ($rc -eq 3) { return }
    if ($rc -eq 0) { Ok 'License valid'; return }
    $e = Read-Env
    $key = $e['LIBRE_DCM_LICENSE_KEY']
    if (-not $key -and (Test-Interactive)) {
        Write-Host ''
        Info 'This is an official LibreDCM build: it needs a license for this PC.'
        Info "Enter your license key (from the LibreKey portal), or 'offline' if this PC has no internet."
        $key = Read-Host '  License key'
    }
    if ($key -eq 'offline' -or (-not $key -and -not (Test-Interactive))) {
        & docker compose -f docker-compose.yml run --rm --no-deps -T dicom -license-request -license-site $e['LIBRE_DCM_AE_TITLE'] 2>$null | Out-File -Encoding ascii license-request.txt
        Info "Activation request written to $PSScriptRoot\license-request.txt"
        Info 'Upload it on your license page in the LibreKey portal, download the license file, then run:'
        Info '  .\LibreDCM.ps1 license-install <file>   and   .\LibreDCM.ps1 install'
        Fail 'Not activated yet'
    }
    if (-not $key) { Fail 'A license key is required for this official build' }
    & docker compose -f docker-compose.yml run --rm --no-deps -T dicom -license-activate $key -license-site $e['LIBRE_DCM_AE_TITLE']
    if ($LASTEXITCODE -ne 0) { Fail 'Activation failed (see the message above)' }
    Set-EnvValue 'LIBRE_DCM_LICENSE_KEY' $key
    Ok 'License activated for this PC'
}

function Install-LicenseFile([string]$File) {
    if (-not $File -or -not (Test-Path -LiteralPath $File)) { Fail 'usage: .\LibreDCM.ps1 license-install FILE' }
    if (-not (Read-Env)['LIBREKEY_HOST_ID']) { Set-EnvValue 'LIBREKEY_HOST_ID' (Get-HostId) }
    $abs = (Resolve-Path -LiteralPath $File).Path
    & docker compose -f docker-compose.yml run --rm --no-deps -T -v "${abs}:/tmp/license.lic:ro" dicom -license-install /tmp/license.lic
    if ($LASTEXITCODE -ne 0) { Fail 'License not installed' }
}

function Backup-Index {
    $dest = Join-Path 'backups' ("index-" + (Get-Date -Format 'yyyyMMdd-HHmmss'))
    New-Item -ItemType Directory -Path $dest -Force | Out-Null
    & docker cp "${Container}:$DataPath/index.db" $dest 2>$null
    if ($LASTEXITCODE -eq 0) { Ok "Index backed up to $dest" } else { Warn 'No index to back up (first install?)' }
}

Write-Host ''
Write-Host "  LibreDCM - $Command   (Mulcahy.io)" -ForegroundColor Cyan

switch ($Command) {
    { $_ -in @('install', 'configure') } {
        Test-Docker; Initialize-Config; Test-Settings
        Info 'Building the image (first build takes a few minutes)...'
        Invoke-Compose build
        Confirm-License
        New-Certificate
        Invoke-Compose up -d
        Invoke-Verify; Show-Summary
    }
    'upgrade' {
        Test-Docker; Initialize-Config
        Info 'Building the new image (the running module keeps serving)...'
        Invoke-Compose build --pull
        Confirm-License
        New-Certificate
        & docker container inspect $Container *> $null
        if ($LASTEXITCODE -eq 0) { Invoke-Compose stop dicom; Backup-Index }
        Invoke-Compose up -d
        Invoke-Verify; Show-Summary
    }
    'verify'  { Test-Docker; Invoke-Verify; Show-Summary }
    'status'  {
        Invoke-Compose ps
        $v = Get-AdminVersion (Read-Env) 3
        if ($v) { Info $v } else { Warn 'Admin endpoint not answering' }
    }
    'logs'    { Invoke-Compose logs -f --tail 200 dicom }
    'start'   { Invoke-Compose up -d; Invoke-Verify }
    'stop'    { Invoke-Compose stop }
    'restart' { Invoke-Compose restart dicom; Invoke-Verify }
    'backup'  { Invoke-Backup }
    'license' { & docker compose -f docker-compose.yml run --rm --no-deps -T dicom -license-status }
    'activate' {
        if (-not $Arg) { Fail 'usage: .\LibreDCM.ps1 activate KEY' }
        Set-EnvValue 'LIBRE_DCM_LICENSE_KEY' $Arg; Confirm-License
        & docker compose -f docker-compose.yml restart dicom 2>$null | Out-Null
    }
    'license-request' { & docker compose -f docker-compose.yml run --rm --no-deps -T dicom -license-request -license-key "$Arg" -license-site (Read-Env)['LIBRE_DCM_AE_TITLE'] }
    'license-install' { Install-LicenseFile $Arg; & docker compose -f docker-compose.yml restart dicom 2>$null | Out-Null; Ok 'License installed' }
    'uninstall' {
        if ($RemoveData) {
            $c = Read-Host '  This permanently deletes all stored images and the index in the Docker volumes. Type DELETE to confirm'
            if ($c -ne 'DELETE') { Fail 'Cancelled' }
            & docker compose -f docker-compose.yml --profile events down --rmi all -v
            $e = Read-Env
            if ($e['LIBRE_DCM_DATA']) { Warn "Bind-mounted folder $($e['LIBRE_DCM_DATA']) was NOT deleted. Remove it yourself if you no longer need it." }
            if ($e['LIBRE_DCM_STORAGE_DRIVER'] -eq 's3') { Warn 'Images in the S3 bucket were NOT deleted.' }
        } else {
            & docker compose -f docker-compose.yml --profile events down --rmi all
            Ok 'Removed containers and image. Your data is kept; reinstall picks it up again.'
        }
    }
}
