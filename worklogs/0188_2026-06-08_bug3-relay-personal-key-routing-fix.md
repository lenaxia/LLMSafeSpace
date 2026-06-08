# Worklog 0188 — Bug 3: relay model routing for personal opencode key users

**Date:** 2026-06-08
**PR:** #67 (fix/glm-model-routing-and-error-display)

## Problem

`SecretsHandler.relayActive` is a static boolean set once at API startup from
`LLMSAFESPACE_INFERENCE_RELAY_URL`. Applied identically to all workspaces. A
workspace where the relay injector was skipped (user has a personal opencode Zen
API key) has no `opencode-relay` provider, but `annotateModels(relayActive=true)`
was still remapping all zero-cost opencode models to `providerID="opencode-relay"`.
The frontend showed these models as selectable. Inference failed with
`"opencode-relay not found"`.

No users affected at time of fix — no personal opencode keys in use. Architecturally
broken for when they are.

## Fix

### Signal design

The discriminating signal — whether the relay injector *actually ran* for a specific
pod — is exposed as `RelayInjected bool` in `agentd.ReadyzResponse`
(`pkg/agentd/types.go`), populated from `getActiveRelayModels() != nil` in the
readyz handler (`cmd/workspace-agentd/main.go:655`).

`getActiveRelayModels()` reads an `atomic.Pointer[[]relayModel]` set by
`setActiveRelayModels()` in the relay injector goroutine on success. It is nil when:
- Injector has not yet run (Phase 1, ~T+0 to T+7s)
- Injector was skipped (personal key — `shouldSkipRelay` returned true)
- Injector failed (no free models after 30s)

### Why readyz, not statusz

`/v1/readyz` uses `healthCache.Snapshot()` (atomic) and `providerCache` (15s TTL).
On cache hit: zero synchronous opencode calls. On miss: up to 3 live opencode calls,
but the 5s `modelHTTPClient` timeout hard-caps the total. `/v1/statusz` has no
latency upper bound (synchronous calls under a mutex, including disk/CPU reads) and
must never be called on hot paths.

### Auth

The agentd admin port (4098) is protected by `requireBearerToken(adminToken, ...)`.
`AGENTD_ADMIN_TOKEN` is set from the same password Secret as `passwordGetter`.
`fetchRelayInjected` sends `Authorization: Bearer <password>` — not Basic auth.

### Caching

`relayInjected` is cached in `modelCachePayload` alongside the model list with the
same 5s TTL. On cache hit: no extra round-trip. On cache miss: one sequential GET
to `/v1/readyz` after the GET to `/provider`.

### Remap guard

`annotateModels(raw, relayGloballyEnabled, relayInjected)`: remap fires only when
both flags are true. In practice the guard is unreachable in Phase 2 because
`disabled_providers:["opencode"]` removes `opencode` from `/provider`'s `connected[]`
before the guard evaluates. Kept as defense-in-depth. Validated on live cluster:
`connected=["opencode-relay"]` on Phase 2 pod.

### SetModel path

`resolveModelIDFromCatalog` (used by `patchAgentModel`) uses the same
`fetchRelayInjected` guard. `patchAgentModel` now returns `(resolved string, error)`
so `SetModel` reuses the resolved `providerID/modelID` for `metricsRecorder` without
a second catalog fetch.

## Implementation bugs found and fixed during review (3 passes)

| Bug | Description | Fix |
|---|---|---|
| BugA | `fetchRelayInjected` sent Basic auth to a Bearer-protected endpoint → always 401, fix was dead code | `req.Header.Set("Authorization", "Bearer "+password)` |
| BugB | Called `/v1/statusz` (unbounded latency) instead of `/v1/readyz` | Switch to readyz; move `RelayInjected` from `StatuszResponse` to `ReadyzResponse` |
| BugC | `SetModel` made 3× GET /provider + 2× GET /v1/statusz per request | `patchAgentModel` returns resolved string; metricsRecorder reuses it |
| BugD | `annotateModels` comment described old behavior (Phase 1 remap) — contradicted the code | Rewritten with accurate Phase 1/Phase 2/personal-key explanation |

## Weak points validated

**In-memory cache is per-API-replica (2 replicas, no session affinity).**

Concrete failure modes, all validated against live cluster behavior:

1. **Stale model picker after credential bind (≤5s):** Replica A evicts cache on
   credential bind; Replica B serves stale list (new provider absent) for ≤5s.
   Cosmetic only — no inference failure.

2. **Stale `relayInjected` during Phase 1→Phase 2 (≤20s):** 5s model cache TTL +
   15s providerCache TTL. Frontend may read stale `currentModelProviderID="opencode"`
   and send a wrong per-request override. **Validated live on cluster:** opencode
   1.15.12 ignores overrides to `disabled_providers` entries, falls back to the
   session model (`opencode-relay/big-pickle`). Inference routes correctly despite
   the stale override.

3. **Stale `Selected` indicator:** `annotatedModel.Selected` is derived from DB
   (`GetDefaultModel`) after cache lookup — never stored in the cache. Always fresh.

**Net impact: cosmetic only. No inference failures, no data loss.**

Redis-backed cache (US-30.11) would eliminate the cross-replica window.

## Backwards compatibility

Old pods (pre-`RelayInjected` field) return readyz without `relay_injected`.
Go's `json.Decode` zero-values it to `false`. Safe: old Phase 2 pods have
`opencode-relay` (not `opencode`) in `connected[]`, so the remap guard
`p.ID=="opencode"` never fires. Validated on live cluster (image `ts-1780939444`).

## Files changed

- `pkg/agentd/types.go` — `RelayInjected bool` in `ReadyzResponse`
- `cmd/workspace-agentd/main.go` — populate `RelayInjected` in readyz handler
- `api/internal/handlers/models.go` — `fetchRelayInjected`, updated `annotateModels`,
  `patchAgentModel` returns resolved string, updated `SetModel` metricsRecorder path
- `api/internal/handlers/models_test.go` — `TestListModels_RelayActive_PersonalKey_NoRemap`,
  `TestListModels_CurrentModelProviderID_RelayActive` (with port-4098 readyz mock),
  `TestAnnotateModels_PersonalKey_NoRemap`, `TestAnnotateModels_Phase1_NotRemapped`
