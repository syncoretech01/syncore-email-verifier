# Syncore Email Verifier — change log

Syncore Email Verifier is a customized internal fork of [AfterShip/email-verifier](https://github.com/AfterShip/email-verifier). The upstream MIT licence and attribution are preserved; upstream release notes follow below.

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
