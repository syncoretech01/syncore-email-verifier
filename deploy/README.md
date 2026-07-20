# Deploying the Syncore Email Verifier

This service is **Layer 1** of the Growth OS verification waterfall: the free,
local first pass the CRM calls before paying for Hunter. It is a stateless
verification function — no database, no queue. Run one process; scale out only if
volume demands it.

Files here:

| File | Purpose |
|---|---|
| `syncore-email-verifier.service` | systemd unit (Linux hosts) |
| `ecosystem.config.js` | PM2 process definition |
| `env.example` | vault-populated env-file template |

## Build

```bash
CGO_ENABLED=0 go build -o apiserver ./cmd/apiserver
# place the binary at /opt/syncore/email-verifier/apiserver
```

## Configure

Copy `env.example` to `/etc/syncore/email-verifier.env`, populate secrets from
your vault, then `chown syncore:syncore` and `chmod 600` it. Configuration is
read from the **process environment only** (no dotenv). See the root `README.md`
for the full variable reference.

## Install (systemd)

```bash
sudo cp syncore-email-verifier.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now syncore-email-verifier
sudo systemctl status syncore-email-verifier
curl -s http://127.0.0.1:8080/health   # -> {"status":"ok"}
```

## Install (PM2)

```bash
set -a && . /etc/syncore/email-verifier.env && set +a
pm2 start ecosystem.config.js && pm2 save
```

## Deployment targets — pick one

The two Growth OS plans point in different directions here; the tradeoff is real,
so decide deliberately.

### A. Co-located on the CRM's EC2 host (loopback) — pilot default

Run this service on the same EC2 instance as the CRM worker, bound to
`127.0.0.1`. The CRM calls `http://127.0.0.1:8080`. No token is strictly required
on a pure loopback bind (nothing off-box can reach it), though you may still set
one.

- **Simplest, most secure surface** (never leaves the box).
- **Caveat:** AWS EC2 blocks outbound TCP port 25 by default, so mailbox probes
  for many providers return `unknown` (see below). Correct and safe — the CRM
  escalates `unknown` to Hunter.

### B. Dedicated port-25 VPS (private subnet)

Run this service on a VPS chosen for **outbound port 25 + reverse-DNS (PTR)
control**, on a private address the CRM can reach. This recovers more local
`valid` results (the low-cost roadmap's egress thesis).

- **Requires:** `SYNCORE_VERIFIER_BIND_ADDR` set to the private address **and**
  `SYNCORE_VERIFIER_AUTH_TOKEN` set. **Startup fails fast** on a non-loopback
  bind with no token.
- Put it on a private subnet / security group reachable only by the CRM. **Never
  bind a public address.**

> These two are mutually exclusive for a single instance: co-locating on the
> port-25-blocked EC2 host (A) cannot also be the clean-port-25 egress box (B).
> Start with A for the pilot; move mailbox probing to B when reducing Hunter
> spend is worth the extra box.

## The port-25 reality

From a host without outbound port 25 (default AWS EC2), SMTP mailbox probes for
Microsoft/Yahoo/Apple and others will **time out → `unknown` (retryable)**. This
is **correct and safe**: the service still performs the free
syntax/MX/null-MX/disposable/role/free/catch-all classification, resolves
Gmail/Yahoo via the `api` path, and returns `unknown` rather than a false
`invalid`. The CRM escalates `unknown` to Hunter. Never "fix" this by treating
timeouts as `invalid`.

## Batch timing bound (the CRM must chunk to this)

`POST /v1/verifications:batch` runs items through a bounded worker pool. The
worst-case wall-clock for a full batch is:

```
ceil(BATCH_MAX_ITEMS / BATCH_CONCURRENCY) x (CONNECT_TIMEOUT + OPERATION_TIMEOUT)
```

At the defaults (100 items, concurrency 10, 10s + 10s) that is **10 rounds ×
20s = 200s**. The server's `WriteTimeout` is derived to cover this
(**≈ 215s** = 200s + 15s headroom), so legitimate slow batches are not truncated.

**Contract for the CRM:**

- **Chunk batch requests to `BATCH_MAX_ITEMS`** (default 100) — the documented cap.
- Use a client/proxy read timeout **≥ the server `WriteTimeout`** (≈ 215s at
  defaults). If you front the service with a reverse proxy, raise its timeouts to
  match.
- To shrink the worst case, lower `BATCH_MAX_ITEMS`, raise `BATCH_CONCURRENCY`
  (mind provider politeness), or lower the SMTP timeouts — then re-document the
  bound here.

## Verify the deployment

```bash
# health (open, no auth)
curl -s http://<bind>/health

# single (bearer only if a token is configured)
curl -s -X POST -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"email":"person@example.com"}' http://<bind>/v1/verifications

# batch
curl -s -X POST -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"emails":["a@example.com","b@example.com"]}' http://<bind>/v1/verifications:batch
```

A known-good and a known-bad address should classify correctly; anything blocked
by port 25 should come back `unknown` (retryable), never `invalid`.
