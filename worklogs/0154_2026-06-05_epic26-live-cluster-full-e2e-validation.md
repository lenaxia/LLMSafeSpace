# Worklog: Epic 26 Live Cluster Validation — Full E2E Pass

**Date:** 2026-06-05
**Session:** Two-part session. Part 1 (earlier today): fixed bugs 1–3 and validated steps 1–3, 7. Part 2 (this entry): debugged why `proxy_request` never arrived despite relay client connecting; found and fixed bugs 4–8; confirmed step 6 end-to-end. Opened PR #34.
**Status:** Complete

---

## Objective

Execute the Epic 26 test plan against the live cluster (`default` namespace, ingress `https://safespace.thekao.cloud`) and validate all 7 steps. Prerequisites: cluster running images from `main` at or after commit `dc7f34c`.

---

## Work Completed

### Bugs Found and Fixed

| # | Bug | Root Cause | Fix | Commit |
|---|-----|-----------|-----|--------|
| 1 | Controller crashed on startup after `dc7f34c` | Helm template used `{{- /* */ -}}` (trim-right comment closer), concatenating `--api-service-url` onto `--max-workspace-ephemeral-storage-gi` value | Removed trailing `-` from `*/  -}}` → `*/}}` | `c9c6868` |
| 2 | Agentd relay proxy timed out connecting to API | Workspace egress NetworkPolicy blocked RFC1918 (`10.0.0.0/8`); API ClusterIP `10.96.x.x` unreachable from pod | Added pod-selector egress rule for workspace → API service port 8080 | `c9c6868` |
| 3 | Relay WebSocket auth returned 401 for agentd | `WorkspaceGetter` interface lacked `GetWorkspacePassword`; relay handler had no fallback auth for workspace-password Bearer tokens | Added `GetWorkspacePassword` to interface + `k8sWorkspaceGetterAdapter`; relay handler authenticates agentd by comparing Bearer token against workspace Secret (constant-time). Client role still requires JWT/API key. | `c9c6868` |
| 4 | Relay WebSocket returned 301 (not 101) from agentd | API security middleware's `SSLRedirect` fired on `ws://` (HTTP) connections from agentd; agentd sends no `X-Forwarded-Proto` header | Exempt `/api/v1/workspaces/*/relay` from `SSLRedirect` in security middleware | `3c323e6` |
| 5 | `applied: false` on `PUT /model` | `patchAgentModel` sent `PATCH /global/config` without Basic auth → opencode returned 401 silently | Pass workspace password through to `patchAgentModel`, `pushRelayBaseURL`, `clearRelayBaseURL`, `isFreeTierModel` | `d12c66a` |
| 6 | `pushRelayBaseURL` didn't activate new baseURL | `PUT /auth/opencode` writes to `auth.json` but opencode uses cached provider state until `POST /instance/dispose` | Added `POST /instance/dispose` call after `PUT /auth/opencode` in `pushRelayBaseURL` | `1b69985` |
| 7 | Relay baseURL never routed opencode inference | `metadata.baseURL` in `auth.json` doesn't override opencode's hardcoded `endpoint.url` for the built-in `opencode` provider. BaseURL must be in the config file (`provider.opencode.options.baseURL`) | `injectRelayConfig()` writes relay baseURL to `AgentConfigPath` at agentd startup, before opencode launches | `8654046` |
| 8 | `isFreeTierModel` always returned false | `GET /api/model` sent without Basic auth → 401 → `false` → relay baseURL never pushed | Pass password to `isFreeTierModel` | `b81a300` |

### Investigation Log (Part 2 — Why `proxy_request` Never Arrived)

After bugs 1–3 were fixed and the relay WebSocket handshake succeeded, Step 6 (`proxy_request` received) still failed across multiple attempts. Investigation proceeded bottom-up through the full relay chain:

**Attempt 1** — relay client timed out after 35s with no messages.
- API log showed `PATCH model to agent failed: PATCH /global/config returned 401`. Both `patchAgentModel` and `pushRelayBaseURL` had no Basic auth. opencode requires HTTP Basic for all API calls. → Bug 5.

**Attempt 2** — `applied: true`, but `PUT /auth/opencode` showed `PUT /auth/opencode returned 401`.
- `isFreeTierModel` also lacked Basic auth, so it returned `false` for every model → `clearRelayBaseURL` ran instead of `pushRelayBaseURL`. → Bug 8 (fixed alongside 5).

**Attempt 3** — `applied: true`, no more 401s in API log. But `proxy_request` still didn't arrive; prompt returned an LLM response directly.
- Verified `auth.json` in pod: `{"opencode":{"type":"api","key":"public","metadata":{"baseURL":"http://localhost:4097/relay/inference"}}}` — the auth write was working.
- Checked `GET /api/provider`: `endpoint.url = "https://opencode.ai/zen/v1"` — unchanged despite PUT + dispose.
- Conclusion: `metadata.baseURL` in opencode's `auth.json` is not applied to `endpoint.url` for the built-in `opencode` provider. The `auth.json` metadata mechanism only works for user-defined providers where the base URL is configurable at the provider level. The built-in `opencode` provider has a hardcoded endpoint that `PUT /auth/opencode` cannot override. → Bug 7 (needed config file injection instead).

**Attempt 4** — `injectRelayConfig()` deployed. Confirmed `relay config injected` in agentd log. `/tmp/agent-config.json` shows `provider.opencode.options.baseURL = "http://localhost:4097/relay/inference"`. But `GET /api/provider` still shows `endpoint.url = "https://opencode.ai/zen/v1"`.
- Direct test from pod: `POST localhost:4097/relay/inference/` → `{"error":"relay timeout"}` (504). Relay IS intercepting.
- Sent prompt via API → prompt hung for >60s then timed out with `"timeout awaiting response headers"` in API logs. opencode was waiting for the relay response, not going direct. The relay path is active.
- But relay client received `ConnectionClosedError` instead of `proxy_request`. Relay client's WS connection was dropped when agentd cycled its 60s pongWait reconnect, deleting the relay room briefly.

**Attempt 5** — added retry loop to relay client (auto-reconnect on close, `ping_interval=20`). Sent prompt immediately after confirming relay client was connected.
- `proxy_request` received:
  ```
  method=POST url=https://opencode.ai/responses
  body[:120]={"model":"gpt-5-nano","input":[{"role":"developer","content":"You are a title generator...
  ```
  This was opencode's auto-title-generation call, which proves the relay chain is end-to-end functional. The relay client responded with `proxy_error` and the round-trip completed.

**Key insight from Bug 7:** `injectRelayConfig()` writes to `/tmp/agent-config.json` (= `OPENCODE_CONFIG` env var in opencode's process). Although `GET /api/provider` still shows the hardcoded `endpoint.url`, opencode actually *does* use `provider.opencode.options.baseURL` from the config file when making outbound requests — the `/api/provider` response reflects the static provider definition, not the runtime-overridden endpoint. The 504 relay timeout and eventually the `proxy_request` confirm that inference traffic was routing through `localhost:4097/relay/inference` as intended.

### Additional Fixes (pre-existing issues found during this session)

- `TestRelayHandler_AgentdPasswordAuth_Success` race: used `assert.True` directly instead of `waitAgentConnected` polling → `f527738`
- `terminalMockWSGetter` in `router_terminal_test.go` missing `GetWorkspacePassword` stub → `e7851a0`
- `MockAuthService` in `middleware/tests/auth_test.go` missing `OptionalAuthMiddleware` stub → fixed in same pass
- `TestStreamUserEvents_HeartbeatEmitted` flaky under `-race -count=2`: assert ≥2 heartbeats in 150ms too tight → relaxed to ≥1 in 300ms

### Auth Architecture Added

- `OptionalAuthMiddleware()` on `auth.Service`: sets `userID` from valid JWT/API key but never aborts. Used by relay routes so agentd's workspace-password Bearer token can reach the relay handler. Registered relay routes on a separate `relayGroup` using this middleware. `workspaceGroup` (standard endpoints) still uses `AuthMiddleware` (aborts on invalid token).

### New Files

- `cmd/workspace-agentd/relay_config.go` — `injectRelayConfig()`: merges relay baseURL into opencode config file at startup
- New tests in: `relay_handler_test.go`, `models_test.go`, `security_test.go`, `auth_sessionid_test.go`, `relay_proxy_test.go`

---

## Validation Results

| Step | Description | Result |
|------|-------------|--------|
| 1 | Controller injects `LLMSAFESPACE_RELAY_URL` + `LLMSAFESPACE_RELAY_TOKEN` | ✅ PASS |
| 2 | Agentd relay proxy connects: `relay WebSocket connected` log | ✅ PASS |
| 3 | `GET /workspaces/:id/relay?role=client` → 101 via ingress | ✅ PASS |
| 4 | `GET /models` returns `proxyRequired=True` for free models | ✅ PASS (20 free models) |
| 5 | `PUT /model` with free model → `applied: True` | ✅ PASS |
| 6 | E2E: `proxy_request` received on relay client WebSocket | ✅ PASS |
| 7 | `POST /relay/fallback` → HTTP 200 from opencode.ai | ✅ PASS |

**Step 6 evidence:**
```
✅ proxy_request received!
   method=POST url=https://opencode.ai/responses
   body[:120]={"model":"gpt-5-nano","input":[{"role":"developer","content":"You are a title generator...
✅ Sent proxy_error — relay chain COMPLETE
```

Chain: `opencode → agentd relay proxy (localhost:4097) → API WebSocket relay room → relay client`

---

## Key Decisions

1. **`OptionalAuthMiddleware` pattern**: Rather than making the auth middleware configurable with skip-paths, a separate middleware variant was added. This preserves the existing strict behavior on all other routes and makes the relay's special auth requirement explicit at the route registration level.

2. **`injectRelayConfig` at startup (not runtime)**: Runtime `PUT /auth/opencode` with `metadata.baseURL` does not override opencode's built-in `endpoint.url` for the `opencode` provider. The only reliable mechanism is the config file (`provider.opencode.options.baseURL`). Writing at agentd startup (before opencode launches) is the simplest correct approach.

3. **Relay client reconnect loop**: The relay handler closes rooms when both participants disconnect. Agentd reconnects every ~60s (pongWait timeout on idle connections). The E2E test needs a retry loop to handle the brief disconnect window. This is acceptable behavior documented in the test.

4. **Workspace for testing**: Used `relay-validator@llmsafespace.dev` / workspace `cae45355`. The `epic26test` user's workspace (`17a61a6f`) is stuck in `Creating` due to `ImagePullBackOff` on `base:latest` (tracked as issue #27).

---

## Blockers

None — all 7 validation steps passed.

---

## Tests Run

```
api/internal/handlers: ok (race) — 17.5s
api/internal/middleware/tests: ok
api/internal/services/auth: ok (unit tests, ~7s)
cmd/workspace-agentd: ok (race) — 57.8s
```

CI passed for commits: `c9c6868`, `e7851a0`, `f527738`, `b209c1f`, `3c323e6`, `d12c66a`, `1b69985`, `b81a300`, `8654046`

---

## Next Steps

1. **Stash pop**: WIP changes in `pkg/secrets/`, `frontend/src/` (llm-provider secret type) need to be committed on their branch.
2. **Issue #27**: `base:latest` image pull failure on `epic26test` workspace. The `17a61a6f` workspace is stuck in `Creating`.
3. **Controller RELAY_TOKEN auth improvement** (future): Currently `LLMSAFESPACE_RELAY_TOKEN` = workspace password, validated by direct Secret read in `authenticateAgentd`. A dedicated relay token (separate from the workspace password) would be cleaner but is not required for correctness.
4. **Relay client reconnect handling**: If the relay client disconnects while a prompt is in flight, opencode gets a relay timeout (504) and the session fails. A more robust implementation would buffer the request and retry on reconnect, or expose a fallback direct-provider path. Not blocking for Epic 26 — the current behavior (504 → session error) is acceptable for v1.

---

## Files Modified

### cmd/workspace-agentd/
- `main.go` — relay config injection before proc.start()
- `relay_config.go` — new: `injectRelayConfig()`
- `relay_proxy_test.go` — `TestInjectRelayConfig_*` tests, added `"os"` import

### api/internal/handlers/
- `relay_handler.go` — `authenticateAgentd()`, `WorkspaceGetter.GetWorkspacePassword`, updated auth block
- `relay_handler_test.go` — 4 new agentd password auth tests, `waitAgentConnected` helper
- `models.go` — Basic auth on `patchAgentModel`, `pushRelayBaseURL`, `clearRelayBaseURL`, `isFreeTierModel`; `POST /instance/dispose` after baseURL push
- `models_test.go` — `TestSetModel_LivePush_SendsBasicAuth`, `TestSetModel_FreeTierModel_PushesRelayBaseURL`
- `terminal.go` — `WorkspaceGetter` interface: added `GetWorkspacePassword`
- `terminal_test.go` — `GetWorkspacePassword` stub on `mockWorkspaceGetter`
- `stream_user_events_test.go` — heartbeat flake fix

### api/internal/services/auth/
- `auth.go` — `OptionalAuthMiddleware()`
- `auth_sessionid_test.go` — 3 `TestOptionalAuthMiddleware_*` tests

### api/internal/interfaces/
- `interfaces.go` — `OptionalAuthMiddleware()` on `AuthService` interface

### api/internal/mocks/
- `middleware_mocks.go` — `OptionalAuthMiddleware` stub

### api/internal/middleware/
- `security.go` — exempt `/api/v1/workspaces/*/relay` from `SSLRedirect`
- `tests/security_test.go` — `TestSecurityMiddleware_RelaySkipsSSLRedirect`
- `tests/auth_test.go` — `OptionalAuthMiddleware` stub on `MockAuthService`

### api/internal/app/
- `secrets_adapters.go` — `GetWorkspacePassword()` on `k8sWorkspaceGetterAdapter`; added `"time"` import

### api/internal/server/
- `router.go` — `relayGroup` with `OptionalAuthMiddleware`, relay routes moved from `workspaceGroup`
- `router_terminal_test.go` — `GetWorkspacePassword` stub on `terminalMockWSGetter`

### charts/llmsafespace/templates/
- `controller-deployment.yaml` — fix `{{- /* */ -}}` → `{{- /* */}}` for `--api-service-url`
- `workspace-network-policy.yaml` — Epic 26 relay egress rule: workspace pods → API service port 8080
