# Worklog: Epic 26 — Client-Proxied Inference Implementation

**Date:** 2026-06-04
**Session:** Full implementation of Epic 26 (US-26.1 through US-26.6)
**Status:** Complete

---

## Objective

Implement client-proxied inference for free-tier models. The system allows free-tier LLM requests to be proxied through each user's own client (browser/SDK) rather than originating from platform server IPs, avoiding rate limiting and IP throttling.

---

## Assumptions Stated and Validated

| # | Assumption | Validation |
|---|---|---|
| A1 | opencode is a standalone binary spawned by agentd | `runtimes/base/Dockerfile` downloads binary from GitHub releases |
| A2 | opencode inherits env from agentd via `buildEnvFrom` | `cmd/workspace-agentd/main.go:1058` |
| A3 | agentd has a user-facing HTTP server on port 4097 | `pkg/agentd/types.go:20` |
| A4 | `gorilla/websocket` is available as dependency | `go.mod` contains `v1.5.3` |
| A5 | opencode supports per-provider baseURL override via PUT /auth/:providerID | `pkg/agent/opencode/client.go:152` — `metadata.baseURL` |
| A6 | The free-tier provider ID is `"opencode"` | `api/internal/handlers/models.go:236` |
| A7 | Model tier classification already exists | `models.go:232-249` — `classifyTier()` function |

**Key architectural decision:** Used baseURL redirect instead of HTTPS_PROXY (epic's original recommendation) because:
1. HTTPS_PROXY support in opencode is unvalidated (Open Question #2 in epic design)
2. baseURL override is a proven, tested mechanism in the codebase
3. Simpler: standard HTTP, no TLS interception or CONNECT tunneling needed

---

## Work Completed

### US-26.1: Local relay endpoint in agentd
- `cmd/workspace-agentd/relay_proxy.go` — HTTP handler that receives requests from opencode (via baseURL redirect to `localhost:4097/relay/inference/`), holds the connection, forwards via WebSocket, streams response back
- Multiplexed concurrent requests by unique ID
- Rate limiting (max 60 pending requests)
- 5s timeout for client response start
- Chunk draining on response end to prevent data loss
- Wired into agentd's user mux (gated by `LLMSAFESPACE_RELAY_URL` env var)
- Reconnection loop with exponential backoff

### US-26.2: WebSocket relay endpoint on API server
- `api/internal/handlers/relay_handler.go` — bidirectional WebSocket relay between agentd and client
- Per-workspace rooms with two participants (agentd + client)
- Message routing: agentd→client for requests, client→agentd for responses
- Application-level ping/pong keepalive
- Automatic cleanup of empty rooms on disconnect
- Registered at `GET /api/v1/workspaces/:id/relay` (authenticated)

### US-26.3: Browser relay client
- `frontend/src/hooks/useRelayClient.ts` — React hook that connects to relay WebSocket
- Receives proxy requests, executes via browser `fetch()`, streams responses back
- Handles CORS errors by sending `proxy_error` with descriptive message
- Reconnection with exponential backoff (1s → 30s)
- Status tracking: `disconnected | connecting | connected | error`
- Active request count for UI indicators

### US-26.4: Go SDK relay client
- `pkg/relay/client/client.go` — Go library for programmatic relay clients
- Connects to relay WebSocket with auth headers
- Handles proxy requests with Go's native HTTP client (no CORS issues)
- Streams responses in 32KB chunks
- Application-level ping/pong keepalive (30s interval)
- Clean shutdown via `Close()` with done channel

### US-26.5: Model tier annotation
- Added `ProxyRequired bool` field to `annotatedModel` in `models.go`
- Set to `true` for all free-tier models (`tier == "free"`)
- Frontend can use this to gate relay WebSocket connection before model selection

### US-26.6: CORS fallback
- `api/internal/handlers/relay_fallback.go` — server-side proxy for when browser can't CORS
- Rate limited: 10 requests/minute/user (per `relay.FallbackRateLimitPerMinute`)
- Host validation: only allowed proxy hosts (opencode.ai, api.opencode.ai)
- Registered at `POST /api/v1/workspaces/:id/relay/fallback` (authenticated)

### Shared protocol types
- `pkg/relay/protocol.go` — shared message types, constants, host validation

---

## Key Decisions

1. **BaseURL redirect over HTTPS_PROXY** — Avoids unvalidated dependency on opencode's proxy support. Uses proven mechanism (`PUT /auth/opencode` with `metadata.baseURL`).

2. **WebSocket relay over HTTP long-poll** — WebSocket provides true bidirectional streaming with low overhead. Each workspace gets one relay room with two participants.

3. **Rate limiting at both layers** — agentd limits pending requests (60 max), fallback handler limits per-user (10/min). Prevents abuse from both runaway agents and malicious clients.

4. **Host allowlist for security** — Only `opencode.ai` and `api.opencode.ai` are permitted targets. Prevents the client from being used as an open proxy.

---

## Blockers

None.

---

## Tests Run

```
pkg/relay/          — 13 tests PASS (protocol types, host validation)
pkg/relay/client/   — 4 tests PASS (SDK client: connect, error, ping, close)
cmd/workspace-agentd/ — 9 relay tests PASS (handler, streaming, timeout, concurrent, rate limit, body forwarding, WebSocket)
api/internal/handlers/ — 12 relay tests PASS (handler: upgrade, two-participants, streaming, disconnect, ping, multi-workspace, concurrent; fallback: auth, host validation, happy path, rate limit, invalid body)
api/internal/server/   — OpenAPI contract test PASS
Full build: go build ./... — PASS (all packages compile)
```

Total: 38 new tests, all passing with -race.

---

## Next Steps

1. **Integration test**: Deploy to a cluster and verify end-to-end: configure opencode's `opencode` provider with `baseURL: "http://localhost:4097/relay/inference"`, connect a browser relay client, and send a prompt using a free model.

2. **Controller wiring**: Add logic to the workspace controller that sets `LLMSAFESPACE_RELAY_URL` env var on the agentd container when the workspace is in free-tier mode (no paid API keys).

3. **Frontend integration**: Use `useRelayClient` hook in the ChatPage, gated by `proxyRequired` from the model list response. Show UI indicator when relay is active.

4. **Provider baseURL injection**: On workspace boot (or model selection), if the selected model has `proxyRequired: true`, push `PUT /auth/opencode` with `baseURL: "http://localhost:4097/relay/inference"` via the agentd opencode client.

---

## Files Modified

### New files
- `pkg/relay/protocol.go` — Shared relay protocol types
- `pkg/relay/protocol_test.go` — Protocol type tests
- `pkg/relay/client/client.go` — Go SDK relay client
- `pkg/relay/client/client_test.go` — SDK client tests
- `cmd/workspace-agentd/relay_proxy.go` — In-pod relay proxy
- `cmd/workspace-agentd/relay_proxy_test.go` — Relay proxy tests
- `api/internal/handlers/relay_handler.go` — WebSocket relay endpoint
- `api/internal/handlers/relay_handler_test.go` — Relay handler tests
- `api/internal/handlers/relay_fallback.go` — CORS fallback handler
- `api/internal/handlers/relay_fallback_test.go` — Fallback tests
- `frontend/src/hooks/useRelayClient.ts` — Browser relay client hook
- `frontend/src/hooks/useRelayClient.test.ts` — Browser relay tests
- `worklogs/0140_2026-06-04_epic-26-client-proxied-inference.md` — This worklog

### Modified files
- `cmd/workspace-agentd/main.go` — Wired relay proxy into user mux
- `api/internal/server/router.go` — Added RelayHandler + RelayFallbackHandler to config and routes
- `api/internal/handlers/models.go` — Added `ProxyRequired` field to annotatedModel
