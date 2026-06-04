# Worklog: Epic 26 — Client-Proxied Inference Implementation

**Date:** 2026-06-04
**Session:** Full implementation of Epic 26 (US-26.1 through US-26.6) + 3 review/fix cycles
**Status:** Complete (merged as PR #21, squash commit 98d157b)

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
| A8 | API server communicates with opencode on pod IP without basic auth | Pattern in existing `ListModels`, `patchAgentModel` — no `SetBasicAuth` calls |
| A9 | Controller disables EnableServiceLinks — agentd cannot discover API server via K8s env | `controller/internal/workspace/pod_builder.go:192-196` |

**Key architectural decision:** Used baseURL redirect instead of HTTPS_PROXY (epic's original recommendation) because:
1. HTTPS_PROXY support in opencode is unvalidated (Open Question #2 in epic design)
2. baseURL override is a proven, tested mechanism in the codebase
3. Simpler: standard HTTP, no TLS interception or CONNECT tunneling needed

---

## Work Completed

### US-26.1: Local relay endpoint in agentd
- `cmd/workspace-agentd/relay_proxy.go` — HTTP handler receives requests from opencode (via baseURL redirect to `localhost:4097/relay/inference/`), holds the connection, forwards via WebSocket, streams response back
- Multiplexed concurrent requests by atomic counter ID
- Rate limiting (max 60 pending requests)
- 5s timeout for client response start
- Blocking chunk delivery with backpressure (not silent drop)
- `failAllPending()` on WebSocket disconnect — immediate error to all in-flight requests
- Write mutex on WebSocket connection (prevents gorilla concurrent write panic)
- Hop-by-hop header stripping (Connection, Transfer-Encoding, Host, etc.)
- Configurable `targetBaseURL` (default: opencode.ai)
- Reconnection loop with exponential backoff in main.go
- Gated by `LLMSAFESPACE_RELAY_URL` + `LLMSAFESPACE_RELAY_TOKEN` env vars

### US-26.2: WebSocket relay endpoint on API server
- `api/internal/handlers/relay_handler.go` — bidirectional WebSocket relay between agentd and client
- Per-workspace rooms with two participants (agentd + client)
- `relayConn` wrapper with write mutex (prevents concurrent write data race)
- Workspace ownership enforcement via `WorkspaceGetter.GetWorkspace` + `Labels["user-id"]` check — applies to BOTH roles
- Application-level ping/pong keepalive
- Automatic cleanup of empty rooms on disconnect
- Wired into `app.go` via `NewRelayHandler(k8sWorkspaceGetterAdapter)`
- Registered at `GET /api/v1/workspaces/:id/relay` (authenticated)

### US-26.3: Browser relay client
- `frontend/src/hooks/useRelayClient.ts` — React hook
- Receives proxy requests, executes via browser `fetch()`, streams responses back via ReadableStream
- CORS error detection → sends `proxy_error` with descriptive message
- Reconnection with exponential backoff (1s → 30s)
- Status tracking: `disconnected | connecting | connected | error`
- Active request count for UI indicators

### US-26.4: Go SDK relay client
- `pkg/relay/client/client.go` — Go library
- Handles proxy requests with Go's native HTTP client (no CORS issues)
- Streams responses in 32KB chunks
- Application-level ping/pong keepalive (30s interval)
- Auth header on WebSocket dial
- Clean shutdown via `Close()` with done channel

### US-26.5: Model tier annotation
- Added `ProxyRequired bool` field to `annotatedModel` in `models.go`
- Set to `true` for all free-tier models (`tier == "free"`)

### US-26.6: CORS fallback
- `api/internal/handlers/relay_fallback.go` — server-side proxy for CORS failures
- Rate limited: 10 requests/minute/user with periodic map cleanup (bounded memory)
- Host validation via `net/url.Parse` + allowlist
- Registered at `POST /api/v1/workspaces/:id/relay/fallback`

### End-to-end wiring
- `api/internal/app/app.go` — instantiates and wires `RelayHandler` + `RelayFallbackHandler`
- `api/internal/handlers/models.go` — `SetModel` pushes relay baseURL to opencode when free-tier model selected; clears it when switching to paid model
- `isFreeTierModel()` checks live catalog
- `pushRelayBaseURL()` / `clearRelayBaseURL()` — typed structs, PUT /auth/opencode

### Shared protocol
- `pkg/relay/protocol.go` — message types, constants, host validation

---

## Review Cycles

### Review 1 (automated, commit e741c19)
**Verdict: REQUEST CHANGES** — 4 blocking + 4 non-blocking findings:
1. CRITICAL: `buildTargetURL` doesn't strip `/relay/inference` prefix → all requests 404
2. CRITICAL: agentd relay sends no auth headers → WebSocket 401
3. HIGH: JSON injection in error response (fmt.Sprintf into JSON)
4. HIGH: No workspace ownership check on relay connections
5. MEDIUM: Spurious `proxy_response_end` in browser ping handler
6. MEDIUM: Unbounded `userCounts` map in fallback
7. LOW: Manual URL parsing instead of `net/url`
8. LOW: `relayUpgrader.CheckOrigin` returns true (acceptable per terminal.go pattern)

### Review 2 (triggered via /ai, commit eaadbc0)
**Verdict: COMMENTED** — All 4 blocking fixed, 1 new blocking found:
- NEW HIGH: `app.go` doesn't wire handlers → relay endpoint doesn't exist in production
- LOW: `map[string]interface{}` in pushRelayBaseURL
- LOW: No baseURL reset when switching free→paid model

### Review 3 (triggered via /ai, commit 7fb5d7e)
**Verdict: APPROVE** — All findings resolved:
- app.go wiring added
- Typed struct for relay payload
- `clearRelayBaseURL` for paid model switch
- 2 non-blocking gaps noted (auth header propagation test, pushRelayBaseURL unit test)

---

## Key Decisions

1. **BaseURL redirect over HTTPS_PROXY** — Avoids unvalidated dependency on opencode's proxy env var support.
2. **WebSocket relay** — True bidirectional streaming with low overhead. One room per workspace.
3. **Ownership check on both roles** — Prevents `?role=agentd` bypass. Both roles resolve to the same userID via the workspace owner's API key.
4. **Blocking chunk delivery** — Backpressure propagates correctly; no silent data loss.
5. **Write mutex per connection** — Standard gorilla pattern, prevents concurrent write panic.
6. **failAllPending on disconnect** — Immediate 502 instead of hanging until timeout.

---

## Blockers

None. Merged successfully.

---

## Known Limitations (documented, not blocking)

1. **Controller must inject `LLMSAFESPACE_RELAY_URL` env var** — Without it, the agentd relay proxy doesn't start. Requires a controller/chart change (out of scope for this epic).
2. **No unit test for `pushRelayBaseURL`/`clearRelayBaseURL`** — These make HTTP calls to the pod; testing would require httptest.Server mock of opencode's `/auth/opencode` endpoint.
3. **No test for auth header propagation in agentd WebSocket dial** — The integration test bypasses this path.
4. **Assumption U4 unvalidated** — "opencode appends standard paths to baseURL." If opencode constructs URLs differently, the relay receives no traffic. Requires live cluster validation.

---

## Tests Run

```
pkg/relay/              — 7 tests (protocol types, host validation)
pkg/relay/client/       — 4 tests (connect, error, ping, close)
cmd/workspace-agentd/   — 13 tests (handler, streaming, error, timeout, concurrent,
                                     rate limit, WebSocket, body, URL build, disconnect,
                                     isConnected, headers)
api/internal/handlers/  — 15 tests (relay handler: 9, fallback: 6, integration: 2,
                                     ownership: 1, IsClientConnected: 1)
api/internal/server/    — OpenAPI contract test PASS
frontend/               — 7 tests (TypeScript hook)

Total: 45+ Go tests + 7 TS tests, all passing with -race.
Full build: go build ./... PASS
golangci-lint: 0 issues
```

---

## Next Steps

1. **Controller wiring**: Inject `LLMSAFESPACE_RELAY_URL` and `LLMSAFESPACE_RELAY_TOKEN` into workspace pod env via pod_builder.go
2. **Live cluster validation**: Use the testing prompt in the PR description to verify end-to-end
3. **Frontend integration**: Use `useRelayClient` hook in ChatPage, gated by model's `proxyRequired` flag
4. **Unit tests for pushRelayBaseURL**: Add httptest.Server mock of opencode `/auth/opencode`
5. **Auth header propagation test**: Verify agentd sends Bearer token on WebSocket dial

---

## Files Modified

### New files
- `pkg/relay/protocol.go` — Shared relay protocol types
- `pkg/relay/protocol_test.go` — Protocol type tests
- `pkg/relay/client/client.go` — Go SDK relay client
- `pkg/relay/client/client_test.go` — SDK client tests
- `cmd/workspace-agentd/relay_proxy.go` — In-pod relay proxy
- `cmd/workspace-agentd/relay_proxy_test.go` — Relay proxy tests (13 tests)
- `api/internal/handlers/relay_handler.go` — WebSocket relay endpoint
- `api/internal/handlers/relay_handler_test.go` — Relay handler tests (9 + 2 tests)
- `api/internal/handlers/relay_fallback.go` — CORS fallback handler
- `api/internal/handlers/relay_fallback_test.go` — Fallback tests (6 tests)
- `api/internal/handlers/relay_integration_test.go` — End-to-end integration tests (2 tests)
- `frontend/src/hooks/useRelayClient.ts` — Browser relay client hook
- `frontend/src/hooks/useRelayClient.test.ts` — Browser relay tests (7 tests)

### Modified files
- `cmd/workspace-agentd/main.go` — Wired relay proxy into user mux
- `api/internal/app/app.go` — Instantiate and wire RelayHandler + RelayFallbackHandler
- `api/internal/server/router.go` — Added handler fields to RouterConfig, route registration
- `api/internal/handlers/models.go` — `ProxyRequired` field, `pushRelayBaseURL`, `clearRelayBaseURL`, `isFreeTierModel`
