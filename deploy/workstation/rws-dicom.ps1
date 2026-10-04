<#
.SYNOPSIS
  Install and manage the RWS DICOM module on a Windows workstation (Docker Desktop).

.DESCRIPTION
  RWS (Radiology Workflow System) - developed by Patrick Mulcahy, Mulcahy.io
  Contact: dev@mulcahy.io

  Commands:
    install    Create .env if missing, build the image, start, verify
    upgrade    Rebuild from the current source, back up the index, restart, verify
    verify     Health check, admin endpoint, host port, DICOM C-ECHO
    status     Container state and version
    logs       Follow the module log (Ctrl+C to stop)
    start | stop | restart
    backup     Copy the whole data area (images + index) to .\backups\<timestamp>
    uninstall  Remove containers and image. Data is kept unless -RemoveData.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File .\rws-dicom.ps1 install
  .\rws-dicom.ps1 install -AETitle RWS_WS1 -DicomPort 104
  .\rws-dicom.ps1 uninstall -RemoveData
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('install', 'upgrade', 'verify', 'status', 'logs', 'start', 'stop', 'restart', 'backup', 'uninstall')]
    [string]$Command = 'install',
    [string]$AETitle,
    [int]$DicomPort,
    [string]$DataDir,
    [switch]$RemoveData
)

# 'Continue' so native docker stderr never throws in Windows PowerShell 5.1;
# failures are checked through `$LASTEXITCODE and Fail.
$ErrorActionPreference = 'Continue'
Set-Location -LiteralPath $PSScriptRoot
$Container = 'rws-dicom'

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

function Set-EnvValue([string]$Key, [string]$Value) {
    $lines = @(Get-Content .env)
    $found = $false
    for ($i = 0; $i -lt $lines.Count; $i++) {
        if ($lines[$i] -match "^\s*$Key\s*=") { $lines[$i] = "$Key=$Value"; $found = $true }
    }
    if (-not $found) { $lines += "$Key=$Value" }
    # UTF-8 without BOM so docker compose reads the first line correctly
    [IO.File]::WriteAllLines((Join-Path $PSScriptRoot '.env'), [string[]]$lines, (New-Object Text.UTF8Encoding $false))
}

function Get-ProfileArgs($envs) {
    if ($envs['RWS_EVENTS_DRIVER'] -eq 'nats') { return @('--profile', 'events') }
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

function Initialize-Config {
    if (-not (Test-Path .env)) { Copy-Item .env.example .env -ErrorAction Stop; Ok 'Created .env from .env.example' }
    if ($AETitle)   { Set-EnvValue 'RWS_DICOM_AE_TITLE' $AETitle.ToUpper() }
    if ($DicomPort) { Set-EnvValue 'RWS_DICOM_PORT' $DicomPort }
    if ($DataDir)   { Set-EnvValue 'RWS_DATA' ($DataDir -replace '\\', '/') }
}

function Test-Settings {
    $e = Read-Env
    $ae = $e['RWS_DICOM_AE_TITLE']
    if (-not $ae -or $ae.Length -gt 16 -or $ae -notmatch '^[A-Za-z0-9_\-\. ]+$') {
        Fail "RWS_DICOM_AE_TITLE '$ae' is invalid: 1-16 characters, letters/digits/_ recommended."
    }
    $data = $e['RWS_DATA']
    if ($data) {
        if ($data -match '^(\\\\|//)') { Fail "RWS_DATA '$data' is a network path. Use a local folder - the SQLite index needs local disk." }
        if ($data -match '^([A-Za-z]):') {
            $disk = Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='$($Matches[1].ToUpper()):'" -ErrorAction SilentlyContinue
            if ($disk -and $disk.DriveType -eq 4) { Fail "RWS_DATA '$data' is on a mapped network drive. Use a local disk (e.g. C:/RWS/dicom-data)." }
        }
        if (-not (Test-Path -LiteralPath $data)) { New-Item -ItemType Directory -Path $data -Force | Out-Null; Ok "Created data folder $data" }
    }
    # Port check - skip when our own container already owns it.
    $running = & docker ps -q -f "name=^$Container$" 2>$null
    if (-not $running) {
        foreach ($port in @($e['RWS_DICOM_PORT'], $e['RWS_ADMIN_PORT'])) {
            if ($port -and (Get-NetTCPConnection -LocalPort ([int]$port) -State Listen -ErrorAction SilentlyContinue)) {
                Fail "Port $port is already in use on this PC. Change it in .env and run again."
            }
        }
    }
    Ok "Settings valid (AE $ae, DICOM port $($e['RWS_DICOM_PORT']))"
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

function Invoke-Verify {
    $e = Read-Env
    Wait-Healthy
    try {
        $v = Invoke-RestMethod -Uri "http://127.0.0.1:$($e['RWS_ADMIN_PORT'])/version" -TimeoutSec 5 -ErrorAction Stop
        Ok "Admin endpoint answering: $($v | ConvertTo-Json -Compress)"
    } catch { Fail "Admin endpoint http://127.0.0.1:$($e['RWS_ADMIN_PORT']) not reachable: $_" }

    $tcp = New-Object Net.Sockets.TcpClient
    try {
        $tcp.ConnectAsync('127.0.0.1', [int]$e['RWS_DICOM_PORT']).Wait(5000) | Out-Null
        if ($tcp.Connected) { Ok "DICOM port $($e['RWS_DICOM_PORT']) reachable from this PC" } else { Fail "DICOM port $($e['RWS_DICOM_PORT']) not reachable from this PC" }
    } finally { $tcp.Dispose() }

    & docker exec $Container /rws-dicom -echo 127.0.0.1:11112
    if ($LASTEXITCODE -ne 0) { Fail 'DICOM C-ECHO failed' }
    Ok 'DICOM C-ECHO succeeded'
}

function Show-Summary {
    $e = Read-Env
    $ips = @(Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.IPAddress -notmatch '^(127\.|169\.254\.)' -and $_.InterfaceAlias -notmatch 'vEthernet|WSL|Loopback' } |
        Select-Object -ExpandProperty IPAddress)
    $data = if ($e['RWS_DATA']) { $e['RWS_DATA'] } else { 'Docker volume rws-dicom-data' }
    Write-Host ''
    Write-Host '  RWS DICOM is running' -ForegroundColor Cyan
    Info "AE title     : $($e['RWS_DICOM_AE_TITLE'])"
    Info "DICOM port   : $($e['RWS_DICOM_PORT'])"
    Info "This PC's IP : $(if ($ips) { $ips -join ', ' } else { 'see ipconfig' })"
    Info "Data         : $data"
    Info "Health       : http://127.0.0.1:$($e['RWS_ADMIN_PORT'])/readyz   Metrics: /metrics"
    Info 'Configure modalities/viewers to send to the AE title, IP and port above.'
    Info 'If other machines cannot connect, allow the DICOM port through Windows Defender Firewall.'
    Write-Host ''
}

function Invoke-Backup {
    $dest = Join-Path 'backups' (Get-Date -Format 'yyyyMMdd-HHmmss')
    New-Item -ItemType Directory -Path $dest -Force | Out-Null
    & docker cp "${Container}:/var/lib/rws/." $dest
    if ($LASTEXITCODE -ne 0) { Fail 'Backup failed' }
    Ok "Backed up data to $dest"
}

function Backup-Index {
    $dest = Join-Path 'backups' ("index-" + (Get-Date -Format 'yyyyMMdd-HHmmss'))
    New-Item -ItemType Directory -Path $dest -Force | Out-Null
    & docker cp "${Container}:/var/lib/rws/index.db" $dest 2>$null
    if ($LASTEXITCODE -eq 0) { Ok "Index backed up to $dest" } else { Warn 'No index to back up (first install?)' }
}

Write-Host ''
Write-Host "  RWS DICOM - $Command   (Mulcahy.io)" -ForegroundColor Cyan

switch ($Command) {
    'install' {
        Test-Docker; Initialize-Config; Test-Settings
        Info 'Building the image and starting the module (first build takes a few minutes)...'
        Invoke-Compose up -d --build
        Invoke-Verify; Show-Summary
    }
    'upgrade' {
        Test-Docker; Initialize-Config
        Info 'Building the new image (the running module keeps serving)...'
        Invoke-Compose build --pull
        Invoke-Compose stop dicom
        Backup-Index
        Invoke-Compose up -d
        Invoke-Verify; Show-Summary
    }
    'verify'  { Test-Docker; Invoke-Verify; Show-Summary }
    'status'  {
        Invoke-Compose ps
        $e = Read-Env
        try { Invoke-RestMethod "http://127.0.0.1:$($e['RWS_ADMIN_PORT'])/version" -TimeoutSec 3 -ErrorAction Stop | Format-List } catch { Warn 'Admin endpoint not answering' }
    }
    'logs'    { Invoke-Compose logs -f --tail 200 dicom }
    'start'   { Invoke-Compose up -d; Invoke-Verify }
    'stop'    { Invoke-Compose stop }
    'restart' { Invoke-Compose restart dicom; Invoke-Verify }
    'backup'  { Invoke-Backup }
    'uninstall' {
        if ($RemoveData) {
            $c = Read-Host '  This permanently deletes all stored images and the index in the Docker volumes. Type DELETE to confirm'
            if ($c -ne 'DELETE') { Fail 'Cancelled' }
            & docker compose -f docker-compose.yml --profile events down --rmi all -v
            $d = (Read-Env)['RWS_DATA']
            if ($d) { Warn "Bind-mounted folder $d was NOT deleted. Remove it yourself if you no longer need it." }
        } else {
            & docker compose -f docker-compose.yml --profile events down --rmi all
            Ok 'Removed containers and image. Your data is kept; reinstall picks it up again.'
        }
    }
}
