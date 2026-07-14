# Syncore Email Verifier — Phase 1 Plan (Revised 3)

**Status:** Final planning revision. Incorporates D1–D8 plus the final corrections (Null MX, tri-state catch-all, `smtp_inconclusive`, tightened nonexistent matching, instance-scoped race-safe seams, explicit service evidence, API-verifier evidence, conditional SMTP-config validation, content-type params, `go test ./...` primary). **No Phase 1 code written yet.** Awaiting final approval.
**Branch:** `feature/verification-foundation`
**Scope:** Turn the upstream reference API into a clean, local, single-instance verification service with a structured classification model. **No database, queue, frontend, bulk upload, paid provider, or CRM integration.**

**All decisions resolved** (§13). Env prefix `SYNCORE_VERIFIER_`. Engine changes are additive-only. Implicit A/AAAA delivery and RFC 7505 Null MX both supported. In-process fake SMTP server mandatory and race-clean.

---

## 1. Repository audit

### 1.1 What this repo is

A fork of `AfterShip/email-verifier` (Go library, module path `github.com/AfterShip/email-verifier`, MIT © 2020 AfterShip). Root package `emailverifier` is the engine; `cmd/apiserver` is a thin reference HTTP server.

| File | Role |
|---|---|
| `verifier.go` | `Verifier`, options, `Verify()` orchestration, `calculateReachable()` |
| `address.go` | Syntax validation (`ParseAddress`, `IsAddressValid`) |
| `mx.go` | `CheckMX()` — `net.LookupMX` wrapper |
| `smtp.go` | `CheckSMTP()`, concurrent MX dial (`newSMTPClient`), `dialSMTP`, catch-all probe |
| `smtp_by_api*.go` | Non-SMTP "API" verifier interface + Yahoo signup-page scraper |
| `error.go` | `ParseSMTPError()` — maps SMTP reply text/codes to `LookupError` |
| `misc.go` | `IsRoleAccount`, `IsFreeDomain`, `IsDisposable` |
| `suggestion.go` | Levenshtein domain typo suggestion |
| `metadata_*.go` | Generated disposable / free / role / suggestion data |
| `schedule.go`, `handler.go` | Daily disposable refresh + remote fetch |
| `cmd/apiserver/main.go` | Reference GET server (hardcoded config) |
| `*_test.go` | Tests — **most hit the live network** |

### 1.2 Request & verification flow (current)

**HTTP** (`cmd/apiserver/main.go`): one package-level verifier with hardcoded options; route `GET /v1/:email/verification`; `err != nil` → **plain-text 500**; `!Syntax.Valid` → **plain-text 400**; else raw `Result` JSON. Binds `127.0.0.1:8080`.

**Engine** (`Verify`): `ParseAddress` → free/role/disposable (+suggest) → (disposable short-circuit) → `CheckMX` (only `no such host` handled specially; other errors raw) → `CheckSMTP` (concurrent MX dial :25; optional Gmail/Yahoo API path; `HELO`/`MAIL`; **catch-all defaults to true**, random RCPT; real RCPT where **`err==nil ⇒ Deliverable=true` and any error is discarded**) → `calculateReachable`.

### 1.3 Confirmed working

Syntax, MX, disposable, free-provider, role-account, Gmail SMTP, single-verifier reuse.

---

## 2. Technical risks, bugs & inconsistencies

**R1 — SMTP uncertainty → HTTP 500 (blocker).** Timeouts/refusals/temporary rejections return a non-nil error → plain-text 500.

**R2 — Temporary vs permanent recipient failure indistinguishable (blocker).** Real-address `RCPT` error discarded (`smtp.go:108`).

**R3 — No-MX domains error out.** Domain with A/AAAA but no MX → `newSMTPClient` re-`LookupMX` → `"No MX records found"` → 500. Must use implicit A/AAAA (D7), except when Null MX is published (correction 1).

**R4 — `calculateReachable` ignores `Disabled`/`FullInbox`; catch-all → `unknown`.** New model: disabled/full/catch-all are `risky`.

**R5 — Non-`no-such-host` DNS errors → 500.** Must be `unknown/dns_error`.

**R6 — Typed-nil error hazard.** `ParseSMTPError` nil-for-≤400 becomes a non-nil typed-nil `error` → spurious 500.

**R7 — Tests depend on public services (blocker).** github/gmail/yahoo + live Yahoo scrape.

**R8 — Redundant double MX lookup** (`CheckMX` then `newSMTPClient`). Accepted minor Phase-1 duplication; noted.

**R9 — Yahoo `isSupported` over-broad + non-deterministic.** Excluded from default suite.

**R10 — Disposable short-circuits before MX/SMTP.** Classifier must not expect MX/SMTP evidence when disposable.

**R11 — Catch-all defaults to `true` and survives timeouts/temporary/blocked random-probe replies (correction 2).** A stalled or blocked random RCPT currently yields `CatchAll=true`, overstating a catch-all. Must become tri-state (`unknown`, not `true`).

**Non-issues:** single verifier reuse; `.exe`, `.env`, `output/`, `tmp/`, `data/private/` already git-ignored; MIT + attribution present.

---

## 3. Proposed Phase 1 architecture

Principle: keep the upstream engine attributable; add a thin, fully unit-testable service layer; the service **orchestrates the engine's granular stage methods** (not monolithic `Verify`) so every stage's evidence and error feeds precise classification and the "no 500" guarantee.

```
cmd/apiserver/
  main.go            # config load+validate → verifier → service → router → hardened server + graceful shutdown (MODIFIED)
  server.go          # routes: GET /v1/:email/verification (extended), POST /v1/verifications, GET /health (NEW)
  handlers.go        # decode+protect input, call service, JSON envelope, status codes, two presenters (NEW)
  handlers_test.go   # httptest + stub service; input-protection + status-code tests (NEW)

internal/config/
  config.go          # process-env → Config; conditional startup validation (NEW)
  config_test.go     # env→Config, defaults, validation failures (NEW)

internal/classify/
  reasons.go         # reason_code constants; status/retryable/confidence tables (NEW)
  classify.go        # Evidence → Assessment{status,reason_code,retryable,confidence}; precedence ladder (NEW)
  classify_test.go   # table-driven, synthetic Evidence, no network (NEW)

internal/verification/
  service.go         # Engine interface + Service.Verify(ctx,email)→Assessment; injected clock; builds Evidence (NEW)
  service_test.go    # stub Engine, deterministic, injected clock (NEW)

(root engine — additive-only changes)
  mx.go              # ADD Null MX detection + A/AAAA fallback + MailHostSource; instance resolver (corr. 1, D7)
  smtp.go            # ADD recipient + tri-state catch-all + source fields; A/AAAA dial; instance seams (D6, corr. 2/6)
  smtp_by_api*.go    # ADD accepted/rejected/unknown evidence + Source="api" (corr. 8)
  verifier.go        # ADD Result.MailHostSource, Result.NullMX; instance-scoped seam fields; no Verify behavior change
  smtp_fake_test.go  # mandatory in-process fake SMTP server + scenarios; race-clean (D8, corr. 3/6)
```

### 3.1 Layering

- **Engine (`emailverifier`)** — additive-only (new struct fields, Null MX + A/AAAA resolution, instance seams). No existing field/signature removed.
- **`Engine` interface** (in `internal/verification`) exposes granular methods so the service is stub-testable and can interpret each stage:
  ```go
  type Engine interface {
      ParseAddress(email string) emailverifier.Syntax
      CheckMX(domain string) (*emailverifier.Mx, error)
      CheckSMTP(domain, username string) (*emailverifier.SMTP, error)
      IsDisposable(domain string) bool
      IsRoleAccount(username string) bool
      IsFreeDomain(domain string) bool
      SuggestDomain(domain string) string
  }
  ```
  `*emailverifier.Verifier` satisfies it. The service composes these into an **`Evidence`** value, classifies it, and never surfaces a raw engine error to the client (fixes R1/R5/R6). DNS errors are inspected with `errors.As(&net.DNSError)` (`IsNotFound` ⇒ `domain_not_found`; timeout/temporary ⇒ `dns_error`).
- **`classify`** — pure functions over `Evidence` → `Assessment`. No I/O, no clock.
- **`config`** — process env → `Config`, validated at startup.
- **HTTP handlers** — protect+decode input, call service, always emit JSON, choose status per §6, render the correct presenter.

### 3.2 `Evidence` (service-built input to the classifier)

```go
type Evidence struct {
    Email       string
    Syntax      emailverifier.Syntax
    DNS         DNSOutcome   // resolved | not_found | temp_error
    MX          *emailverifier.Mx    // HasMXRecord, NullMX, ImplicitMX, MailHostSource
    Disposable  bool
    RoleAccount bool
    Free        bool
    Suggestion  string
    SMTP        *emailverifier.SMTP  // nil when SMTP not performed

    // explicit service evidence (correction 7)
    SMTPAttempted   bool   // an SMTP/API recipient interaction was actually made
    SMTPCheckReason string // attempted | disabled | disposable | catch_all | dns_failure | no_mail_host | null_mx | api_verifier
    Source          string // "smtp" | "api" | "" (verification source; correction 8)
}
```

`RecipientResult == "not_checked"` **alone must not imply `smtp_disabled`** — the classifier keys `smtp_disabled` off `SMTPCheckReason == "disabled"` (correction 7). `SMTPCheckReason` values:

| value | meaning | `SMTPAttempted` |
|---|---|---|
| `attempted` | recipient checked over SMTP | true |
| `catch_all` | connected; real RCPT skipped because catch-all confirmed | true |
| `api_verifier` | handled by Gmail/Yahoo API path | true |
| `disabled` | SMTP disabled by config | false |
| `disposable` | skipped (disposable short-circuit) | false |
| `dns_failure` | skipped (DNS temp failure) | false |
| `no_mail_host` | skipped (no MX and no A/AAAA) | false |
| `null_mx` | skipped (RFC 7505 Null MX) | false |

### 3.3 Response shapes

Two presenters over one internal `Assessment` (classification computed once).

**GET (extended legacy) — D1.** Preserves every existing field so the current PowerShell CSV verifier keeps working; **appends** new fields by embedding the engine `Result`:

```go
type LegacyResponse struct {
    *emailverifier.Result             // ALL legacy keys: email, reachable, syntax, smtp, gravatar, suggestion,
                                      // disposable, role_account, free, has_mx_records, + additive null_mx, mail_host_source
    Status          string     `json:"status"`
    ReasonCode      string     `json:"reason_code"`
    Retryable       bool       `json:"retryable"`
    Confidence      int        `json:"confidence"`
    CheckedAt       time.Time  `json:"checked_at"`
    SMTPAttempted   bool       `json:"smtp_attempted"`
    SMTPCheckReason string     `json:"smtp_check_reason"`
    Source          string     `json:"source"`
    Error           *ErrorInfo `json:"error"`   // sanitized; null on success
}
```

**POST (structured):**

```go
type Verification struct {
    Email      string     `json:"email"`
    Status     string     `json:"status"`      // valid|invalid|risky|unknown
    ReasonCode string     `json:"reason_code"`
    Retryable  bool       `json:"retryable"`
    Confidence int        `json:"confidence"`  // 0..100
    CheckedAt  time.Time  `json:"checked_at"`  // RFC3339 UTC
    Source     string     `json:"source"`      // "smtp" | "api" | ""
    Syntax     SyntaxDTO  `json:"syntax"`      // username, domain, valid
    Domain     DomainDTO  `json:"domain"`      // §3.5
    Account    AccountDTO `json:"account"`     // role_account
    SMTP       SMTPDTO    `json:"smtp"`        // §3.4
    Error      *ErrorInfo `json:"error"`       // sanitized; null on success
}

type ErrorInfo struct {
    Code    string `json:"code"`    // stage: "dns" | "mx" | "smtp" | "input" | "internal"
    Message string `json:"message"` // normalized, sanitized; NEVER raw server text, creds, proxy URI, or IPs
}
```

`CheckedAt` from an injected `clock func() time.Time` (default `time.Now().UTC`). `ErrorInfo.Message` is built from the normalized reason + sanitized SMTP code only; raw `LookupError.Details` is used **internally** and never serialized.

**Legacy `reachable` (GET).** Because the service orchestrates the granular engine methods rather than `Verify`, it constructs the embedded `*Result` itself and populates the legacy `reachable` field derived from the new status — `valid → "yes"`, `invalid → "no"`, `risky`/`unknown → "unknown"` — so the existing CSV verifier keeps reading a stable, backward-compatible value.

### 3.4 SMTP evidence — engine additive fields (D6, corr. 2/8; precise)

Existing fields preserved and still populated; new additive fields:

```go
type SMTP struct {
    // ---- existing, preserved (legacy CSV consumer relies on these) ----
    HostExists  bool `json:"host_exists"`
    FullInbox   bool `json:"full_inbox"`
    CatchAll    bool `json:"catch_all"`    // == (CatchAllResult == "confirmed")  — semantics tightened (R11)
    Deliverable bool `json:"deliverable"`  // == (RecipientResult == "accepted")
    Disabled    bool `json:"disabled"`

    // ---- Syncore Phase-1 additive fields ----
    RecipientResult string `json:"recipient_result"` // accepted | rejected | temporary | blocked | unknown | not_checked
    RecipientReason string `json:"recipient_reason"` // "" | mailbox_not_found | mailbox_disabled | full_inbox |
                                                     //      policy_block | rate_limited | temporary_failure | greylisted
    SMTPCode        int    `json:"smtp_code"`         // sanitized numeric RCPT reply code (0 if none/not checked)
    CatchAllResult  string `json:"catch_all_result"`  // confirmed | not_catch_all | unknown | not_checked  (corr. 2)
    Source          string `json:"source"`            // "smtp" | "api"  (corr. 8)
}
```

**Tri-state catch-all (R11, corr. 2).** `CatchAll` bool now means *confirmed* catch-all only. During the random-recipient probe:

| random-RCPT reply | `CatchAllResult` | `CatchAll` |
|---|---|---|
| 250/251 accepted | `confirmed` | `true` |
| explicit nonexistent / clean 550 reject | `not_catch_all` | `false` |
| **421/450/451/452 temporary, 554 policy/blocked, or dial/op timeout** | `unknown` | `false` |
| catch-all check disabled or not reached | `not_checked` | `false` |

A timeout, temporary rejection, or provider block during the probe yields `catch_all_result="unknown"`, **never** `catch_all=true`.

`SMTPDTO` mirrors the evidence:
```go
type SMTPDTO struct {
    HostExists      bool   `json:"host_exists"`
    Deliverable     bool   `json:"deliverable"`
    CatchAll        bool   `json:"catch_all"`
    CatchAllResult  string `json:"catch_all_result"`
    FullInbox       bool   `json:"full_inbox"`
    Disabled        bool   `json:"disabled"`
    RecipientResult string `json:"recipient_result"`
    RecipientReason string `json:"recipient_reason"`
    SMTPCode        int    `json:"smtp_code"`
    SMTPAttempted   bool   `json:"smtp_attempted"`
    SMTPCheckReason string `json:"smtp_check_reason"`
}
```

**API-verifier evidence (corr. 8).** The Gmail/Yahoo API path sets `Source="api"`, `CatchAllResult="not_checked"`, and maps its result to recipient evidence:

| API outcome | RecipientResult | RecipientReason | → classification |
|---|---|---|---|
| deliverable / exists | `accepted` | `` | valid `smtp_accepted` (or risky if role) |
| nonexistent | `rejected` | `mailbox_not_found` | invalid `mailbox_rejected` |
| API failure / rate limit / error | `unknown` | `` | unknown `smtp_inconclusive` |

The verification source is preserved end-to-end (`Source` in evidence + both response shapes).

### 3.5 MX evidence — Null MX + implicit A/AAAA (corr. 1, D7; additive)

```go
type Mx struct {
    HasMXRecord    bool       // ≥1 usable explicit MX (Host != ".") — legacy, preserved
    Records        []*net.MX  // legacy; synthesized {Host: domain} when implicit
    NullMX         bool       // RFC 7505: single MX target "." → domain refuses mail  (corr. 1)
    ImplicitMX     bool       // no MX, but A/AAAA present → domain used as mail host
    MailHostSource string     // "mx" | "a" | "aaaa" | "null" | "none"
}
```

Resolution order in `CheckMX` (additive):
1. `LookupMX`.
2. **Null MX:** exactly one record whose target is `"."` (or empty) → `NullMX=true`, `MailHostSource="null"`, **no A/AAAA fallback** (corr. 1).
3. ≥1 usable MX → `HasMXRecord=true`, `MailHostSource="mx"`.
4. No MX records → `LookupIP`: IPv4 ⇒ `"a"`, else IPv6 ⇒ `"aaaa"`, `ImplicitMX=true`; none ⇒ `"none"`.

Engine `Result` gains additive `NullMX bool` and `MailHostSource string`. `DomainDTO`:
```go
type DomainDTO struct {
    HasMXRecords   bool   `json:"has_mx_records"`
    NullMX         bool   `json:"null_mx"`
    ImplicitMX     bool   `json:"implicit_mx"`
    MailHostSource string `json:"mail_host_source"`
    Disposable     bool   `json:"disposable"`
    FreeProvider   bool   `json:"free_provider"`
    Suggestion     string `json:"suggestion"`
}
```

Classification consequences: `null_mx` ⇒ **invalid/`null_mx`** (never A/AAAA fallback); `MailHostSource=="none"` ⇒ invalid/`no_mail_host`; absence of explicit MX with A/AAAA present is **not** invalid.

### 3.6 Instance-scoped, race-safe test seams (corr. 6)

**No package-global seams.** The `Verifier` gains unexported, instance-scoped dependency fields, defaulted in `NewVerifier`:

```go
type Verifier struct {
    // ... existing ...
    lookupMX func(name string) ([]*net.MX, error) // default net.LookupMX
    lookupIP func(host string) ([]net.IP, error)  // default net.LookupIP
    dial     func(addr string, connectTimeout, operationTimeout time.Duration) (*smtp.Client, error) // default dialSMTP
}
```

`CheckMX`/`CheckSMTP`/`newSMTPClient` call `v.lookupMX`/`v.lookupIP`/`v.dial` instead of the package functions. Tests build a dedicated `*Verifier` (in-package) and set these fields to point at the in-process fake SMTP listener — **each test owns its own verifier, so there is no shared mutable global** and the default suite passes under `-race` (corr. 6). Production behavior is unchanged (defaults are the real `net` funcs).

### 3.7 Response examples

**POST accepted (valid):**
```json
{
  "email": "person@example.com",
  "status": "valid",
  "reason_code": "smtp_accepted",
  "retryable": false,
  "confidence": 95,
  "checked_at": "2026-07-13T19:05:29Z",
  "source": "smtp",
  "syntax": { "username": "person", "domain": "example.com", "valid": true },
  "domain": { "has_mx_records": true, "null_mx": false, "implicit_mx": false,
              "mail_host_source": "mx", "disposable": false, "free_provider": false, "suggestion": "" },
  "account": { "role_account": false },
  "smtp": { "host_exists": true, "deliverable": true, "catch_all": false, "catch_all_result": "not_catch_all",
            "full_inbox": false, "disabled": false, "recipient_result": "accepted", "recipient_reason": "",
            "smtp_code": 250, "smtp_attempted": true, "smtp_check_reason": "attempted" },
  "error": null
}
```

**POST SMTP timeout (unknown, retryable):**
```json
{
  "email": "user@outlook.com",
  "status": "unknown",
  "reason_code": "smtp_timeout",
  "retryable": true,
  "confidence": 10,
  "checked_at": "2026-07-13T19:06:11Z",
  "source": "smtp",
  "syntax": { "username": "user", "domain": "outlook.com", "valid": true },
  "domain": { "has_mx_records": true, "null_mx": false, "implicit_mx": false,
              "mail_host_source": "mx", "disposable": false, "free_provider": true, "suggestion": "" },
  "account": { "role_account": false },
  "smtp": { "host_exists": false, "deliverable": false, "catch_all": false, "catch_all_result": "not_checked",
            "full_inbox": false, "disabled": false, "recipient_result": "unknown", "recipient_reason": "",
            "smtp_code": 0, "smtp_attempted": true, "smtp_check_reason": "attempted" },
  "error": { "code": "smtp", "message": "connection to mail server timed out" }
}
```

**POST Null MX (invalid):**
```json
{
  "email": "sales@no-mail.example",
  "status": "invalid",
  "reason_code": "null_mx",
  "retryable": false,
  "confidence": 99,
  "checked_at": "2026-07-13T19:07:02Z",
  "source": "",
  "syntax": { "username": "sales", "domain": "no-mail.example", "valid": true },
  "domain": { "has_mx_records": false, "null_mx": true, "implicit_mx": false,
              "mail_host_source": "null", "disposable": false, "free_provider": false, "suggestion": "" },
  "account": { "role_account": false },
  "smtp": { "host_exists": false, "deliverable": false, "catch_all": false, "catch_all_result": "not_checked",
            "full_inbox": false, "disabled": false, "recipient_result": "not_checked", "recipient_reason": "",
            "smtp_code": 0, "smtp_attempted": false, "smtp_check_reason": "null_mx" },
  "error": null
}
```

**GET (extended legacy)** — same underlying data as flat legacy keys plus the appended fields:
```json
{
  "email": "person@example.com", "reachable": "yes",
  "syntax": { "username": "person", "domain": "example.com", "valid": true },
  "smtp": { "host_exists": true, "full_inbox": false, "catch_all": false, "deliverable": true, "disabled": false,
            "recipient_result": "accepted", "recipient_reason": "", "smtp_code": 250,
            "catch_all_result": "not_catch_all", "source": "smtp" },
  "gravatar": null, "suggestion": "", "disposable": false, "role_account": false, "free": false,
  "has_mx_records": true, "null_mx": false, "mail_host_source": "mx",
  "status": "valid", "reason_code": "smtp_accepted", "retryable": false, "confidence": 95,
  "checked_at": "2026-07-13T19:05:29Z", "smtp_attempted": true, "smtp_check_reason": "attempted",
  "source": "smtp", "error": null
}
```

---

## 4. Classification precedence (corr. 1, 2, 4, 5, 8)

Evaluated top-to-bottom over `Evidence`; **first match sets `status` + `reason_code`.**

| # | Condition | status | reason_code |
|---|---|---|---|
| 1 | Syntax invalid | `invalid` | `syntax_invalid` |
| 2 | Domain does not resolve (NXDOMAIN / "no such host"; no MX and no A/AAAA) | `invalid` | `domain_not_found` |
| 3 | DNS temporary failure (timeout / SERVFAIL) | `unknown` | `dns_error` |
| 4 | **Null MX published** (`MX.NullMX`) | `invalid` | `null_mx` |
| 5 | Domain resolves but `MailHostSource == "none"` (no MX **and** no A/AAAA) | `invalid` | `no_mail_host` |
| 6 | Disposable domain (SMTP short-circuited) | `risky` | `disposable_domain` |
| 7 | Recipient `rejected` with reason `mailbox_not_found` (explicit nonexistent, §4.1) | `invalid` | `mailbox_rejected` |
| 8 | Recipient reason `mailbox_disabled` (explicit disabled/inactive/suspended/deactivated) | `risky` | `mailbox_disabled` |
| 9 | Recipient reason `full_inbox` (quota/over-space) | `risky` | `full_inbox` |
| 10 | `CatchAllResult == "confirmed"` | `risky` | `catch_all` |
| 11 | Recipient `accepted` **and** local part is a role account | `risky` | `role_account` |
| 12 | Recipient `accepted` (not catch-all, not role) | `valid` | `smtp_accepted` |
| 13 | Recipient `temporary`, reason `temporary_failure`/`greylisted` (421/450) | `unknown` | `temporary_rejection` |
| 14 | Recipient `temporary`, reason `rate_limited` (451 / 452 too-many) | `unknown` | `rate_limited` |
| 15 | Recipient `blocked` (policy/spam/reputation/blacklist/denied/generic permanent) | `unknown` | `provider_blocked` |
| 16 | SMTP transport timeout (dial/operation deadline) | `unknown` | `smtp_timeout` |
| 17 | SMTP connection refused | `unknown` | `connection_refused` |
| 18 | `SMTPCheckReason == "disabled"` (SMTP off by config) | `unknown` | `smtp_disabled` |
| 19 | Any other inconclusive SMTP/API outcome (incl. `CatchAllResult=="unknown"` with no recipient verdict, `RecipientResult=="unknown"`, typed-nil) | `unknown` | `smtp_inconclusive` |

**Baked-in decisions:**
- **Null MX (corr. 1):** rule 4, above `no_mail_host`; Null MX never falls back to A/AAAA.
- **Catch-all (corr. 2):** rule 10 requires `CatchAllResult=="confirmed"`. A `"unknown"` catch-all probe result does **not** produce `catch_all`; it flows to rule 19 (`smtp_inconclusive`) unless a specific recipient verdict already matched.
- **`smtp_inconclusive` (corr. 4):** replaces `internal_error` as the normal-verification fallthrough — **unknown + retryable**. `internal_error` is reserved for genuine HTTP 500 application faults (§6) and is never a verification `status` outcome.
- **`smtp_disabled` (corr. 7):** keyed off `SMTPCheckReason=="disabled"`, **not** off `RecipientResult=="not_checked"`.
- **Role (rule 11)** downgrades only a confirmed-accepted mailbox; when SMTP is inconclusive/disabled the honest `unknown` wins and `account.role_account` remains as evidence.
- **Free provider** never changes status.

### 4.1 Recipient-reply → reason mapping (tightened, corr. 5)

`classifyRecipientReply(code, text)` (additive helper) buckets replies. **Nonexistent-mailbox (→ `mailbox_rejected`/invalid) requires one of these explicit phrases only:**

> `user unknown`, `no such user`, `no such mailbox`, `recipient does not exist`, `address does not exist`, `invalid recipient`, `recipient not found`, `account not found`.

Broad phrases **`recipient rejected`, `address rejected`, `mailbox unavailable`** do **not** independently prove nonexistence. Any ambiguous permanent reply → **`policy_block` → unknown/`provider_blocked`** (never silently `invalid`).

| SMTP reply | RecipientResult | RecipientReason | Rule → status |
|---|---|---|---|
| 250/251 | `accepted` | `` | 11/12 |
| 550/551/553 + explicit-nonexistent phrase (above) | `rejected` | `mailbox_not_found` | 7 → invalid |
| 550/554 + disabled/inactive/suspended/deactivated | `rejected` | `mailbox_disabled` | 8 → risky |
| 452/552 + quota/full/over-space/insufficient | `temporary` | `full_inbox` | 9 → risky |
| 550/554 + policy/spam/reputation/blacklist/denied/blocked, **or generic/ambiguous permanent** | `blocked` | `policy_block` | 15 → unknown |
| 421 / 450 | `temporary` | `temporary_failure`/`greylisted` | 13 → unknown |
| 451 / 452-too-many | `temporary` | `rate_limited` | 14 → unknown |
| dial/op deadline exceeded | `unknown` (transport) | — | 16 → unknown |
| connection refused | `unknown` (transport) | — | 17 → unknown |

The same buckets drive the tri-state catch-all probe (§3.4): accepted ⇒ `confirmed`; explicit nonexistent/clean reject ⇒ `not_catch_all`; temporary/policy/timeout ⇒ `unknown`.

---

## 5. `status`, `retryable`, `confidence`

Centralized in `internal/classify/reasons.go`.

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

**19 verification reason codes.** `internal_error` is **reserved for HTTP 500** application faults (§6) and is intentionally excluded from this table — it is never a verification outcome.

---

## 6. HTTP status-code behavior (corr. 1, 4, 10)

**Core rule: a completed verification is HTTP 200 for `valid`/`invalid`/`risky`/`unknown`, on both GET and POST.** SMTP timeout, DNS uncertainty, provider blocking, connection refusal → **200 `status:"unknown"`**, never 5xx. 5xx is reserved for genuine application faults (and only then is `internal_error` used). **Every** response — including syntax errors and input-protection rejections — is JSON.

| Condition | HTTP | Body |
|---|---|---|
| Verification completed (any status, incl. `syntax_invalid`, timeouts, blocking, `null_mx`) | `200` | GET: `LegacyResponse` · POST: `Verification` |
| Request body over size limit | `413` | `{"error":{...}}` |
| POST without a JSON media type (see §7) | `415` | `{"error":{...}}` |
| Malformed JSON / unknown fields / trailing data / missing-empty email / control chars / over-length email | `400` | `{"error":{...}}` |
| Wrong method on a known path | `405` | `{"error":{...}}` |
| Unknown route | `404` | `{"error":{...}}` |
| Panic / marshal failure / unexpected fault | `500` | `{"error":{"code":"internal",...}}` |
| `GET /health` | `200` | `{"status":"ok"}` |

Invalid **syntax** is a 200 result (`invalid`/`syntax_invalid`), not 400.

### 6.1 Endpoints

- `GET /v1/:email/verification` — preserved and extended (D1); always 200 JSON for completed verifications.
- `POST /v1/verifications` — body `{"email":"person@example.com"}` → `Verification` JSON.
- `GET /health` — `{"status":"ok"}`.

---

## 7. HTTP hardening & lifecycle (corr. 10, 11-server)

**Input protections (POST):**
- `http.MaxBytesReader` cap `SYNCORE_VERIFIER_MAX_BODY_BYTES` (default 4 KiB) → over-limit ⇒ **413**.
- **Content-Type (corr. 10):** parse with `mime.ParseMediaType`; accept when the **media type** is `application/json`, **ignoring parameters** — e.g. `application/json; charset=utf-8` is accepted. Non-JSON media type ⇒ **415**.
- `json.Decoder` + `DisallowUnknownFields()` → unknown field ⇒ **400**.
- Reject trailing data: a second `Decode` must return `io.EOF` ⇒ else **400**.
- Email length cap (default 320 bytes, RFC 5321) ⇒ **400**.
- Reject control characters (`< 0x20`, `0x7f`) in the email ⇒ **400**.
- Empty/missing `email` ⇒ **400**.

(GET validates the same length/control-char rules on the path parameter; syntax *format* remains a 200 result.)

**Server settings + graceful shutdown:** `ReadHeaderTimeout` 5s, `ReadTimeout` 10s, `IdleTimeout` 60s, `MaxHeaderBytes` 1 MiB; `WriteTimeout` default **35s** (`> ConnectTimeout + OperationTimeout + DNS budget`, §8); `signal.NotifyContext(SIGINT,SIGTERM)` → `server.Shutdown(ctx)` with a bounded drain.

---

## 8. Timeout & context boundary (correction 12, prior revision)

The upstream engine has **no `context` parameter and no mid-operation cancellation** (verified):
- **Connect:** `net.DialTimeout("tcp", host:25, ConnectTimeout)` per MX candidate, concurrent; first success wins.
- **SMTP operations:** `conn.SetDeadline(now + OperationTimeout)` set **once at connect** — a single absolute deadline covering **all** of `HELO`+`MAIL`+`RCPT`(+catch-all RCPT), **not** reset per command.
- **Worst case (SMTP path):** `≈ ConnectTimeout + OperationTimeout` (concurrent MX ⇒ hosts don't sum) + OS-resolver DNS time (no explicit engine timeout).

**Explicit statement:** the HTTP request `ctx` (client disconnect) will **not** interrupt in-flight DNS or SMTP in Phase 1. `Service.Verify(ctx, …)` takes `ctx` for request-scoped values / future use, but Phase 1 does **not** claim it aborts SMTP. `WriteTimeout` is sized above the SMTP worst case so legitimate slow responses aren't truncated. Context-based cancellation is deferred to a later engine change.

---

## 9. Configuration (`SYNCORE_VERIFIER_` prefix; corr. 8-prior, 9)

Config is read **from the process environment only**. `.env.example` is **documentation only** — Phase 1 adds **no dotenv dependency** and does **not** read `.env` at runtime.

| Env var | Purpose | Default |
|---|---|---|
| `SYNCORE_VERIFIER_BIND_ADDR` | bind address | `127.0.0.1:8080` |
| `SYNCORE_VERIFIER_SMTP_ENABLED` | enable SMTP check | `true` |
| `SYNCORE_VERIFIER_FROM_EMAIL` | `MAIL FROM` address | `hello@syncoretech.com` |
| `SYNCORE_VERIFIER_HELLO_NAME` | `EHLO` name | `syncoretech.com` |
| `SYNCORE_VERIFIER_CONNECT_TIMEOUT` | dial timeout (Go duration) | `10s` |
| `SYNCORE_VERIFIER_OPERATION_TIMEOUT` | SMTP op deadline (Go duration) | `10s` |
| `SYNCORE_VERIFIER_DISPOSABLE_AUTOUPDATE` | daily disposable refresh | `false` |
| `SYNCORE_VERIFIER_DOMAIN_SUGGEST` | enable typo suggestions | `true` |
| `SYNCORE_VERIFIER_MAX_BODY_BYTES` | POST body cap | `4096` |

**Startup validation — fail fast, non-zero exit, before binding:**
- `BIND_ADDR` parses as `host:port`, port numeric.
- **`FROM_EMAIL` and `HELLO_NAME` are validated only when `SMTP_ENABLED=true` (correction 9)** — `FROM_EMAIL` passes `IsAddressValid`; `HELLO_NAME` non-empty, no whitespace. When SMTP is disabled they are unused and not validated.
- `CONNECT_TIMEOUT`/`OPERATION_TIMEOUT` parse as duration and `> 0`.
- `SMTP_ENABLED`/`DISPOSABLE_AUTOUPDATE`/`DOMAIN_SUGGEST` parse as bool.
- `MAX_BODY_BYTES` parses as positive int.
- Failure logs the offending variable + expected format, then exits non-zero.

---

## 10. Deterministic testing strategy (corr. 3, 6, 7; D8)

**Principle:** default `go test ./...` touches **no network / no public provider**, is reproducible, and **passes `-race`**.

1. **Split live tests per-function (prior corr. 7).** Move only network-hitting functions into new `*_live_test.go` files with `//go:build live`; keep deterministic functions in the default files.

   | Origin | → `//go:build live` (moved) | Stays in default suite |
   |---|---|---|
   | `verifier_test.go` | `SMTPHostNotExists`, `SMTPHostExists_NotCatchAll`, `SMTPHostExists_FreeDomain`, `RoleAccount`, `DisabledSMTPCheck`, `EnableDomainSuggest`, `EnableDomainSuggest_Gmail`, all `AutoUpdateDisposable*`, `StopCurrentScheduleOK` | `ErrorSyntax`, `Disposable`, `Disposable_override`, `StopCurrentSchedule_ScheduleIsNil` |
   | `smtp_test.go` | `ByApi`, `HostExists`, `CatchAllHost`, `NoCatchAllHost*`, `UpdateFromEmail`, `UpdateHelloName`, `WithNoExistUsername`, `HostNotExists`, `NewSMTPClientOK`, `NewSMTPClientFailed*`, `DialSMTPFailed_NoSuchHost` | `UnSupportedVendor`, `DisabledSMTPCheck`, `DialSMTPFailed_NoPortIsConfigured` |
   | `smtp_by_api_yahoo_test.go` | `TestYahooCheckByAPI` | `TestGetAcrumb` |

2. **Mandatory in-process fake SMTP server (D8, corr. 3/6)** — `smtp_fake_test.go` (default suite, package `emailverifier`). A `net.Listen("tcp","127.0.0.1:0")` goroutine speaks scripted SMTP; tests build a `*Verifier` with **instance-scoped** `lookupMX`/`lookupIP`/`dial` (§3.6) pointing at it — **race-clean, no globals**. Required scenarios:
   - accepted recipient → `smtp_accepted`/valid
   - permanent nonexistent recipient (550 *user unknown*) → `mailbox_rejected`/invalid
   - broad-but-ambiguous permanent (550 *mailbox unavailable* / *recipient rejected*) → `provider_blocked`/unknown (corr. 5)
   - temporary `421`, `450`, `451` → `temporary_rejection`/`rate_limited`/unknown
   - full inbox (452/552 quota) → `full_inbox`/risky
   - explicit disabled `554`/`550` → `mailbox_disabled`/risky
   - policy/blocked `554` (and generic 554) → `provider_blocked`/unknown
   - **random recipient 421** → `catch_all_result="unknown"` (corr. 3)
   - **random recipient 554 policy rejection** → `catch_all_result="unknown"` (corr. 3)
   - **random recipient timeout** → `catch_all_result="unknown"` (corr. 3)
   - confirmed catch-all (random RCPT accepted) → `catch_all_result="confirmed"` → `catch_all`/risky
   - not-catch-all (random RCPT clean 550) then real RCPT accepted → valid
   - real-recipient timeout → `smtp_timeout`/unknown

3. **Null MX deterministic test (corr. 1)** — inject `lookupMX` returning a single `.`-target record; assert `Mx.NullMX`, `MailHostSource=="null"`, no A/AAAA fallback, and classifier → `invalid/null_mx`. Also an implicit-MX test (no MX + injected A record) → resolves, not invalid.

4. **Classifier tests (`internal/classify`)** — table-driven over synthetic `Evidence`; cover every rule (§4), the tightened phrase buckets (§4.1), tri-state catch-all, `smtp_inconclusive` vs `smtp_disabled` (via `SMTPCheckReason`), API-verifier mapping, and every reason code + `retryable`/`confidence` (§5).

5. **Service tests (`internal/verification`)** — stub `Engine` returning canned stage results (accepted, 550 user-unknown, 550 mailbox-unavailable, 421, timeout, disposable, catch-all confirmed/unknown, syntax-invalid, no-MX+A, Null MX, DNS NXDOMAIN, DNS timeout, API accepted/nonexistent/error). Injected clock ⇒ fixed `checked_at`. Assert `SMTPAttempted`/`SMTPCheckReason`/`Source` and both presenters.

6. **HTTP handler tests (`cmd/apiserver`)** — `httptest` + stub service: 200 for all classifications incl. syntax-invalid, timeout, null_mx; 413 oversize; 415 non-JSON; **200 for `application/json; charset=utf-8`** (corr. 10); 400 bad JSON / unknown field / trailing data / missing email / control char / over-length; 405 wrong method; 404 unknown route; `/health` 200; every body JSON; legacy GET retains all legacy keys.

7. **Config tests (`internal/config`)** — env→Config, defaults, each validation failure, and that `FROM_EMAIL`/`HELLO_NAME` are **not** validated when `SMTP_ENABLED=false` (corr. 9).

8. **Keep & extend `error_test.go`** for `classifyRecipientReply` buckets incl. the tightened nonexistent allow-list and ambiguous→`provider_blocked`.

9. **CI/Makefile** — primary command is **`go test ./...`** (cross-platform; corr. 11). `Makefile` keeps `test`/`test-live` convenience targets for Unix. `.github/workflows/ci.yml` runs `go test -race ./...` (deterministic) only.

---

## 11. Files to create / modify / remove

### Create
- `docs/PHASE_1_PLAN.md` *(this file)*
- `internal/config/config.go`, `internal/config/config_test.go`
- `internal/classify/reasons.go`, `internal/classify/classify.go`, `internal/classify/classify_test.go`
- `internal/verification/service.go`, `internal/verification/service_test.go`
- `cmd/apiserver/server.go`, `cmd/apiserver/handlers.go`, `cmd/apiserver/handlers_test.go`
- `smtp_fake_test.go` *(root pkg; fake SMTP server + scenarios incl. random-probe 421/554/timeout; race-clean — D8, corr. 3/6)*
- `verifier_live_test.go`, `smtp_live_test.go`, `smtp_by_api_yahoo_live_test.go` *(`//go:build live`, moved functions)*
- `.env.example` *(documentation only; not read at runtime)*

### Modify
- `cmd/apiserver/main.go` — env config + conditional validation; reusable verifier; extended GET + POST + `/health`; input protections (incl. media-type param parsing); hardened `http.Server`; graceful shutdown; bind `127.0.0.1`.
- `smtp.go` — additive: `classifyRecipientReply`; `RecipientResult`/`RecipientReason`/`SMTPCode`/`CatchAllResult`/`Source`; tri-state catch-all (default `false`, `confirmed` only on accept); populate real-address evidence; A/AAAA implicit dial; **instance-scoped `v.lookupMX`/`v.lookupIP`/`v.dial`** (corr. 6).
- `mx.go` — additive: **Null MX detection** (corr. 1), A/AAAA fallback, `ImplicitMX`, `MailHostSource`; use instance `v.lookupMX`/`v.lookupIP`.
- `smtp_by_api.go` / `smtp_by_api_yahoo.go` — additive: set `Source="api"`, `CatchAllResult="not_checked"`, map deliverable/nonexistent/error to `RecipientResult` (corr. 8).
- `verifier.go` — additive: `Result.NullMX`, `Result.MailHostSource`; instance seam fields defaulted in `NewVerifier`. No `Verify` behavior change.
- `error.go` — extend keyword buckets for `classifyRecipientReply`, incl. tightened nonexistent allow-list (corr. 5). Existing `ParseSMTPError` behavior preserved.
- `README.md` — fork notice + attribution at top; Windows dev + API usage; env via process env; **`go test ./...` as the primary test command, `make` optional** (corr. 11); timeout-boundary note; MIT preserved.
- `verifier_test.go`, `smtp_test.go`, `smtp_by_api_yahoo_test.go` — remove moved live functions.
- `Makefile` — `test`/`test-live` convenience (documented optional on Windows).
- `.github/workflows/ci.yml` — `go test -race ./...` deterministic suite only.
- `CHANGELOG.md` — Phase 1 entry.

### Remove
- **Nothing.** `email-verifier-api.exe` already git-ignored (`*.exe`), untracked. Live tests relocated, not removed.

---

## 12. README structure

1. **Syncore fork notice** + MIT attribution to AfterShip (2020) — at the very top.
2. **Windows/PowerShell** dev guide — Go 1.22+, `go build ./cmd/apiserver`, `$env:SYNCORE_VERIFIER_*`, running, startup log.
3. **API usage** — extended `GET /v1/{email}/verification` and `POST /v1/verifications` with `Invoke-RestMethod`/`curl.exe`; sample responses (both shapes, incl. `null_mx`, tri-state catch-all, `smtp_inconclusive`); `GET /health`.
4. Port-25 egress note → Microsoft/Yahoo/iCloud may return `unknown`; §8 timeout boundary.
5. `.env.example` is documentation only (no runtime dotenv).
6. **Testing: `go test ./...` is the primary, cross-platform command** (works without `make`); `go test -race ./...` for the race detector; `go test -tags=live ./...` for live tests; `make test`/`make test-live` optional on Unix (corr. 11).
7. Upstream README preserved below the Syncore section.

---

## 13. Decisions — all resolved

- **D1 — GET** preserved **and extended** (legacy fields intact + `status`/`reason_code`/`retryable`/`confidence`/`checked_at`/`source`/`smtp_attempted`/`smtp_check_reason`/`error`); GET and POST both 200 for completed verifications; syntax/SMTP/DNS uncertainty are JSON, not 500.
- **D2 — 554/permanent split**, further tightened (corr. 5): explicit disabled → risky/`mailbox_disabled`; explicit nonexistent allow-list → invalid/`mailbox_rejected`; broad/ambiguous/policy → unknown/`provider_blocked`.
- **D3 — `dns_error`** added.
- **D4 — `smtp_disabled`** added; keyed off `SMTPCheckReason=="disabled"` (corr. 7).
- **D5 — env prefix `SYNCORE_VERIFIER_`.**
- **D6 — Option A**, minimal additive engine patch; seams are **instance-scoped/race-safe** (corr. 6).
- **D7 — implicit A/AAAA** delivery; `no_mx_records` → `no_mail_host`; invalid only when no MX **and** no A/AAAA.
- **D8 — fake SMTP server mandatory**, race-clean, incl. random-probe 421/554/timeout (corr. 3).
- **Null MX (corr. 1)** — detect `.` target; no A/AAAA fallback; invalid/`null_mx`; `NullMX` evidence + tests.
- **Tri-state catch-all (corr. 2)** — `CatchAllResult` {confirmed, not_catch_all, unknown, not_checked}; timeout/temp/block ⇒ `unknown`, never `catch_all=true`.
- **`smtp_inconclusive` (corr. 4)** — normal-verification fallthrough (unknown, retryable); `internal_error` reserved for HTTP 500.
- **Explicit service evidence (corr. 7)** — `smtp_attempted` + `smtp_check_reason`; `not_checked` alone never implies `smtp_disabled`.
- **API-verifier evidence (corr. 8)** — deliverable→accepted, nonexistent→rejected, failure/rate-limit→unknown; `source` preserved.
- **Conditional config validation (corr. 9)** — `FROM_EMAIL`/`HELLO_NAME` validated only when `SMTP_ENABLED=true`.
- **Content-Type params (corr. 10)** — `application/json; charset=utf-8` accepted via `mime.ParseMediaType`.
- **`go test ./...` primary (corr. 11)** — `make` optional (Windows may lack it).

---

## 14. Out of scope for Phase 1

Database, queue/retry-worker, frontend, bulk/batch upload, authentication, paid providers, CRM (Lead Engine) integration. The `Engine` interface and per-request `Service` allow these to layer on later without rework.

---

*No implementation will proceed until this plan is approved.*
