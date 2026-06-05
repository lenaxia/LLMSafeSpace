# Worklog: Epic 26 Architecture Pivot — WebSocket Relay → Cloudflare Worker

**Date:** 2026-06-05
**Session:** Remove WebSocket relay, replace with CF Worker proxy. Final simplification.
**Status:** Complete (PR #35 merged as `65d26b5`)

---

## Objective

Replace the Epic 26 WebSocket relay architecture with a Cloudflare Worker after discovering the browser relay path is fundamentally broken (CORS), and simplify to the minimum viable implementation.

---

## Key Discoveries (validated this session)

| # | Finding | Evidence |
|---|---------|----------|
| 1 | `opencode.ai/zen/v1` has NO CORS headers | `curl -sI` — no `Access-Control-Allow-Origin` in response |
| 2 | Browser relay path can never work for opencode.ai | Same-origin policy blocks response reading without CORS |
| 3 | Real provider URL is `opencode.ai/zen/v1` (not `opencode.ai`) | `models.dev/api.json` → `opencode.api = "https://opencode.ai/zen/v1"` |
| 4 | opencode uses `/responses` path (OpenAI Responses API) | `provider.ts:29` defines `"openai/responses"` endpoint type |
| 5 | `OPENCODE_AUTH_CONTENT` with `metadata.baseURL` DOES work | `account.ts:36`: `Object.assign(provider.options.aisdk.provider, metadata)` → `catalog.ts:138-139` moves to endpoint.url |
| 6 | `Authorization: Bearer public` sent regardless of baseURL | `auth-options.ts:52`: reads `apiKey` independently from baseURL |
| 7 | Agentd middleman adds resource contention for zero security benefit | Worker URL is the "secret"; anyone who finds it just gets free-tier access |
| 8 | One CF Worker = 300+ global IPs (all edge POPs) | Cloudflare's `smart` placement runs from nearest POP |

---

## Final Architecture

```
opencode (in pod) → https://relay.safespaces.dev → CF Worker → opencode.ai/zen/v1
```

- `OPENCODE_AUTH_CONTENT` = `{"opencode":{"type":"api","key":"public","metadata":{"baseURL":"https://relay.safespaces.dev"}}}`
- Controller injects via `--inference-relay-url` flag (Helm value)
- Worker is 37 lines — transparent proxy, no auth
- URL rotation via DNS CNAME update (zero pod disruption)

---

## What Was Removed (~4000 lines)

- `pkg/relay/` — protocol types, Go SDK client
- `cmd/workspace-agentd/relay_proxy.go`, `relay_config.go`
- `api/internal/handlers/relay_handler.go`, `relay_fallback.go`, integration tests
- `frontend/src/hooks/useRelayClient.ts`
- Controller relay env vars (`LLMSAFESPACE_RELAY_URL`, `LLMSAFESPACE_RELAY_TOKEN`)
- `OptionalAuthMiddleware` relay routes, SSLRedirect relay skip
- All relay-specific tests

## What Was Added (~100 lines)

- `workers/inference-relay/` — 37-line CF Worker + wrangler.toml + README
- `--inference-relay-url` controller flag
- `buildOpenCodeAuthContent()` in pod_builder.go
- `inferenceRelayURL: "https://relay.safespaces.dev"` Helm value
- 2 unit tests for `buildOpenCodeAuthContent`

---

## Activation Steps

1. `cd workers/inference-relay && npm i && npx wrangler deploy`
2. Add custom domain route `relay.safespaces.dev/*` → Worker in CF dashboard
3. Deploy chart (controller injects baseURL into new workspace pods)
4. Existing pods: suspend + resume to pick up new env var

---

## Filed

- Issue #36: `TestE2E_RealAuth_ChangePassword` hangs (pre-existing, unrelated)
- PR #34: Closed (superseded — fixed relay that we then removed)
