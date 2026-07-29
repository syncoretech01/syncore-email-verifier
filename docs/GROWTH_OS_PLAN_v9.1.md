# GROWTH OS PLAN — `syncore-email-verifier`

**Aligned to:** Growth OS Plan v9.1 · **Roadmap phase:** P3 (Data spine)
**Size:** Smallest job in the build (~2% of total effort). Days, not weeks.
**Language:** Go 1.22 · **Deploy:** systemd, co-located with the CRM worker, private/loopback bind

> **Status as of 2026-07-28: EV0–EV3 complete.** All three items §1 previously
> listed as "needs confirming" are implemented and tested. The verified state is
> recorded with file:line evidence in [`../INVENTORY.md`](../INVENTORY.md).
> Remaining work in this repo is one recommended test (§3 EV5) and EV4, which is
> optional by design. **This repo is not blocking Phase 3** — the outstanding P3
> work is the verifier bridge fix in `syncore-lead-hub`.
>
> This file is the v9.1-aligned plan. The older
> [`GROWTH_OS_PLAN.md`](GROWTH_OS_PLAN.md) reconstructs the pre-v9.1 enterprise
> effort and uses its own EV0–EV4 numbering; where the two disagree, this file
> wins for forward planning and `INVENTORY.md` wins for what actually shipped.

---

## 0. Role — what this repo is and is not

**IS:** the free **Layer-1** email verification engine. It answers "is this address real?" without sending mail, and it is honest when it doesn't know.

**IS NOT:** a lead store, a campaign tool, or a paid verification service. It has exactly one caller (**the Lead Hub**) and no knowledge of campaigns, costs, or CRM entities.

**Its one irreplaceable property:** the **honesty guarantee** — a timeout, a temporary rejection, or a blocked provider is `unknown`, *never* `invalid`. Everything downstream (suppression, paid escalation, spend) depends on this being true. Protect it above all else.

---

## 1. Ground truth — verify at session start

Re-run these before planning any work. Do not trust prior claims, including this file's.

```bash
grep -nE '"/(v1|batches|health|ready|metrics|admin)' cmd/apiserver/server.go   # route inventory
grep -rn 'Authorization\|Bearer' cmd/ internal/ --include='*.go' | grep -v _test  # auth present?
grep -n 'WriteTimeout\|writeTimeoutFor' cmd/apiserver/server.go                 # timeout derivation
go build ./... && go vet ./... && go test ./...
```

**Confirmed present (re-verified 2026-07-28 at `508fd9f`):**
- Routes: `GET /v1/:email/verification` · `POST /v1/verifications` · `POST /v1/verifications:batch` (bounded sync) · `POST /batches`, `GET /batches/:id`, `GET /batches/:id/results` (async + completion webhook) · `POST /v1/feedback[/ses|/smartlead]` · `POST /admin/erasure` · `GET /health|/ready|/metrics`
- Classification in `internal/classify/reasons.go`: `valid|invalid|risky|unknown` with fixed reason codes. Honesty guarantee holds in code — `temporary_rejection`, `provider_blocked`, `smtp_timeout` → `unknown, retryable:true` (confidence 10–20); `syntax_invalid`, `domain_not_found`, `null_mx` → `invalid` (99–100)
- Config: env-only, all vars prefixed `SYNCORE_VERIFIER_*`, fail-fast validation
- SES/Smartlead feedback ingestion → per-domain reputation priors
- Deterministic fake-SMTP tests, with `*_live_test.go` clearly separated
- MIT LICENSE and the AfterShip module name preserved
- `go build ./... && go vet ./... && go test ./...` green across 13 packages

**Previously "needs confirming" — now closed** (evidence in [`../INVENTORY.md`](../INVENTORY.md)):
- **Bearer auth is implemented and required by default** on every route except `/health` and `/ready`, constant-time via `subtle.ConstantTimeCompare` (`cmd/apiserver/server.go:95`, `:150-166`). It accepts hashed API keys as well as the single token — a superset of what §3 EV1 specified. `/metrics` stays authenticated.
- **The non-loopback-without-token startup refusal is wired** — `config.validateBindSecurity` (`internal/config/config.go:366-381`), with an unparseable host failing safe as non-loopback.
- **`/batches` write timeout is batch-aware** — `writeTimeoutFor` takes the larger of the single-verification and full-batch bounds (`cmd/apiserver/server.go:208-243`). ≈215s at defaults; documented in `deploy/README.md`.

---

## 2. Golden rules

1. **Never map an inconclusive result to `invalid`.** Timeouts, greylisting, blocks, and connection failures are `unknown` with `retryable: true`. A wrong `invalid` permanently suppresses a real prospect.
2. **MIT LICENSE and the upstream module name stay.** This is a fork of AfterShip/email-verifier; attribution is non-negotiable.
3. **Config is env-only, `SYNCORE_VERIFIER_*` prefixed, fail-fast validated.** No config files, no flags, no defaults that silently enable network behavior.
4. **Deterministic tests only in the default suite.** Live SMTP tests stay in `*_live_test.go` and never run in CI.
5. **No dependency additions without justification.** Current set (pgx, httprouter, testify) is deliberate. No paid SDKs.
6. **This service never learns about campaigns.** No `campaignId`, no costs, no CRM concepts. The Hub owns that context.
7. **Never bind publicly.** Private/loopback only, and refuse to start if bound non-loopback without a token.

---

## 3. Phases

### EV0 — Inventory & baseline — **DONE (2026-07-28)**

Ground-truth commands re-run; actual state recorded in [`../INVENTORY.md`](../INVENTORY.md);
`go build/vet/test` green on a clean checkout; auth posture documented precisely
(present, required by default, token **or** hashed API key).

**Acceptance met:** all three "needs confirming" items are stated with file:line evidence.

---

### EV1 — Auth required by default — **DONE**

`Authorization: Bearer <token>` required on all endpoints except `GET /health`
(and `/ready`, which §3 permitted as an optional exemption); constant-time
comparison; token from `SYNCORE_VERIFIER_AUTH_TOKEN`; fail-fast at startup on a
non-loopback bind with no credential; `401` with no detail leakage.

**Acceptance met:** all four cases plus malformed-header variants are covered in
`cmd/apiserver/auth_test.go`; the startup refusal is covered in
`internal/config/config_test.go`.

---

### EV2 — Batch timing correctness — **DONE**

Implemented as option (a): `WriteTimeout` is derived from the batch cap and SMTP
concurrency — `ceil(BatchMaxItems / BatchConcurrency) × (ConnectTimeout +
OperationTimeout)`, +15s headroom, 35s floor. ≈**215s at defaults** (100 items,
concurrency 10, 10s + 10s). Async `/batches` decouples completion from the
request, so only the `202` needs a normal timeout — confirmed in code.

**Acceptance met:** the bound is locked by `cmd/apiserver/batch_test.go:197-216`,
per-item faults degrade to `unknown/retryable` without failing the batch
(`batch_test.go`), and the bound is documented for the Hub in
`deploy/README.md:91-112`.

---

### EV3 — Deployment & operational doc — **DONE**

`deploy/` carries the systemd unit, a PM2 config, an env template, and a guide
covering the co-located-vs-port-25 tradeoff. The **port-25 reality** is
documented at `deploy/README.md:83-89` — EC2 blocks outbound 25 by default, so
SMTP-level `valid` is rare from that host; syntax/MX/null-MX/disposable/role/free
checks still run free, Gmail/Yahoo resolve via the `api` path, and everything
else returns `unknown` (retryable), which correctly escalates to paid
verification. **This is expected, not a bug.** `/metrics` is scrapeable locally
(authenticated).

---

### EV4 — Domain-level cache *(optional — do not build speculatively)*

Short-TTL in-memory cache collapsing repeated MX / catch-all probes for the same
domain within a run. A per-email result cache and an MX cache TTL already exist
(`SYNCORE_VERIFIER_CACHE_TTL`, `SYNCORE_VERIFIER_MX_CACHE_TTL`, plus PR #12's
domain-cache retention). The within-a-run collapse is **not** separately built.
**Add it only if a real batch profile shows redundant DNS/SMTP work.**

---

### EV5 — Pin the honesty guarantee in a test *(recommended, ~an hour)*

**Why this is worth doing:** `reasons.go` maps every inconclusive cause to
`unknown, retryable: true` correctly today, but no test asserts it. The existing
`TestReasonMetadata_CompleteAndConsistent` only checks that each code has *a*
valid status — a one-line edit turning `smtp_timeout` into `invalid` would keep
the suite green while breaking the property §0 calls irreplaceable.

**Task:** a table test in `internal/classify` pinning the inconclusive set
(`temporary_rejection`, `rate_limited`, `provider_blocked`, `dns_error`,
`smtp_timeout`, `connection_refused`, `smtp_inconclusive`) to
`Status == StatusUnknown && Retryable == true`.

**Acceptance:** flipping any one of those to `invalid` in `reasonMeta` fails the
suite. Prove it by making the edit locally and watching it go red.

---

## 4. Contracts

**Consumed:** none. This service calls nothing.

**Provided (schemas live in `syncore-contracts`; Go keeps a mirror + conformance test):**

| Endpoint | Purpose | Notes |
|---|---|---|
| `POST /v1/verifications` | Single verification | Sync |
| `POST /v1/verifications:batch` | Bounded sync batch | Cap 100 by default; ≈215s bound |
| `POST /batches` | Async batch + completion webhook | **The Hub's primary path**; cap 10,000 |
| `GET /batches/:id/results` | Paged results | Fallback if a webhook is missed |
| `POST /v1/feedback/{ses,smartlead}` | Bounce/complaint ingestion | Feeds domain reputation |
| `POST /admin/erasure` | Right-to-erasure | Compliance |

**Completion webhook (verifier → Hub):** signed `X-Syncore-Signature: HMAC-SHA256(rawBody, key)`. The Hub verifies it and dedupes on `(batch_id, email)`.

**Result shape (per item):** `email`, `status` (`valid|invalid|risky|unknown`), `reason_code`, `retryable`, `confidence`, plus the sub-signals (`disposable`, `role_account`, `free_provider`, `catch_all`) and `checked_at`.

---

## 5. Anti-scope — do NOT build

- Campaign, cost, or CRM awareness of any kind
- A paid-provider fallback (MillionVerifier runs in the **Hub**)
- A UI or dashboard *(note: an optional dev console exists behind `SYNCORE_VERIFIER_DEV_CONSOLE`, off by default — it is a local debugging aid, not a product surface)*
- Persistence beyond the existing cache/idempotency tables
- Any change that makes an inconclusive result look conclusive

---

## 6. Session protocol

1. Read this file, [`../INVENTORY.md`](../INVENTORY.md), and v9.1 §3.4, §9.6
2. Re-run the §1 ground-truth commands — **do not trust prior claims, including this file's status header**
3. Propose a phase-scoped plan; get approval
4. Implement in small commits; `go build ./... && go vet ./... && go test ./...` after each
5. `/clear` between phases

**CI:** `go build`, `go vet`, `go test` on every push. Live SMTP tests excluded.
**Branching:** never develop directly on `main` (see [`../AGENTS.md`](../AGENTS.md)).
