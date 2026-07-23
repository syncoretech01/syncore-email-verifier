<#
.SYNOPSIS
  Post a signed sending-outcome event to the local feedback endpoint.

.DESCRIPTION
  Computes the HMAC-SHA256 signature the server expects (X-Syncore-Signature)
  and POSTs {"email","type"} to /v1/feedback. Feeding delivered/bounced outcomes
  for a domain is what resolves catch_all_confidence and the reputation score on
  the next verification of any address at that domain.

  The feedback store is in-memory, so history resets when the server restarts
  (unless SYNCORE_VERIFIER_STORE=postgres).

.EXAMPLE
  # Mark a delivery (repeat >= 5 times for a domain to move catch_all_confidence)
  .\scripts\send-feedback.ps1 -Email ann@syncoretech.com

.EXAMPLE
  .\scripts\send-feedback.ps1 -Email bad@syncoretech.com -Type bounced
#>
param(
  [Parameter(Mandatory = $true)][string]$Email,
  [ValidateSet("delivered", "bounced", "complained", "engaged")][string]$Type = "delivered",
  [string]$BaseUrl     = "http://127.0.0.1:8080",
  [string]$FeedbackKey = "demo-feedback-key"
)
$ErrorActionPreference = "Stop"

# The signature is over the exact request-body bytes, so build the body once and
# sign and send the very same string.
$body = "{""email"":""$Email"",""type"":""$Type""}"
$bytes = [System.Text.Encoding]::UTF8.GetBytes($body)

$hmac = [System.Security.Cryptography.HMACSHA256]::new([System.Text.Encoding]::UTF8.GetBytes($FeedbackKey))
try {
  $digest = $hmac.ComputeHash($bytes)
}
finally {
  $hmac.Dispose()
}
$sig = "sha256=" + ((($digest | ForEach-Object { $_.ToString("x2") })) -join "")

try {
  $resp = Invoke-RestMethod -Method Post -Uri "$BaseUrl/v1/feedback" `
    -ContentType "application/json" `
    -Headers @{ "X-Syncore-Signature" = $sig } `
    -Body $body
  Write-Host "$Type  $Email  ->  accepted=$($resp.accepted)" -ForegroundColor Green
}
catch {
  Write-Host "$Type  $Email  ->  FAILED: $($_.Exception.Message)" -ForegroundColor Red
  throw
}
