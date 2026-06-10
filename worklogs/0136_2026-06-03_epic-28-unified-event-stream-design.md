# Worklog — Epic 28: Unified User-Scoped Event Stream Design

**Date:** 2026-06-03
**Operator:** opencode
**Epic:** 28 — Unified User-Scoped Event Stream
**Stories:** S28.1–S28.8

---

## Summary

Full design for the unified user-scoped SSE event stream, produced through iterative
critique rounds. The design was revised nine times before being committed. Every claim
was validated against the actual codebase before being incorporated.

---

## Problem being solved

Measured in worklog 0132: when a user activates two workspaces and navigates between
them, the workspace they're not viewing reaches Active at backend ~T+38s but the UX
shows it ready only at T+128s — 90 seconds after the backend was done. The root cause:
SSE is connected per-workspace. Background workspace phase transitions are published to
zero subscribers and permanently lost.

---

## Design iterations and findings

Each round of critique produced validated findings. Invalidated claims are explicitly
marked. The final design addresses 27 distinct issues across 9 critique rounds.

### Round 1 — Initial design

Established the core architecture: replace `/workspaces/:id/events` with `/api/v1/events`
(user-scoped lifecycle) + `/workspaces/:id/session-events` (workspace-scoped in-session).
Defined event routing split, wire format (`workspace_id` field), SSE response format with
`id:` fields and heartbeats, broker sharding (C1), replay buffer (FM2), snapshot ordering
(FM4), and the full list of FM/C/G items.

**Key validated items from round 1:**
- FM6 (`EnsureWatching` only on connect) — **invalidated**: fires on write ops too
- C4 (phaseEventCh worker goroutine) — **dropped as over-engineering**: all ops in
  `onPhaseChange` complete in < 10µs, no I/O, adding channel adds latency for no benefit
- Q4 (how to enumerate user workspaces for snapshot) — **resolved**: k8s list with
  `user-id` label selector, 5s timeout, failure emits `resync`

### Round 2 — Second critique

Found G1–G9 gaps. All validated:
- **G1:** `/session-events` not in rate-limit `ExemptPaths` — fixed: `strings.HasSuffix`
  matching covers both paths
- **G5:** k8s list for snapshot had no timeout/failure behaviour — fixed: 5s context
  timeout, failure sends `resync` to `s.ch`, connection stays open
- **G6:** heartbeat goroutine didn't detect write errors — fixed: shared `streamCancel`
- **G8:** N separate `RLock` calls for snapshot — fixed: `GetAllKnownPhases()` method,
  one lock acquisition
- **G9:** route auth middleware not specified — fixed: dedicated `eventsGroup` with
  `AuthMiddleware()`

### Round 3 — Third critique

Found F1–F7. All validated:
- **F1 (CRITICAL):** snapshot and heartbeat goroutines would write directly to `c.Writer`
  concurrently with the live loop. `gin.ResponseWriter` has no internal mutex. Race
  detector in CI would catch this. **Fix:** single writer invariant — heartbeat and
  snapshot send into `s.ch`, live loop is sole `c.Writer` writer after live loop start
- **F2 (CRITICAL):** subscriber channel carries no sequence ID; live loop could not emit
  `id:` fields. **Fix:** `event_id uint64` on `WorkspaceSSEEvent`, set by `PublishToUser`
- **F3 (HIGH):** ring-wrap gap was silent. **Fix:** `Replay()` returns `gapDetected bool`;
  `resync` prepended when `lastID > 0 && lastID < oldestBufferedID`
- **F4 (MEDIUM):** snapshot could emit `{phase:""}` for workspace deleted mid-snapshot.
  **Fix:** filter `phase == ""` in snapshot loop
- **F5 (LOW):** `Last-Event-ID: 0` on first connect triggers unnecessary 128-event replay.
  **Fix:** `lastEventID` ref initialised to `null`, header only sent when non-null
- **F6 (LOW):** `shard.mu` held during `rb.append` — originally proposed releasing before
  append. **Later corrected** (see round 5)
- **F7 (LOW):** empty snapshot at startup — acknowledged, 10s poll covers it

### Round 4 — Fourth critique (goroutine boundary ambiguity + unsubscribe)

- **Finding 1:** snapshot ordering section was ambiguous about which goroutine does the
  k8s list. Implementer could serialise the list in the handler goroutine (blocking live
  events for 5s). **Fix:** ordering section rewritten with explicit `Handler goroutine`
  and `Snapshot goroutine` labelled blocks with Go-style `go func()` calls
- **Finding 2:** `defer broker.UnsubscribeUser(userID, s)` was not specified in S28.3.
  Without it, stale subscriber entries accumulate in `shard.userSubs` and count toward
  the 20-connection limit on every reconnect. **Fix:** added as `**required**` step
  immediately after `SubscribeUser` in both design section and S28.3 spec

### Round 5 — Fifth critique (`flusher.Flush()` and F6 lock scope)

- **Finding 1 (CRITICAL):** `flusher.Flush()` was completely absent from the design doc
  (zero occurrences). Without flush, SSE events sit in the HTTP buffer. Without flush
  after replay writes specifically, replayed events are not delivered until the live
  loop's first write. **Fix:** full flush discipline specified: acquire `flusher` at
  entry, flush after headers, after each replay write, after each live loop write
- **Finding 2 (write deadline):** also added `http.ResponseController.SetWriteDeadline`
  extended after each successful flush. Dead client holds goroutine and subscriber slot
  until TCP keepalive (~2.5 min). With 30s write deadline, slot released within 30s.
  Validated: Go 1.25 supports `http.ResponseController`; gin's `Unwrap()` chain reaches
  `net.Conn`
- **Finding 3 (F6 corrected):** original F6 fix (release `shard.mu` before `rb.append`)
  creates a window for out-of-order ring entries if `PublishToUser` is ever called
  concurrently for the same user. **Corrected fix:** hold `shard.mu` through both ID
  assignment AND `appendLocked()`; release only before fan-out. `replayBuffer` methods
  split into `appendLocked` (no own mutex) and `sinceLocked`

### Round 6 — Sixth critique (single writer invariant stated incorrectly)

The invariant was stated as "all `c.Writer` writes happen in the live loop" which
contradicts replay writing directly to `c.Writer` before the live loop starts. **Fix:**
invariant restated accurately: "at any point in time, exactly one goroutine writes to
`c.Writer` — the handler goroutine during replay, the live loop goroutine after that."

### Round 7 — Seventh critique (send-to-closed-channel panic)

**Finding (CRITICAL):** `s.send()` panics if `s.ch` is closed. Two trigger paths:

- **Path A:** `PublishToUser` copies subscriber targets under `shard.mu`, releases lock,
  fans out via `s.send()`. `UnsubscribeUser` can close `s.ch` between the release and
  the fan-out. Watcher goroutine triggers this on every phase transition that races with
  a client disconnect.
- **Path B:** Heartbeat goroutine's `select` can fire the ticker case at the same instant
  `streamCtx` is cancelled. It calls `s.send()`. `defer UnsubscribeUser` closes `s.ch`
  concurrently.

Both paths exist in the **current codebase** too. The new design adds more trigger paths
without fixing the root cause.

**Fix:** `recover()` in `s.send()`:
```go
func (s *subscriber) send(evt WorkspaceSSEEvent) {
    defer func() { recover() }()
    // existing missedEvent + channel send logic
}
```
Standard Go pattern (used in Kubernetes watch infrastructure). Scoped to channel-send
only. Discard-on-dead-subscriber is correct behaviour.

### Round 8 — Eighth critique (slow client flush blocking)

`flusher.Flush()` is already addressed by the write deadline (round 5). The write
deadline was added to fix exactly this: goroutine blocked on flush to a dead/slow client
releasing its subscriber slot within 30s. No new finding.

### Round 9 — Final critique

Thirteen assumptions validated. **No new functional issues found.** Design confirmed
sound.

---

## Validated non-issues (explicitly checked and cleared)

| Item | Check | Result |
|------|-------|--------|
| Replay concurrent with PublishToUser | New event goes to ring + s.ch; client gets replay then live event | Correct, no duplication |
| `event_id` copy semantics | Local copy modified under lock, stored in ring + channel | Correct |
| CleanupUser + reconnect | Fresh snapshot covers current state | Acceptable |
| `resync` workspace_id check | Frontend checks `type=="resync"`, not `workspace_id` | Correct |
| `currentEventID` from SubscribeUser | Discarded with `_`; unused on first connect | Correct |
| uint64 event_id overflow | ~584 billion years at 1 event/s | Not practical |
| Write deadline vs first snapshot | Snapshot arrives ~5s after connect; well within 30s | Safe |
| Heartbeat loop after recover() | Live loop detects closed s.ch, exits, cancels streamCtx | Cannot loop indefinitely |
| `_heartbeat` sentinel convention | No external event type starts with `_` | Clear convention |
| FM6 (EnsureWatching) | Fires on write ops too, not just browser connect | Invalidated |
| C4 (phaseEventCh) | All onPhaseChange ops < 10µs, no I/O | Over-engineering, dropped |

---

## Final design state

**Stories:** S28.1 (broker, 4pts) → S28.2 (watcher, 2pts) → S28.4 (publish, 1pt) →
S28.3 (user stream endpoint, 3pts) → S28.5 (session stream rename, 1pt) →
S28.6 (frontend hook, 2pts) → S28.7 (ChatPage cleanup, 1pt) → S28.8 (tests, 5pts)

**Total:** ~19 points

**Design file:** `design/stories/epic-28-unified-event-stream/README.md`
