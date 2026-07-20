# Syncore Email Verifier — change log

Syncore Email Verifier is a customized internal fork of [AfterShip/email-verifier](https://github.com/AfterShip/email-verifier). The upstream MIT licence and attribution are preserved; upstream release notes follow below.

## Growth OS foundation — auth, cache, batch, domain health

Adds the net-new surface the CRM needs to call this service as Layer 1 of the verification waterfall. Additive and config-flagged: with no new variables set, behavior is unchanged.

**Authentication & safe bind**
- Optional `SYNCORE_VERIFIER_AUTH_TOKEN`. When set, all verification endpoints require `Authorization: Bearer <token>` (constant-time compare); `/health` stays open; missing/invalid → **401**.
- Startup **fails fast** if bound to a non-loopback address with no token set, so the service is never exposed unauthenticated.

**Result cache (persistence seam)**
- New `internal/store` package: a generic, concurrency-safe TTL `Store[V]` with a bounded in-memory backend (a durable backend can implement the same interface later).
- Optional result cache (`SYNCORE_VERIFIER_CACHE_TTL`, off by default): caches terminal results for the full TTL and retryable `unknown` results for a shorter TTL, sparing repeat DNS/SMTP work. Classification is never altered.

**Batch endpoint**
- New **`POST /v1/verifications:batch`** — stateless, bounded (`SYNCORE_VERIFIER_BATCH_MAX_ITEMS`), ordered results, per-item fault isolation (a bad item never fails the batch), processed through a bounded worker pool (`SYNCORE_VERIFIER_BATCH_CONCURRENCY`).
- Server `WriteTimeout` is now batch-aware and covers the documented worst-case batch duration; the bound is stated in `deploy/`.

**Deliverability score & domain health**
- Additive **`deliverability_score`** (0–100): a deterministic, network-free estimate of how likely an address is to accept mail, distinct from `confidence`.
- Optional **`domain_health`** (`SYNCORE_VERIFIER_DOMAIN_HEALTH`, off by default): free SPF/DMARC/MX DNS signals folded into the domain evidence; the classifier stays pure.

**Deployment**
- New `deploy/` unit files (systemd + PM2), a vault-populated env template, and a deployment guide documenting the co-located-vs-port-25 tradeoff, the port-25 reality, and the batch timing bound.

## Phase 1 — local verification service

Turns the upstream reference API into a clean, local, single-instance verification service with a structured classification model. No database, queue, retry worker, bulk upload, authentication, paid provider, frontend, or CRM integration.

**Engine (Phase 1A)**
- Enhanced MX evidence and recipient-level SMTP outcomes: `recipient_result`, `recipient_reason`, sanitized `smtp_code`, and verification `source` (`smtp`/`api`).
- **Null MX** support (RFC 7505 `.` target) → classified as `invalid`.
- **Implicit A/AAAA mail delivery**: a domain with no MX but an A/AAAA record uses that host as an implicit mail exchanger (`mail_host_source` = `a`/`aaaa`).
- **Tri-state catch-all detection** (`catch_all_result`: `confirmed` / `not_catch_all` / `unknown` / `not_checked`); catch-all is asserted only when confirmed.
- **Fixed a data race** in the concurrent multi-MX dialer (unsynchronized completion flag).
- **Fixed a blocked-send / goroutine leak** in the multi-MX dialer (buffered results + background drain of unused successful clients).
- Instance-scoped, race-safe DNS/SMTP dependencies (no mutable package globals); SOCKS proxy support preserved.
- **Deterministic in-process fake SMTP server tests**; **live-network tests isolated** behind the `live` build tag.

**Classification & service (Phase 1B)**
- `valid` / `invalid` / `risky` / `unknown` classifications with a centralized reason-code table carrying `status`, `retryable`, and `confidence`.
- Pure classifier with an explicit precedence ladder (no I/O, network, or clock).
- Verification service that orchestrates the granular engine methods (not the monolithic `Verify`), builds an internal `Assessment`, applies an injected UTC clock for `checked_at`, and emits sanitized error evidence.

**Runtime configuration & HTTP API (Phase 1C)**
- `SYNCORE_VERIFIER_*` **environment configuration** with startup validation; `.env.example` is documentation only (no dotenv dependency, `.env` not auto-loaded); `FROM_EMAIL`/`HELLO_NAME` validated only when SMTP is enabled.
- **Extended legacy `GET /v1/{email}/verification`** (all legacy + additive fields, plus appended classification).
- **Structured `POST /v1/verifications`** endpoint.
- **`GET /health`** liveness endpoint.
- **JSON for every response**, including a consistent error envelope.
- **HTTP input protection**: body-size limit (413), media-type check (415), `DisallowUnknownFields`, trailing-data rejection, and length/control-character limits.
- **Hardened server timeouts** (`ReadHeaderTimeout`, `ReadTimeout`, `IdleTimeout`, `MaxHeaderBytes`; `WriteTimeout` derived from configuration), **panic recovery** (500 JSON, no leaked internals), and **graceful shutdown** on `SIGINT`/`SIGTERM`.

**Verification**
- **Linux CI performs the authoritative race-detector execution** (`go test -race ./...`); the race detector requires cgo and is not run on the Windows development host.

## [Change log](https://github.com/AfterShip/email-verifier/releases)

v1.4.0
----------
* Feature: Support Gmail&Yahoo SMTP check by API [#76](https://github.com/AfterShip/email-verifier/pull/88)
* Optimization: Return HasMXRecord as true when at least one valid mx records exist [#94](https://github.com/AfterShip/email-verifier/pull/94)
* Update Dependencies

v1.3.3
----------
* Making catchAll detection optional [#76](https://github.com/AfterShip/email-verifier/pull/76)
* When the user enables `EnableAutoUpdateDisposable()`, the disposable domains configuration is updated once by default.
* Update test cases
* Update Dependencies

v1.3.2
----------
* Uses x/net/proxy to fix issue when using SOCKS5

v1.3.1
----------
* Fix a bug: `IsDisposable()` matches the complete email domain
* Update dependent metadata
* Update Dependencies

v1.3.0
----------
* Support setting SOCKS5 proxy to perform `CheckSMTP()`
* Make pkg compatible with earlier versions of Go

v1.2.0
----------
* Support adding custom disposable email domains 
* Fix a wrong reference in README 
* Update dependent metadata  
* Update Dependencies

v1.1.0
----------
* Performance optimization:
    * reduce Result struct size from 96 to 80
    * `ParseAddress()` return `Syntax` instead of reference, for reducing GC pressure and improve memory locality.
* Provide a simple API server
* Bugfix: gravatar images may not exist

v1.0.3
----------
* Add a New feature: domain suggestion (typo check)

v1.0.2
----------
* Add build metadata tools to generate metadata_*.go files 
* Update load meta data logic
