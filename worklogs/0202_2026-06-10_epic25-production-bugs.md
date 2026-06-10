# 0202 — Epic 25 Production Bugs (B2, G1, B5)

**Date:** 2026-06-10
**Status:** Planning — not started

---

## Objectives

Fix three confirmed production bugs in the API server hot path identified during Epic 25
(API Server Robustness & Correctness). All three are in `api/internal/handlers/proxy.go`.

---

## Bugs

### B2 — Streaming loop silent break on read error → HTTP 200 with corrupt JSON

**Location:** `api/internal/handlers/proxy.go` (SSE streaming loop)

**Problem:** When the upstream pod (opencode serve) restarts mid-stream, the read loop
encounters EOF or a connection reset. The loop silently `break`s, returns nil, and the
HTTP response already has status 200 (written on the first SSE chunk). The client receives
a 200 with a truncated/corrupt JSON stream — no error signal. Reconnect logic on the
client side cannot distinguish "stream ended normally" from "pod died".

**Fix approach:** Detect read errors inside the streaming loop and write an SSE error
event (`event: error\ndata: {...}\n\n`) before closing the stream, so the client can
distinguish clean termination from upstream failure.

---

### G1 — `io.ReadAll` without `LimitReader` at proxy.go:457

**Location:** `api/internal/handlers/proxy.go:457` (non-streaming proxy path)

**Problem:** `io.ReadAll(resp.Body)` with no size bound. A misbehaving or compromised
upstream could send an arbitrarily large response, causing unbounded memory allocation
and potential OOM kill of the API server.

**Fix approach:** Wrap with `io.LimitReader(resp.Body, maxResponseBytes)` — use the same
32 MB limit already established in `models.go` for the `/provider` response.

---

### B5 — Activity tracker map entries never deleted on NotFound → unbounded growth

**Location:** `api/internal/handlers/proxy.go` (activity tracker / SSE tracker)

**Problem:** The per-workspace activity tracker stores an entry per workspace ID. When a
workspace is deleted (pod NotFound), the tracker entry is never removed. Over time, with
many workspace creates/deletes, the map grows without bound — a memory leak proportional
to the number of workspaces ever created.

**Fix approach:** On upstream 404/NotFound response, delete the workspace entry from the
activity tracker map. Add a cleanup hook to the workspace deletion handler.

---

## Files to Change

- `api/internal/handlers/proxy.go` — all three bugs
- `api/internal/handlers/proxy_test.go` — regression tests for each fix

---

## Definition of Done

- B2: test exercises mid-stream read error; client receives SSE error event, not silent 200
- G1: test sends response > limit; handler returns 502, not OOM
- B5: test deletes workspace; tracker map entry is absent after deletion
- All existing proxy tests pass
