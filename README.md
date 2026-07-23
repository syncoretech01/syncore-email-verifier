# Syncore Email Verifier

**Syncore Email Verifier is a customized internal fork of [AfterShip/email-verifier](https://github.com/AfterShip/email-verifier).**

- The upstream project and its **MIT licence attribution are preserved**.
- The **original copyright remains with AfterShip** — see [LICENSE](LICENSE).
- The upstream library README is preserved **unchanged below the Syncore section**.

Phase 1 turns the upstream reference API into a clean, local, single-instance verification service with a structured classification model. It intentionally adds **no** database, queue, retry worker, bulk upload, authentication, paid provider, frontend, or CRM integration.

---

## Requirements

- **Go 1.22 or newer** (the version declared in [`go.mod`](go.mod)).

Check your Go version:

```
go version
```

## Development (Windows-first)

All commands are plain `go` invocations and work identically on Windows (PowerShell or Git Bash), macOS, and Linux. `make` is an optional convenience only (see [Makefile](Makefile)) and is **not required** on Windows.

```
go build ./...            # build everything
go run ./cmd/apiserver    # run the API server (default http://127.0.0.1:8080)
go test ./...             # run the deterministic test suite (primary command)
go vet ./...              # static analysis
```

The server binds to **http://127.0.0.1:8080** by default and logs a startup line. **Stop it with `Ctrl+C`** — the process performs a graceful shutdown (drains in-flight requests, then exits).

### Setting environment variables

**PowerShell:**

```powershell
$env:SYNCORE_VERIFIER_SMTP_ENABLED = "true"
$env:SYNCORE_VERIFIER_BIND_ADDR    = "127.0.0.1:8080"
go run ./cmd/apiserver
# later, to clear one:
Remove-Item Env:\SYNCORE_VERIFIER_SMTP_ENABLED
```

**Git Bash:**

```bash
export SYNCORE_VERIFIER_SMTP_ENABLED=true
export SYNCORE_VERIFIER_BIND_ADDR=127.0.0.1:8080
go run ./cmd/apiserver
```

## Configuration

Configuration comes **only from process environment variables**. `.env.example` is **documentation only** — the service does **not** load `.env`, and there is **no dotenv dependency**. Invalid configuration fails fast at startup (before the server binds), naming the offending variable and expected format.

| Variable | Purpose | Default | Format | Required | Validation |
|---|---|---|---|---|---|
| `SYNCORE_VERIFIER_BIND_ADDR` | HTTP bind address | `127.0.0.1:8080` | `host:port` | No | Must parse as `host:port`; port numeric, `0`–`65535`. |
| `SYNCORE_VERIFIER_SMTP_ENABLED` | Enable mailbox-level SMTP checks | `true` | boolean | No | Must parse as a boolean (`true`/`false`). |
| `SYNCORE_VERIFIER_FROM_EMAIL` | `MAIL FROM` address for SMTP | `hello@syncoretech.com` | email | No | **Only when SMTP is enabled**: must be a valid email address. |
| `SYNCORE_VERIFIER_HELLO_NAME` | `EHLO`/`HELO` name for SMTP | `syncoretech.com` | hostname | No | **Only when SMTP is enabled**: non-empty, no whitespace or control characters. |
| `SYNCORE_VERIFIER_CONNECT_TIMEOUT` | TCP connect timeout for SMTP dialing | `10s` | Go duration | No | Must parse as a duration and be `> 0`. |
| `SYNCORE_VERIFIER_OPERATION_TIMEOUT` | SMTP operation deadline | `10s` | Go duration | No | Must parse as a duration and be `> 0`. |
| `SYNCORE_VERIFIER_DISPOSABLE_AUTOUPDATE` | Refresh disposable-domain list daily | `false` | boolean | No | Must parse as a boolean. |
| `SYNCORE_VERIFIER_DOMAIN_SUGGEST` | Suggest a likely-correct domain for typos | `true` | boolean | No | Must parse as a boolean. |
| `SYNCORE_VERIFIER_MAX_BODY_BYTES` | Maximum accepted `POST` body size (bytes) | `4096` | positive integer | No | Must be a positive integer. |
| `SYNCORE_VERIFIER_AUTH_TOKEN` | Bearer token required on verification endpoints | _(empty)_ | string | No | When empty, auth is disabled — allowed **only** on a loopback bind. |
| `SYNCORE_VERIFIER_CACHE_TTL` | Result-cache lifetime for terminal (`valid`/`invalid`) results; `0` disables the cache | `0` | Go duration | No | Must parse as a duration and be `>= 0`. |
| `SYNCORE_VERIFIER_CACHE_TTL_UNKNOWN` | Result-cache lifetime for retryable (`unknown`) results | `0` → `min(CACHE_TTL, 1m)` | Go duration | No | Must parse as a duration and be `>= 0`; clamped to `CACHE_TTL`. |
| `SYNCORE_VERIFIER_CACHE_MAX_ENTRIES` | Max in-memory result-cache entries | `10000` | positive integer | No | Must be a positive integer. |
| `SYNCORE_VERIFIER_MX_CACHE_TTL` | Per-domain MX-lookup cache lifetime; `0` disables it | `0` | Go duration | No | Collapses repeat MX lookups for same-domain addresses; must parse as a duration and be `>= 0`. |
| `SYNCORE_VERIFIER_PURGE_INTERVAL` | Interval for the background sweep that drops expired in-memory entries; `0` disables it | `0` | Go duration | No | Only sweeps the in-memory backend (Postgres expires in-DB); must parse as a duration and be `>= 0`. |
| `SYNCORE_VERIFIER_BATCH_MAX_ITEMS` | Max emails per batch request | `100` | positive integer | No | Must be a positive integer. |
| `SYNCORE_VERIFIER_BATCH_CONCURRENCY` | Batch worker-pool size | `10` | positive integer | No | Must be a positive integer. |
| `SYNCORE_VERIFIER_BATCH_MAX_BODY_BYTES` | Max batch request body size (bytes) | `65536` | positive integer | No | Must be a positive integer. |
| `SYNCORE_VERIFIER_DOMAIN_HEALTH` | Enable free SPF/DMARC/MX domain-health lookups | `false` | boolean | No | Must parse as a boolean. |
| `SYNCORE_VERIFIER_GRAVATAR_CHECK` | Enable a per-address Gravatar lookup (engagement signal) | `false` | boolean | No | Adds one external HTTP call per verification; must parse as a boolean. |
| `SYNCORE_VERIFIER_DNSBL_CHECK` | Enable a domain-blocklist (Spamhaus DBL) lookup | `false` | boolean | No | Adds one external DNS lookup per verification; must parse as a boolean. |
| `SYNCORE_VERIFIER_DEV_CONSOLE` | Serve a browser console at `GET /` for trying verifications by hand | `false` | boolean | No | For local use; the page is same-origin and passes through auth/rate-limit like any route. Must parse as a boolean. |
| `SYNCORE_VERIFIER_STORE` | Result-cache / idempotency backend | `memory` | `memory` \| `postgres` | No | Must be `memory` or `postgres`. |
| `SYNCORE_VERIFIER_DATABASE_URL` | Postgres connection string | _(empty)_ | URL | When `STORE=postgres` | Required when `STORE=postgres`. |
| `SYNCORE_VERIFIER_API_KEYS` | Additional accepted credentials (`name:key` or `key`, comma-separated) | _(empty)_ | list | No | Any valid key authenticates like the bearer token; hashed at load. |
| `SYNCORE_VERIFIER_RATE_LIMIT_PER_MINUTE` | Per-client (token/IP) request limit | `0` | non-negative int | No | `0` disables rate limiting. |
| `SYNCORE_VERIFIER_DAILY_QUOTA` | Per-client requests per UTC day | `0` | non-negative int | No | `0` disables; in-memory, resets on restart. |
| `SYNCORE_VERIFIER_WORKERS` | Async-batch worker-pool size | `4` | positive int | No | — |
| `SYNCORE_VERIFIER_ASYNC_BATCH_MAX_ITEMS` | Max emails per `POST /batches` | `10000` | positive int | No | — |
| `SYNCORE_VERIFIER_RETRY_MAX_ATTEMPTS` | Async-batch retries for retryable items | `0` | non-negative int | No | `0` disables retries. |
| `SYNCORE_VERIFIER_RETRY_BACKOFF` | Base backoff between async-batch retries | `0s` | Go duration | No | `0` = no wait. |
| `SYNCORE_VERIFIER_WEBHOOK_SIGNING_KEY` | HMAC key for async-batch completion webhooks | _(empty)_ | string | No | Empty = unsigned. |
| `SYNCORE_VERIFIER_SUPPRESS_EMAILS` | Do-not-verify list (comma-separated) | _(empty)_ | list | No | Suppressed addresses skip all network checks. |
| `SYNCORE_VERIFIER_FEEDBACK_SIGNING_KEY` | HMAC key enabling `POST /v1/feedback` | _(empty)_ | string | No | Empty disables feedback ingestion. |
| `SYNCORE_VERIFIER_FEEDBACK_ADAPTER_TOKEN` | Shared secret enabling the provider adapters `POST /v1/feedback/ses` and `/v1/feedback/smartlead` | _(empty)_ | string | No | Empty disables the adapters. Sent via `X-Syncore-Token` header or `?token=` query. |

`FROM_EMAIL` and `HELLO_NAME` are **validated only when `SMTP_ENABLED=true`**. When SMTP is disabled they are unused and will not block startup even if malformed.

**Safe bind:** startup **fails fast** if `SYNCORE_VERIFIER_BIND_ADDR` is a non-loopback address (e.g. `0.0.0.0`, a LAN IP, or `:port`) while `SYNCORE_VERIFIER_AUTH_TOKEN` is unset — the service is never exposed without authentication. Loopback binds need no token.

For deployment units (systemd/PM2), the co-located-vs-port-25 tradeoff, and the batch timing bound, see [`deploy/`](deploy/README.md).

## HTTP API

Four endpoints. Every response is JSON.

### Authentication

When `SYNCORE_VERIFIER_AUTH_TOKEN` is set, **all verification endpoints** require
an `Authorization: Bearer <token>` header (constant-time compared); a missing or
wrong token returns **401** with the standard error envelope. **`GET /health`
stays open** so probes and load balancers work without a credential. When the
token is unset, auth is disabled (permitted only on a loopback bind — see
Configuration).

```bash
curl.exe -s -X POST -H "Content-Type: application/json" \
  -H "Authorization: Bearer $env:SYNCORE_VERIFIER_AUTH_TOKEN" \
  -d "{\"email\":\"person@example.com\"}" http://127.0.0.1:8080/v1/verifications
```

### `GET /health`

Liveness only — performs **no** DNS, SMTP, or provider checks.

```powershell
Invoke-RestMethod http://127.0.0.1:8080/health
```
```bash
curl.exe http://127.0.0.1:8080/health     # Windows
curl      http://127.0.0.1:8080/health     # Git Bash
```
```json
{ "status": "ok" }
```

### `GET /v1/{email}/verification`

The **preserved legacy endpoint**, extended: it returns all legacy response fields (`email`, `reachable`, `syntax`, `smtp`, `gravatar`, `suggestion`, `disposable`, `role_account`, `free`, `has_mx_records`) plus the Phase 1A additive evidence (`null_mx`, `mail_host_source`, and — inside `smtp` — `recipient_result`, `recipient_reason`, `smtp_code`, `catch_all_result`, `source`) and **appends** the Phase 1 classification: `status`, `reason_code`, `retryable`, `confidence`, `checked_at`, `smtp_attempted`, `smtp_check_reason`, `source`, `error`.

```powershell
Invoke-RestMethod http://127.0.0.1:8080/v1/person@example.com/verification
```
```bash
curl.exe "http://127.0.0.1:8080/v1/person@example.com/verification"
```

### `POST /v1/verifications`

The structured endpoint. Request body:

```json
{ "email": "person@example.com" }
```

```powershell
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/v1/verifications `
  -ContentType 'application/json' -Body '{"email":"person@example.com"}'
```
```bash
# Windows curl.exe
curl.exe -s -X POST -H "Content-Type: application/json" -d "{\"email\":\"person@example.com\"}" http://127.0.0.1:8080/v1/verifications
# Git Bash
curl -s -X POST -H 'Content-Type: application/json' -d '{"email":"person@example.com"}' http://127.0.0.1:8080/v1/verifications
```

Structured response:

```json
{
  "email": "person@example.com",
  "status": "valid",
  "reason_code": "smtp_accepted",
  "retryable": false,
  "confidence": 95,
  "deliverability_score": 95,
  "checked_at": "2026-07-13T19:05:29Z",
  "source": "smtp",
  "syntax": { "username": "person", "domain": "example.com", "valid": true },
  "domain": {
    "has_mx_records": true, "null_mx": false, "implicit_mx": false,
    "mail_host_source": "mx", "disposable": false, "free_provider": false, "suggestion": ""
  },
  "account": { "role_account": false },
  "smtp": {
    "host_exists": true, "deliverable": true, "catch_all": false, "catch_all_result": "not_catch_all",
    "full_inbox": false, "disabled": false, "recipient_result": "accepted", "recipient_reason": "",
    "smtp_code": 250, "smtp_attempted": true, "smtp_check_reason": "attempted"
  },
  "error": null
}
```

Two additive fields accompany the classification:

- **`deliverability_score`** (0–100) — a deterministic, network-free estimate of how likely the address is to **accept mail**, derived from status + evidence. It is distinct from `confidence` (our certainty in the *classification*): an `invalid` result scores `0`, a clean `valid` scores high, and a `disposable` or `catch_all` address scores low even when we are confident. This is a v1 heuristic; per-domain reputation refinement is future work.
- **`domain.domain_health`** — present only when `SYNCORE_VERIFIER_DOMAIN_HEALTH=true` and the domain resolves. Reports free DNS hygiene signals: `spf` (a `v=spf1` record), `dmarc` (a `v=DMARC1` policy at `_dmarc.<domain>`), and `mx` (a usable mail host). DKIM is intentionally omitted — it is selector-specific and cannot be verified without a signed message. These signals never change the classification.
- **`domain.blocklisted`** — present only when `SYNCORE_VERIFIER_DNSBL_CHECK=true` and the domain resolves. `true` if the domain is on a domain-based blocklist (Spamhaus DBL — spam/phishing/malware domains). A listing **caps the `deliverability_score`** (a strong "do not send" signal) but **never** changes the classification. One extra DNS lookup; Spamhaus error/blocked return codes are treated as *not listed* to avoid false positives.
- **`account.gravatar`** — present only when `SYNCORE_VERIFIER_GRAVATAR_CHECK=true`. A public Gravatar profile (`has_gravatar` + `url`) is a weak positive **engagement** signal. It gives a small bonus to the `deliverability_score` of an *uncertain* result (`unknown`/`risky` only — never a `valid` or `invalid`), always capped by a poor `domain.reputation`, and it **never** changes the classification. Off by default because it adds one external HTTP call per verification.

### `POST /v1/verifications:batch`

Verify a bounded list of emails in one request. Stateless — no persistence, no queue. Results are returned **one per input, in order**; a single bad or faulty item never fails the batch (it comes back as its own classification, e.g. `invalid`/`syntax_invalid`, or `unknown`/`retryable` on an internal fault). An optional `meta` object is echoed back verbatim.

Request body (`emails` bounded by `SYNCORE_VERIFIER_BATCH_MAX_ITEMS`, default 100):

```json
{ "emails": ["a@example.com", "b@example.com"], "meta": { "batch_id": "abc" } }
```

```bash
curl.exe -s -X POST -H "Content-Type: application/json" \
  -H "Authorization: Bearer $env:SYNCORE_VERIFIER_AUTH_TOKEN" \
  -d "{\"emails\":[\"a@example.com\",\"b@example.com\"]}" \
  http://127.0.0.1:8080/v1/verifications:batch
```

Response:

```json
{
  "results": [ { "email": "a@example.com", "status": "valid",  "...": "..." },
               { "email": "b@example.com", "status": "invalid", "...": "..." } ],
  "meta": { "batch_id": "abc" }
}
```

Items run through a **bounded worker pool** (`SYNCORE_VERIFIER_BATCH_CONCURRENCY`, default 10) so batches never stampede providers. Over-cap, empty, or missing `emails` → **400**; an oversize body (`SYNCORE_VERIFIER_BATCH_MAX_BODY_BYTES`) → **413**. Because a full batch can take up to `ceil(max_items / concurrency) × (connect + operation)` timeout, the server `WriteTimeout` is sized to cover it and **the CRM must chunk to `BATCH_MAX_ITEMS`** — the bound is documented in [`deploy/`](deploy/README.md).

### Classification model

`status` is one of **`valid`**, **`invalid`**, **`risky`**, **`unknown`**, backed by a `reason_code` with fixed `retryable` and `confidence` (0–100):

| reason_code | status | retryable | confidence |
|---|---|---|---|
| `smtp_accepted` | valid | false | 95 |
| `syntax_invalid` | invalid | false | 100 |
| `domain_not_found` | invalid | false | 99 |
| `null_mx` | invalid | false | 99 |
| `no_mail_host` | invalid | false | 95 |
| `mailbox_rejected` | invalid | false | 90 |
| `disposable_domain` | risky | false | 90 |
| `mailbox_disabled` | risky | false | 80 |
| `role_account` | risky | false | 70 |
| `full_inbox` | risky | true | 60 |
| `catch_all` | risky | false | 50 |
| `temporary_rejection` | unknown | true | 20 |
| `rate_limited` | unknown | true | 15 |
| `provider_blocked` | unknown | true | 15 |
| `dns_error` | unknown | true | 10 |
| `smtp_timeout` | unknown | true | 10 |
| `connection_refused` | unknown | true | 10 |
| `smtp_inconclusive` | unknown | true | 5 |
| `smtp_disabled` | unknown | false | 30 |

**Evidence fields:**
- **`source`** — how deliverability was checked: `smtp` (port-25 conversation), `api` (Gmail/Yahoo API path), or empty when SMTP was not attempted.
- **SMTP evidence** — `host_exists`, `deliverable`, `full_inbox`, `disabled`, plus the recipient-level `recipient_result` (`accepted` / `rejected` / `temporary` / `blocked` / `unknown` / `not_checked`), `recipient_reason`, and sanitized `smtp_code`.
- **Tri-state catch-all** — `catch_all_result` is one of `confirmed`, `not_catch_all`, `unknown`, or `not_checked`. A timeout, temporary rejection, or provider block during the catch-all probe yields **`unknown`** — it is **never** reported as `catch_all=true`.
- **Catch-all sub-confidence** — for a *confirmed* catch-all (where per-mailbox checks are impossible), `smtp.catch_all_confidence` refines the guess from the domain's real feedback-loop bounce history: `likely_valid` (reliably delivers → the `deliverability_score` is lifted above the flat catch-all baseline), `likely_invalid` (poor history → the reputation cap lowers the score), or `unknown` (no/insufficient history). It **never** changes the `risky`/`catch_all` classification. Active automatically once the feedback loop has data for the domain.
- **MX evidence** — `has_mx_records`, `mail_host_source` (`mx` / `a` / `aaaa` / `null` / `none`), and `implicit_mx`.
- **Null MX** — a domain publishing an RFC 7505 Null MX (`.` target) is `invalid` / `null_mx` and is **not** probed further.
- **Implicit A/AAAA delivery** — when a domain has no MX records but has an A/AAAA record, that host is used as an implicit mail exchanger (`mail_host_source` = `a` or `aaaa`); the absence of an explicit MX record alone is **not** treated as invalid.
- **`deliverability_score` + `score_components`** — a deterministic 0–100 estimate of how likely the address is to accept mail, decomposed into `syntax` / `domain` / `mailbox` sub-scores. Distinct from `confidence` (certainty in the classification).
- **`suppressed`** — `true` when the address is on the do-not-verify list (`SYNCORE_VERIFIER_SUPPRESS_EMAILS`); such an address returns `risky` + `suppressed:true` with **no network check performed**.

## Enterprise endpoints & capabilities

All of the following are additive and config-flagged — unset variables leave behavior unchanged. Every route except `/health` and `/ready` requires auth when a token or API key is configured.

**Authentication.** A global bearer token (`SYNCORE_VERIFIER_AUTH_TOKEN`) and/or multiple **API keys** (`SYNCORE_VERIFIER_API_KEYS`, hashed at load). Optional per-client **rate limiting** (`SYNCORE_VERIFIER_RATE_LIMIT_PER_MINUTE` → `429`) and a per-client **daily quota** (`SYNCORE_VERIFIER_DAILY_QUOTA` → `429 quota_exceeded`).

**Async batch** (in-memory jobs; `WORKERS`, `ASYNC_BATCH_MAX_ITEMS`, `RETRY_*`, `WEBHOOK_SIGNING_KEY`):
- `POST /batches` — `{ "emails": [...], "callback_url"?, "meta"? }` → `202 { "batch_id", "state", "total" }`.
- `GET /batches/{id}` — progress + per-status counts.
- `GET /batches/{id}/results?offset=&limit=` — paginated results.
- Retryable (`unknown`) items are retried up to `RETRY_MAX_ATTEMPTS`; on completion an **HMAC-SHA256-signed** webhook (`X-Syncore-Signature`) is POSTed to `callback_url`.

**Persistence** (`SYNCORE_VERIFIER_STORE=postgres` + `DATABASE_URL`): the result cache and idempotency store are backed by Postgres (jsonb + TTL). `Idempotency-Key` on `POST /v1/verifications` returns the stored result without re-verifying.

**Feedback loop** (`SYNCORE_VERIFIER_FEEDBACK_SIGNING_KEY`): `POST /v1/feedback` ingests a signed `{ "email", "type" }` outcome (`delivered`/`bounced`/`complained`/`engaged`) into per-domain reputation priors (body must carry a valid `X-Syncore-Signature`). **The loop is closed:** those priors surface as `domain.reputation` on subsequent verifications and pull down `deliverability_score` for a domain with a poor real-world bounce history (never the classification).

**Provider adapters** (`SYNCORE_VERIFIER_FEEDBACK_ADAPTER_TOKEN`): instead of running a forwarder that re-signs each event, point your ESPs straight at:

- **`POST /v1/feedback/ses`** — accepts **raw Amazon SES events delivered over SNS** (and SNS `SubscriptionConfirmation` handshakes). Permanent bounces → `bounced`, complaints → `complained`, deliveries → `delivered`, opens/clicks → `engaged` (transient bounces are ignored). SNS delivers with `Content-Type: text/plain`, which this endpoint accepts.
- **`POST /v1/feedback/smartlead`** — accepts **raw Smartlead webhook events**. `EMAIL_BOUNCE` → `bounced`, `EMAIL_REPLY`/`OPEN`/`CLICK` → `engaged`, `EMAIL_SENT` → `delivered`.

Both are gated by the shared `FEEDBACK_ADAPTER_TOKEN`, sent in the `X-Syncore-Token` header **or** a `?token=` query parameter (constant-time compared). The query form exists because SNS HTTPS subscriptions cannot set custom headers — put the token in the subscription URL. On success they return `{ "accepted": true, "recorded": <n> }`.

```bash
# Smartlead example (Git Bash)
curl -s -X POST -H 'X-Syncore-Token: <token>' -H 'Content-Type: application/json' \
  -d '{"event_type":"EMAIL_BOUNCE","to_email":"bad@example.com"}' \
  http://127.0.0.1:8080/v1/feedback/smartlead
```

> Auth note: the adapters use the shared token as the gate. Full Amazon SNS message-signature verification (fetching the signing certificate chain) is a documented future hardening — until then, keep the endpoint private and rely on the token.

**Compliance.** `POST /admin/erasure` `{ "email" }` removes an address's cached data (right-to-erasure). Verifications and erasures emit a structured audit event carrying only a **SHA-256 of the email** — never plaintext.

**Observability.** `GET /metrics` (Prometheus text; auth-protected) exposes `verifications_total{status}`, `http_requests_total{route,method,code}`, and a request-duration histogram. `GET /ready` reports readiness (pings Postgres when configured). Structured JSON access logs carry a per-request `X-Request-ID` and never log the email.

## HTTP response behavior

| Situation | Status |
|---|---|
| Any completed verification (`valid`/`invalid`/`risky`/`unknown`, incl. invalid syntax, timeouts, Null MX, no mail host) | **200** |
| Malformed request structure (bad/duplicate JSON, unknown field, missing/empty/over-254-byte/control-character email) | **400** |
| Missing or invalid bearer token (when `AUTH_TOKEN` is set; `/health` is exempt) | **401** |
| Unknown route | **404** |
| Unsupported method on a known route | **405** (with `Allow` header) |
| Request body larger than `MAX_BODY_BYTES` | **413** |
| Missing or non-JSON `Content-Type` | **415** |
| Genuine unexpected internal fault | **500** |

Error responses use a consistent envelope and never leak raw SMTP text, IP addresses, proxy URLs, credentials, or stack traces:

```json
{ "error": { "code": "invalid_request", "message": "..." } }
```

**Invalid email syntax is a completed result, not a request error:** a well-formed request whose email merely fails syntax validation returns **HTTP 200** with `status = "invalid"` and `reason_code = "syntax_invalid"`.

## Interpreting results

The classification is an input to your outreach decisions, not a delivery guarantee:

- **`valid`** — suitable for normal outreach handling.
- **`invalid`** — suppress.
- **`risky`** — review, or use cautiously.
- **`unknown` with `retryable = true`** — retry later.
- **`unknown` with `retryable = false`** — leave unresolved or review manually.

**`valid` does not guarantee** message delivery, inbox placement, recipient engagement, or the absence of spam filtering. It means the receiving server accepted the recipient at check time.

## SMTP limitations

Mailbox checks are performed honestly over **SMTP port 25**, and:

- Outbound port 25 is frequently **blocked** by residential, office, ISP, and cloud networks. When it is blocked, mailbox checks cannot complete.
- **Gmail commonly works** from a given host while **Microsoft, Yahoo, or Apple time out** on the same host.
- **Timeouts, temporary rejections, and provider blocking become `unknown` — never `invalid`.**
- **No email message is ever sent.**
- Receiving providers may **intentionally conceal** whether a mailbox exists (e.g. catch-all, greylisting, uniform responses).
- **Verification results can change over time** — re-checking later can yield a different outcome.

## Timeouts & cancellation

- **`SYNCORE_VERIFIER_CONNECT_TIMEOUT`** bounds each SMTP TCP connect.
- **`SYNCORE_VERIFIER_OPERATION_TIMEOUT`** is a single deadline covering the SMTP conversation (`HELO`/`MAIL`/`RCPT`) after connecting.
- The HTTP server's **`WriteTimeout`** is derived from configuration so it never truncates a legitimate verification: it is at least **`ConnectTimeout + OperationTimeout + 15s`, with a 35-second floor**.
- **Request cancellation (client disconnect) does not currently stop an in-flight DNS or SMTP operation.** The real bound is the engine's own connect/operation deadlines.
- **DNS relies on the operating-system resolver's behavior** and has no separate application timeout.

These are **known Phase 1 limitations**.

## Testing

The default suite is deterministic and contacts **no** public network:

```
go test ./...
```

Race detector (see the note below on Windows):

```
go test -race ./...
```

Live tests — **these contact public DNS, SMTP, HTTP, and provider services** (Gmail/Yahoo/etc.) and are excluded from the default suite:

```
go test -tags=live ./...
```

**Race detector on Windows** requires:

- `CGO_ENABLED=1`, and
- a compatible C compiler such as **MinGW-w64 GCC** on `PATH`.

If a C compiler is not installed, `go test -race ./...` cannot run on Windows. **Linux CI is the authoritative race-detector run** (see [`.github/workflows/ci.yml`](.github/workflows/ci.yml)); do not treat a local Windows run as authoritative.

---

# email-verifier

✉️ A Go library for email verification without sending any emails.

[![Build Status](https://github.com/AfterShip/email-verifier/workflows/CI%20Actions/badge.svg)](https://github.com/AfterShip/email-verifier/actions)
[![Godoc](http://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://godoc.org/github.com/AfterShip/email-verifier)
[![Coverage Status](https://coveralls.io/repos/github/AfterShip/email-verifier/badge.svg?t=VTgVfL)](https://coveralls.io/github/AfterShip/email-verifier)
[![Go Report Card](https://goreportcard.com/badge/github.com/AfterShip/email-verifier)](https://goreportcard.com/report/github.com/AfterShip/email-verifier)
[![license](http://img.shields.io/badge/license-MIT-red.svg?style=flat)](https://github.com/AfterShip/email-verifier/blob/main/LICENSE)

## Features

- Email Address Validation: validates if a string contains a valid email.
- Email Verification Lookup via SMTP: performs an email verification on the passed email (catchAll detection enabled by default)
- MX Validation: checks the DNS MX records for the given domain name
- Misc Validation: including Free email provider check, Role account validation, Disposable emails address (DEA) validation
- Email Reachability: checks how confident in sending an email to the address

## Install

Use `go get` to install this package.

```shell script
go get -u github.com/AfterShip/email-verifier
```

## Usage

### Basic usage

Use `Verify` method to verify an email address with different dimensions

```go
package main

import (
	"fmt"
	
	emailverifier "github.com/AfterShip/email-verifier"
)

var (
	verifier = emailverifier.NewVerifier()
)


func main() {
	email := "example@exampledomain.org"

	ret, err := verifier.Verify(email)
	if err != nil {
		fmt.Println("verify email address failed, error is: ", err)
		return
	}
	if !ret.Syntax.Valid {
		fmt.Println("email address syntax is invalid")
		return
	}

	fmt.Println("email validation result", ret)
	/*
		result is:
		{
			"email":"example@exampledomain.org",
			"disposable":false,
			"reachable":"unknown",
			"role_account":false,
			"free":false,
			"syntax":{
			"username":"example",
				"domain":"exampledomain.org",
				"valid":true
			},
			"has_mx_records":true,
			"smtp":null,
			"gravatar":null
		}
	*/
}
```

### Email verification Lookup

Use `CheckSMTP` to performs an email verification lookup via SMTP.

```go
var (
    verifier = emailverifier.
        NewVerifier().
        EnableSMTPCheck()
)

func main() {

    domain := "domain.org"
    username := "username"
    ret, err := verifier.CheckSMTP(domain, username)
    if err != nil {
        fmt.Println("check smtp failed: ", err)
        return
    }

    fmt.Println("smtp validation result: ", ret)

}
```

If you want to disable catchAll checking, use the `DisableCatchAllCheck()` switch (in effect only when SMTP verification is enabled).

```go
 verifier = emailverifier.
        NewVerifier().
        EnableSMTPCheck().
        DisableCatchAllCheck()
```

> Note: because most of the ISPs block outgoing SMTP requests through port 25 to prevent email spamming, the module will not perform SMTP checking by default. You can initialize the verifier with  `EnableSMTPCheck()`  to enable such capability if port 25 is usable, 
> or use a socks proxy to connect over SMTP

### Use a SOCKS5 proxy to verify email 

Support setting a SOCKS5 proxy to verify the email, proxyURI should be in the format: `socks5://user:password@127.0.0.1:1080?timeout=5s`

The protocol could be socks5, socks4 and socks4a.

```go
var (
    verifier = emailverifier.
        NewVerifier().
        EnableSMTPCheck().
    	Proxy("socks5://user:password@127.0.0.1:1080?timeout=5s")
)

func main() {

    domain := "domain.org"
    username := "username"
    ret, err := verifier.CheckSMTP(domain, username)
    if err != nil {
        fmt.Println("check smtp failed: ", err)
        return
    }

    fmt.Println("smtp validation result: ", ret)

}
```

### Misc Validation

To check if an email domain is disposable via `IsDisposable`

```go
var (
    verifier = emailverifier.
        NewVerifier().
        EnableAutoUpdateDisposable()
)

func main() {
    domain := "domain.org"
    if verifier.IsDisposable(domain) {
        fmt.Printf("%s is a disposable domain\n", domain)
        return
    }
    fmt.Printf("%s is not a disposable domain\n", domain)
}
```

> Note: It is possible to automatically update the disposable domains daily by initializing verifier with `EnableAutoUpdateDisposable()`

### Suggestions for domain typo

Will check for typos in an email domain in addition to evaluating its validity. 
If we detect a possible typo, you will find a non-empty "suggestion" field in the validation result containing what we believe to be the correct domain.
Also, you can use the `SuggestDomain()` method alone to check the domain for possible misspellings

```go
func main() {
    domain := "gmai.com"
    suggestion := verifier.SuggestDomain(domain) 
    // suggestion should be `gmail.com`
    if suggestion != "" {
        fmt.Printf("domain %s is misspelled, right domain is %s. \n", domain, suggestion)
        return 
    }
    fmt.Printf("domain %s has no possible misspellings. \n", domain)
}

```

> Note: When using the `Verify()` method, domain typo checking is not enabled by default, you can enable it in a verifier with `EnableDomainSuggest()`
 
For more detailed documentation, please check on godoc.org 👉 [email-verifier](https://godoc.org/github.com/AfterShip/email-verifier)

## API 

We provide a simple **self-hosted** [API server](https://github.com/AfterShip/email-verifier/tree/main/cmd/apiserver) script for reference.

The API interface is very simple. All you need to do is to send a GET request with the following URL.

The `email` parameter would be the target email you want to verify.

`https://{your_host}/v1/{email}/verification`

## Similar Libraries Comparison

|                                     | [email-verifier](https://github.com/AfterShip/email-verifier) | [trumail](https://github.com/trumail/trumail) | [check-if-email-exists](https://reacher.email/) | [freemail](https://github.com/willwhite/freemail) |
| ----------------------------------- | :----------------------------------------------------------: | :-------------------------------------------: | :---------------------------------------------: | :-----------------------------------------------: |
| **Features**                        |                              〰️                              |                      〰️                       |                       〰️                        |                        〰️                         |
| Disposable email address validation |                              ✅                               |       ✅, but not available in free lib        |                        ✅                        |                         ✅                         |
| Disposable address autoupdate       |                              ✅                               |                       🤔                       |                        ❌                        |                         ❌                         |
| Free email provider check           |                              ✅                               |       ✅, but not available in free lib        |                        ❌                        |                         ✅                         |
| Role account validation             |                              ✅                               |                       ❌                       |                        ✅                        |                         ❌                         |
| Syntax validation                   |                              ✅                               |                       ✅                       |                        ✅                        |                         ❌                         |
| Email reachability                  |                              ✅                               |                       ✅                       |                        ✅                        |                         ❌                         |
| DNS records validation              |                              ✅                               |                       ✅                       |                        ✅                        |                         ❌                         |
| Email deliverability                |                              ✅                               |                       ✅                       |                        ✅                        |                         ❌                         |
| Mailbox disabled                    |                              ✅                               |                       ✅                       |                        ✅                        |                         ❌                         |
| Full inbox                          |                              ✅                               |                       ✅                       |                        ✅                        |                         ❌                         |
| Host exists                         |                              ✅                               |                       ✅                       |                        ✅                        |                         ❌                         |
| Catch-all                           |                              ✅                               |                       ✅                       |                        ✅                        |                         ❌                         |
| Gravatar                            |                              ✅                               |       ✅, but not available in free lib        |                        ❌                        |                         ❌                         |
| Typo check                          |                              ✅                              |       ✅, but not available in free lib        |                        ❌                        |                         ❌                         |
| Use proxy to connect over SMTP      |                              ✅                              |                        ❌                       |                        ✅                        |                         ❌                         |
| Honeyport dection                   |                              🔜                               |                       ❌                       |                        ❌                        |                         ❌                         |
| Bounce email check                  |                              🔜                               |                       ❌                       |                        ❌                        |                         ❌                         |
| **Tech**                            |                              〰️                              |                      〰️                       |                       〰️                        |                        〰️                         |
| Provide API                         |                              ✅                               |                       ✅                       |                        ✅                        |                         ❌                         |
| Free API                            |                              ✅                               |                       ❌                       |                        ❌                        |                         ❌                         |
| Language                            |                              Go                              |                      Go                       |                      Rust                       |                       JavaScript                        |
| Active maintain                     |                              ✅                               |                       ❌                       |                        ✅                        |                         ✅                         |
| High Performance                    |                              ✅                               |                       ❌                       |                        ✅                        |                         ✅                         |



## FAQ

#### The library hangs/takes a long time after 30 seconds when performing email verification lookup via SMTP

Most ISPs block outgoing SMTP requests through port 25 to prevent email spamming. `email-verifier` needs to have this port open to make a connection to the email's SMTP server. With the port being blocked, it is not possible to perform such checking, and it will instead hang until timeout error. Unfortunately, there is no easy workaround for this issue.

For more information, you may also visit [this StackOverflow thread](https://stackoverflow.com/questions/18139102/how-to-get-around-an-isp-block-on-port-25-for-smtp).

#### The output shows `"connection refused"` in the `smtp.error` field.

This error can also be due to SMTP ports being blocked by the ISP, see the above answer.

#### What does reachable: "unknown" means

This means that the server does not allow real-time verification of an email right now, or the email provider is a catch-all email server.

## Credits

- [trumail](https://github.com/trumail/trumail)
- [check-if-email-exists](https://github.com/amaurymartiny/check-if-email-exists)
- [mailcheck](https://github.com/mailcheck/mailcheck)
- disposable domains from [ivolo/disposable-email-domains](https://github.com/ivolo/disposable-email-domains)
- free provider data from [willwhite/freemail](https://github.com/willwhite/freemail)

## Contributing

For details on contributing to this repository, see the [contributing guide](https://github.com/AfterShip/email-verifier/blob/main/CONTRIBUTING.md).

## License

This package is licensed under MIT license. See [LICENSE](https://github.com/AfterShip/email-verifier/blob/main/LICENSE) for details.
