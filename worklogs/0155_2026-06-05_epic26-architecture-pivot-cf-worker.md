# Worklog: Epic 26 Architecture Pivot — WebSocket Relay → Cloudflare Worker

**Date:** 2026-06-05
**Session:** Remove WebSocket relay system, replace with CF Worker CORS proxy
**Status:** Complete (PR #35)

---

## Objective

Replace the Epic 26 WebSocket relay architecture with a Cloudflare Worker after discovering that the browser relay path is fundamentally broken (CORS).

---

## Discovery

### Validated findings:
1. `opencode.ai/zen/v1` (the real inference endpoint, from `models.dev/api.json`) returns NO CORS headers
2. Browser `fetch()` to a cross-origin endpoint without CORS headers will always fail (same-origin policy blocks response reading)
3. The relay's browser execution path can NEVER work for the opencode.ai provider
4. PR #34's live test captured `url=https://opencode.ai/responses` — which 404s (correct URL is `opencode.ai/zen/v1/responses`)
5. A Cloudflare Worker adding CORS headers provides IP distribution via 300+ edge POPs at zero/minimal cost

### Architecture decision:
Replace the entire relay system (agentd ↔ API WebSocket ↔ browser ↔ provider) with a 20-line CF Worker (browser → Worker → provider). The Worker adds CORS headers so browsers can call it. IP distribution is achieved naturally via Cloudflare's global edge network.

---

## Work Completed

- Deleted 3972 lines of relay code (15 files removed, 9 edited)
- Added CF Worker (`workers/inference-relay/`) — CORS proxy to opencode.ai/zen/v1
- Added `--inference-relay-url` controller flag + `buildOpenCodeAuthContent()` helper
- Added `inferenceRelayURL` Helm value
- Fixed wiring bug (inferenceRelayURL not passed to reconciler)
- Removed all stale relay references (SSLRedirect skip, comments)
- Closed PR #34 (relay fixes superseded)

---

## Files Modified

### Deleted
- `pkg/relay/` (protocol + SDK client)
- `cmd/workspace-agentd/relay_proxy.go`, `relay_config.go`
- `api/internal/handlers/relay_handler.go`, `relay_fallback.go`, `relay_integration_test.go`
- `frontend/src/hooks/useRelayClient.ts` + test
- `controller/internal/workspace/relay_test.go`

### Added
- `workers/inference-relay/src/index.ts` — CF Worker
- `workers/inference-relay/wrangler.toml` + `package.json`
- `controller/internal/workspace/inference_relay_test.go`

### Modified
- `controller/` — flag, reconciler field, `buildOpenCodeAuthContent()`
- `api/internal/` — removed relay handlers, routes, stale comments
- `charts/` — removed relay flag, added inferenceRelayURL value
