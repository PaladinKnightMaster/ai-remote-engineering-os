# review-wd3.ps1 - drive the WD-3 agent side for a local v0.3.0 review.
#
# You play BOTH roles: this terminal = the autonomous agent; the browser = the
# operator. The agent fires remote.exec; it PARKS and blocks until you Approve or
# Deny it in the browser cockpit (http://localhost:5173, "Pending tool invocations").
#
# Uses curl.exe (the real curl, bundled with Windows). PowerShell's `curl` is an
# alias for Invoke-WebRequest and will not work. JSON bodies are passed as files
# (--data @file) because Windows PowerShell 5.1 mangles inline-JSON quotes when
# calling native exes. The session cookie is Secure, so it is sent as a manual
# Cookie header (curl will not auto-send Secure cookies over http; the browser does
# on localhost). All ASCII on purpose: PS 5.1 chokes on em-dashes and on > / < in
# strings.
#
# Run:  powershell -ExecutionPolicy Bypass -File ai-remote-engineering-os\infra\scripts\review-wd3.ps1
param(
  [string]$Command    = "echo REVIEW-OK",
  [string]$ResourceId = "01900000-0000-7000-8000-0000000000a2",
  [string]$Api        = "http://localhost:8080",
  [string]$Gateway    = "http://localhost:8085",
  [string]$User       = "admin",
  [string]$Password   = "e2e-operator-pw"
)
$ErrorActionPreference = "Stop"
if (-not (Get-Command curl.exe -ErrorAction SilentlyContinue)) {
  Write-Error "curl.exe not found (needs Windows 10 1803+ or Windows 11)."; exit 1
}

$jar     = (New-TemporaryFile).FullName
$loginF  = (New-TemporaryFile).FullName
$mintF   = (New-TemporaryFile).FullName
$invokeF = (New-TemporaryFile).FullName
$utf8 = New-Object System.Text.UTF8Encoding $false
function WJson($path, $obj) { [IO.File]::WriteAllText($path, ($obj | ConvertTo-Json -Compress -Depth 8), $utf8) }

try {
  WJson $loginF  @{ username = $User; password = $Password }
  WJson $mintF   @{ agentId = "review"; resourceId = $ResourceId; operations = @("exec") }
  WJson $invokeF @{ jsonrpc = "2.0"; id = 1; method = "tools/call"; params = @{ name = "remote.exec"; arguments = @{ resourceId = $ResourceId; command = $Command } } }

  # 1. operator login -> cookie jar
  curl.exe -s -c $jar -X POST "$Api/v1/auth/login" -H "Content-Type: application/json" --data "@$loginF" | Out-Null
  $lines = Get-Content $jar
  $sess = (($lines | Where-Object { $_ -match "aireos_session" }) -split "`t")[-1]
  $csrf = (($lines | Where-Object { $_ -match "aireos_csrf" })    -split "`t")[-1]
  if (-not $sess -or -not $csrf) { Write-Error "login failed (no session/csrf cookie). Is the backend up on $Api ?"; exit 1 }
  $cookie = "aireos_session=$sess; aireos_csrf=$csrf"

  # 2. operator provisions a scoped agent-tool token (you authorizing an agent)
  $mintRaw = curl.exe -s -X POST "$Api/v1/agent-tokens" -H "Cookie: $cookie" -H "X-CSRF-Token: $csrf" -H "Content-Type: application/json" --data "@$mintF"
  $token = ($mintRaw | ConvertFrom-Json).token
  if (-not $token) { Write-Error "agent-token mint failed: $mintRaw"; exit 1 }
  Write-Host "[wd3] agent token minted." -ForegroundColor Cyan
  Write-Host "[wd3] firing remote.exec on $ResourceId. It will PARK and block here." -ForegroundColor Cyan
  Write-Host "[wd3] ACTION: open the browser, go to 'Pending tool invocations', click Approve (or Deny)." -ForegroundColor Yellow

  # 3. the AGENT calls remote.exec - BLOCKS until you resolve it in the browser
  $result = curl.exe -s --max-time 180 -X POST "$Gateway/mcp" -H "Authorization: Bearer $token" -H "Content-Type: application/json" --data "@$invokeF"
  Write-Host ""
  Write-Host "[wd3] gateway result:" -ForegroundColor Green
  try { ($result | ConvertFrom-Json) | ConvertTo-Json -Depth 8 } catch { Write-Host $result }
}
finally {
  Remove-Item $jar, $loginF, $mintF, $invokeF -Force -ErrorAction SilentlyContinue
}
