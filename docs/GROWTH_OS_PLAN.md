# Growth OS Plan (Reconstructed)

> **Reconstructed document.** The original `GROWTH_OS_PLAN` was never committed to this repository. This file reconstructs the plan's scope and intent from the implementation and status records — [`IMPLEMENTATION_STATUS.md`](IMPLEMENTATION_STATUS.md) and [`REMAINING_WORK.md`](REMAINING_WORK.md) — so the repo has an **auditable source of truth** to check the code against. Where this differs from the original intent, the status/remaining docs are authoritative for *what shipped*.
>
> Delivered on branch `feature/growth-os-foundation`, **merged into `main` via PR #4** (merge commit `ec8ac5b`). Companion planning docs: [`ROADMAP.md`](ROADMAP.md) (full enterprise plan) and [`LOW_COST_ROADMAP.md`](LOW_COST_ROADMAP.md) (cost-optimized path).

---

## Goal

Take the committed **Phase 1** service (engine evidence → classification → verification service → hardened HTTP API) and layer on the **enterprise foundation** needed to run it as a real internal product for Syncore Lead Engine CRM — persistence, batch, feedback, auth, observability, and compliance — **plus a small "pilot slice" (EV0–EV4)** that makes it deployable and callable from the CRM.

Everything is **additive and config-flagged**: with no new environment variables set, behavior is identical to Phase 1.

## Non-goals (deliberately out of scope for this effort)

SMTP egress infrastructure (Phase 4, blocked — needs a port-25 VPS), the provider-strategy registry (Phase 5), spam-trap/engagement intelligence (Phase 11), CRM-repo integration (separate repo), durable Postgres-backed job queue and quota, encryption-at-rest, and distributed tracing/dashboards. These are tracked in [`REMAINING_WORK.md`](REMAINING_WORK.md).

---

## The pilot slice (EV0–EV4)

The minimum needed to deploy the service and have the CRM call it.

| ID | Objective | Acceptance |
|---|---|---|
| **EV0** | Branch + green baseline | New branch off Phase-1 `main`; `go build ./... && go vet ./... && go test ./...` green from the first commit. |
| **EV1** | Bearer-token auth + safe bind | Verification endpoints require `Authorization: Bearer <token>` when `AUTH_TOKEN` is set (constant-time compare); `GET /health` stays open. **Startup fails fast** if bound to a non-loopback address with no auth token. |
| **EV2** | Stateless synchronous batch endpoint | `POST /v1/verifications:batch` verifies a list with a bounded worker pool, ordered results, and per-item faults degraded to `unknown/retryable`; documented timing bound. |
| **EV3** | Co-located deployment path | `deploy/` with a systemd unit + PM2 config + env template + guide covering the co-located-vs-port-25 tradeoff, the port-25 reality, and the batch timing bound. |
| **EV4** | Result cache (optional) | A cache that dedups repeat verifications, with TTL by outcome (long for `valid`/`invalid`, short for `unknown`). *Note: shipped as a per-email result cache; a domain-level MX/catch-all cache within a batch was deferred — see `REMAINING_WORK.md`.* |

## Enterprise phases targeted (subset of `ROADMAP.md`)

The plan pulled the *buildable-now, free, verifiable* enterprise phases forward on top of the pilot slice:

| Phase | Objective | Delivered as |
|---|---|---|
| **2 — Persistence, cache, idempotency** | Durable store + TTL result cache + idempotency | `internal/store` generic `Store[V]` (in-memory + Postgres via `pgx`), `Idempotency-Key` on `POST /v1/verifications`. |
| **3 — Async jobs** | Batch jobs + retry + signed webhooks | `internal/jobs`: `POST /batches` (+ status + paginated results), bounded workers, retry worker for retryable unknowns, HMAC-SHA256 completion webhooks. *(In-memory queue; durable Postgres queue deferred.)* |
| **6 — Deliverability score + domain health** | Blended score + free domain signals | `deliverability_score` (0–100) + `score_components`; optional SPF/DMARC/MX health. *(DNSSEC/domain-age/DNSBL/DKIM deferred.)* |
| **7 — Feedback loop** | Learn from real sending outcomes | `internal/feedback`: signed `POST /v1/feedback`, per-domain reputation priors closed into the score. |
| **8 — Auth, quotas** | API keys + limits | Bearer token + hashed API keys, per-client rate limiting + daily quota. *(Full multi-tenancy/credits deferred.)* |
| **9 — Observability** | Metrics + logs + readiness | Dependency-free Prometheus `/metrics`, structured slog access logs, `/ready`. *(Tracing/dashboards deferred.)* |
| **10 — Compliance** | Suppression + erasure + audit | Suppression list (honored before any network check), `POST /admin/erasure`, hashed-email audit log. *(Encryption-at-rest/retention/SOC2 deferred.)* |

## Guardrails (must hold across every item)

- Preserve the **MIT licence and AfterShip attribution**.
- **No paid dependency** without explicit approval (the one new dependency, `pgx`, is free/MIT).
- **Configuration from environment variables only** (`SYNCORE_VERIFIER_*`); no secrets in code or logs (audit logs carry only a SHA-256 of the email).
- **Deterministic default tests** (no public network/DB); live tests behind `//go:build live`; Linux CI runs `-race`.
- **Timeouts / temporary failures / provider blocks are always `unknown`, never `invalid`.** No email message is ever sent.
- **Additive, config-flagged** rollout — the service still runs in its simplest Phase-1 mode with nothing set.

## Verification bar

`go build ./... && go vet ./... && go test ./...` green across all packages; the Postgres path exercised by a `//go:build live` integration test; an end-to-end smoke test of every new endpoint; the feedback loop verified live (ingesting bounces lowers a subsequent `deliverability_score`).

---

## Source-of-truth map

- **This file** — the plan (reconstructed): intended scope + acceptance.
- [`IMPLEMENTATION_STATUS.md`](IMPLEMENTATION_STATUS.md) — what actually shipped, phase by phase, with verification.
- [`REMAINING_WORK.md`](REMAINING_WORK.md) — what was deliberately left, grouped by why (blocked / deferred / done-but-limited).
- [`ROADMAP.md`](ROADMAP.md) / [`LOW_COST_ROADMAP.md`](LOW_COST_ROADMAP.md) — the full and cost-optimized forward plans.
