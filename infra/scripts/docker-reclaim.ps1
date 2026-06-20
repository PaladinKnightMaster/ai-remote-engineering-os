<#
.SYNOPSIS
  Tear down the AI ReOS review/test Docker stacks, prune Docker disk, and compact the
  WSL2 VHDX so the freed space is returned to your Windows drive.

.DESCRIPTION
  Run this when you are DONE reviewing (it shuts WSL down, so Docker Desktop stops).
  Steps:
    1. `docker compose down -v` for the review/e2e/smoke projects (removes their
       containers + named volumes — the Go build cache + pgdata are the big ones).
    2. Prune Docker build cache + dangling images + unused volumes.
       -Aggressive also removes ALL unused images (you'll re-pull next time).
    3. `wsl --shutdown` (stops the WSL2 backend so the VHDX can be compacted).
    4. Compact the Docker WSL2 VHDX: Optimize-VHD (Hyper-V) if available, else diskpart.

  Windows 11 Pro. Optimize-VHD needs the Hyper-V PowerShell module; the diskpart
  fallback needs none. Re-open Docker Desktop afterward to use Docker again.

.PARAMETER Aggressive
  Also `docker image prune -a` (remove all unused images, not just dangling).

.PARAMETER VhdxPath
  Override the auto-detected Docker WSL2 VHDX path.

.EXAMPLE
  pwsh -File infra/scripts/docker-reclaim.ps1
  pwsh -File infra/scripts/docker-reclaim.ps1 -Aggressive
#>
[CmdletBinding()]
param(
  [switch]$Aggressive,
  [string]$VhdxPath
)

$ErrorActionPreference = 'Stop'
function Info($m) { Write-Host "[reclaim] $m" -ForegroundColor Cyan }
function Warn($m) { Write-Host "[reclaim] $m" -ForegroundColor Yellow }

$repoCompose = Join-Path $PSScriptRoot '..\docker-compose'

# 1. Tear down the known project stacks (ignore errors if a stack isn't up).
foreach ($f in @('docker-compose.e2e.yml', 'docker-compose.smoke.yml', 'docker-compose.mvp.yml')) {
  $path = Join-Path $repoCompose $f
  if (Test-Path $path) {
    Info "compose down -v: $f"
    $env:MSYS_NO_PATHCONV = '1'
    try { docker compose -f $path down -v 2>$null } catch { Warn "  (not running)" }
  }
}

# 2. Prune Docker disk.
Info 'prune build cache'
docker builder prune -af | Out-Null
Info 'prune unused volumes'
docker volume prune -f | Out-Null
if ($Aggressive) {
  Info 'prune ALL unused images (-Aggressive)'
  docker image prune -af | Out-Null
} else {
  Info 'prune dangling images'
  docker image prune -f | Out-Null
}
Info 'docker disk usage after prune:'
docker system df

# 3. Stop WSL so the VHDX is closed (this stops Docker Desktop too).
Warn 'shutting down WSL (Docker Desktop will stop) ...'
wsl --shutdown
Start-Sleep -Seconds 3

# 4. Locate + compact the Docker WSL2 VHDX.
if (-not $VhdxPath) {
  $candidates = @(
    "$env:LOCALAPPDATA\Docker\wsl\disk\docker_data.vhdx",   # newer Docker Desktop
    "$env:LOCALAPPDATA\Docker\wsl\data\ext4.vhdx",          # older Docker Desktop
    "$env:LOCALAPPDATA\Docker\wsl\main\ext4.vhdx"
  )
  $VhdxPath = $candidates | Where-Object { Test-Path $_ } | Select-Object -First 1
}
if (-not $VhdxPath -or -not (Test-Path $VhdxPath)) {
  Warn "Could not auto-find the Docker WSL2 VHDX. Pass -VhdxPath '<path>'."
  Warn "Find it with:  Get-ChildItem `$env:LOCALAPPDATA\Docker\wsl -Recurse -Filter *.vhdx"
  exit 1
}
Info "VHDX: $VhdxPath"
$before = (Get-Item $VhdxPath).Length / 1GB

$optimized = $false
if (Get-Command Optimize-VHD -ErrorAction SilentlyContinue) {
  try {
    Info 'compacting via Optimize-VHD (Hyper-V) ...'
    Optimize-VHD -Path $VhdxPath -Mode Full
    $optimized = $true
  } catch { Warn "Optimize-VHD failed ($($_.Exception.Message)); falling back to diskpart" }
}
if (-not $optimized) {
  Info 'compacting via diskpart ...'
  $script = @"
select vdisk file="$VhdxPath"
attach vdisk readonly
compact vdisk
detach vdisk
exit
"@
  $tmp = New-TemporaryFile
  Set-Content -Path $tmp -Value $script -Encoding ascii
  diskpart /s $tmp
  Remove-Item $tmp -Force
}

$after = (Get-Item $VhdxPath).Length / 1GB
Info ("VHDX compacted: {0:N2} GB -> {1:N2} GB (reclaimed {2:N2} GB)" -f $before, $after, ($before - $after))
Info 'done. Re-open Docker Desktop to use Docker again.'
