# INVENTORY — ground-truth state of `syncore-email-verifier`

**Verified:** 2026-07-28, on `main` at `508fd9f`, by re-running the ground-truth
commands in [`docs/GROWTH_OS_PLAN_v9.1.md`](docs/GROWTH_OS_PLAN_v9.1.md) §1.
This is the EV0 deliverable: what is *actually* in the code, with file:line
evidence, so no future session has to take a plan document's word for it.

**Headline:** phases **EV0–EV3 are complete**, and all three items the plan
listed as "needs confirming" are implemented and tested. See
[Open items](#open-items) for the two things that are genuinely not done.

---

## Baseline

| Check | Result |
|---|---|
| `go build ./...` | clean |
| `go vet ./...` | clean |
| `go test ./...` | green — 13 packages, no failures, no skips in the default suite |

Module name `github.com/AfterShip/email-verifier` ([go.mod:1](go.mod#L1)) and the
MIT licence with the AfterShip copyright ([LICENSE:1-3](LICENSE#L1-L3)) are both
preserved, as golden rule 2 requires.

## Route inventory

All registered in [cmd/apiserver/server.go:27-41](cmd/apiserver/server.go#L27-L41):

| Route | Auth | Notes |
|---|---|---|
| `GET /v1/:email/verification` | required | |
| `POST /v1/verifications` | required | honours `Idempotency-Key` |
| `POST /v1/verifications:batch` | required | bounded sync batch |
| `POST /batches` | required | async + completion webhook |
| `GET /batches/:id` | required | |
| `GET /batches/:id/results` | required | paged |
| `POST /v1/feedback` | required **+ HMAC** | second factor: `jobs.SignatureHeader`, constant-time ([feedback.go:50](cmd/apiserver/feedback.go#L50)) |
| `POST /v1/feedback/ses` | required **+ adapter token** | constant-time ([feedback_adapters.go:36](cmd/apiserver/feedback_adapters.go#L36)) |
| `POST /v1/feedback/smartlead` | required **+ adapter token** | as above |
| `POST /admin/erasure` | required | |
| `GET /health` | **open** | liveness |
| `GET /ready` | **open** | readiness |
| `GET /metrics` | required | carries operational data |
| `GET /` | required | only when `SYNCORE_VERIFIER_DEV_CONSOLE` is set; **off by default** ([server.go:45-48](cmd/apiserver/server.go#L45-L48)) |

Auth runs **before** routing and panic recovery wraps both, so the exempt list is
the only way past it — including on `NotFound` and `MethodNotAllowed`
([server.go:20-23](cmd/apiserver/server.go#L20-L23), [:57-60](cmd/apiserver/server.go#L57-L60)).
The feedback endpoints are therefore doubly gated: bearer *and* their own
signature or adapter token.

The plan's contract table (§4) matches this. Nothing the Hub is documented to
call is missing.

## The three "needs confirming" items — all closed

### 1. Bearer auth — implemented, required by default ✅

Enforced on every route except `/health` and `/ready`
([server.go:150-166](cmd/apiserver/server.go#L150-L166), exemption list at
[server.go:143-145](cmd/apiserver/server.go#L143-L145)). Comparison is
constant-time via `subtle.ConstantTimeCompare`
([server.go:95](cmd/apiserver/server.go#L95)) — no `==` anywhere on the credential
path. Failures return `401` with a generic message and no detail leakage
([server.go:161](cmd/apiserver/server.go#L161)). The `Bearer` scheme is matched
case-insensitively per RFC 7235 ([server.go:171-177](cmd/apiserver/server.go#L171-L177)).

Tests in [cmd/apiserver/auth_test.go](cmd/apiserver/auth_test.go) cover all four
acceptance cases plus malformed headers (`Basic …`, bare token, `Bearer` with no
space, `Bearer ` with an empty credential — [auth_test.go:60](cmd/apiserver/auth_test.go#L60)).

**Two documented divergences from the plan, both deliberate:**

- Auth accepts **hashed API keys** as well as the single
  `SYNCORE_VERIFIER_AUTH_TOKEN` ([server.go:87-105](cmd/apiserver/server.go#L87-L105),
  `SYNCORE_VERIFIER_API_KEYS`). The plan only describes the bearer token. This is
  a superset — the Hub's single-token path is unaffected.
- `/ready` is **exempt** from auth alongside `/health`. The plan said `/ready`
  *may* require auth, so this is a permitted choice, not a gap. `/metrics` is
  *not* exempt ([observability_test.go:74](cmd/apiserver/observability_test.go#L74)).

### 2. Non-loopback-without-token startup refusal — wired ✅

`validateBindSecurity` refuses to start when `SYNCORE_VERIFIER_BIND_ADDR` is
non-loopback and no token (or API key) is configured
([internal/config/config.go:366-381](internal/config/config.go#L366-L381)), with
loopback detection at [config.go:387-392](internal/config/config.go#L387-L392) —
an unparseable or empty host is treated as non-loopback (fails safe).

Covered by [config_test.go:167](internal/config/config_test.go#L167) (refusal),
[config_test.go:220](internal/config/config_test.go#L220) (allowed with a token),
[config_test.go:135](internal/config/config_test.go#L135) (allowed with API keys),
and [config_test.go:239](internal/config/config_test.go#L239) (loopback needs no token).

### 3. Batch write-timeout derivation — batch-aware ✅

`writeTimeoutFor` takes the **larger** of the single-verification bound and the
full-batch worst case, plus 15s headroom, floored at 35s
([server.go:208-230](cmd/apiserver/server.go#L208-L230)). The batch worst case is
`ceil(BatchMaxItems / BatchConcurrency) × (ConnectTimeout + OperationTimeout)`
([server.go:232-243](cmd/apiserver/server.go#L232-L243)) — this is the plan's
EV2 option (a), derived from the batch cap and SMTP concurrency.

At defaults — 100 items, concurrency 10, 10s connect + 10s operation
([config.go:199](internal/config/config.go#L199), [config.go:202](internal/config/config.go#L202),
[config.go:175-178](internal/config/config.go#L175-L178)) — that is
`10 rounds × 20s = 200s`, so `WriteTimeout ≈ 215s`. Locked by
[batch_test.go:197-216](cmd/apiserver/batch_test.go#L197-L216) (both the
batch-aware case and the single-verification floor).

The bound is written down for the Hub in
[deploy/README.md:91-112](deploy/README.md#L91-L112), including the instruction
to chunk to `BATCH_MAX_ITEMS` and to raise any reverse-proxy timeout to match.

Per-item isolation — the plan's other EV2 acceptance criterion — is covered by
[`TestBatch_PerItemInvalidDoesNotFailBatch`](cmd/apiserver/batch_test.go#L68) and
[`TestBatch_PerItemFaultBecomesUnknown`](cmd/apiserver/batch_test.go#L94): one
deliberately failing address degrades to `unknown/retryable` for that item and
does not fail its batch.

**Async `/batches`:** completion is decoupled from the request — only the `202`
needs a normal timeout, as the plan predicted. Worth noting the async cap is
`SYNCORE_VERIFIER_ASYNC_BATCH_MAX_ITEMS`, defaulting to **10,000**
([config.go:232](internal/config/config.go#L232)) — two orders of magnitude above
the sync cap, which is the correct shape for the Hub's primary path.

## Honesty guarantee — holds in code

The reason-code → status table is at
[internal/classify/reasons.go:52-70](internal/classify/reasons.go#L52-L70). Every
inconclusive cause maps to `unknown, retryable: true`:

| Reason code | Status | Retryable | Confidence |
|---|---|---|---|
| `temporary_rejection` | `unknown` | true | 20 |
| `rate_limited` | `unknown` | true | 15 |
| `provider_blocked` | `unknown` | true | 15 |
| `smtp_timeout`, `connection_refused`, `dns_error`, `smtp_inconclusive` | `unknown` | true | 10–20 |
| `syntax_invalid` | `invalid` | false | 100 |
| `domain_not_found`, `null_mx` | `invalid` | false | 99 |

`Classify` can never fall through to an unmapped reason — ambiguous evidence
resolves to `smtp_inconclusive` (`unknown`), and `internal_error` is explicitly
barred from being a verification reason code
([classify_test.go:37-50](internal/classify/classify_test.go#L37-L50)).

## Deployment

[`deploy/`](deploy/) contains the systemd unit
([syncore-email-verifier.service](deploy/syncore-email-verifier.service)), a PM2
config, an env template ([env.example](deploy/env.example)), and a guide covering
the co-located-vs-port-25 tradeoff. The **port-25 reality** is documented at
[deploy/README.md:83-89](deploy/README.md#L83-L89) and
[:132](deploy/README.md#L132): from a host without outbound 25 (default AWS EC2),
mailbox probes return `unknown` (retryable), never `invalid` — expected, not a bug.

## Configuration

Env-only, all `SYNCORE_VERIFIER_*` prefixed, 36 variables declared as constants
at [config.go:19-54](internal/config/config.go#L19-L54), with fail-fast
validation. No config files, no flags.

## Open items

Two things are genuinely not done. Neither blocks the Hub.

1. **No test locks the honesty guarantee.** The mapping in `reasons.go` is
   correct, and `TestReasonMetadata_CompleteAndConsistent`
   ([classify_test.go:12-40](internal/classify/classify_test.go#L12-L40)) asserts
   every code has *a* valid status — but nothing asserts that the *inconclusive*
   codes specifically are never `invalid`. A one-line edit to `reasonMeta` could
   silently turn `smtp_timeout` into `invalid` and the suite would stay green.
   Given this is the property the plan says to "protect above all else", a table
   test pinning the inconclusive set to `unknown, retryable: true` is cheap
   insurance. **Recommended.**

2. **EV4 domain-level cache — partially present.** A per-email result cache and
   an MX cache TTL exist (`SYNCORE_VERIFIER_CACHE_TTL`,
   `SYNCORE_VERIFIER_MX_CACHE_TTL`, [config.go:30-32](internal/config/config.go#L30-L32),
   plus the domain-cache retention work in PR #12). The plan's EV4 —
   collapsing repeated MX/catch-all probes for the same domain *within a run* —
   is not separately implemented, and the plan says explicitly not to build it
   speculatively. **Leave it** until a real batch profile shows redundant work.

## What this repo is waiting on

Nothing here blocks Phase 3. The roadmap's "one thing that matters" for P3 is
**fixing the verifier bridge**, and that work is entirely in `syncore-lead-hub`:
it still posts to a nonexistent `/api/verify` instead of `POST /batches`, sends
no bearer token, and does not verify the completion webhook's
`X-Syncore-Signature` (v9.1 §3, §9.6). The contract on this side is live,
authenticated, and documented.
