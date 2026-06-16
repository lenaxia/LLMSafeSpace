# Worklog: Abort stale busy sessions after every opencode restart

**Date:** 2026-06-15
**Session:** Fix sessions stuck in "busy" after opencode is killed mid-run (PR #184)
**Status:** Complete

---

## Objective

When opencode is killed mid-run (relay injector SIGTERM, agent reload, pod OOM), the in-flight session is left with a busy flag in SQLite. The next opencode instance loads this stale state and refuses new messages with "session is busy; retry after idle", requiring manual intervention.

Discovered live: workspace `c68073e2`, session `ses_13c67b02effeWzX0dH4XftLszG` ("Epic 42 implementation start") was stuck at step 317. The previous opencode instance was killed by the relay injector's SIGTERM at `19:40:39`. The new instance loaded the session as busy; users could not send new messages even after switching models.

---

## Work Completed

### Root cause

Session status is a **runtime concept** in opencode — not persisted to SQLite, not returned by the REST API (`/session` or `/session/:id`). It exists only in the in-memory SSE event stream. After a restart, the SSE tracker is empty and there is no API call that can distinguish a stale-busy session from a genuinely-busy one. Validated live against the running opencode pod.

### Fix

Abort every session unconditionally after each opencode start. After a restart opencode has no in-flight LLM calls, so any busy flag is stale by definition. Aborting an idle session is a no-op in opencode (returns `true`, does nothing). This is safe and idempotent.

### Implementation

**`cmd/workspace-agentd/stale_sessions.go`** (new file):
- `OpenCodeClient.doPost` — authenticated POST helper (empty body)
- `OpenCodeClient.AbortSession` — `POST /session/:id/abort`, any 2xx = success
- `abortStaleSessions` — lists all sessions, aborts each with a 5s per-abort timeout; logs failures but always returns (best-effort)
- `abortStaleSessionsAfterStart` — polls health up to 30s, then calls `abortStaleSessions`; the production `onStart` callback

**`cmd/workspace-agentd/main.go`**:
- Add `onStart func()` field to `managedProcess`, fired in a `probeWg`-tracked goroutine after each child starts (same pattern as `healthProbeAfterRestart`, so `stop()` correctly joins it)
- Wire `abortStaleSessionsAfterStart` as the production `onStart` callback

### Race condition found and fixed during review

The original wiring assigned `proc.onStart` **after** `proc.start()` in `main()`:

```go
proc = &managedProcess{}
proc.start()           // supervisor goroutine forks HERE
...
proc.onStart = func(){...}   // unsynchronised write — races with supervise()'s read
```

`supervise()` reads `p.onStart` under `p.mu` on the first iteration, but the assignment was not synchronised with that read. Two consequences:
1. Go memory-model data race.
2. On the initial boot the supervisor could observe `onStart == nil` and **silently skip the stale-session cleanup** — the primary scenario this fix targets. `go test -race` did not catch it because the tests set `onStart` in the struct literal *before* `start()`.

Fix: construct the HTTP `client` before `managedProcess` and set `onStart` in the struct literal before `start()`. The goroutine-creation happens-before relationship guarantees the supervisor observes the non-nil callback. Documented the constraint in the `onStart` field comment.

### Test improvements

The three `managedProcess.onStart` tests were rewritten to use deterministic channel-based synchronisation (replacing `time.Sleep` assertions that the reviewer flagged as flaky on loaded CI). Added `waitForChildStart` helper so the nil-callback test no longer blocks for the full child lifetime when `stop()` races ahead of child assignment.

---

## Key Decisions

**Abort ALL sessions, not just busy ones.** Validated live: neither `GET /session` nor `GET /session/:id` returns a status field. Status is ephemeral (SSE-only), unavailable at startup. Unconditional abort is the only correct approach and is safe — abort of an idle session returns `true` and does nothing.

**`onStart` must be set before `start()`.** Assigning it after `start()` races with the supervisor. This is now enforced by the field comment and covered by a deterministic boot test.

**Scope narrowed to the stale-session fix only.** The original PR bundled the `EnsureWorkspaceConfig` create-or-update fix, the init-container `workspace-config.json` copy, and a `.trivyignore` CVE suppression. Those landed independently via PR #183 (and the CVE entry is already on main). This branch contains only the stale-session abort mechanism to keep the conventional-commit title honest.

---

## Assumptions (stated and validated)

1. **opencode session status is not queryable after restart** — validated via `kubectl exec` against the live pod: `GET /session` and `GET /session/:id` return no status field.
2. **Aborting an idle session is a no-op** — opencode returns `true` from `POST /session/:id/abort` when no LLM call is in progress. Confirmed by the abort endpoint behaviour documented in the opencode 1.15.12 binary.
3. **opencode has no in-flight LLM calls immediately after start** — true by construction: the process was just forked and has not received any request yet.

---

## Blockers

None.

---

## Tests Run

```
go build ./cmd/workspace-agentd/...
# → OK

go test -timeout 120s -race -run "TestAbortSession|TestAbortStaleSessions|TestManagedProcess|TestAbortStaleSessionsAfterStart" ./cmd/workspace-agentd/ -v
# → 12 tests PASS (race detector clean, 22.6s)

gofmt -l stale_sessions.go stale_sessions_test.go main.go
# → clean (no files listed)
go vet ./cmd/workspace-agentd/...
# → clean
```

---

## Next Steps

1. Merge PR #184 after CI re-runs on the rebased branch.
2. Operationally, the live workspace `c68073e2` can now be unblocked by any opencode restart (pod restart or agent reload) — no manual `POST /session/:id/abort` needed.
3. Consider an opencode-side fix: persist session status to SQLite so a restart can distinguish busy from idle. That would make selective abort possible but is out of scope for this defensive fix.

---

## Files Modified

- `cmd/workspace-agentd/stale_sessions.go` (new) — `doPost`, `AbortSession`, `abortStaleSessions`, `abortStaleSessionsAfterStart`
- `cmd/workspace-agentd/main.go` — `managedProcess.onStart` field (with ordering constraint documented); wire `abortStaleSessionsAfterStart` as `onStart` before `start()`; construct `client` before `managedProcess`
- `cmd/workspace-agentd/stale_sessions_test.go` (new) — 12 tests; deterministic channel-based `managedProcess.onStart` tests + `waitForChildStart` helper
- `worklogs/0303_2026-06-15_abort-stale-sessions-on-restart.md` — this worklog
