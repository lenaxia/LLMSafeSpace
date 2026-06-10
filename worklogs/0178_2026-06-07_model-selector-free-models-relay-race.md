# Worklog: Fix Model Selector UX — Free Models Classification and Relay Injector Race

**Date:** 2026-06-07
**Agent:** agent-audit-0606
**Session:** Post-Epic-30 UX validation
**Status:** Complete — PR #53, merged `24e71f59`

---

## Objective

Fix two UX regressions reported after the Epic 30 credential system deployment:
1. Model selector showing all catalog models with wrong availability (all appearing as "available"/"paid")
2. Free models broken — messages using `providerID=opencode-relay` failing with `ProviderModelNotFoundError`

---

## Investigation

### Full end-to-end trace

```
Browser GET /models
  → models.go:ListModels
    → GET http://{podIP}:4096/api/model (opencode catalog)
    → annotateModels(catalog, relayActive)
      → classifyAvailability per model
      → remap opencode→opencode-relay when relayActive + FreeTier
    → filter Unavailable
    → return {models, currentModel}
```

### Root cause 1: `isZeroCostOpencode` missed `opencode-relay`

opencode has two phases:
- **Phase 1** (startup, before relay injection): free models have `providerID="opencode"`, cost.input=0
- **Phase 2** (after relay injection): relay injector writes `disabled_providers:["opencode"]` and registers `opencode-relay` as a custom provider — free models now have `providerID="opencode-relay"`

`isZeroCostOpencode()` only checked `providerID == "opencode"`. In Phase 2, all relay models had `providerID="opencode-relay"` and were classified as `ModelAvailable` (paid), not `ModelFreeTier`. The 20 free models appeared in the selector as paid models requiring an API key.

### Root cause 2: `annotateModels` remap was too broad

The `opencode`→`opencode-relay` remap in `annotateModels` checked `avail == ModelFreeTier` but not `m.ProviderID == "opencode"`. In Phase 2 when models already have `providerID="opencode-relay"`, the remap was redundant. Narrowed to only remap when source is `"opencode"`.

### Root cause 3: Relay injector race with provider catalog initialization (primary)

The relay injector waits for `HealthCheck()` (~5s, opencode HTTP server responding) then immediately queries `/api/model`. But opencode's provider catalog takes ~16s to fully initialize after startup (`providers_connected` gate). In the 11-second window between health check and providers connected:

```
ts=1780801078 "relay injector: no free opencode models found, skipping relay config"
ts=1780801087 "startup gate reached gate=providers_connected elapsed=16.4s"
```

The injector **permanently skips** injection for the pod lifetime. Consequence:
1. `disabled_providers:["opencode"]` never written → opencode stays enabled
2. `opencode-relay` custom provider never registered
3. `annotateModels` still remaps to `opencode-relay` in the API response
4. When user sends message, opencode gets `model: "opencode-relay/qwen3.6-plus-free"` → `ProviderModelNotFoundError`

This explained why the user's messages (providerID=opencode-relay, modelID=qwen3.6-plus-free) were failing silently.

---

## Fixes

### Fix 1: `isZeroCostOpencode` extended to `opencode-relay` (models.go:324)

```go
// Before:
if providerID != "opencode" { return false }

// After:
if providerID != "opencode" && providerID != "opencode-relay" { return false }
```

### Fix 2: `annotateModels` remap narrowed (models.go:286)

```go
// Before:
if relayActive && avail == ModelFreeTier {
// After:
if relayActive && avail == ModelFreeTier && m.ProviderID == "opencode" {
```

### Fix 3: Relay injector retry on 0-model response (relay_injector.go:299-324)

```go
// Before: single call, permanent skip on 0 models
// After: retry for up to 30s (5s intervals)
fetchDeadline := time.Now().Add(30 * time.Second)
for {
    models, err = fetchFreeModels(...)
    if len(models) > 0 { break }
    if time.Now().After(fetchDeadline) { /* permanent skip */ return }
    log.Info("retrying in 5s...")
    time.Sleep(5 * time.Second)
}
```

---

## Tests Added (5)

| Test | What It Proves |
|------|----------------|
| `TestClassifyAvailability_OpencodeRelayZeroCost` | opencode-relay + zero cost = FreeTier |
| `TestClassifyAvailability_OpencodeRelayNoCostEntries` | opencode-relay + no cost = FreeTier |
| `TestClassifyAvailability_OpencodeRelayPaidCost` | opencode-relay + paid = Available (relay can have paid models) |
| `TestAnnotateModels_RelayActive_OnlyRemapsOpencode` | Phase 2 (catalog already has opencode-relay): no double-remap |
| `TestStartRelayInjector_RetriesWhenZeroModels` | First call returns 0 models, second call succeeds → injection completes |

---

## Files Changed

- `api/internal/handlers/models.go` — `isZeroCostOpencode`, `annotateModels` remap guard
- `api/internal/handlers/models_test.go` — 4 classification tests + 1 annotation test
- `cmd/workspace-agentd/relay_injector.go` — retry loop in `startRelayInjector`
- `cmd/workspace-agentd/relay_injector_test.go` — retry regression test

---

## Remaining Issue (not in this PR)

The workspace `5b573d58` (Mike's workspace) still has the relay injector's permanent-skip in effect because it ran before this fix. The relay will not be re-injected until the pod restarts. The injected credential config (`agent-config.json`) has both `openai` and `opencode` providers working correctly — so paid models work. Free models via `opencode-relay` will work after the next pod restart.

**Manual workaround applied:** The DB was updated to bind the OpenAI platform credential to Mike's workspace and the `opencode-free-tier` credential was reverted to `apiKey=public`. After the next pod restart, the relay injector will retry until it finds providers and complete Phase 2 injection.
