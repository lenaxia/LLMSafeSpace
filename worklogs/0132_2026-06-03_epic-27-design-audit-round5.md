# Worklog: Epic 27a/27b Design Audit — Round 5

**Date:** 2026-06-03
**Scope:** Full re-audit of `design/stories/epic-27a-credential-reload-foundation/README.md` and
`design/stories/epic-27b-credential-reload-polish/README.md` against actual source code.
Every claim in the design was verified against the real files. All findings are graded and
have a validity assessment.

---

## Context

Prior audit rounds (1–4) corrected many factual errors in the original Epic 27 draft and its
successors. Round 5 is a fresh pass from first principles: read both epics end-to-end, verify
every stated assumption against the codebase, then evaluate the design against: internal
consistency, code consistency, robustness, reliability, maintainability, scalability, security,
performance, SOLID principles, idiomatic Go, and right complexity.

Source files read during this audit:
- `pkg/agent/opencode/client.go`
- `api/internal/handlers/session_tracker.go`
- `api/internal/handlers/proxy.go`
- `api/internal/handlers/secrets.go`
- `api/internal/handlers/models.go`
- `api/internal/services/database/database.go`
- `api/internal/services/workspace/workspace_service.go`
- `api/internal/server/router.go`
- `api/internal/app/app.go`
- `api/internal/errors/errors.go`
- `api/migrations/000001_initial_schema.up.sql`
- `api/migrations/000002_workspaces.up.sql`
- `api/migrations/000008_user_secrets.up.sql`
- `api/migrations/000013_workspace_default_model.up.sql`
- `cmd/workspace-agentd/secrets.go`
- `cmd/workspace-agentd/main.go`
- `pkg/agentd/types.go`
- `pkg/secrets/types.go`
- `pkg/types/types.go`
- `pkg/apis/llmsafespace/v1/workspace_types.go`

---

## Findings

Each finding states the criticism, assesses its validity, and gives a disposition.

Severity ratings:
- **MUST FIX** — will cause compile failure, data corruption, or a silent correctness bug
- **SHOULD FIX** — degrades robustness, maintainability, or creates a likely implementation mistake
- **INFO** — observation worth recording; no required action

---

### Cross-Epic Findings

---

#### X-1 · MUST FIX — `WorkspaceListItem` mapping in `workspace_service.ListWorkspaces` not shown

**Finding:** The design extends `WorkspaceMetadata`, `types.Workspace`, and `types.WorkspaceListItem`
with two new fields (`AgentNeedsRefresh`, `CredentialsPendingSince`). It shows the `GetWorkspace`
mapping explicitly (US-27a.3 code snippet). It states "`WorkspaceListItem` is defined at
`types.go:424`; it also gains the two new fields." But it never shows the mapping in
`workspace_service.ListWorkspaces`.

`workspace_service.ListWorkspaces` has its own mapping loop at `workspace_service.go:309-317`
that constructs `types.WorkspaceListItem` from each `WorkspaceMetadata`. This loop is a separate
code path from `GetWorkspace`'s mapping at lines 265-278. If an implementer extends the DB
query and the struct but forgets this loop, `agentNeedsRefresh` silently returns `false` in
every list response, making the workspace-list banner (US-27a.8) inoperable.

**Validity:** VALID — the omission is the exact same class of gap found in prior audit rounds.

**Disposition:** Add an explicit code snippet in US-27a.3 showing the `WorkspaceListItem`
population in `workspace_service.ListWorkspaces`, parallel to the `GetWorkspace` snippet.

---

#### X-2 · SHOULD FIX — `workspaces.user_id VARCHAR(255)` vs `users.id VARCHAR(36)` — undocumented pre-existing defect

**Finding:** The design correctly identifies and fixes Bug 11 (`user_secret_bindings.workspace_id
VARCHAR(36)` vs `workspaces.id UUID`). However there is a second pre-existing type mismatch:
`workspaces.user_id` is `VARCHAR(255)` (`000002_workspaces.up.sql:4`) while `users.id` is
`VARCHAR(36)` (`000001_initial_schema.up.sql:2`), with no FK. The `ListPendingReloadWorkspaces`
query in 27b filters `WHERE w.user_id = $1` — correct at runtime (string equality) but
the missing FK means deleted users' workspaces are not cascade-cleaned.

Epic 27a is already doing schema hygiene work. Silently inheriting this while explicitly
repairing Bug 11 is inconsistent with the design's quality bar.

**Validity:** VALID.

**Disposition:** Record as Bug 12 in the assumptions table. Decide whether to fix in migration
000014 or defer to a follow-up. Either way, it must be named and have an explicit disposition
rather than passing in silence.

---

#### X-3 · MUST FIX — `agent_drain.WaitUntilIdle` package qualifier is wrong

**Finding:** The 27b design places `WaitUntilIdle` and `ErrDrainTimeout` in
`api/internal/handlers/agent_drain.go`. All files in `api/internal/handlers/` are
`package handlers` — a file's path does not create a new package in Go. Yet the US-27b.3
pseudocode writes:

```go
if err := agent_drain.WaitUntilIdle(...)
var drainErr *agent_drain.ErrDrainTimeout
```

The qualifier `agent_drain.` does not exist within `package handlers`. The call within the
same package is simply `WaitUntilIdle(...)` and `*ErrDrainTimeout`.

**Verified:** every `.go` file in `api/internal/handlers/` declares `package handlers`.
The "Files Likely Affected" table lists `api/internal/handlers/agent_drain.go` — same
directory, same package.

**Validity:** VALID — the pseudocode will not compile as written.

**Disposition:** Remove the `agent_drain.` qualifier from all call sites in US-27b.3. If a
separate package is actually desired, the design must specify a new directory
(`api/internal/handlers/agent_drain/`) and update all import paths accordingly.

---

#### X-4 · MUST FIX — `apierrors` not imported in `package handlers`; `NewConflictError` message is semantically wrong

**Finding:** Two separate issues in US-27a.7's `AgentReloadHandler.Reload` pseudocode:

1. `apierrors.NewConflictError(...)` and `apierrors.NewInternalError(...)` are called, but
   `package handlers` does not import `github.com/lenaxia/llmsafespace/api/internal/errors`.
   No file in `api/internal/handlers/` imports that package today (verified: only
   `api/internal/services/workspace/workspace_service.go` and `max_active.go` import it among
   non-test files in `api/`). This is a missing import that must be added explicitly.

2. `NewConflictError(resourceType, resourceID, err)` produces the message
   `"{resourceType} {resourceID} already exists"` (verified `errors.go:133`). The design uses
   it for "phase not Active" and "pod not reachable" — neither of which is a "resource already
   exists" situation. This is the wrong constructor; HTTP 409 is the right status code but
   `NewConflictError`'s canned message is misleading.

**Validity:** VALID on both counts.

**Disposition:**
1. Add `apierrors "github.com/lenaxia/llmsafespace/api/internal/errors"` to `agent_reload.go`'s
   import block.
2. Replace `apierrors.NewConflictError` with `c.JSON(http.StatusConflict, gin.H{"error": "..."})` 
   and a message that accurately describes the condition (e.g., "workspace is not Active" /
   "workspace pod is not reachable"). Or add a `NewPreconditionFailedError` constructor to the
   errors package.

---

#### X-5 · SHOULD FIX (documentation) — `extractAuth` vs `GetUserID` pattern divergence needs explicit documentation

**Finding:** The design says `extractAuth(c)` is the correct pattern for struct handlers in
`package handlers`. This is correct. But all inline workspace route closures in `router.go`
use `authSvc.GetUserID(c)` (verified `router.go:407, 424, 442, 458, 481, 507, 545, 559, 579`).
A future implementer reading `router.go` alongside `agent_reload.go` will see two auth-extraction
patterns and may not understand why they differ or which to use in a new handler.

**Validity:** VALID as a documentation gap; not a code bug.

**Disposition:** Update A12 in the 27a assumptions table to explicitly state:
"Inline route closures in `router.go` use `authSvc.GetUserID(c)` — a `package server`
function. Struct handlers in `package handlers` use `extractAuth(c)` (`secrets.go:795`).
Both read the same JWT claims. The difference is package boundary: `authSvc` is not accessible
from `package handlers`, so struct handlers use `extractAuth`."

---

#### X-6 · SHOULD FIX — `SubscribeDrain` iterates the inner map outside the lock — data race

**Finding:** The `dispatchProperties` fan-out pseudocode in US-27b.1 is:

```go
t.drainMu.Lock()
subs := t.drainSubs[workspaceID]  // assigns inner map by reference
t.drainMu.Unlock()
for _, s := range subs {          // iterates outside the lock
    s.onIdle(workspaceID, p.SessionID)
}
```

`subs` is the **inner map** (`map[uint64]*drainSub`) by reference. A concurrent
`SubscribeDrain` or `cancel()` call that modifies `t.drainSubs[workspaceID]` while the
`for-range` runs produces a concurrent map read/write — a data race under Go's memory model.

**Validity:** VALID.

**Disposition:** Copy values to a slice under the lock before releasing it:

```go
t.drainMu.Lock()
var callbacks []*drainSub
for _, s := range t.drainSubs[workspaceID] {
    callbacks = append(callbacks, s)
}
t.drainMu.Unlock()
for _, s := range callbacks {
    s.onIdle(workspaceID, p.SessionID)
}
```

This is the standard Go pattern for lock-free iteration of a snapshot.

---

#### X-7 · SHOULD FIX — `"retry"` session status not dispatched by `dispatchProperties` — drain busy set cannot clear on retry transition

**Finding:** `WaitUntilIdle` seeds the busy set with `if typ != "idle"` (correctly counting
`"retry"` sessions as busy). However `dispatchProperties` in `session_tracker.go` only handles
`case "idle"` and `case "busy"` (verified `session_tracker.go:262-268`). There is no
`case "retry"` dispatch.

A session transitioning `busy → retry` will: be added to the busy set on the `busy` event,
then the `retry` event is swallowed — the set still contains the session. The session is
only removed when an eventual `idle` event arrives. If the SSE connection drops after
`retry` (BF.1 scenario), the session stays in the busy set until the deadline.

The design is silent on whether `"retry"` events should be dispatched to drain subscribers.

**Validity:** VALID — the current silence is an ambiguity that will produce a question during
implementation.

**Disposition:** Choose one of:
(a) Add `case "retry"` to `dispatchProperties` calling `s.onActive` for drain subscribers
    (treating retry as still-busy — correct semantic). Document in US-27b.1.
(b) Explicitly state that `"retry"` events are not dispatched; retry sessions are only removed
    from the busy set via their eventual `"idle"` event; the drain timeout is the escape hatch.
Either is acceptable; the silence is not.

---

#### X-8 · MUST FIX (27b) — `getPassword` / `sseTracker` injection from `app.go` is structurally impossible as designed

**Finding:** The 27b design says drain mode requires `AgentReloadHandler` to receive
`passwordGetter` and `sseTracker` "injected directly from `app.go`." But:

- `ProxyHandler.getPassword` is an unexported method (`proxy.go:685`) — `app.go` cannot
  reference it as `proxyHandler.getPassword`.
- `ProxyHandler.sseTracker` is an unexported field (`proxy.go:76`) — `app.go` cannot access it.
- Neither is exposed via an exported getter. There is no `GetSSETracker()` or `GetPasswordGetter()`
  on `ProxyHandler` today (verified: no such methods exist).

The design says "Do NOT call `proxyHandler.GetPassword` or `proxyHandler.SSETracker()` — neither
exists as an exported method" and then immediately proposes passing these values from `app.go`
without explaining how `app.go` obtains them.

**Validity:** VALID — the wiring is structurally impossible as written.

**Disposition:** Choose one concrete solution and specify it explicitly:

**Option A (recommended):** Invert construction order. Extract `*SSETracker` construction and
the password-getter closure out of `NewProxyHandler`. Construct them in `app.go` before
`NewProxyHandler`. Inject into both `ProxyHandler` (via a new constructor parameter or setter)
and `AgentReloadHandler`. `app.go` holds the primary references and passes them to both.

**Option B:** Add exported accessors to `ProxyHandler`:
```go
func (h *ProxyHandler) SSETracker() *SSETracker { return h.sseTracker }
func (h *ProxyHandler) PasswordFunc() func(ctx context.Context, workspaceID string) (string, error) {
    return h.getPassword
}
```

Option A is cleaner (avoids exposing internals); Option B requires fewer changes to
`NewProxyHandler`. Either must be specified — the current design leaves the implementer to
discover the impossibility mid-implementation.

---

#### X-10 · SHOULD FIX — `reloadOne` referenced in `BulkReload` but never defined

**Finding:** The `BulkReload` pseudocode calls `h.reloadOne(c.Request.Context(), userID, ws.ID,
drain, drainTimeout)` and assigns its return to a `result` struct. `reloadOne` is never shown
anywhere in either epic.

Its implementation is non-trivial: it must call `GetLastCredentialChangedAt`, call the agentd
reload endpoint (with optional drain via `WaitUntilIdle`), call `MarkAgentReloaded` in a
transaction, and construct a success or failure `result`. The warning path (dispose OK, DB commit
failed) must be preserved in bulk mode too.

**Validity:** VALID.

**Disposition:** Add a pseudocode body for `reloadOne` in US-27b.4. The core logic is
`AgentReloadHandler.Reload` with HTTP response calls replaced by struct-field assignments. This
ensures the warning path and error cases are specified rather than left to implementer discretion.

---

### Epic 27a — Specific Findings

---

#### A27a-1 · INFO — `BeginTx` in `AgentStateStore` interface is coupled to `*sql.Tx` — testability note missing

**Finding:** `AgentStateStore.BeginTx(ctx, *sql.TxOptions) (*sql.Tx, error)` uses
`database/sql` concrete types. This is idiomatic Go (stdlib types are acceptable in interfaces)
but it means unit-testing `AgentReloadHandler` with a pure mock for `AgentStateStore` requires
`sqlmock` or a test database rather than a hand-rolled struct. The design does not note this.

**Validity:** VALID OBSERVATION — low severity.

**Disposition:** Add a note in US-27a.7 or the test plan: "`AgentStateStore.BeginTx` returns
`*sql.Tx`, which is a concrete stdlib type. Tests for `AgentReloadHandler` should use
`github.com/DATA-DOG/go-sqlmock` or a test PostgreSQL instance, not a hand-rolled mock struct."

---

#### A27a-2 · SHOULD FIX — `SELECT FOR UPDATE` on a missing row silently skips the lock

**Finding:** `MarkAgentReloaded`'s `SELECT FOR UPDATE` is intended to block concurrent
`MarkCredentialChanged` calls. The comment states the row is guaranteed to exist because
"a `workspace_agent_state` row already exists (`pending_refresh = TRUE`) which guarantees a row."
This invariant is relied upon for correctness but not enforced mechanically.

If the row is missing (migration error, test environment, future code change removing the
`pending_refresh = true` guard), `SELECT FOR UPDATE` returns `sql.ErrNoRows`. The code then
skips the lock via `if err != nil && err != sql.ErrNoRows` and falls through to the
`INSERT ... ON CONFLICT DO UPDATE`. A concurrent `MarkCredentialChanged` can INSERT the row
between the (empty) `SELECT FOR UPDATE` and this INSERT, and `MarkAgentReloaded`'s
`ON CONFLICT DO UPDATE` will overwrite `pending_refresh = FALSE`, silently losing the
concurrent credential change.

**Validity:** VALID.

**Disposition:** When `SELECT FOR UPDATE` returns `sql.ErrNoRows`, return a distinct error
(e.g., `ErrNoAgentStateRow`). The handler maps this to a 409 response: "workspace has no
pending credentials to reload." This converts a silent correctness hole into a loud failure
that surfaces the invariant violation. Add `TestMarkAgentReloaded_NoRow_ReturnsError` to the
test plan.

---

#### A27a-3 · SHOULD FIX — Application clock for `last_agent_disposed_at` vs DB clock for `last_credential_changed_at`

**Finding:** `MarkCredentialChanged` uses `NOW()` (DB server clock) for
`last_credential_changed_at`. `MarkAgentReloaded` uses `now := time.Now().UTC()` (application
clock) for `last_agent_disposed_at`. The two timestamps in the same table row are from different
clocks. This has no correctness impact on the CAS logic (which compares `last_credential_changed_at`
values against each other, both DB-clock). But it makes the row inconsistent for monitoring
and auditing: a `last_agent_disposed_at` that appears to precede `last_credential_changed_at`
by 50ms due to NTP drift would look like a bug.

**Validity:** VALID.

**Disposition:** Replace `$2` (application `now`) in `MarkAgentReloaded` with `NOW()` and use
`RETURNING last_agent_disposed_at` to get the DB-written timestamp back. Remove the application-
side `now := time.Now().UTC()` variable. All timestamps in `workspace_agent_state` then use the
DB clock consistently.

---

#### A27a-4 · INFO — env-restart in a mixed batch picks up staged LLM credentials as a side-effect

**Finding:** When a batch contains both LLM-provider secrets and env secrets, `StageCredentials`
runs (writing to `auth.json`) and `proc.restart()` runs (restarting the opencode process). The
restart will re-read `auth.json` and pick up the staged LLM credentials naturally, making the
`StageCredentials` call redundant (but harmless) in this path.

The prior `configReloaded` flag suppressed this scenario explicitly. Its removal makes the
behaviour implicit.

**Validity:** VALID OBSERVATION — no correctness issue.

**Disposition:** Add a code comment in `reloadSecretsHandler` noting: "If `shouldRestart` is
also true in the same batch, the env-restart will pick up staged LLM credentials from
`auth.json` as a side-effect, making the `StageCredentials` call redundant but harmless."
No code change required.

---

#### A27a-5 · SHOULD FIX — `renderError` in `agent_reload.go` duplicates `respondWithError` in `package server`

**Finding:** The design explicitly copies `respondWithError` (`router.go:743`) into
`agent_reload.go` as `renderError`, justifying it as "a one-time copy." But `package handlers`
will accumulate more struct handlers over time. Each one that needs error rendering will
face the same choice: duplicate or go without. The right fix — moving `respondWithError` to
`package handlers` or a shared `api/internal/httputil` package — is available now.

**Validity:** VALID.

**Disposition:** As part of US-27a.7, move `respondWithError` from `router.go` (where it is
`package server`-private) to `package handlers` (where all struct handlers can share it).
Update `router.go` to call `handlers.RespondWithError(c, err)`. This removes the duplication
and establishes the correct precedent for future handlers.

---

### Epic 27b — Specific Findings

---

#### B27b-1 · SHOULD FIX — `GetSessionStatuses` discards error body on 4xx, reducing drain debuggability

**Finding:** `GetSessionStatuses` handles `resp.StatusCode >= 400` with:
```go
return nil, fmt.Errorf("GET /session/status returned %d", resp.StatusCode)
```
The structured opencode error body (which contains the `_tag`, `message`, and other fields)
is discarded. When a drain fails because `GET /session/status` returns 401 or 500, the
operator log shows only the status code — not the actual error. Given that drain failures are
already hard to diagnose (they block deployment automation), losing the error message is a
disproportionate cost.

This is consistent with `PushCredentials` and `DisposeInstance` (both also discard error bodies),
but the drain scenario makes it more impactful.

**Validity:** VALID.

**Disposition:** Add `io.LimitReader(resp.Body, 512)` body capture on 4xx/5xx in
`GetSessionStatuses` and include the text in the error string:
```go
body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
return nil, fmt.Errorf("GET /session/status returned %d: %s", resp.StatusCode, string(body))
```
Consider applying the same pattern to `DisposeInstance` in a follow-up.

---

#### B27b-2 · INVALID — Channel-drop in `WaitUntilIdle` does not cause permanent deadlock

**Criticism raised:** A dropped `idle` event (channel full) could leave a session in the busy
set forever.

**Assessment:** The design correctly handles this. A dropped `idle` event means the session
stays in the busy set → drain waits until the deadline → `ErrDrainTimeout` is returned →
caller retries. On retry, `GET /session/status` snapshot shows the session as idle → drain
completes immediately. The design explicitly states "correctness is preserved; the worst case is
a spurious `DrainTimeout` error on extreme burst." **This criticism is invalid.**

**Disposition:** No fix required. The design should add a note explaining why 64 is a sufficient
channel capacity: typical workspaces have 1-5 sessions; 64 concurrent session transitions in a
burst is effectively impossible in practice.

---

#### B27b-3 · INFO — Goroutine lifecycle note for `BulkReload` fan-in

**Finding:** `BulkReload` has a background goroutine `go func() { wg.Wait(); close(results) }()`.
If context is cancelled while workers are blocked on `sem <-`, workers will exit via context
cancellation inside `reloadOne` (since they take `ctx` as first arg). The `wg.Wait()` goroutine
therefore completes promptly and closes `results`. No leak. The design is correct.

**Validity:** OBSERVATION — no fix required.

**Disposition:** Add a one-line comment on the `wg.Wait()` goroutine: "// Workers exit promptly
when ctx is cancelled; wg.Wait() unblocks within one reloadOne timeout."

---

#### B27b-4 · SHOULD FIX — `chatErrorEnrichmentWriter.WriteHeader` does not initialize `statusCode` before delegating — `WriteHeader(0)` edge case

**Finding:** `WriteHeader` is:
```go
func (w *chatErrorEnrichmentWriter) WriteHeader(code int) {
    w.statusCode = code
    if code < 400 {
        w.ResponseWriter.WriteHeader(code)
    }
}
```
If the proxy writes an error body by calling `Write` without first calling `WriteHeader`
(using Go's implicit 200 for the first `Write`), then `statusCode` is 0 on the first `Write`,
gets set to 200 in `Write`'s guard, and the write is forwarded. Later, `Finalize` calls
`w.ResponseWriter.WriteHeader(0)` — which is invalid.

The fix is to initialize `statusCode` in `WriteHeader`:
```go
func (w *chatErrorEnrichmentWriter) WriteHeader(code int) {
    if code == 0 { code = http.StatusOK }
    w.statusCode = code
    if code < 400 {
        w.ResponseWriter.WriteHeader(code)
    }
}
```

**Validity:** VALID.

**Disposition:** Add the `code == 0` guard to `WriteHeader` as shown above. The scenario
(proxy calls `Write` before `WriteHeader`) is unlikely for opencode's error responses but
defensive coding eliminates the possibility.

---

#### B27b-5 · SHOULD FIX — `chatErrorEnrichmentWriter` does not override `Written()` / `Status()` — gin middleware sees stale state

**Finding:** `gin.ResponseWriter` extends `http.ResponseWriter` with `Written() bool`,
`Status() int`, `Size() int`, `WriteHeaderNow()`, and others. `chatErrorEnrichmentWriter`
embeds `gin.ResponseWriter` and inherits these. For error responses, `WriteHeader` does NOT
call the base `ResponseWriter.WriteHeader` (it defers to `Finalize`). Therefore:
- `Written()` on the base returns `false` even though the wrapper has accepted bytes.
- `Status()` on the base returns `200` (default) even though the real status is 4xx/5xx.

Gin middleware that checks `c.Writer.Written()` before adding response headers (e.g., CORS,
security headers, metrics middleware) will see incorrect state and may write headers after
the response is already committed, or skip headers it should add.

**Validity:** VALID.

**Disposition:** Override `Written()` and `Status()` on `chatErrorEnrichmentWriter`:
```go
func (w *chatErrorEnrichmentWriter) Written() bool { return w.statusCode != 0 }
func (w *chatErrorEnrichmentWriter) Status() int {
    if w.statusCode == 0 { return http.StatusOK }
    return w.statusCode
}
```

---

#### B27b-6 · INFO — Client-side and server-side drain elapsed times will diverge in the UI

**Finding:** The design has the frontend modal compute elapsed time client-side (for the live
counter) AND the server returns `drainElapsedMs` in the response body. The two values differ
by network round-trip time plus clock skew. The UI should use the server-returned value as the
authoritative final elapsed time and switch to it when the response arrives.

**Validity:** VALID OBSERVATION — UX only, no correctness impact.

**Disposition:** Specify in US-27b.3's frontend notes: "Use client-side timer only as a live
estimate. Replace displayed value with server-returned `drainElapsedMs` when the response
arrives."

---

## Summary

| # | Epic | Severity | Finding |
|---|---|---|---|
| X-1 | Both | MUST FIX | `WorkspaceListItem` mapping in `workspace_service.ListWorkspaces` not shown |
| X-2 | Both | SHOULD FIX | `workspaces.user_id VARCHAR(255)` vs `users.id VARCHAR(36)` — undocumented pre-existing defect (Bug 12) |
| X-3 | 27b | MUST FIX | `agent_drain.WaitUntilIdle` package qualifier wrong — same `package handlers` |
| X-4 | 27a | MUST FIX | `apierrors` not imported in `package handlers`; `NewConflictError` message wrong for phase/pod errors |
| X-5 | 27a | SHOULD FIX | `extractAuth` vs `GetUserID` pattern divergence — needs explicit documentation in A12 |
| X-6 | 27b | SHOULD FIX | `SubscribeDrain` iterates inner map outside lock — data race |
| X-7 | 27b | SHOULD FIX | `"retry"` status not dispatched by `dispatchProperties` — drain ambiguity |
| X-8 | 27b | MUST FIX | `getPassword` / `sseTracker` injection from `app.go` structurally impossible as written |
| X-10 | 27b | SHOULD FIX | `reloadOne` referenced in `BulkReload` but never defined |
| A27a-1 | 27a | INFO | `BeginTx` couples `AgentStateStore` to `*sql.Tx` — testability note missing |
| A27a-2 | 27a | SHOULD FIX | `SELECT FOR UPDATE` on missing row silently skips lock — invariant not mechanically enforced |
| A27a-3 | 27a | SHOULD FIX | Application clock for `last_agent_disposed_at`; DB clock for `last_credential_changed_at` — inconsistency |
| A27a-4 | 27a | INFO | Mixed batch: env-restart picks up staged LLM creds as side-effect — note it in code |
| A27a-5 | 27a | SHOULD FIX | `renderError` duplication — move `respondWithError` to `package handlers` instead |
| B27b-1 | 27b | SHOULD FIX | `GetSessionStatuses` discards 4xx error body — reduces drain debuggability |
| B27b-2 | 27b | INVALID | Channel-drop causing permanent deadlock — correctly handled by ErrDrainTimeout + retry |
| B27b-3 | 27b | INFO | `BulkReload` fan-in goroutine lifecycle — correct, add comment |
| B27b-4 | 27b | SHOULD FIX | `WriteHeader(0)` edge case in `chatErrorEnrichmentWriter` |
| B27b-5 | 27b | SHOULD FIX | `Written()` / `Status()` not overridden — gin middleware sees stale state |
| B27b-6 | 27b | INFO | Client-side vs server-side drain elapsed time diverge — UX only |

**MUST FIX (5):** X-1, X-3, X-4, X-8, and A27a-2 (invariant unenforced — silent data loss under violation)

**SHOULD FIX (9):** X-2, X-5, X-6, X-7, X-10, A27a-3, A27a-5, B27b-1, B27b-4, B27b-5

**Informational / invalid (6):** A27a-1, A27a-4, B27b-2, B27b-3, B27b-6, X-10 (informational note)

---

## Next Steps

1. Apply all MUST FIX corrections to the design documents before implementation begins.
2. Apply SHOULD FIX corrections in the same pass — they are all small (one-liners or added
   paragraphs) and will pay back in implementation correctness.
3. Re-run this audit checklist against the final design before handing off to implementation.
4. The INVALID finding (B27b-2) should have a confirming note added to the design so future
   auditors do not re-raise it.
