# Stuck Sessions Recovery Guide

**Issue:** Sessions stuck showing "session is busy; retry after idle" (HTTP 409 Conflict) in the UI
**Root Cause:** Stale entries in the API server's in-memory `activeSess` map (`api/internal/handlers/proxy.go:67`) — NOT in opencode's SQLite database. See worklog 0309 for full root cause analysis.
**Status:** PR #197 (`reconcileSessionState`) provides automatic recovery within ~5 minutes of the bug condition. This guide is for emergency manual recovery before that fix lands or for cases where automatic recovery doesn't trigger.

## Affected Sessions (2026-06-16 incident)

1. **Session from Incident B (Unsafe Restart)**
   - Workspace: `8154ae86-d7b7-4f53-b046-d8d3b462b972`
   - Session: `ses_130c14344ffeVF52UQ6QGPmB0P`

2. **Session from Incident A (OOMKill)**
   - Workspace: `a847faa5-19b4-463d-a434-1ce473a16f93`
   - Session: `ses_13076538bffeYtLrhoZ2ccRM1E`

---

## Why Sessions Get Stuck

When opencode dies mid-stream (OOM, SIGTERM, crash), it never emits the `session.status=idle` SSE event. The chain:

1. opencode emits `session.status=busy` → API's `onSessionActive()` adds the session to `activeSess["{workspace}"]["{session}"]`
2. opencode dies before emitting `session.status=idle`
3. The SSE tracker reconnects after ~5 minutes idle timeout
4. Pre-PR-#197: `reconcileStrandedQueues` only fixed sessions with queued messages — sessions with empty queues stayed stuck
5. Post-PR-#197: `reconcileSessionState` cleans up stale entries on every reconnect

The 409 error originates from `proxy_handlers.go:78` checking `h.isSessionActive(wid, sid)` against the in-memory map. Note: opencode's SQLite separately tracks the orphaned assistant message (no `time.completed`), but that does NOT cause the 409 — only the API in-memory map does.

---

## Recovery Options

### Option 1: Wait for Automatic Recovery (Post-PR-#197)
**How it works:** PR #197's `reconcileSessionState` function runs on every SSE reconnect. The SSE tracker's idle timeout is 5 minutes (`api/internal/services/sse/tracker.go:20`), so worst-case recovery is ~5 minutes from the bug condition.

```bash
# Verify recovery happened
kubectl logs -n default deploy/llmsafespace-api --tail=200 \
  | grep "reconcileSessionState: clearing stale activeSess"
```

**Verdict:** ✅ Best option once PR #197 ships. Requires no manual intervention.

---

### Option 2: Force Abort via opencode (Manual)
**How it works:** Even when the API's `activeSess` map is stale, opencode's actual session state is correct (idle). Calling opencode's abort endpoint emits `session.status=idle` via SSE, which propagates to the API and clears the stale entry.

```bash
# Get workspace password
PASSWORD=$(kubectl get secret workspace-pw-${WORKSPACE_ID} -n default \
  -o jsonpath='{.data.password}' | base64 -d)

# Find the workspace pod
POD=$(kubectl get pods -n default -l llmsafespace.com/workspace=${WORKSPACE_ID} \
  -o jsonpath='{.items[0].metadata.name}')

# Trigger abort (this emits session.status=idle via SSE)
kubectl exec -n default ${POD} -c workspace -- \
  curl -s -X POST -u "opencode:${PASSWORD}" \
  "http://localhost:4096/session/${SESSION_ID}/abort"
```

**Verdict:** ✅ Reliable manual recovery. Used successfully on 2026-06-16 to recover both stuck sessions.

---

### Option 3: API Pod Restart (Nuclear Option)
**How it works:** Restarting all API pods clears the in-memory `activeSess` map cluster-wide.

```bash
kubectl rollout restart deploy/llmsafespace-api -n default
```

**Verdict:** ⚠️ Works but disruptive — affects all in-flight requests on the API. Use only if Options 1 and 2 are not feasible.

---

### Option 4: Clear Browser State
**How it works:** In rare cases the frontend caches the busy state.

```bash
1. Hard refresh (Ctrl+Shift+R)
2. Clear browser cache for the chat domain
```

**Verdict:** ⚠️ Worth trying as a first step — costs nothing. Does not address the actual issue (which is server-side).

---

## Recommended Action

**For the 2026-06-16 incident:** Use Option 2 (force abort via opencode). Confirmed working.

**For future incidents post-PR-#197:** Wait 5 minutes for automatic recovery (Option 1). If it doesn't trigger (unlikely), fall back to Option 2.

**Long-term (Epic 44 + Epic 45):** Both stuck sessions and the underlying bug class are addressed:
- Epic 44 — Detects opencode death and emits terminal SSE events to inform users
- Epic 45 — Externalizes `activeSess` to Valkey/Redis, eliminating per-replica state drift entirely

---

## Files Referenced

- `api/internal/handlers/proxy_events.go` — `reconcileSessionState` (PR #197)
- `api/internal/handlers/proxy_handlers.go:78` — The 409 check that traps stuck sessions
- `api/internal/handlers/proxy.go:67` — The `activeSess` map (the actual bug source)
- `api/internal/services/sse/tracker.go` — SSE reconnect logic
- `cmd/workspace-agentd/stale_sessions.go` — Stale session abort on agentd restart
- `worklogs/0309_2026-06-16_clear-stale-activesess-on-sse-reconnect.md` — Full root cause analysis
- `design/stories/epic-44-session-reliability-transparency/INCIDENT-ANALYSIS.md` — Original incident details
- `design/stories/epic-45-multi-replica-state-consistency/README.md` — Long-term architectural fix
