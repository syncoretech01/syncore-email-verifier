<#
.SYNOPSIS
  Build and run the Syncore Email Verifier locally with every optional signal on.

.DESCRIPTION
  Starts the API server in the foreground (Ctrl+C to stop). Binds to loopback by
  default, so no auth token is required. Enables the browser console, domain
  health, Gravatar, DNSBL, the MX cache, the retention sweep, and the HMAC-signed
  feedback endpoint. Open the console in a browser at the printed URL.

.EXAMPLE
  .\scripts\run-local.ps1
  .\scripts\run-local.ps1 -BindAddr 127.0.0.1:9090
#>
param(
  [string]$BindAddr    = "127.0.0.1:8080",
  [string]$FeedbackKey = "demo-feedback-key"
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
  Write-Host "Building cmd/apiserver..." -ForegroundColor Cyan
  New-Item -ItemType Directory -Force -Path bin | Out-Null
  go build -o bin/apiserver.exe ./cmd/apiserver
  if ($LASTEXITCODE -ne 0) { throw "go build failed" }

  $env:SYNCORE_VERIFIER_BIND_ADDR            = $BindAddr
  $env:SYNCORE_VERIFIER_DEV_CONSOLE          = "true"
  $env:SYNCORE_VERIFIER_DOMAIN_HEALTH        = "true"
  $env:SYNCORE_VERIFIER_GRAVATAR_CHECK       = "true"
  $env:SYNCORE_VERIFIER_DNSBL_CHECK          = "true"
  $env:SYNCORE_VERIFIER_MX_CACHE_TTL         = "5m"
  $env:SYNCORE_VERIFIER_PURGE_INTERVAL       = "10m"
  $env:SYNCORE_VERIFIER_FEEDBACK_SIGNING_KEY = $FeedbackKey

  Write-Host ""
  Write-Host "Console:  http://$BindAddr/"            -ForegroundColor Green
  Write-Host "Health:   http://$BindAddr/health"      -ForegroundColor Green
  Write-Host "Feedback signing key: $FeedbackKey"     -ForegroundColor DarkGray
  Write-Host "Press Ctrl+C to stop."                  -ForegroundColor DarkGray
  Write-Host ""
  & ".\bin\apiserver.exe"
}
finally {
  Pop-Location
}
