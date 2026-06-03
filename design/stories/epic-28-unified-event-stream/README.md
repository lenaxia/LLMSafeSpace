# Epic 28: Unified User-Scoped Event Stream

**Status:** Design — ready for implementation
**Created:** 2026-06-03
**Priority:** High
**Depends on:** Epic 2 (workspaces), Epic 18 (S18.11 readyz decoupling)

---

## Problem Statement

The current SSE architecture has a fundamental scope mismatch: the SSE stream is
workspace-scoped (`GET /api/v1/workspaces/:id/events`), but the browser navigates
between workspaces. When a user is viewing workspace A, all phase events for workspace
B are published to zero subscribers and permanently lost. The user must click on
workspace B to discover it has become Active.

This was measured and documented in worklog 0132:

- `98c53aec` reached `Active` at backend ~T+38s
- User was viewing `6d36952e` at that time
- User had to click on `98c53aec` to discover it was ready (noted explicitly in the
  console log at T+43.720s: "98c53aec didnt update until I clicked on it. thats not great")

The root cause: SSE is connected per-workspace, not per-user. Phase transitions for
background workspaces have no delivery path to the frontend.

The short-term fix (S18.10/S18.11 work) connected SSE unconditionally for the current
workspace. That fixed the spinner gap for the active workspace but left the sidebar
staleness problem for background workspaces untouched.

---

## Solution: User-Scoped Unified Event Stream

Replace the per-workspace SSE endpoint with a single per-user SSE endpoint:

```
GET /api/v1/events                            ← user-scoped, authenticated, lifecycle events
GET /api/v1/workspaces/:id/session-events     ← workspace-scoped, in-session events only
```

The user stream delivers `workspace.phase` events for ALL of the user's workspaces
regardless of which one is currently viewed. The workspace stream delivers high-frequency
in-session events (agent messages, session status, agent.question/permission) only while
the user is actively on that workspace page.

**Hard cutover.** No backward compatibility required — nothing is in production.

---

## Design

### Event routing split

| Event type | Stream | Reason |
|---|---|---|
| `workspace.phase` | User stream only | Must be visible regardless of which workspace is in view |
| `session.status` | Workspace stream only | High-frequency, only relevant while viewing that workspace |
| `opencode.event` | Workspace stream only | High-frequency streaming content for active session |
| `agent.question` | Workspace stream only | Requires user interaction on that workspace |
| `agent.permission` | Workspace stream only | Requires user interaction on that workspace |
| `agent.question.resolved` | Workspace stream only | Paired with question |
| `agent.permission.resolved` | Workspace stream only | Paired with permission |
| `resync` | User stream only | Full-resync recovery signal; always `workspace_id=""` (see FM5) |

### Wire format

`WorkspaceSSEEvent` gains two new fields:

- **`workspace_id string`** — required on all events; allows user stream to route events
  to the correct workspace cache on the frontend.
- **`event_id uint64`** — set by `PublishToUser` before delivering to the subscriber
  channel. The live loop emits `id: N\n` when `event_id != 0`. Heartbeat sentinels and
  snapshot events have `event_id = 0` and are written without an `id:` line. (F2 fix)

```json
{"event_id":42,"workspace_id":"6d36952e-...","type":"workspace.phase","phase":"Active"}
```

The `session_id`, `status`, `event_type`, and `data` fields remain unchanged.

### SSE response format

```
id: 42
data: {"event_id":42,"workspace_id":"6d36952e-...","type":"workspace.phase","phase":"Active"}

:

id: 43
data: {"event_id":43,"workspace_id":"98c53aec-...","type":"workspace.phase","phase":"Suspended"}

```

`id:` fields are per-user monotonically increasing uint64. On reconnect the browser
sends `Last-Event-ID: 42`; the server replays buffered events since that ID before
resuming live delivery. Heartbeat lines (`:\n\n`) have no `id:` field — they are SSE
comments, not events.

### Single writer invariant, flush discipline, and write deadline (F1 fix)

**The handler has two writing phases with a hard boundary at live loop start:**

- **Before live loop starts (replay phase):** The handler goroutine writes replay
  events directly to `c.Writer`. No other goroutine exists yet. No concurrent write
  risk. `flusher.Flush()` is called after each replay write so the client receives
  replayed events immediately rather than waiting for the HTTP buffer to fill.

- **After live loop starts (live phase):** The live loop goroutine is the **sole**
  writer to `c.Writer`. All other goroutines (heartbeat, snapshot) communicate with
  the live loop by sending into the subscriber channel `s.ch`, not by writing to
  `c.Writer` directly. `gin.ResponseWriter` has no internal mutex; concurrent writes
  from multiple goroutines are a data race caught by the race detector in CI.

The invariant is therefore: **at any point in time, exactly one goroutine writes to
`c.Writer`** — the handler goroutine during replay, the live loop goroutine after that.

This preserves the same pattern as `emitPendingInputRequests`, which publishes to the
broker channel and never writes to `c.Writer` directly.

**`flusher` acquisition, flush discipline, and write deadline:**

Every SSE write must be flushed immediately or clients will not receive events until
the HTTP response buffer fills. Additionally, without a per-write deadline, a goroutine
blocked in `flusher.Flush()` will hold a subscriber slot (counting toward the
20-connection limit) until TCP keepalive detects the dead peer — approximately 2.5
minutes on Linux with Go's default keepalive settings. A user whose wifi drops and
reconnects within that window may find all 20 connection slots occupied by zombies and
receive 429 on every reconnect until the slots clear.

The handler must:

1. Acquire `flusher` at handler entry: `flusher, ok := c.Writer.(http.Flusher)`. Return
   `500` if the writer does not implement `http.Flusher`.
2. Acquire write controller: `rc := http.NewResponseController(c.Writer)`.
   `http.ResponseController.SetWriteDeadline` works with gin via gin's `Unwrap()` chain
   to the underlying `net.Conn`. Available in Go 1.20+; this project uses Go 1.25.
3. Call `flusher.Flush()` once after writing the initial SSE response headers.
4. In every write path (replay, live loop, heartbeat): write, then flush, then extend
   the deadline: `rc.SetWriteDeadline(time.Now().Add(30 * time.Second))`. A 30-second
   window means a dead client is detected and the goroutine released within 30s of the
   last successful write, instead of waiting for TCP keepalive (~2.5 minutes).
5. On write or deadline error: call `streamCancel()`, return.

Without step 4, replayed events sit in the HTTP buffer and are not delivered to the
client until the live loop's first write triggers a flush. Without the deadline
extension, dead clients accumulate as zombies and exhaust the per-user connection limit.

Concrete live loop mechanics:

- **Heartbeat sentinel** (`Type == "_heartbeat"`): write `:\n\n`; flush; extend deadline.
- **Snapshot event** (`event_id == 0`): marshal; write `data: ...\n\n`; flush; extend deadline; no `id:` line.
- **Live event** (`event_id > 0`): marshal; write `id: N\ndata: ...\n\n`; flush; extend deadline.
- **Write or deadline error**: call `streamCancel()`, return.

### Router group and authentication

`GET /api/v1/events` is registered in a dedicated authenticated group:

```go
eventsGroup := router.Group("/api/v1")
eventsGroup.Use(services.GetAuth().AuthMiddleware())
eventsGroup.GET("/events", proxyHandler.StreamUserEvents)
```

`c.Get("userID")` is guaranteed non-empty within this group because `AuthMiddleware`
sets it at `auth.go:109` and rejects unauthenticated requests before the handler runs.
The handler asserts this as a defensive check and returns `401` if somehow empty. (G9
fix)

### Rate limiter exemption

Both long-lived SSE endpoints are exempt from per-request rate limiting. `router.go`
`ExemptPaths` must include both suffixes:

```go
rlCfg.ExemptPaths = []string{"/events", "/session-events"}
```

This covers `/api/v1/events` (ends in `/events`) and
`/api/v1/workspaces/:id/session-events` (ends in `/session-events`). The middleware
uses `strings.HasSuffix` — no collision between these two patterns. (G1 fix)

### Initial snapshot ordering (FM4)

Subscribe before snapshot to eliminate the "missed Active during snapshot" race.
The handler goroutine and snapshot goroutine have distinct responsibilities — this
is explicit below to prevent a serialisation misread that would delay live events
by up to 5 seconds.

**Handler goroutine (sequential):**
```
1. s, _, err := broker.SubscribeUser(userID)  — atomic; events from this point land in s.ch
   defer broker.UnsubscribeUser(userID, s)    — always clean up subscriber on exit
2. emit replay events directly to c.Writer + flusher.Flush() per event (pre-live-loop)
3. go snapshotGoroutine(streamCtx, s, userID) — spawned; runs concurrently with live loop
4. go heartbeatGoroutine(streamCtx, s)        — spawned; runs concurrently with live loop
5. enter live loop (sole c.Writer writer from this point; drains s.ch)
```

**Snapshot goroutine (concurrent with live loop):**
```
a. k8s list filtered by user-id label, 5s timeout derived from streamCtx
   → on failure: s.send(resync); return
b. watcher.GetAllKnownPhases() — single RLock, full map copy
c. for each workspace ID from list:
     phase := phases[id]
     if phase == "" { continue }   — deleted between list and map read (F4 fix)
     s.send(WorkspaceSSEEvent{Type:"workspace.phase", WorkspaceID:id, Phase:phase, EventID:0})
```

The snapshot goroutine runs concurrently with the live loop. Live events that arrive
during the k8s list queue in `s.ch` and are delivered by the live loop without delay.
Snapshot events arrive in `s.ch` after the list completes and are interleaved with
live events; the frontend deduplicates by cache invalidation (receiving the same phase
twice is harmless — both trigger a re-fetch that returns current state).

`defer broker.UnsubscribeUser(userID, s)` in step 1 is required: it closes `s.ch` when
the handler exits, causing any goroutines that write to `s.ch` to discard their sends
(non-blocking) and exit via `streamCtx.Done()`. Omitting this leaves stale subscriber
entries in `shard.userSubs[userID]` that count toward the 20-connection limit and
accumulate without bound on repeated reconnects. (Finding 2 fix)

Step 5 filters out workspaces whose phase is empty string in `GetAllKnownPhases()`.
This handles the race where a workspace is deleted between the k8s list (step a) and the
phase map read (step b): `CleanupWorkspace` removes the entry, `GetAllKnownPhases()`
returns `""` for it, and the snapshot correctly skips it rather than emitting a
malformed `{phase:""}` event. (F4 fix)

`GetAllKnownPhases()` is a new watcher method that acquires `knownPhasesMu.RLock()` once
and returns a full map copy — O(N) with a single lock acquisition, not N separate ones.
(G8 fix)

**Startup race:** if the watcher has not yet completed `seedResourceVersion` when the
first user connects (typically < 1s window), `GetAllKnownPhases()` returns an empty map
and the snapshot emits nothing. No `resync` is emitted for this case — the empty
snapshot is valid. The 10s `refetchInterval` belt-and-suspenders on `useWorkspaceStatus`
covers this transient window. (F7 — acknowledged, no code change)

### Heartbeat and write-error termination (FM1, G6)

Both the user stream and workspace session stream use a shared cancellable context:

```go
streamCtx, streamCancel := context.WithCancel(c.Request.Context())
defer streamCancel()
```

The live loop calls `streamCancel()` on any write error. The heartbeat goroutine selects
on `streamCtx.Done()` between ticks. Because the heartbeat sends into `s.ch` (not
directly to `c.Writer`), write errors are detected by the live loop, which cancels the
shared context, causing the heartbeat goroutine to exit on its next select. No goroutine
outlives the connection. (F1, G6)

### Replay and ring-wrap gap detection (FM2, F3)

The per-user ring buffer holds 128 events. On reconnect with `Last-Event-ID: N`:

```
oldestID = nextID - count   (ID of oldest buffered event)

if N == 0:
    no Last-Event-ID sent on first connect (F5 fix) — emit fresh snapshot, no replay
if N > 0 and N < oldestID:
    gap: events (N..oldestID-1) are unrecoverable
    prepend resync event, then replay all 128 buffered events (F3 fix)
if N >= oldestID:
    no gap: replay events with ID > N
```

The `Replay(userID, lastID)` method returns `([]replayEntry, gapDetected bool)`. When
`gapDetected == true`, the handler emits `resync` first, then the buffered events. This
makes ring-wrap gaps explicit and handled rather than silent. (F3 fix)

### `Last-Event-ID` header — first connect vs reconnect (F5 fix)

The frontend uses `fetch()`, not the browser's built-in `EventSource`. The browser does
not automatically send `Last-Event-ID`. The `useUserEventStream` hook manages it
explicitly:

- `lastEventID` ref is initialised to `null`, not `0`.
- On **first connect**: no `Last-Event-ID` header is sent. Server delivers fresh
  snapshot + live events. No replay is triggered.
- On **reconnect** (after at least one event received): `Last-Event-ID: N` header is
  sent where N is the `event_id` from the last received data event. Server replays
  missed events per the algorithm above.

This avoids triggering a 128-event replay on every fresh tab open, which would add
latency and is unnecessary since the snapshot covers current state.

### `PublishToUser` lock scope (F6 fix)

`PublishToUser` must hold `shard.mu` through both the ID assignment **and** the
`rb.append()` call, then release before fanning out to subscribers:

```go
// Under shard.mu: assign ID, append to replay buffer, copy targets
shard.mu.Lock()
if shard.replay[userID] == nil {
    shard.replay[userID] = newReplayBuffer()
}
evt.EventID = shard.replay[userID].nextID
shard.replay[userID].nextID++
shard.replay[userID].appendLocked(evt) // appends under the already-held shard.mu
targets := copyTargets(shard.userSubs[userID])
shard.mu.Unlock()

// Outside shard.mu: fan out to all subscribers (the expensive part)
for _, s := range targets {
    s.send(evt)
}
```

`rb` does not need its own mutex when `appendLocked` is only ever called under
`shard.mu`. The `replayBuffer` ring buffer methods split into:
- `appendLocked(evt)` — assumes caller holds the containing shard's mutex; no internal lock.
- `sinceLocked(lastID)` — for `Replay`, called while holding `shard.mu` long enough
  to copy the slice, then released before returning.

This is correct because:
1. ID assignment and ring append are atomic under `shard.mu` — no out-of-order entries
   possible even under concurrent `PublishToUser` calls for the same user shard.
2. The fan-out (`s.send`) happens outside `shard.mu`, eliminating the primary source of
   lock contention: calling `s.send()` per subscriber while holding the shard lock.

**Why the previous version (release before rb.append) was wrong:** assigning
`id = nextID; nextID++` under `shard.mu`, releasing, then calling `rb.append(id)` outside
creates a window where two goroutines can have IDs 5 and 6, both release the lock, and
goroutine B's `rb.append(6)` runs before goroutine A's `rb.append(5)`. The ring buffer
would contain out-of-order entries; `Replay(since=4)` would return `[6, 5]` instead of
`[5, 6]`. This is not triggered in the current architecture (the watcher goroutine
serialises all `PublishToUser` calls for phase events), but it is a latent bug that
would break if `PublishToUser` were ever called concurrently for the same user.

---

## Failure Modes and Critiques Addressed

Each item was validated against the actual codebase. Items marked ✗ were
invalidated during review.

### FM1 — Proxy idle timeout kills quiet SSE connections (HIGH)

**Validated:** nginx ingress default `proxy_read_timeout 60s`, AWS ALB 60s, GCP GCLB
30s, Azure App Gateway ~4min. Design must be portable across all k8s deployment
topologies.

**Fix:** Both user stream and workspace session stream emit `:\n\n` heartbeats every 25s
(under the 30s GCLB floor). Heartbeat sends into `s.ch` (single-writer invariant —
F1 fix). Heartbeat goroutine exits when `streamCtx` is cancelled (G6 fix).

### FM2 — No `Last-Event-ID` / no replay on reconnect (MEDIUM)

**Validated:** No `id:` field emitted anywhere in current codebase. Frontend sends no
`Last-Event-ID` header.

**Fix:**
- `event_id uint64` field on `WorkspaceSSEEvent`; set by `PublishToUser` (F2 fix).
- Live loop emits `id: N\n` when `event_id != 0`.
- Per-user ring buffer of 128 events.
- Frontend sends `Last-Event-ID: N` only when `lastEventID !== null` (F5 fix).
- Ring-wrap gap detected; `resync` prepended when gap unrecoverable (F3 fix).

### FM3 — `knownPhases` not seeded; first phase event silently dropped after restart (HIGH)

**Validated:** `seedResourceVersion()` stores only `list.ResourceVersion`. Stable
workspaces never appear in the Watch after seeding; their first transition is swallowed
(`existed=false`).

**Fix:** `seedResourceVersion` iterates `list.Items`: populates `knownPhases` and calls
`broker.RecordWorkspaceOwner` for every workspace.

### FM4 — No initial snapshot on connect (HIGH)

**Validated:** `StreamEvents` emits nothing phase-related on connect.

**Fix:** See "Initial snapshot ordering" section. Subscribe → replay → snapshot goroutine
→ live loop. Snapshot events go into `s.ch` (single-writer invariant — F1 fix). Empty
phase filtered out before sending (F4 fix).

### FM5 — Buffer overflow is silent (LOW-MEDIUM)

**Validated:** `brokerChannelBuffer = 16`. Silent drop at >5 concurrent transitions.

**Fix:** Buffer raised to 128. `subscriber.send()` sets `missedEvent` flag on overflow;
next successful send prepends `resync` (empty `workspace_id`). `resync` is always a
full resync — the flag does not track which workspace was dropped.

### FM6 — `EnsureWatching` only on browser connect ✗ INVALIDATED

`EnsureWatching` fires on `StreamEvents` (line 311) AND on write ops (line 411).
Original claim wrong. No fix needed.

### FM7 — User ID not available in session-event publishers (LOW)

**Validated:** Session callbacks only have `workspaceID`.

**Fix:** Broker `wsOwner` map (`workspaceID → userID`) populated at startup via
`seedResourceVersion` and on every phase event via `RecordWorkspaceOwner`.

### FM8 — No per-user subscriber limit (LOW)

**Validated:** No limit in `WorkspaceEventBroker`.

**Fix:** `SubscribeUser` returns `ErrTooManySubscribers` at ≥20 connections → `429`.

### FM9 — Reconnect recovery is workspace-scoped (HIGH)

**Validated:** `handleSSEReconnect` invalidates only `["workspace-status", workspaceId]`.

**Fix:** `useUserEventStream.onReconnect` invalidates `["workspaces"]` and all
`["workspace-status"]` entries. Combined with `Last-Event-ID` replay and `resync`.

### C1 — Single global broker mutex (LOW)

**Validated:** One global mutex.

**Fix:** 16 shards, FNV-32 keying, bitwise-AND modulo. Each shard has its own mutex,
subscriber maps, owner map, replay buffer map.

### C2 — Goroutines run without context (MEDIUM)

**Validated:** `emitPendingInputRequests` launched without request context.

**Fix:** All handler-spawned goroutines receive `streamCtx`. Heartbeat selects on
`streamCtx.Done()`. Snapshot uses `streamCtx` for k8s list timeout and selects between
writes. `emitPendingInputRequests` accepts a `context.Context` parameter.

Note: because heartbeat and snapshot send into `s.ch` (not `c.Writer`), they do not
need to check write errors themselves — the live loop detects write errors and calls
`streamCancel()`, which causes both goroutines to exit via `streamCtx.Done()`. (F1, G6)

### C3 — No wire format version field (LOW)

**Decision:** Deferred to next breaking wire format change. Tracked as design debt.

### C4 — `onPhaseChange` in watcher goroutine ✗ OVER-ENGINEERING, DROPPED

All operations in `onPhaseChange` are non-blocking or hold locks for microseconds. No
I/O, no network calls. Async channel would add latency for no benefit. Dropped.

### C5 — `knownPhases` leaks deleted workspaces (MEDIUM)

**Validated:** No `watch.Deleted` case in `handleEvent`.

**Fix:** `handleEvent` handles `watch.Deleted`: removes from `knownPhases`, calls
`broker.CleanupWorkspace`.

### G1 — `/session-events` not rate-limit exempt (MEDIUM)

**Validated:** `HasSuffix` matching. Fix: `ExemptPaths = []string{"/events", "/session-events"}`.

### G2 — C4 phaseEventCh over-engineering ✓ Resolved — C4 dropped

### G3 — `resync` cannot be workspace-specific ✓ Resolved — always full resync

### G4 — Acceptance criterion contradicts S28.4 ✓ Resolved — criterion corrected below

### G5 — Snapshot k8s list timeout unspecified (HIGH)

**Fix:** 5s timeout derived from `streamCtx`. On failure: send `resync` into `s.ch`,
skip snapshot, continue with live stream.

### G6 — Heartbeat doesn't detect write errors (MEDIUM)

**Fix:** Heartbeat sends into `s.ch` (not `c.Writer`). Live loop detects write errors
and calls `streamCancel()`. Heartbeat exits via `streamCtx.Done()`. No separate write-
error check needed in heartbeat goroutine.

### G7 — Replay buffer ID=0 edge case ✗ WITHDRAWN

Correctly handled. No issue.

### G8 — N separate RLock calls for snapshot (LOW)

**Fix:** `GetAllKnownPhases() map[string]string` — one RLock, full map copy.

### G9 — Route auth middleware not specified (HIGH)

**Fix:** Dedicated `eventsGroup` with `AuthMiddleware()`. Defensive 401 check in handler.

### F1 — Snapshot and heartbeat goroutines race on `c.Writer` (CRITICAL)

**Validated:** `gin.ResponseWriter` has no internal mutex (confirmed from
`gin@v1.10.0/response_writer.go`). The existing `emitPendingInputRequests` avoids this
by publishing to the broker channel — the live loop is the sole `c.Writer` writer. The
proposed design would break this invariant by having snapshot and heartbeat write
directly to `c.Writer` from separate goroutines. The race detector in CI would catch
this.

**Fix:** Two-phase write discipline — handler goroutine writes during replay (before live
loop starts), live loop is sole writer after that. Heartbeat and snapshot goroutines send
into `s.ch` only; never write to `c.Writer`. Full specification in "Single writer
invariant and flush discipline" section above, including `flusher.Flush()` after every
write path.

### F2 — Subscriber channel carries no sequence ID (CRITICAL)

**Validated:** `WorkspaceSSEEvent` has no ID field. `PublishToUser` assigns an ID and
stores it in the replay buffer, but the event delivered to `s.ch` carried no ID. The
live loop had no way to emit `id: N\n`.

**Fix:** `event_id uint64` field added to `WorkspaceSSEEvent`. Set by `PublishToUser`
before sending to `s.ch`. Live loop emits `id:` only when `event_id != 0`. Heartbeat
sentinels and snapshot events have `event_id = 0`.

### F3 — Ring-wrap gap is silent (HIGH)

**Validated:** `replayBuffer.since(lastID)` returns all entries with `id > lastID`.
When the ring has wrapped (>128 events since `lastID`), entries between `lastID` and
`oldestBufferedID` are permanently gone. The client receives a partial replay with no
indication.

**Fix:** `Replay()` returns `([]replayEntry, gapDetected bool)`. Gap detected when
`lastID > 0 && lastID < oldestID`. Handler prepends a `resync` event before buffered
entries when gap is detected. Full algorithm in "Replay and ring-wrap gap detection"
section.

### F4 — Snapshot emits empty `phase` for deleted workspace (MEDIUM)

**Validated:** Race: workspace deleted between k8s list (step 3) and `GetAllKnownPhases`
(step 4). `CleanupWorkspace` removes entry; `GetAllKnownPhases` returns `""`. Without
filtering, snapshot would emit `{type:"workspace.phase", phase:""}`.

**Fix:** Snapshot loop filters: `if phase == "" { continue }`. One-line guard in S28.3.

### F5 — `Last-Event-ID: 0` sent on first connect triggers unnecessary replay (LOW)

**Validated:** `fetch()` doesn't send `Last-Event-ID` automatically. If the frontend
initialises `lastEventID = 0` and sends it on first connect, `Replay(userID, 0)`
returns all 128 buffered events — unnecessary latency on first connect.

**Fix:** `lastEventID` ref initialised to `null`. Header only sent when `lastEventID !==
null`. On first connect: no replay, fresh snapshot instead.

### F6 — `shard.mu` held during `rb.append` unnecessarily (LOW)

**Validated:** `PublishToUser` holds `shard.mu` while calling `rb.append()` which
acquires `rb.mu`. No deadlock (order is always `shard.mu → rb.mu`), but `shard.mu` is
held longer than necessary on the hot publish path. The expensive part — fanning out to
all subscribers via `s.send()` — happens while the shard lock is held.

**Fix:** Hold `shard.mu` through ID assignment and `rb.appendLocked()`, then release
before the fan-out. The `replayBuffer` ring buffer does not need its own mutex when
`appendLocked` is always called under the containing shard's lock. Full specification
in "`PublishToUser` lock scope" section above.

**Previous version was incorrect:** The prior text said to release `shard.mu` before
`rb.append()`. This created a window for out-of-order ring entries under concurrent
`PublishToUser` calls — ID assignment and ring append must be atomic. The current
architecture (watcher serialises phase events) does not trigger this bug, but it is a
latent correctness issue. The corrected fix moves only the fan-out outside `shard.mu`.

### FP1 — `s.send()` panics on send-to-closed channel (CRITICAL)

**Validated:** In Go, sending to a closed channel panics, including inside a non-blocking
`select { case s.ch <- evt: ... default: ... }`. `s.ch` is closed by `UnsubscribeUser`
(and `UnsubscribeWorkspace`). Two concurrent paths can send to `s.ch` after it is closed:

**Path A — `PublishToUser` vs `UnsubscribeUser`:**
`PublishToUser` copies subscriber targets under `shard.mu`, then releases the lock
before fanning out via `s.send()`. `UnsubscribeUser` removes `s` from `shard.userSubs`
and closes `s.ch` under `shard.mu`. If `PublishToUser` copies `s` into `targets`,
releases `shard.mu`, `UnsubscribeUser` then closes `s.ch`, and `PublishToUser` then
calls `s.send(evt)` — that is a send to a closed channel. This race fires from the
watcher goroutine on every phase transition that coincides with a client disconnect.

**Path B — heartbeat goroutine vs `UnsubscribeUser`:**
The heartbeat goroutine's `select` can fire the ticker case at the same instant
`streamCtx` is cancelled. It calls `s.send(_heartbeat)`. Concurrently, the live loop's
`defer UnsubscribeUser` closes `s.ch`. Same panic.

Both paths exist in the current codebase too (the existing `broker.Publish` +
`broker.Unsubscribe` have the same race). The new design adds more trigger paths
(heartbeat goroutine, snapshot goroutine, `PublishToUser`) without fixing the root cause.

**Fix:** Add `recover()` to `s.send()`:

```go
func (s *subscriber) send(evt WorkspaceSSEEvent) {
    defer func() { recover() }()
    if s.missedEvent.Load() {
        resync := WorkspaceSSEEvent{Type: "resync"}
        select {
        case s.ch <- resync:
            s.missedEvent.Store(false)
        default:
            return
        }
    }
    select {
    case s.ch <- evt:
    default:
        s.missedEvent.Store(true)
    }
}
```

The `recover()` is correctly scoped: the only panic possible inside `s.send()` is
send-to-closed-channel. Recovering it means discarding the event, which is correct
(the subscriber is dead). This is the standard Go pattern for safe sends on
potentially-closed channels, used in Kubernetes' watch infrastructure and similar
production systems. No WaitGroup or additional ordering constraint is needed — the
recover handles all three sender paths (heartbeat, snapshot, PublishToUser) uniformly.

### F7 — Empty snapshot if watcher not yet seeded at startup (LOW, acknowledged)

**Validated:** `watcher.Start()` seeds `knownPhases` asynchronously. A user connecting
within the first ~1s of API server startup gets an empty snapshot (no events emitted).
No `resync` is issued — this is a valid degraded mode, not an error.

**No code change.** The 10s `refetchInterval` belt-and-suspenders on `useWorkspaceStatus`
covers this transient window. Acknowledged in design; noted in Design Debt.

---

## Implementation Plan

### S28.1 — Broker redesign (backend, ~4 points)

Rewrite `event_broker.go`:
- `event_id uint64` field on `WorkspaceSSEEvent` (F2)
- `_heartbeat` sentinel type constant for heartbeat events (F1)
- Sharded map structure, 16 buckets, FNV-32 keying (C1)
- `subscriber` struct with `missedEvent` flag; `send()` emits `resync` on overflow;
  `send()` uses `recover()` to safely discard sends to a closed channel (FP1 fix)
- Buffer raised to 128 (FM5)
- User-scoped `SubscribeUser` / `UnsubscribeUser` / `PublishToUser` (core feature)
  - Under `shard.mu`: assigns `event_id`, calls `rb.appendLocked()`, copies targets
  - Outside `shard.mu`: fans out via `s.send()` — the expensive part (F6)
  - `replayBuffer` methods split into `appendLocked` (no internal lock, called under
    shard.mu) and `since` (called under shard.mu, returns copy) (F6)
- `replayBuffer` ring buffer, 128 entries
- `Replay(userID, lastEventID) ([]replayEntry, gapDetected bool)` — detects ring-wrap
  gap and returns flag (F3)
- `RecordWorkspaceOwner` / `WorkspaceOwner` / `CleanupWorkspace` / `CleanupUser` (FM7, C5)
- `SubscribeUser` returns `ErrTooManySubscribers` at ≥20 connections (FM8)
- Legacy `Subscribe` / `Unsubscribe` / `Publish` shims for existing callers

### S28.2 — Watcher fixes (backend, ~2 points)

Update `crd_watcher.go`:
- `seedResourceVersion` populates `knownPhases` AND calls `broker.RecordWorkspaceOwner`
  from `list.Items` (FM3, FM7)
- `handleEvent` handles `watch.Deleted`: removes from `knownPhases`, calls
  `broker.CleanupWorkspace` (C5)
- `GetAllKnownPhases() map[string]string`: one RLock, full map copy (G8)
- C4 NOT implemented

### S28.3 — User stream endpoint (backend, ~3 points)

Add `StreamUserEvents` handler; register in `eventsGroup` with auth middleware (G9):
- Returns `401` if `c.Get("userID")` empty
- Returns `429` on `ErrTooManySubscribers` (FM8)
- Acquire `flusher`: `flusher, ok := c.Writer.(http.Flusher)`; return `500` if not ok
- Acquire write controller: `rc := http.NewResponseController(c.Writer)`
- Write SSE headers (`text/event-stream`, `no-cache`, `X-Accel-Buffering: no`,
  `WriteHeader(200)`); `flusher.Flush()`; `rc.SetWriteDeadline(time.Now().Add(30*time.Second))`
- `streamCtx, streamCancel := context.WithCancel(c.Request.Context())`
- `s, _, err := broker.SubscribeUser(userID)`; return `429` on `ErrTooManySubscribers`
- `defer broker.UnsubscribeUser(userID, s)` — **required**; closes `s.ch` on exit,
  preventing stale subscriber accumulation on reconnect (Finding 2)
- Replay phase (handler goroutine, live loop not started yet — F1):
  - Read `Last-Event-ID` header; call `broker.Replay`
  - If `gapDetected`: write resync; `flusher.Flush()`; extend deadline (F3)
  - For each replayed entry: write `id: N\ndata: ...\n\n`; `flusher.Flush()`; extend deadline (FM2, F1)
  - On write/deadline error: `streamCancel()`; return
- Start snapshot goroutine (concurrent with live loop — Finding 1; sends into `s.ch`):
  - k8s list with 5s timeout derived from `streamCtx` (G5)
  - On failure: `s.send(resync)`; return
  - `GetAllKnownPhases()`, filter `phase==""` (F4)
  - For each workspace: `s.send(WorkspaceSSEEvent{..., EventID:0})` (F1, F2)
- Start heartbeat goroutine (concurrent with live loop; sends `_heartbeat` into `s.ch`
  every 25s; exits on `streamCtx.Done()`) (FM1, F1, C2)
- Enter live loop — **sole writer to `c.Writer` from this point** (F1):
  - Drain `s.ch`; on each event:
    - `type == "_heartbeat"`: write `:\n\n`; flush; extend deadline; continue
    - `event_id == 0`: marshal; write `data: ...\n\n`; flush; extend deadline; continue
    - `event_id > 0`: marshal; write `id: N\ndata: ...\n\n`; flush; extend deadline (FM2)
  - On any write or deadline error: `streamCancel()`; return (G6)

Update `router.go`:
- `eventsGroup` with `AuthMiddleware()` and `GET /events` (G9)
- `ExemptPaths = []string{"/events", "/session-events"}` (G1)

### S28.4 — Phase publish to user stream (backend, ~1 point)

Update `onPhaseChange`:
- `broker.RecordWorkspaceOwner(workspace.Name, workspace.Spec.Owner.UserID)` (FM7)
- `broker.PublishToUser(userID, WorkspaceSSEEvent{WorkspaceID: workspace.Name,
  Type: "workspace.phase", Phase: string(phase)})` — `PublishToUser` assigns `event_id`
- Remove `broker.Publish(workspace.Name, ...)` for `workspace.phase` (hard cutover)

### S28.5 — Workspace session stream (backend, ~1 point)

- Rename `StreamEvents` → `StreamSessionEvents`
- Route: `/workspaces/:id/events` → `/workspaces/:id/session-events`
- Acquire `flusher`; return `500` if not ok; acquire `rc := http.NewResponseController(c.Writer)`
- Write SSE headers; `flusher.Flush()`; `rc.SetWriteDeadline(time.Now().Add(30*time.Second))`
- Apply single-writer invariant and `streamCtx`/`streamCancel` pattern (F1, G6)
- Use `broker.SubscribeWorkspace(workspaceID)` (returns `*subscriber`) directly — not
  the legacy `Subscribe` shim which returns `chan WorkspaceSSEEvent` (F1: the new
  `*subscriber` is needed so the live loop can receive `_heartbeat` sentinels)
- `defer broker.UnsubscribeWorkspace(workspaceID, s)`
- Heartbeat goroutine: sends `_heartbeat` sentinel into `s.ch` every 25s (FM1, F1)
- Live loop: same write/flush/extend-deadline/cancel discipline as user stream (F1)
- `emitPendingInputRequests` accepts `context.Context` (C2)
- All published events carry `workspace_id` field
- `workspace.phase` not published here (removed in S28.4)

### S28.6 — Frontend: `useUserEventStream` hook (frontend, ~2 points)

New `frontend/src/hooks/useUserEventStream.ts`:
- Connects to `/api/v1/events` from root layout on mount
- `lastEventID` ref initialised to `null`; `Last-Event-ID` header only sent when
  `lastEventID !== null` (F5)
- Updates `lastEventID` from `event.event_id` on every received data event
- On `workspace.phase`: invalidates `["workspaces"]` and `["workspace-status", workspace_id]`
- On `resync`: invalidates `["workspaces"]` and ALL `["workspace-status"]` entries
- `onReconnect`: invalidates `["workspaces"]` and all `["workspace-status"]` (FM9)
- Logs `[ws-timing] user_stream.*` events

### S28.7 — Frontend: ChatPage workspace stream cleanup (frontend, ~1 point)

- SSE URL → `/workspaces/:id/session-events`
- Remove `workspace.phase` from `handleSSEEvent`
- Remove `["workspaces"]` invalidation from `handleSSEEvent`
- Keep all in-session event handlers unchanged
- Reduce `refetchInterval` to 10s on `useWorkspaceStatus`

### S28.8 — Tests (backend + frontend, ~5 points)

Backend (`event_broker_test.go`):
- Sharded subscribe/publish isolation (different shards don't interfere)
- User channel delivery and per-user isolation
- Replay buffer: `since()` correctness, ring-wrap, gap detection returns `gapDetected=true`
- `Replay` with `lastID=0` returns all events, `gapDetected=false`
- `Replay` with `lastID < oldestBufferedID` returns `gapDetected=true`
- `resync` on overflow — always empty `workspace_id`
- `PublishToUser` sets `event_id` on delivered event; event ID is monotonically increasing
- Concurrent `PublishToUser` for same user: ring entries are in monotonic ID order (validates F6 lock scope)
- `ErrTooManySubscribers` at ≥20 connections
- `CleanupWorkspace` / `CleanupUser`
- Concurrent subscribe+publish under race detector (`-race` flag)
- `s.send()` on a closed channel does not panic (simulate by closing channel then sending) (FP1)

Backend (`stream_events_test.go`):
- User stream: initial snapshot emitted before first live event
- Snapshot skipped and `resync` emitted when k8s list fails
- Snapshot filters out empty-phase workspaces (F4)
- Heartbeat `:\n\n` appears at correct interval with no `id:` line
- Replay events are flushed to client immediately (not held in buffer until next live event) (F1)
- Every live data line has `id: N\n` prefix; every snapshot/heartbeat line has no `id:` (F2)
- `Last-Event-ID: N` triggers replay; gap triggers `resync` before replay (F3)
- No `Last-Event-ID` header → no replay, fresh snapshot
- 429 on ≥20 connections
- `workspace.phase` NOT in session stream
- All events have `workspace_id`
- `streamCancel` fired on write error; goroutine count verified — no goroutine leak (F1)
- Write deadline extended after each successful flush; expired deadline fires `streamCancel` within deadline window (write deadline fix)
- Session stream: heartbeat present, `workspace_id` on all events, uses `SubscribeWorkspace` not legacy shim

Backend (`crd_watcher_test.go`):
- `seedResourceVersion` populates `knownPhases` for all list items
- `handleEvent(Deleted)` removes from `knownPhases` and calls `CleanupWorkspace`
- `GetAllKnownPhases` returns copy; mutation of returned map doesn't affect watcher state

Frontend (`useUserEventStream.test.tsx`):
- `Last-Event-ID` NOT sent on first connect
- `Last-Event-ID: N` sent on reconnect after receiving events
- `workspace.phase` invalidates correct caches
- `resync` triggers full invalidation
- `onReconnect` invalidates workspaces list

Note (G4): `TestStreamEvents_OnPhaseChange_PublishesToBroker` must be updated in S28.8.
All other existing `event_broker_test.go` tests pass via legacy shims.

---

## Acceptance Criteria

**Backend:**
- [ ] `GET /api/v1/events` returns `401` without valid auth; `200 text/event-stream` with valid auth
- [ ] `GET /api/v1/events` returns `429` when user has ≥20 open connections
- [ ] Every `data:` line preceded by `id: N\n` when `event_id > 0`; no `id:` on heartbeats or snapshot events
- [ ] Stream emits `:\n\n` heartbeat every 25s ± 5s
- [ ] Replay events are delivered to client immediately on reconnect (flushed after each write, not buffered)
- [ ] All handler goroutines terminate within 35s of client network failure (write deadline); subscriber slot released within that window
- [ ] All handler goroutines terminate within 1s of clean client disconnect (FIN received); no goroutine leaks under race detector
- [ ] On connect, stream emits one `workspace.phase` event per user workspace before live events; skips workspaces with empty phase
- [ ] If k8s list fails, stream emits `resync` and continues; connection is not dropped
- [ ] `Last-Event-ID: N` triggers replay of events with ID > N; if gap detected (ring wrapped), `resync` is emitted first
- [ ] `Last-Event-ID` header absent → no replay; fresh snapshot delivered
- [ ] Ring overflow for a subscriber emits `resync` (empty `workspace_id`) on next write; no silent drops
- [ ] All events carry `workspace_id` field and `event_id` field (0 for snapshot/heartbeat)
- [ ] `workspace.phase` appears on user stream; NOT on workspace session stream
- [ ] After API server restart, first phase transition per workspace fires `onPhaseChange`
- [ ] `watch.Deleted` removes workspace from `knownPhases` and broker ownership map
- [ ] `/api/v1/events` and `/workspaces/:id/session-events` are both exempt from rate limiting
- [ ] Concurrent `PublishToUser` calls for same user produce monotonically ordered ring buffer entries

**Frontend:**
- [ ] `useUserEventStream` connects to `/api/v1/events` from root layout on mount
- [ ] `Last-Event-ID` header NOT sent on first connect; sent with last seen `event_id` on reconnect
- [ ] `workspace.phase` event invalidates `["workspaces"]` and `["workspace-status", workspace_id]`
- [ ] `resync` invalidates `["workspaces"]` and ALL `["workspace-status"]` entries
- [ ] On reconnect, `["workspaces"]` and all `["workspace-status"]` entries are invalidated
- [ ] Sidebar phase indicator updates within 2s of backend transition without user clicking
- [ ] `ChatPage` connects to `/workspaces/:id/session-events`; does not handle `workspace.phase`

**Regression:**
- [ ] All existing `event_broker_test.go` tests pass via legacy shims
- [ ] `TestStreamEvents_OnPhaseChange_PublishesToBroker` updated in S28.8
- [ ] Resume spinner still works: `ui.workspace_ready` fires within 2s of `sse.workspace_phase Active` on user stream

---

## Open Questions (resolved)

| # | Question | Resolution |
|---|----------|------------|
| Q1 | `Last-Event-ID` replay for workspace session stream? | Deferred. `emitPendingInputRequests` handles reconnect for agent.question/permission. |
| Q2 | Wire format version field? | Deferred to next breaking change. |
| Q3 | Initial snapshot include session status? | Deferred to S28.3 investigation. |
| Q4 | How to enumerate user workspaces for snapshot? | Resolved: option (c) — k8s list with `user-id` label selector, 5s timeout, failure emits `resync`. |

---

## Implementation Order

```
S28.1 (broker: event_id field, sharding, replay gap detection, lock scope fix)
  └── S28.2 (watcher: seed knownPhases, Deleted handler, GetAllKnownPhases)
        └── S28.4 (publish workspace.phase to user stream; remove from workspace stream)
              └── S28.3 (user stream endpoint: single-writer, snapshot, heartbeat, replay)
                    └── S28.5 (session stream: rename, single-writer, heartbeat)
                          └── S28.6 (frontend useUserEventStream: null lastEventID, event_id)
                                └── S28.7 (ChatPage cleanup)
                                      └── S28.8 (tests)
```

---

## Design Debt

- **C3 (wire format version):** No `schema_version` field. Add at next breaking change.
- **Q3 (session status in snapshot):** Snapshot does not include session idle/busy state.
  A user connecting while a session is busy sees correct state only after the next
  `session.status` event or page reload.
- **F7 (startup empty snapshot):** A user connecting within ~1s of API server startup
  gets an empty snapshot. The 10s `refetchInterval` covers this transient window.
