# Remaining Work — What's Left Behind

**Branch:** `feature/growth-os-foundation` — **merged into `main` via [PR #4](https://github.com/syncoretech01/syncore-email-verifier/pull/4)** (merge commit `ec8ac5b`).
Companion to [`IMPLEMENTATION_STATUS.md`](IMPLEMENTATION_STATUS.md) and [`GROWTH_OS_PLAN.md`](GROWTH_OS_PLAN.md). Grouped by *why* each item is not done. Nothing here blocks the pilot — each is a deliberate, documented boundary.

---

## A. Genuinely blocked — needs infrastructure or a decision (cannot build at this stage)

| Item | Phase | Why blocked | What it needs to proceed |
|---|---|---|---|
| **SMTP egress pool / IP rotation / rDNS-PTR / DNSBL self-check** | 4 | AWS EC2 blocks outbound port 25; real mailbox reachability needs a clean port-25 IP with reverse DNS | A VPS that allows outbound TCP :25 **and** lets you set PTR; then build `internal/egress` behind the engine's existing dial seam, with per-domain throttling + blocklist self-check |
| **CRM Integration Track** (Prisma `EmailVerification` model, `lib/verifier.ts`, outreach gate, webhook receiver, bounce forwarder) | Integration | Lives in a **separate repo**, and no Prisma project exists on disk (`Twenty` is TypeORM; `syncoretech` is Next.js without Prisma) | Decide which repo is the CRM; the verifier already exposes everything the adapter needs (verify, batch, feedback, webhooks) |
| **Spam-trap / toxic / engagement intelligence** | 11 | Depends on accumulated feedback data + (approval-gated) paid trap/threat feeds | Let Phase-7 data accrue; derive zero-engagement / trap signals from your own CRM data first |
| **SES-SNS / Smartlead-specific ingestion adapters** | 7 | The generic signed `POST /v1/feedback` works, but provider-specific signature verification (SES SNS cert chain) can't be tested without live SNS | Thin adapters that normalize each provider's payload into the existing endpoint, validated against live webhooks |

## B. Buildable, but with real caveats (external deps / marginal value) — deliberately deferred

| Item | Phase | Caveat |
|---|---|---|
| **DNSSEC** signal | 6 | The Go stdlib can't read the resolver AD flag / DS records — needs a new dependency (`miekg/dns`) for one boolean of marginal deliverability value |
| **Domain-age** signal | 6 | Requires per-domain **RDAP HTTP** calls (external, rate-limited by registries); adds latency; weak deliverability signal |
| **DNSBL** blocklist check | 6 | Requires live external DNSBL queries per MX-IP; adds latency; more relevant to Phase-4 egress-IP monitoring than to domain health |
| **DKIM** presence | 6 | Selector-specific — cannot be verified without a signed message (documented in code) |

## C. Built, but with a known limitation ("done, but…")

| Item | Limitation | If you want it hardened later |
|---|---|---|
| **Async batch queue** (Phase 3) | Job store is **in-memory** — a crash/restart loses queued/in-flight batches | Add a durable Postgres-backed queue (`SELECT … FOR UPDATE SKIP LOCKED`) behind the existing `jobs` interface |
| **Daily quota** (Phase 8) | **In-memory** — counters reset on restart and aren't shared across instances | Back it with the store using atomic counters (needs an atomic-increment op on the `Store` interface) |
| **Multi-tenancy / credits** (Phase 8) | API keys authenticate, but there's **no tenant model, per-key scopes, credit metering, or admin issue/revoke endpoints** | Add a tenant/key table + accounting once the service is sold externally |
| **EV4 domain-level cache** | The result cache dedups repeat *same-email* checks, but doesn't collapse repeated *MX/catch-all* probes for the same domain within one batch | Add a short-TTL per-domain MX/catch-all cache at the engine stage |
| **Compliance** (Phase 10) | Suppression + erasure + audit are done; **no encryption-at-rest for sensitive columns, configurable retention/auto-purge, or SOC2 posture doc** | Add on demand for external/enterprise deployments |
| **Observability** (Phase 9) | Metrics + structured logs + readiness are done; **no OpenTelemetry tracing or prebuilt dashboards** | Add tracing + Grafana dashboards when operating at scale |

---

## Suggested next step (highest value, lowest cost)

**Phase 4 egress on a port-25 VPS.** It's the single biggest accuracy lever — it turns many `unknown` results into decisive `valid`/`invalid`, reducing dependence on paid Hunter/ZeroBounce. Everything upstream (classification, retry, feedback) is already built to feed it, so the marginal work is just the egress abstraction + a clean IP.

Second-highest: **durable Postgres-backed job queue** (crash-safe async batches) and **store-backed quota counters** — both small, both remove the "in-memory, resets on restart" caveats above.

---

_Prepared 2026-07-22 · Companion: `IMPLEMENTATION_STATUS.md`_
