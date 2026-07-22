# Syncore Email Verifier — Roadmap (Phase 2 → Enterprise Grade)

**Audience:** the engineer(s) building this out.
**Goal of this document:** a complete, phase-by-phase plan to evolve the Phase 1 local verification service into an enterprise-grade email verification platform comparable to ZeroBounce / Bouncer / NeverBounce, and to integrate it deeply with **Syncore Lead Engine CRM**.

> Read [docs/PHASE_1_PLAN.md](PHASE_1_PLAN.md) and [AGENTS.md](../AGENTS.md) before starting. Phase 1 (1A engine evidence, 1B classification + service, 1C config + HTTP API, 1D docs/CI) is **complete and committed**.

---

## 0. The honest framing (read this first)

The verification **logic** is already ~80% of a commercial product. The gap to ZeroBounce is **two moats you build, not buy**:

1. **Infrastructure moat — SMTP egress + IP reputation.** Real mailbox checks need port-25 egress from IPs with rDNS/PTR, SPF-aligned HELO, warmed reputation, and clean blocklist status. Without this, Microsoft/Yahoo/Apple return `unknown`. **This is the single biggest accuracy lever (Phase 4).**
2. **Data moat — bounce history + spam-trap intelligence.** Their accuracy comes from proprietary datasets accumulated over years. **Your unfair advantage:** the verifier is wired to a CRM that *sends* (SES + Smartlead), so you can build a closed feedback loop most standalone verifiers can't (Phase 7).

Everything else (persistence, batch, queue, auth, dashboard) is standard engineering. **Sequence the moats early; they compound.**

## 0.1 Guiding principles (apply to every phase)

- **Preserve the MIT licence and upstream AfterShip attribution.** Never remove them.
- **No paid third-party dependency without explicit written approval** (AGENTS.md). Any paid-provider fallback is a separate, approval-gated task.
- **Configuration via environment variables** (`SYNCORE_VERIFIER_*`), documented in `README.md` + `.env.example`. No secrets in code or logs.
- **Deterministic default test suite** — `go test ./...` contacts no public network. Live/integration tests behind the `//go:build live` tag. New phases keep this invariant.
- **Never classify a timeout / temporary failure / provider block as `invalid`** — those are `unknown`.
- **Structured JSON everywhere; sanitized errors** — never leak SMTP text, IPs, proxy URIs, credentials, or stack traces.
- **Additive, not destructive** — extend `internal/*` packages; preserve existing engine behavior and public response shapes. Each phase ships behind config flags so the service still runs in its simpler mode.
- **Every phase is independently shippable** and leaves `main` green (build, vet, `go test ./...`, and Linux CI `-race`).

## 0.2 Target architecture (where this is heading)

```
                         ┌──────────────────────────────────────────────┐
   Lead Engine CRM ────► │  API layer (cmd/apiserver)                    │
   (Next.js/Prisma)      │  auth · rate-limit · real-time + batch · WH   │
                         └───────────────┬──────────────────────────────┘
                                         │
                    ┌────────────────────┼───────────────────────┐
                    ▼                    ▼                        ▼
            verification.Service   internal/store (Postgres)  internal/queue
            (classify pipeline)    cache · results · audit    jobs · retries
                    │                                             │
                    ▼                                             ▼
            engine (emailverifier)  ◄──── internal/egress ───►  worker pool
            evidence + strategies         proxy/MTA pool,       (batch + retry)
                    ▲                      IP rotation, rDNS           │
                    │                                                 ▼
            internal/feedback  ◄─────── SES bounce/complaint + Smartlead webhooks
            (closed accuracy loop)
```

## 0.3 Phase dependency map

```
Phase 1 (done)
   └─► Phase 2 (persistence/cache) ─► Phase 3 (batch/queue/retry) ─► Phase 4 (egress) ─► Phase 5 (providers/catch-all)
                    │                         │                                              │
                    └─────────► Phase 7 (feedback loop) ◄──────────────────────────────────┘
   Phase 6 (score/domain health) depends on 2+5 · Phase 8 (auth/tenancy) depends on 2
   Phase 9 (observability) parallel from Phase 3 · Phase 10 (compliance) parallel from Phase 2
   Phase 11 (trap/engagement) depends on 7 · Integration Track runs in parallel from Phase 2
```

Effort is a T-shirt estimate (S ≈ days, M ≈ 1–2 wks, L ≈ 2–4 wks, XL ≈ 1–2 mo) for one mid-level Go engineer; **indicative only**.

---

## Phase 2 — Persistence, Caching & Idempotency  ·  Effort: **M**

**Objective:** give the service durable memory so results can be reused, audited, and fed to later phases.

**Why it matters:** re-verifying the same address is slow and wasteful; every later phase (batch, retry, feedback, dashboard) needs a store. Results decay, so caching must be TTL-aware.

**Scope (in):**
- `internal/store` with a `Store` interface (Save/Get verification, upsert by normalized email). Two backends: in-memory (default, dev/test) and **PostgreSQL** (via `pgx` or `database/sql` + `lib/pq`; a lightweight query layer, not necessarily an ORM).
- Result **cache with TTL**: on `Verify`, return a cached result if fresh (`checked_at` within `RESULT_TTL`); otherwise re-verify and upsert. Different TTLs by outcome (e.g. `valid`/`invalid` long, `unknown` short) — configurable.
- **Email normalization** for cache keys (lowercase domain, trim, optional Gmail dot/plus normalization — behind a flag, since it changes identity).
- **Idempotency keys** for POST so retried CRM calls don't duplicate work.
- DB migrations (plain SQL files + a tiny migrator, or `golang-migrate`).

**Scope (out):** batch, queue, multi-tenancy (later phases).

**Config additions:** `SYNCORE_VERIFIER_STORE` (`memory`|`postgres`), `SYNCORE_VERIFIER_DATABASE_URL`, `SYNCORE_VERIFIER_RESULT_TTL`, `SYNCORE_VERIFIER_RESULT_TTL_UNKNOWN`.

**Acceptance criteria:**
- Service runs unchanged with `STORE=memory` (no Postgres required for dev/test).
- With Postgres, a repeated verification within TTL returns the cached row and does **not** re-hit DNS/SMTP (assert via the stub engine / call counters).
- Migrations apply cleanly; schema documented.
- Store interface is mockable; unit tests use the in-memory backend; a `//go:build live`/integration test covers Postgres (e.g. via testcontainers or a documented local DB) — excluded from the default suite.

---

## Phase 3 — Async Jobs: Batch Verification & Retry Queue  ·  Effort: **L**

**Objective:** scale beyond one-at-a-time and resolve the `unknown` tail.

**Why it matters:** enterprise buyers expect bulk list verification; `retryable=true` results (timeouts, greylisting, rate limits) become decisive on a later retry.

**Scope (in):**
- `internal/queue` with a `Queue` interface; backends: in-memory (default) and a durable **Postgres-backed job queue** (`SELECT … FOR UPDATE SKIP LOCKED`). Optionally Redis/river later, but avoid a heavy dependency without need.
- **Worker pool** with bounded concurrency (config), graceful drain on shutdown.
- **Batch API:**
  - `POST /v1/batches` — submit a list (JSON array, or an uploaded file reference) → returns `batch_id`.
  - `GET /v1/batches/{id}` — status + progress (queued/running/done, counts by status).
  - `GET /v1/batches/{id}/results` — paginated results (and/or downloadable NDJSON/CSV).
- **Retry worker:** re-verifies `retryable` unknowns on an exponential backoff with a max-attempts cap; **greylisting-aware** (short first retry). Never upgrades to `valid` without a real accept.
- **Webhooks/callbacks:** `POST` results to a CRM-supplied URL on batch completion and when a retried address resolves (HMAC-signed, retried on delivery failure).

**Config additions:** `SYNCORE_VERIFIER_WORKERS`, `SYNCORE_VERIFIER_QUEUE` (`memory`|`postgres`), `SYNCORE_VERIFIER_RETRY_MAX_ATTEMPTS`, `SYNCORE_VERIFIER_RETRY_BACKOFF`, `SYNCORE_VERIFIER_WEBHOOK_SIGNING_KEY`.

**Acceptance criteria:**
- A 10k-address batch processes with bounded concurrency and correct per-status counts; results downloadable.
- Retry worker re-queues only `retryable` results and stops at the cap; deterministic tests use a stub engine that flips `unknown`→`valid` on attempt N.
- Webhook delivery is signed and retried; a stub receiver test asserts payload + signature.
- Shutdown drains in-flight jobs; no lost/duplicated work (idempotency from Phase 2).

---

## Phase 4 — SMTP Egress & Deliverability Infrastructure  ·  Effort: **L–XL**  ·  *(highest accuracy leverage)*

**Objective:** actually reach mailboxes so `unknown` becomes decisive. **This is the #1 differentiator.**

**Why it matters:** from a normal cloud host port 25 is blocked → Microsoft/Yahoo/Apple time out → `unknown`. Commercial verifiers own reputable egress. Solve this and accuracy jumps.

**Scope (in):**
- `internal/egress`: a **connection-source abstraction** the engine dials through. Backends: direct (current), **SOCKS5 proxy pool** (engine already supports proxy), and a **pool of egress workers/MTAs** on port-25-capable hosts.
- **IP rotation & selection** across the pool; per-IP and per-destination-domain **rate limiting / throttling** so probes don't get the pool blocklisted.
- **Reputation hygiene:** documented requirement + healthchecks for **rDNS/PTR** on each egress IP, **HELO hostname aligned** to the IP's forward/reverse DNS, and **blocklist monitoring** (Spamhaus/Barracuda/etc.) with alerting and auto-quarantine of dirty IPs.
- **Greylisting/backoff coordination** with the Phase 3 retry worker.
- Ops runbook: how to provision a clean egress IP, set PTR, warm it, and monitor it.

**Scope (out):** running your own full MTA cluster if a managed egress/proxy provider suffices (evaluate; paid providers need approval).

**Config additions:** `SYNCORE_VERIFIER_EGRESS` (`direct`|`proxy_pool`|`worker_pool`), `SYNCORE_VERIFIER_EGRESS_PROXIES` (list), `SYNCORE_VERIFIER_EGRESS_RATE_PER_IP`, per-domain throttle table.

**Acceptance criteria:**
- Engine dials through the egress abstraction with **no change to classification logic**; `direct` remains the default and keeps all current behavior.
- Deterministic tests use a fake egress + fake SMTP server (extend Phase 1's fake) to prove rotation, throttling, and quarantine logic — **no public network**.
- Documented, reproducible manual/live validation from a port-25-capable host showing previously-`unknown` providers resolving to decisive results.
- Measurable **reduction in `unknown` rate** on a live sample (report before/after).

---

## Phase 5 — Provider-Specific Strategies & Catch-All Resolution  ·  Effort: **L**

**Objective:** close the accuracy gap on the hard cases: big providers and catch-all domains.

**Why it matters:** Gmail/O365/Yahoo/iCloud each behave differently; catch-all is where verifiers differentiate most (a flat `risky/catch_all` under-serves the customer).

**Scope (in):**
- **Strategy interface** (`ProviderStrategy`) selected by MX/domain fingerprint; a registry with per-provider implementations. Generalize the existing Yahoo API-verifier path into this framework.
- Provider modules: Gmail, Microsoft (O365/Outlook/Hotmail), Yahoo, Apple/iCloud, generic SMTP — each encapsulating the known-best technique and its quirks; versioned so they can be updated as providers change.
- **Catch-all resolution beyond binary:** probe patterns (known-invalid + plausible-valid), historical bounce data for the domain (from Phase 7), and heuristics to emit `catch_all` with a **sub-confidence** ("catch-all, likely valid/invalid") instead of a flat risky. Keep `catch_all` tri-state evidence intact.
- Confidence-scoring hooks feeding Phase 6.

**Acceptance criteria:**
- Strategy selection is deterministic and unit-tested via the fake SMTP server with per-provider scripted responses.
- Adding/updating a provider strategy is isolated (no engine-wide changes).
- Catch-all outputs carry a documented sub-confidence; precedence and reason codes remain consistent with Phase 1B.

---

## Phase 6 — Deliverability Score & Domain-Health Signals  ·  Effort: **M**

**Objective:** a single, actionable **0–100 deliverability score** plus sub-signals, and richer domain intelligence.

**Scope (in):**
- **Signal fusion:** blend syntax, MX/host source, disposable, role, free, catch-all sub-confidence, provider strategy result, SMTP outcome, and (later) feedback signals into one score + component sub-scores. Deterministic, documented weighting; keep the existing `confidence` field and add `deliverability_score`.
- **Domain health:** MX health/reachability, SPF/DKIM/DMARC record presence, DNSSEC, domain age/blocklist checks; expose as `domain` evidence.
- **Typo/"did you mean"** upgraded with confidence (extends the existing suggestion engine).

**Acceptance criteria:**
- Score is pure and unit-tested across representative evidence combinations; documented formula in `internal/classify` (kept free of I/O — domain-health lookups live in the service/engine, not the pure classifier).
- New fields are additive to both GET (legacy) and POST responses.

---

## Phase 7 — Feedback Loop & Continuous Accuracy  ·  Effort: **L**  ·  *(the data moat — your unfair advantage)*

**Objective:** get more accurate over time by learning from real sending outcomes. **Most standalone verifiers cannot do this; you can.**

**Why it matters:** bounce/complaint data is the highest-quality accuracy signal. You already send via SES + Smartlead.

**Scope (in):**
- `internal/feedback`: ingest **SES bounce/complaint SNS notifications** and **Smartlead webhooks** (delivered/bounced/complained/replied), normalize, and store keyed to `email + reason_code + checked_at`.
- **Closed-loop correction:** when a delivery outcome contradicts a prediction (e.g. a `valid` hard-bounces, or a `catch_all` engages), record it and adjust — per-domain reputation tables, catch-all likelihood priors (feeds Phase 5), and score weights (feeds Phase 6).
- **Accuracy metrics:** predicted-vs-actual dashboards (bounce rate by predicted status, unknown-resolution rate), exportable.
- Optional later: an ML risk model trained on the accumulated labels (out of scope for the first cut — start with transparent rules/priors).

**Acceptance criteria:**
- Ingestion endpoints validate signatures (SES/SNS confirmation, Smartlead signing) and are idempotent.
- A documented, deterministic replay test feeds synthetic bounce events and asserts the resulting reputation/priors updates.
- Demonstrated improvement: re-scoring a labeled sample after feedback moves accuracy in the right direction (report the metric).

---

## Phase 8 — Multi-Tenancy, Auth, Quotas & Credits  ·  Effort: **M–L**

**Objective:** turn the service into a real, safe-to-expose API (beyond localhost).

**Scope (in):**
- **API keys** + tenant model; per-key scopes. Keys hashed at rest.
- **Rate limiting** (per key/tenant) and **quotas**; **credit accounting/metering** per verification and per batch.
- Optional admin endpoints to issue/revoke keys and read usage.

**Config/security:** keys in DB (hashed), signing secrets from env; document rotation. Middleware in `cmd/apiserver`.

**Acceptance criteria:**
- Unauthenticated requests to protected routes → 401 JSON; over-quota → 429 JSON (consistent envelope).
- `/health` stays public and auth-free.
- Deterministic handler tests cover auth, rate-limit, and quota paths with a stub store.

---

## Phase 9 — Observability, SLOs, Dashboard & Reporting  ·  Effort: **M**

**Objective:** operate it like a product; prove accuracy and reliability.

**Scope (in):**
- **Metrics** (Prometheus): verification volume, latency histograms, status/reason distributions, **unknown rate per provider**, queue depth, worker utilization, egress IP health.
- **Structured logging** (sanitized) + **tracing** (OpenTelemetry) with request IDs.
- **Readiness/liveness** endpoints (extend `/health` with a deeper readiness check for DB/queue).
- **Admin/reporting dashboard** (can be a page in the CRM or a small internal UI): accuracy, unknown-resolution, deliverability impact on SES/Smartlead bounce rates.

**Acceptance criteria:**
- Metrics endpoint exposed and documented; dashboards defined.
- No PII or secrets in logs/metrics (test/assert sanitization).

---

## Phase 10 — Compliance, Security & Data Governance  ·  Effort: **M**  ·  *(parallelizable)*

**Objective:** meet the bar enterprise buyers require.

**Scope (in):**
- **GDPR/CCPA:** data retention policy, **right-to-erasure** endpoint, configurable retention windows, PII minimization (store hashes where feasible).
- **Suppression / do-not-verify lists** (per tenant) honored before any network check.
- **Encryption at rest** for sensitive columns; secrets via env/secret manager; dependency scanning in CI.
- **Audit log** of verifications and admin actions (immutable).
- Document a SOC2-readiness posture (controls, access, logging).

**Acceptance criteria:**
- Erasure removes/anonymizes all traces of an address; test proves it.
- Suppressed addresses are never network-verified; test proves the short-circuit.

---

## Phase 11 — Advanced Data Moat: Spam-Trap / Toxic / Engagement Intelligence  ·  Effort: **L–XL**  ·  *(last mile to ZeroBounce-grade)*

**Objective:** the hardest, highest-value data signals.

**Why it matters:** sending to spam traps destroys sender reputation; commercial verifiers' trap coverage is a core selling point.

**Scope (in):**
- **Spam-trap / honeypot detection:** curated trap indicators, toxic/abuse domain lists, known-complainer/litigator lists, and heuristics (zero-engagement domains from Phase 7 data). Emit a `toxic`/`spam_trap` risk signal.
- **Engagement/activity scoring:** derive "recently active?" from CRM opens/clicks/replies (via Phase 7 data) — the ZeroBounce "activity data" analogue.
- **Threat-feed integration** (approval-gated if paid): blocklist/abuse feeds.

**Acceptance criteria:**
- New risk signals are additive evidence with documented precedence; never override a definitive `invalid`/`valid` incorrectly.
- Deterministic tests with synthetic trap/engagement fixtures.

**Reality check:** trap coverage takes time to accumulate. Start with your own CRM engagement + public abuse feeds; expand as data grows.

---

## Integration Track (parallel with Phases 2+) — Lead Engine CRM

Deliver value early; don't wait for Phase 12.

**Deliverables (in the `lead-engine-crm` repo, Next.js/TS + Prisma/Postgres):**
- **Prisma `EmailVerification` model** joined to `Lead` (email, status, reason_code, retryable, confidence, deliverability_score, checked_at, source, evidence JSON) with a freshness TTL.
- **`lib/verifier.ts` client** + a Next.js API route / server action calling `POST /v1/verifications` during the **enrichment** step (symmetric with Twilio Lookup for phones).
- **Outreach gate** before Smartlead/SES sends: suppress `invalid`, flag `risky`, queue `unknown/retryable`.
- **Batch list verification** from CRM imports (uses Phase 3 batch API).
- **Webhook receiver** in the CRM for async/retry results (Phase 3) and the reverse **bounce/complaint forwarder** into the verifier's feedback loop (Phase 7).
- **Audit surfacing:** show `reason_code`/`confidence`/`checked_at` on the lead record.

**Deploy topology:** run the Go service where **port 25 egress is open** (or via the proxy/MTA pool from Phase 4); the CRM reaches it over a private network with an API key (Phase 8).

---

## Cross-cutting requirements (every phase)

- **Tests:** deterministic default suite (no public network); live/integration tests behind `//go:build live`; keep Linux CI `-race` green.
- **Docs:** update `README.md`, `.env.example`, and `CHANGELOG.md`; add a short design note per phase under `docs/`.
- **Backwards compatibility:** existing GET/POST response shapes remain valid; new fields are additive.
- **Config-flagged rollout:** each capability can be disabled to run the simpler service.
- **Security:** no secrets/PII/SMTP-text/IPs/proxy-URIs in responses, logs, or metrics.

## Suggested sequencing (fastest path to "feels enterprise")

1. **Phase 2 + Phase 3** — persistence, cache, batch, retry, webhooks → it *scales* like a paid tool.
2. **Phase 4** — egress → it becomes *accurate* (kills the `unknown` tail).
3. **Phase 7** — feedback loop → it gets *better over time* (the moat).
4. **Phase 5 + Phase 6** — provider strategies, catch-all nuance, deliverability score → closes the accuracy gap.
5. **Phase 8 + Phase 9** — auth/tenancy, observability/dashboard → sellable/operable.
6. **Phase 10 + Phase 11** — compliance + trap/engagement intelligence → ZeroBounce-grade.
7. **Integration Track** in parallel throughout, delivering CRM value from Phase 2 onward.

*Estimates are indicative for one mid-level Go engineer; parallelize where the dependency map allows.*
