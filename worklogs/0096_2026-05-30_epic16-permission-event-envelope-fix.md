# Worklog: Epic 16 US-16.3 — Permission/Question SSE envelope-vs-properties fix

**Date:** 2026-05-30
**Session:** Diagnose stuck session `ses_188884ed9ffeyuJslsFErY2r8p` (workspace `c98963e7-3d3e-473b-be51-350dbc6d4e76`); recover the live session; fix the underlying bug in shipped Epic 16 backend code.
**Status:** Complete

---

## Objective

User reported their chat session was unresponsive. Diagnose end-to-end (browser → API → opencode), unblock the live session, and fix the root cause so future parallel-tool-call permission prompts reach the API broker correctly.

Out of scope (deferred): the rest of Epic 16 frontend work (US-16.8–16.12). The frontend has zero handlers for `agent.permission`/`agent.question` events; even with this backend fix, the UI will not render prompts until those stories ship. User explicitly chose to defer.

---

## Stated assumptions and validation

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | Session `ses_188884ed9ffeyuJslsFErY2r8p` is wedged because opencode is awaiting permission approvals (not a generic crash) | Verified: `GET http://10.69.6.2:4097/v1/statusz` returned `"sessions":[{"status":"busy"}]`; `GET http://10.69.6.2:4096/permission` returned 4 pending `external_directory` permissions for `/tmp/*` and `/sandbox-cfg/*`, callIDs matching the 4 stuck `running` tool calls in session message history |
| A2 | API logs `permission event missing id or sessionID` warnings indicate a parser drop, not a network/transport issue | Verified: warnings fire from `proxy.go:924` `emitNormalizedInputEvent`; raw `opencode.event` envelopes still reach the broker (subsequent SSE events delivered to browser ran 1m43s @ 200) |
| A3 | The wire format from opencode is the flat envelope `{"type":"permission.asked","properties":{"id":"per_…","sessionID":"ses_…",…}}` per US-16.3 design | Verified: `dialect.go:74-84` documents the exact shape; `session_tracker.go:212-236` parses envelope into `evt.Type` + `evt.Properties` correctly; production-confirmed by `GET /permission` REST returning the same flat-properties shape (one entry per pending permission) |
| A4 | Existing US-16.3 unit tests pass *flat* properties to `onRawEvent` directly, bypassing `processEvent`, so they never exercised the production wiring | Verified: `proxy_input_test.go:212-379` (pre-fix) called `handler.onRawEvent("ws-1", "question.asked", flatProps)` — never went through `tracker.processEvent`, which is the only production caller |
| A5 | The fix specified in story US-16.3 (extract `properties` from envelope inside `emitNormalizedInputEvent`) is the correct contract; the shipped code deviated from the story | Verified: `design/stories/epic-16-agent-input-requests/US-16.3-normalized-events.md:50-57` shows the envelope-extraction pattern that the shipped code omitted |
| A6 | Approving one `external_directory` permission with `reply: "always"` cascades to other matching patterns | Verified live: 1st approval returned `true`; 2nd returned `PermissionNotFoundError` because `/tmp/*` was already approved (pattern dedupe); same for `/sandbox-cfg/*` after 3rd approval |
| A7 | Frontend has no handler for `agent.permission`/`agent.question` events (US-16.8–16.12 unimplemented) | Verified: grep `agent\.permission|agent\.question|QuestionPrompt|PermissionPrompt` across `frontend/src/` returns zero matches; `ChatPage.tsx:321-376` `handleSSEEvent` switch covers only `workspace.phase`, `session.status`, `opencode.event` |

---

## Work Completed

### 1. Live session recovery

POSTed `{"reply":"always"}` to all 4 pending permissions on `10.69.6.2:4096/permission/<per_*>/reply` via the workspace password from `workspace-pw-c98963e7-…` Secret. Two returned `true`, two returned `PermissionNotFoundError` (cascade per A6). `statusz` reported session `idle` and `/permission` returned `[]` afterwards. User session unblocked end-to-end.

### 2. Root-cause analysis (E2E trace)

Followed the SSE pipeline:
1. opencode `/event` SSE emits flat envelope (verified shape, A3)
2. `api/internal/handlers/session_tracker.go:212-236` `processEvent` parses envelope into `evt.Type` + `evt.Properties` correctly, then calls `onRawEvent(workspaceID, evt.Type, data)` where `data` is the **whole envelope string**
3. `api/internal/handlers/proxy.go:870-891` `onRawEvent` publishes the raw `opencode.event` (correct), then forwards `rawData` to `emitNormalizedInputEvent`
4. **Bug in `proxy.go:896` (pre-fix):** `properties := json.RawMessage(rawData)` — variable named `properties` but actually holds the full envelope. `dialect.ParsePermissionRequest` (`pkg/agent/opencode/dialect.go:174-178`) expects `id`/`sessionID` at the *top level*; in the envelope those fields are nested under `properties`. Result: silent zero-valued struct, parser returns `permission event missing id or sessionID`, `agent.permission` event never published to broker
5. Browser only ever sees `opencode.event{event_type:"permission.asked"}`. Frontend ignores it (US-16.8–16.12 unshipped). User stares at silence

Same defect applied to `question.asked`, `permission.replied`, `question.replied`, `question.rejected` — all four normalized event paths were broken.

### 3. TDD fix

#### RED — new e2e integration tests in `api/internal/handlers/proxy_input_test.go`

Added 4 tests that drive the real production entry point — `SSETracker.processEvent` — with full opencode envelopes, asserting both raw `opencode.event` and normalized `agent.{permission,question}{,.resolved}` events reach the broker:

- `TestNormalizedEvents_E2E_PermissionAsked_ViaProcessEvent`
- `TestNormalizedEvents_E2E_QuestionAsked_ViaProcessEvent`
- `TestNormalizedEvents_E2E_PermissionResolved_ViaProcessEvent`
- `TestNormalizedEvents_E2E_QuestionResolved_ViaProcessEvent`

Plus a `recvWithTimeout(t, ch, what)` helper so dropped events surface as a fast 2s failure rather than hanging the test runner. First run: `*Asked` tests timed out at the helper (event never published); `*Resolved` tests received empty `request_id`/`session_id`/`reply` (envelope unmarshaled into wrong-shape struct). Confirmed RED.

#### GREEN — fix in `api/internal/handlers/proxy.go:893-967`

Per US-16.3 spec lines 50-57:

```go
var envelope struct {
    Properties json.RawMessage `json:"properties"`
}
if err := json.Unmarshal([]byte(rawData), &envelope); err != nil || len(envelope.Properties) == 0 {
    return
}
properties := envelope.Properties
```

Added godoc explaining the envelope contract so the next reader does not re-introduce the same bug.

#### Test correction (not a hack)

Pre-existing `TestNormalizedEvents_*` tests at `proxy_input_test.go:212-379` enforced the **wrong** contract — they fed flat properties to `onRawEvent` directly, exercising a code path that never exists in production. After the fix, the new behavior (envelope unwrap with early return on missing `properties`) means flat-data tests would not publish anything. Two options per Rule 5 (no hack-tests-to-pass): leave them broken, or correct them. Chose correction because the tests were always wrong relative to US-16.3:

- All 5 tests now build envelopes via a shared `makeEnvelope(eventType, props)` helper
- All `<-ch` reads replaced with `recvWithTimeout` to guard against future regressions
- `TestNormalizedEvents_RawEventAlwaysPublished` and `TestNormalizedEvents_ParseError_NoNormalizedEvent` updated to use envelope format
- `TestNormalizedEvents_BrokerNil_NoPanic` updated to use envelope format

### 4. Documentation alignment

`api/internal/handlers/event_broker.go:9-22` godoc on `WorkspaceSSEEvent` previously listed only 3 event types; extended per US-16.3 spec lines 96-104 to document all 7 production event types and the `Data` payload type for each.

---

## Key Decisions

| Decision | Rationale |
|----------|-----------|
| Fix at the consumer (`emitNormalizedInputEvent`), not the producer (`session_tracker`) | Matches US-16.3 design verbatim. Keeps `onRawEvent` signature stable so the raw-event publish path is unchanged (no regression to streaming/title/error event handling that depends on the full envelope shape) |
| Treat the existing `TestNormalizedEvents_*` tests as wrong, not as a contract to preserve | They never went through `processEvent`. Per Rule 0 ("end-to-end tests that exercise the real wiring"), unit tests on `onRawEvent` with hand-rolled flat data were the kind of "tests pass in isolation" failure mode the README explicitly forbids. Updating them to envelope format is consistent with the story spec |
| Add `recvWithTimeout` helper instead of relying on test-process timeout | A blocking `<-ch` for a never-published event hangs for 60s and produces a panic dump. A 2s timed receive produces a clean `Fatalf` with a label like `"timed out waiting for agent.permission"`, so a future regression is diagnosable in one glance |
| Defer Epic 16 frontend (US-16.8–16.12) | User explicitly scoped this session to backend recovery + bug fix. Frontend rendering is the second-half of the broken pipeline but a multi-component delta in its own right |

---

## Blockers

None.

Frontend rendering of `agent.permission` / `agent.question` (US-16.8–16.12) remains unshipped. Until those land, the API will correctly publish normalized events, the SSE wire will deliver them, but the browser will discard them. User accepts this and will pick up the frontend work in a later session.

---

## Tests Run

```bash
# RED before fix — confirmed test hangs / empty fields
cd api && go test -timeout 60s -run 'TestNormalizedEvents_E2E_' ./internal/handlers/...
  → FAIL (timeout in *Asked, empty request_id in *Resolved)

# GREEN after fix — all 4 e2e + 7 existing TestNormalizedEvents pass
cd api && go test -timeout 60s -run 'TestNormalizedEvents' ./internal/handlers/...
  → ok   github.com/lenaxia/llmsafespace/api/internal/handlers   0.125s

# No regressions — full api suite with race detector
cd api && go test -timeout 180s -race ./...
  → all 16 test packages ok (auth 75s due to bcrypt cost 12, expected)

# pkg + cmd suite
go test -timeout 180s -race ./pkg/... ./cmd/...
  → all 18 test packages ok

# controller suite
go test -timeout 60s -race ./controller/...
  → all 5 test packages ok (one transient build-cache flake; passed on retry)

# Build all packages
go build ./...
  → ok

# Static analysis
go vet ./...
  → ok
```

Live cluster validation:

```bash
# Pre-fix verification of the bug
GET  http://10.69.6.2:4096/permission        → 4 pending entries (workspace c98963e7-…)
API logs at 21:35:22-23: 4× "Failed to parse permission event"

# After approving permissions
GET  http://10.69.6.2:4097/v1/statusz        → sessions[0].status = "idle"
GET  http://10.69.6.2:4096/permission        → []
```

---

## Next Steps

1. **Build + push image**: commit and push to `main`; CI builds `ghcr.io/lenaxia/llmsafespace/api:sha-<new-commit>`. Deploy by updating the Helm release values to pin the new sha (current cluster runs `sha-eb5c33e`; the bug is present there).
2. **Validate fix on cluster**: trigger a permission-asking interaction in any workspace; confirm `agent.permission` events appear in the `/events` SSE stream (currently still ignored by frontend, but the wire format is now correct and inspectable in browser devtools or via `curl`).
3. **Resume Epic 16 frontend (US-16.8–16.12)** in a future session:
   - `frontend/src/api/types.ts`: add `QuestionRequest`, `PermissionRequest`, `AgentQuestionEvent`, `AgentPermissionEvent`
   - `frontend/src/api/input.ts`: client functions for `questionReply`, `questionReject`, `permissionReply`, `listQuestions`, `listPermissions`
   - `frontend/src/components/chat/QuestionPrompt.tsx` + `PermissionPrompt.tsx`
   - `frontend/src/pages/ChatPage.tsx`: route `agent.question`/`agent.permission`/`*.resolved` events; clear stale prompts on `session.status: idle`

---

## Files Modified

- `api/internal/handlers/proxy.go` — `emitNormalizedInputEvent` now extracts `properties` from envelope per US-16.3 spec; added godoc explaining contract
- `api/internal/handlers/event_broker.go` — `WorkspaceSSEEvent` godoc extended with the 4 `agent.*` event types
- `api/internal/handlers/proxy_input_test.go` — added `recvWithTimeout` helper; added 4 e2e integration tests via `SSETracker.processEvent`; corrected 7 pre-existing `TestNormalizedEvents_*` tests to use envelope format (the production contract)
