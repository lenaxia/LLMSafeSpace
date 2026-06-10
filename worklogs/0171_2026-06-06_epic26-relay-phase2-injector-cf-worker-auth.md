# Worklog: Epic 26 Relay — Phase-2 Injector + CF Worker Auth + Model Surfacing

**Date:** 2026-06-06
**Session:** Implemented the correct end-to-end relay mechanism for Epic 26
after extensive empirical investigation of opencode's internals.
**Status:** Complete

---

## Objective

Get free-tier opencode model inference to route through `relay.safespaces.dev`
(CF Worker) with auth, and surface relay-enabled models correctly to the
LLMSafeSpace API layer.

---

## Key Findings (Validated, Not Assumed)

| Claim | Validated How | Result |
|-------|--------------|--------|
| `options.baseURL` in config file changes actual routing | Empirical: wrote CANARY, restarted, measured CF hits | ❌ Does NOT work |
| `metadata.baseURL` in auth.json changes routing | Empirical: PUT /auth/opencode + dispose, measured CF hits | ❌ Does NOT work |
| `disabled_providers: ["opencode"]` disables built-in provider in model display | Empirical | ❌ Account service re-enables it |
| `disabled_providers: ["opencode"]` blocks inference routing | Observed `ProviderModelNotFoundError` | ✅ Does work at routing level |
| `opencode-relay` custom provider is loaded | Log: `providerID=opencode-relay found` | ✅ Loaded |
| `opencode-relay` models appear in `/api/model` | Empirical query | ❌ 0 models returned (opencode InstanceState scoping) |
| `opencode-relay` provider routes inference correctly | Sent prompt with `providerID: "opencode-relay"`, got response | ✅ Works |
| CF Worker receives traffic from `opencode-relay` inference | CF analytics: 1 hit at 03:48:43Z matching inference time | ✅ Confirmed |
| Phase-2 injector fires after opencode healthy | Agentd logs: `relay injector: fetched free models count=20` | ✅ Confirmed |
| `GET /models` returns `opencode-relay` models with `proxyRequired=true` | Live cluster test | ✅ 20 relay models |

---

## What Was Built

### 1. CF Worker Secret Auth (`workers/inference-relay/src/index.ts`)
Secret-in-path approach: the relay secret is embedded as the first path
segment of `inferenceRelayURL`. Worker validates and strips it before
forwarding. Without correct secret → 403. 9 TDD tests.

### 2. Controller: `INFERENCE_RELAY_BASEURL` Pod Env Injection
`controller/internal/workspace/pod_builder.go`: injects
`INFERENCE_RELAY_BASEURL=<relay URL>/<secret>` as env var on workspace
pod main container. Controller flag `--inference-relay-secret`.

### 3. Agentd Phase-2 Relay Injector (`cmd/workspace-agentd/relay_injector.go`)
After opencode is healthy, the injector goroutine:
1. Checks `auth.json` — if personal opencode API key exists (not "public"),
   bypass relay entirely (paying Zen subscriber)
2. Calls `GET /api/model` to get live free model list
3. Writes `agent-config.json` with `disabled_providers: ["opencode"]` + 
   `provider.opencode-relay` using `@ai-sdk/openai-compatible` + relay URL
4. Writes `opencode-relay` auth entry (preserves paid provider entries)
5. Triggers `proc.restart()` — opencode rereads config with relay active
13 TDD tests.

### 4. LLMSafeSpace API Model Surfacing (`api/internal/handlers/models.go`)
`annotateModels(raw, relayActive bool)`: when `relayActive=true`, remaps
free-tier opencode model `providerID` from `"opencode"` to `"opencode-relay"`.
Clients see `providerID=opencode-relay, proxyRequired=true` and route
inference through the relay.

`Config.Server.InferenceRelayURL` + `LLMSAFESPACE_SERVER_INFERENCERELAYURL`
env var on API deployment. When set, `relayActive=true`. 2 new TDD tests.

---

## Validation Results

```
GET /api/v1/workspaces/:id/models:
  Total: 67 models
  opencode-relay: 20 models  ← free tier, routes through CF Worker
  opencode (direct): 47 models  ← paid tier, routes direct
  proxyRequired=true: 20 models

CF Worker analytics:
  03:48:43Z — 1 request (title generation during inference)
  
Phase-2 injector log:
  relay injector: fetched free models count=20
  relay injector: wrote relay config path=/tmp/agent-config.json models=20
  relay injector: updated auth.json with opencode-relay entry
  relay injector: triggered opencode restart to apply relay config
```

---

## Architecture (Final)

```
User selects free model (providerID=opencode-relay)
  ↓  PUT /api/v1/workspaces/:id/model
  ↓  SetModel stores providerID=opencode-relay
opencode inference call
  ↓  opencode-relay custom provider (options.baseURL=relay.safespaces.dev/<secret>)
  ↓  CF Worker validates secret, strips it
  ↓  Proxies to opencode.ai/zen/v1
  ↓  LLM response back

Personal Zen API key:  phase-2 injector detects, skips relay entirely
Paid providers:        unaffected, route directly
```

---

## Decisions

1. **Phase-2 startup injection** vs config-at-boot: opencode's `disabled_providers`
   requires knowing the model list which is only available after opencode runs.
   Two-phase approach (boot clean, inject after healthy, restart) is the only
   reliable mechanism.

2. **opencode-relay providerID in API response**: since `opencode-relay` models
   don't surface in `/api/model` (opencode InstanceState scoping bug), the
   LLMSafeSpace API remaps free opencode models to `opencode-relay` providerID
   based on the same cost-zero classification. This is correct: the relay
   injector ensures those model IDs exist under `opencode-relay` in the pod.

3. **Personal key bypass**: if `auth.json["opencode"]["key"] != "public"`,
   the relay is skipped entirely. Paying Zen subscribers get direct access.

---

## Files Modified

- `cmd/workspace-agentd/relay_injector.go` — new: phase-2 injector
- `cmd/workspace-agentd/relay_injector_test.go` — 13 TDD tests
- `cmd/workspace-agentd/main.go` — wire injector, remove old phase-1 code
- `cmd/workspace-agentd/secrets.go` — simplify applyWorkspaceConfig
- `cmd/workspace-agentd/secrets_test.go` — remove obsolete relay tests
- `api/internal/handlers/models.go` — relayActive flag in annotateModels
- `api/internal/handlers/models_test.go` — 2 new annotation tests
- `api/internal/handlers/secrets.go` — SecretsHandler.relayActive field
- `api/internal/config/config.go` — Server.InferenceRelayURL field
- `api/internal/app/app.go` — wire relayActive from config
- `charts/llmsafespace/templates/api-deployment.yaml` — relay URL env var
- `workers/inference-relay/src/index.ts` — secret-path auth (9 TDD tests)
- Various: controller pod_builder, reconciler, chart values, Helm flags

---

## Next Steps

1. **SetModel handler**: when model has `providerID=opencode-relay`, ensure
   `patchAgentModel` uses `opencode-relay` not `opencode` in the `/global/config`
   patch — otherwise the model set may not persist correctly across restarts.
2. **Workspace suspension**: phase-2 only runs once per pod lifetime. Suspended
   workspaces resume with the config already in place (PVC persistent).
3. **CF Worker analytics lag**: ~30-60s lag; confirmed 1 hit at 03:48:43Z.
