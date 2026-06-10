# 0109 — Epic 17 Phase C/G6: F1.1.2 + RT-2.16 sessionId path traversal

**Date:** 2026-05-31
**Author:** mikekao + opencode (sonnet)
**Phase:** Epic 17 Phase C, F1.1.2 + RT-2.16
**Status:** Code-level fix complete; awaiting CI build + live cluster re-pentest

---

## Summary

Closes the High-severity **F1.1.2** (`:sessionId` upstream path
traversal, Phase 1 reconnaissance) and its Phase 2 live-cluster
counterpart **RT-2.16**. Pre-fix the proxy handlers concatenated the
URL parameter `c.Param("sessionId")` straight into the upstream URL
path:

```go
sid := c.Param("sessionId")
h.proxyToWorkspace(c, "/session/"+sid+"/message", true, sid)
```

A user with `sessionId = "../../../v1/admin"` produced
`/session/../../../v1/admin/message` which, when normalised by
HTTP libraries, addressed an arbitrary upstream endpoint.

Fix: `validateSessionID(sid)` is called at the start of every
`:sessionId` handler. The validator rejects empty / overlong (>128
chars) / `..` / non-allowlist-char inputs. Adversarial inputs return
HTTP 400 with `invalid sessionId: <reason>`.

---

## Stated assumptions (validated)

- **A1** — `c.Param("sessionId")` is the only place sessionId enters
  the proxy. (Validated: `grep -nE 'sessionId|sessionID' proxy.go`.)
- **A2** — All five `:sessionId` handlers go through
  `proxyToWorkspace(targetPath, ...)` which concatenates without
  re-normalising. (Validated: read each handler.)
- **A3** — Legitimate session IDs are alphanumeric+dot+dash+underscore
  (UUIDs, opencode `sess_*` IDs). 128-char cap is generous.

---

## Changes

1. `api/internal/handlers/proxy.go`:
   - **NEW** `sessionIDPattern` = `^[a-zA-Z0-9._-]+$`.
   - **NEW** `validateSessionID(s)` rejects empty, >128 chars, `..`,
     and any char outside the allow-list.
   - 5 handlers (`SendMessage`, `SendPromptAsync`, `GetHistory`,
     `GetSession`, `AbortSession`) call `validateSessionID` at top
     and return 400 on rejection.

2. `api/internal/handlers/proxy_sessionid_validation_test.go` (NEW):
   - `TestG6_F112_ValidSessionIDsAreAccepted` (4 valid payloads).
   - `TestG6_F112_RejectsTraversalSessionIDs` (13 adversarial
     payloads including URL-encoded traversal, slashes, query/
     fragment chars, whitespace, NUL, shell metacharacters,
     overlong).

---

## Tests

| Command | Result |
|---|---|
| `go test -count=1 -timeout 30s -run TestG6_F112 ./api/internal/handlers/...` | PASS |
| `go test -count=1 -timeout 60s ./api/...` | PASS |
| `go build ./api/...` | clean |

---

## Live re-pentest plan

After CI ships the new API image:

1. Authenticate as a normal user with a valid workspace.
2. `curl -X POST 'https://safespace.thekao.cloud/api/v1/workspaces/<ws>/session/../../../v1/admin/message' -H "Authorization: ..." -d '{}'`
3. Must respond **HTTP 400**: `{"error": "invalid sessionId: ..."}`.
4. Re-run phase-1 RT-1.1 traversal probe and phase-2 RT-2.16
   inconclusive → must convert to PASS.

---

## Tracker update

`MASTER-TRACKER.md`:
- F1.1.2 → MINE / live-pending
- RT-2.16 → resolved as duplicate of F1.1.2

---

## Next finding

Phase C/G7 — F1.4.2 (agentd `/v1/statusz` and `/v1/healthz`
unauthenticated).
