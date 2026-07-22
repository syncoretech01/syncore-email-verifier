# Syncore Email Verifier — Low-Cost Roadmap

**Audience:** the engineer(s) building this out on a tight budget.
**Goal:** reach "genuinely good, ZeroBounce-comparable for our CRM's needs" for **~$5–15/month of infrastructure** (plus developer time) — no licensing, no per-verification fees.

> Companion to the full [docs/ROADMAP.md](ROADMAP.md). That document is the complete enterprise plan; **this** document is the cost-optimized path through it. Same phases, reprioritized and trimmed for minimum spend. Read [docs/PHASE_1_PLAN.md](PHASE_1_PLAN.md) and [AGENTS.md](../AGENTS.md) first. Phase 1 is complete and committed.

---

## 1. The thesis (why "good" can be cheap)

Three facts make a low-cost build viable:

1. **Almost all accuracy is free.** Syntax, MX, **Null MX**, implicit A/AAAA, disposable, role account, free provider, **catch-all detection**, and typo suggestion cost nothing but DNS lookups — and they already exist (Phase 1). They remove most junk *before* you send.
2. **Your biggest accuracy multiplier is nearly free.** You already send via **SES + Smartlead**. Feeding their bounce/complaint events back in (Phase 7) costs fractions of a cent and makes you *more accurate over time* — the thing you'd otherwise rent from ZeroBounce.
3. **Only mailbox reachability costs money, and it can be tiny.** Reaching Microsoft/Yahoo/Apple needs **one clean port-25 IP with reverse DNS** — not a pool. Aggressive caching keeps that one IP lightly loaded.

**What you give up vs. the enterprise plan:** raw throughput (one IP throttles to thousands/day, fine for CRM lead flow, not for selling bulk verification of millions) and paid spam-trap datasets (replaced over time by your own engagement data). Everything else is achievable.

## 2. Target monthly cost

| Item | Low-cost choice | Typical cost* |
|---|---|---|
| Compute (Go service + workers) | 1 small VPS (1 vCPU / 1–2 GB) | included below |
| PostgreSQL | On the same VPS **or** a free managed tier (Neon/Supabase free) | $0 |
| **SMTP egress** | The **same VPS**, chosen for **outbound port 25 + rDNS/PTR control** | included |
| Observability | Self-hosted Prometheus/Grafana, or just structured logs + a metrics endpoint | $0 |
| TLS / domain | You likely already own a domain; Let's Encrypt is free | ~$0 |
| **Total** | **One correctly-chosen VPS runs everything** | **~$5–15/mo** |

\* Cutoff-era orders of magnitude — validate current pricing. No per-email fees at any volume.

**The one requirement that matters:** pick a VPS provider that **(a) allows outbound TCP port 25** and **(b) lets you set reverse DNS (PTR)** on the IP. Many budget hosts do (some require a support ticket to unblock port 25). Without both, mailbox accuracy is capped — see §7.

## 3. Architecture: one box

```
   Lead Engine CRM  ──HTTPS/API key──►  ┌───────────────────────────────┐
   (Next.js/Prisma)                     │  Single VPS                    │
                                        │  ┌──────────────────────────┐  │
   SES + Smartlead ──bounce webhooks──► │  │ apiserver (Go)           │  │
                                        │  │  real-time + batch + WH  │  │
                                        │  │ verification.Service     │  │
                                        │  │ workers (batch + retry)  │  │
                                        │  │ internal/store (Postgres │  │
                                        │  │   or SQLite) + TTL cache │  │
                                        │  │ internal/feedback        │  │
                                        │  └──────────┬───────────────┘  │
                                        │      port 25 + rDNS/PTR         │
                                        └─────────────┼───────────────────┘
                                                      ▼
                                        Recipient mail servers (SMTP :25)
```

One process group, one database, one egress IP. Scale out later only if volume demands it (§7).

## 4. Build order (low-cost priority)

Do these in order. Each is independently shippable, config-flagged, and leaves `main` green (build, vet, `go test ./...`, Linux CI `-race`).

### Step 1 — Phase 2: Persistence + aggressive caching · Cost: **$0**
- `internal/store` with a `Store` interface; backends: in-memory (default) and **Postgres** (via `pgx`/`database/sql`). For the absolute cheapest single-box start, **SQLite is acceptable** as a third backend — but prefer Postgres if you have the free tier, since Phase 7 and reporting benefit.
- **Result cache with TTL** keyed by normalized email. Long TTL for `valid`/`invalid`, short TTL for `unknown`. This is the #1 cost saver: it slashes repeat verifications and keeps the egress IP idle.
- Idempotency keys for POST.
- **Acceptance:** repeated verification within TTL returns the cached row and does **not** re-hit DNS/SMTP (assert via call counters); runs unchanged with `STORE=memory`; migrations documented.

### Step 2 — Phase 7: Feedback loop · Cost: **~$0**
- `internal/feedback`: ingest **SES bounce/complaint SNS** + **Smartlead webhooks**, normalize, store keyed to `email + reason_code + checked_at`.
- Closed-loop correction: contradictions (a `valid` that hard-bounces, a `catch_all` that engages) update **per-domain reputation priors** used by later steps. Start with transparent rules/priors — **no ML, no paid data**.
- **Acceptance:** signed + idempotent ingestion; a deterministic replay test feeds synthetic bounces and asserts prior updates; re-scoring a labeled sample improves accuracy (report the metric).

### Step 3 — Phase 4 (minimal): One-IP egress · Cost: **the only real spend (small)**
- `internal/egress` connection-source abstraction; default `direct`. Add a **single-IP egress** mode that simply runs on the port-25 VPS, plus optional **SOCKS5** (engine already supports proxy) for flexibility.
- **Per-destination-domain throttling** and backoff so one IP stays clean; **rDNS/PTR + HELO alignment** documented and healthchecked; a lightweight **blocklist self-check** (query a couple of public DNSBLs for your own IP) with an alert/quarantine flag.
- **No IP pool, no paid proxy provider** at this stage.
- **Acceptance:** classification logic unchanged; deterministic tests use the fake SMTP server to prove throttling/backoff/quarantine (no public network); a documented live run from the port-25 box shows previously-`unknown` providers resolving; report the `unknown`-rate drop.

### Step 4 — Phase 3 (lean): Batch + retry · Cost: **$0**
- `internal/queue` with in-memory default and a **Postgres-backed durable queue** (`FOR UPDATE SKIP LOCKED`) — no Redis/broker dependency.
- Bounded **worker pool**; batch endpoints (`POST /v1/batches`, `GET /v1/batches/{id}`, `…/results` as paginated/NDJSON); **retry worker** (exponential backoff, greylisting-aware, max-attempts cap) that recovers `retryable` unknowns — **free accuracy**.
- **HMAC-signed webhooks** back to the CRM on completion / resolution.
- **Acceptance:** a large batch processes with bounded concurrency and correct counts; retry re-queues only `retryable` and stops at the cap (stub engine flips `unknown`→`valid` on attempt N); shutdown drains cleanly; webhook signed + retried.

### Step 5 — Phases 5 & 6 (selective): Provider strategies, catch-all nuance, score · Cost: **$0 (pure code)**
- `ProviderStrategy` interface + registry; implement the **big four** (Gmail, Microsoft/O365, Yahoo, Apple/iCloud) and a generic SMTP fallback. Generalize the existing Yahoo API path into it.
- **Catch-all sub-confidence** using the Phase 7 per-domain priors ("catch-all, likely valid/invalid") instead of a flat risky.
- A blended **`deliverability_score` (0–100)** + basic **domain health** (SPF/DKIM/DMARC record presence, MX health) — all free DNS lookups.
- **Acceptance:** deterministic per-provider tests via the fake SMTP server; new fields additive to GET (legacy) and POST; classifier stays pure (domain-health lookups live in the service, not `internal/classify`).

### Step 6 — Integration Track (parallel, in the CRM repo) · Cost: **$0**
- Prisma `EmailVerification` model joined to `Lead`; `lib/verifier.ts` client; enrichment call during lead intake (symmetric with Twilio Lookup).
- **Outreach gate** before Smartlead/SES: suppress `invalid`, flag `risky`, queue `unknown/retryable`.
- CRM **webhook receiver** for async/retry results, and a **bounce forwarder** into Step 2's feedback loop.

## 5. Defer or skip (until you actually need them)

| Item | Full-plan phase | Why skip on a budget |
|---|---|---|
| Managed proxy / egress providers | 4 | One self-run port-25 IP is enough at CRM volumes. |
| Paid blocklist-monitoring SaaS | 4 | Free DNSBL self-checks cover the basics. |
| Paid spam-trap / threat feeds | 11 | Use your own CRM engagement data instead. |
| Paid verifier fallback (ZeroBounce, etc.) | — | Defeats the purpose; **needs explicit approval** (AGENTS.md). |
| Multi-tenancy, billing, credits | 8 | Not needed for internal CRM use. Add only if you sell it. |
| Heavy observability SaaS (Datadog…) | 9 | Self-host Prometheus/Grafana, or start with logs + a metrics endpoint. |
| ML risk model | 7/11 | Transparent rules/priors first; ML only once you have lots of labels. |

Keep **basic auth (an API key) and `/health`** from Phase 8/9 even in the lean build — they're cheap and needed the moment the CRM calls the service over a network.

## 6. Cost-cutting techniques (bake these in)

- **Cheap checks first, short-circuit early.** Syntax/MX/disposable catch most invalids before any SMTP call (engine already does this) — free, and it spares the egress IP.
- **Cache + TTL everywhere.** Never re-verify a fresh address; re-verify stale ones on read. Biggest single cost lever.
- **One warm IP, throttled** beats many poorly-managed ones — and costs a fraction.
- **Feedback loop instead of bought data** — accuracy that grows for free.
- **Postgres-backed queue, not a broker** — no Redis/RabbitMQ to host.
- **Self-host on one box**; only split services when a real bottleneck appears (§7).

## 7. Honest limits & when to spend more

The lean setup is **excellent for internal CRM enrichment/suppression at thousands of leads/day.** Spend more only when you hit a real trigger:

| Trigger | What to add | Approx. added cost |
|---|---|---|
| Consistent throttling / one IP can't keep up | 2–3 more clean port-25 IPs + rotation (Phase 4 pool) | ~$5–15/mo per IP |
| Selling verification externally | Multi-tenancy, auth, quotas, billing (Phase 8) | dev time + modest infra |
| Accuracy plateau on spam traps | Paid trap/threat feeds (Phase 11) — **approval-gated** | $$ (evaluate ROI) |
| Reliability/SLO demands | Redundancy, managed Postgres, paid observability | moderate |

None of these are needed to be "good" for the CRM. They're growth costs, incurred only when volume or productization justifies them.

## 8. Cross-cutting constraints (unchanged from the main plan)

- Preserve the **MIT licence and AfterShip attribution**.
- **No paid dependency without explicit approval.**
- **Env-var config** (`SYNCORE_VERIFIER_*`), documented in `README.md` + `.env.example`; no secrets in code/logs.
- **Deterministic default tests** (no public network); live tests behind `//go:build live`; Linux CI runs `-race`.
- **Never classify timeouts/temporary failures/blocks as `invalid`** — they are `unknown`.
- **Additive, config-flagged** rollout so the service still runs in its simplest mode.

## 9. Bottom line

- **Infrastructure:** ~$5–15/month (one VPS with port 25 + rDNS; Postgres on it or a free tier). No licensing, no per-email fees, ever.
- **Real cost:** developer time.
- **Result:** free signals + the SES/Smartlead feedback loop + one clean egress IP + retry-the-unknowns gets you **most of the way to ZeroBounce for your CRM's needs.** The expensive gap (large IP pools, bought trap datasets) only matters if you turn this into a high-volume commercial product — and you can add it later, incrementally, when revenue justifies it.
