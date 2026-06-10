# 0181 — Relay Secret Automation + Model Selector Fix

**Date:** 2026-06-07  
**Session:** Epic 26 relay fix, /provider endpoint migration, relay secret chart automation

---

## Context

Continuing from 0179. System was back online but two issues remained:
1. Model selector showing "⚠ Models" / "could not load models"
2. Relay inference returning `403 Forbidden`

---

## Root Cause Investigation

### /api/model returns [] on all pods

Validated against opencode source (`~/personal/opencode`):
- `/api/model` uses the **v2 Catalog.Service** (`handlers/v2/model.ts`) which blocks on `pluginBoot.wait()`
- `pluginBoot.wait()` resolves but `catalog.model.available()` returns `[]` — the v2 catalog is not populated by any plugin in opencode 1.15.12
- The **v1 `/provider` endpoint** (`handlers/provider.ts`) uses `Provider.Service` (the live v1 catalog) and IS populated
- `GET /provider` returns `{all: [...139 providers...], connected: [...]}` where `connected[]` is the only reliable signal for which providers have live credentials
- Confirmed on live cluster: `/api/model` → `[]` (Content-Length: 2) on ALL pods including 5d-old ones

### /provider response structure
- `all[]` — every provider from models.dev (139 providers, ~5MB total) regardless of auth
- `connected[]` — subset with live credentials (only `["opencode"]` without relay, `["opencode-relay"]` with relay)
- `cost` is an object `{input, output}` — NOT an array
- No `enabled` field on models
- Filter: model is accessible iff its `providerID` is in `connected[]`

### 403 Forbidden on relay
- `INFERENCE_RELAY_BASEURL=https://relay.safespaces.dev` — no secret path segment
- `--inference-relay-secret` was never passed to the controller at deploy time
- CF Worker returns 403 for any request without the secret as first path segment
- Secret was set in CF Worker via `wrangler secret put` but never stored in cluster

### /provider response truncation (model selector broken)
- `ListModels` used `io.LimitReader(resp.Body, 1<<20)` (1MB)
- `/provider` response is ~5MB → JSON truncated → `json.Unmarshal` fails → `annotateModels` errors → frontend gets HTTP 200 with truncated/invalid body → `isError=true` → "⚠ Models"

---

## Fixes Shipped

### 1. Switch /api/model → /provider (PR #55, merged ed955fc4)

**`api/internal/handlers/models.go`:**
- `ListModels`, `fetchCatalog`, `resolveModelIDFromCatalog` — all call `GET /provider` instead of `GET /api/model`
- New types: `providerListResponse`, `providerInfo`, `providerModel`, `providerCost`
- `annotateModels` filters `all[]` to `connected[]` only; `classifyAvailability` takes `providerCost` object
- Cache now stores annotated result (small) instead of raw 5MB provider body
- Read limit raised from 1MB to 32MB

**`cmd/workspace-agentd/relay_injector.go`:**
- `fetchFreeModels` calls `GET /provider` instead of `GET /api/model`
- Filters: `"opencode"` in `connected[]` AND `cost.input == 0`

All tests updated. 38 handler tests + 6 relay injector tests pass.

### 2. Relay secret recovery

Generated new secret: `<redacted>`

- Pushed to CF Worker via CF API (token redacted — stored in cluster secret)
- Added `--inference-relay-secret` to controller via `helm upgrade --set inferenceRelaySecret=<value>`
- `values-cluster.yaml` updated with `inferenceRelayURL` (secret passed via `--set`, not committed)
- Pod `df0a1090` restarted with correct `INFERENCE_RELAY_BASEURL=https://relay.safespaces.dev/<secret>`
- Relay injector fired successfully: `count=5` free models, `relayURL=https://relay.safespaces.dev/03699376...`
- `/provider` on pod: `connected: ["opencode-relay"]` ✓

### 3. Chart relay secret automation (PR #56, open)

**New operator inputs (via `--set` once, never committed):**
- `cloudflare.apiToken` — Workers:Edit scoped token
- `cloudflare.accountId` — CF account ID

**`secret.yaml`** additions:
- `inference-relay-secret`: auto-generated `randAlphaNum 64` on first install; operator > existing > generated; stable across upgrades
- `cloudflare-api-token`: stored for sync Job; preserved across upgrades

**`controller-deployment.yaml`:**
- Relay secret now injected as `$(INFERENCE_RELAY_SECRET)` env from `secretKeyRef` — no longer visible as plaintext in pod args

**`relay-secret-sync-job.yaml`** (new):
- `post-install,post-upgrade` hook Job
- Calls `PUT /accounts/{id}/workers/scripts/{name}/secrets` to push relay secret to CF
- `backoffLimit: 2`, `activeDeadlineSeconds: 120`
- Skipped if `cloudflare.apiToken` or `cloudflare.accountId` empty
- `hook-delete-policy: before-hook-creation,hook-succeeded`
- Rollback-safe: Job runs post-upgrade; if upgrade rolls back, Job never fires

**Rotation procedure:**
```bash
kubectl patch secret llmsafespace-credentials -n default \
  --type=json -p='[{"op":"remove","path":"/data/inference-relay-secret"}]'
helm upgrade llmsafespace . -f values-cluster.yaml \
  --set cloudflare.apiToken=<token> --set cloudflare.accountId=<id>
```

---

## Deployed State

| Component | Version |
|---|---|
| API/Controller/Frontend | `ts-1780821820` |
| Relay secret | `03699376...8878f` |
| CF Worker | `llmsafespace-inference-relay` (RELAY_SECRET updated) |
| Workspace df0a1090 | Active, `opencode-relay` connected, 5 free models |

---

## Open Items

- PR #55 merged ✓
- PR #56 (chart automation) awaiting CI + review
- CI retry triggered for `models.go` 32MB fix (`05c80ae3`) — Docker Hub flake on first run
- Deploy `ts-1780821820` + relay secret to cluster pending CI pass
- models.go 32MB + cache fix not yet deployed (waiting on CI image build)

---

## Key Learnings

- opencode v2 catalog (`/api/model`) is not populated in 1.15.12 — use v1 `/provider`
- `/provider` `all[]` includes ALL models.dev providers regardless of auth; only `connected[]` is authoritative for what the user can actually use
- `cost` in `/provider` is `{input, output}` object, not `[]cost` array
- CF Worker secret cannot be read back via API — must store the value in cluster at time of setting
- The `--inference-relay-secret` arg being plaintext in pod args was a security gap; now fixed via `secretKeyRef`

