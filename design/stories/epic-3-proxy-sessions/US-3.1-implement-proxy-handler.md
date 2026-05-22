# US-3.1: Implement Proxy Handler

**Epic:** 3 - Proxy and Sessions
**Priority:** Critical
**Depends on:** US-2.4, US-1.1

## User Story

As a user, I want the API to transparently proxy my requests to the opencode server inside the sandbox, so that I can interact with the AI agent without knowing about pod IPs or authentication.

## Acceptance Criteria

- [ ] Proxy resolves sandbox ID → pod IP from CRD status
- [ ] Injects Basic Auth header with server password (cached per sandbox)
- [ ] Proxies all session endpoints (create, message, prompt, abort, history, events)
- [ ] HTTP streaming passes through transparently (Persona 1)
- [ ] SSE stream passes through transparently (events endpoint)
- [ ] Stale IP: retries once with fresh IP from CRD status
- [ ] Active session limit enforced: message/prompt to new sessions returns 429 with `Retry-After` when `maxActiveSessions` reached
- [ ] Read-only operations (GET history, GET sessions, GET events) bypass active session limit
- [ ] Hard connection ceiling of 10 concurrent proxy connections per sandbox
- [ ] 503 with Retry-After on persistent connection failure
- [ ] Active session tracking uses opencode `session.status` events (busy→active, idle→inactive)

## Technical Details

**Note:** The API uses **Gin** (`github.com/gin-gonic/gin`). All handler code uses `gin.Context`.

**WebSocket ↔ SSE bridge is deferred to V2.1.** SSE is sufficient for V1 browsers.

**New file:** `api/internal/handlers/proxy.go`

### Active vs Inactive Sessions

Sessions have two states from the proxy's perspective:

| State | Trigger | Allowed operations |
|-------|---------|-------------------|
| **Active** | User sends message/prompt to session, or agent reports `busy` | All operations |
| **Inactive** | Streaming response completes + agent reports `idle`, no open connections | Read-only (GET history, GET sessions) |

The proxy tracks which sessions are active per sandbox. When a write operation (message/prompt) targets a session that isn't already active and the active session count has reached `maxActiveSessions` (from the Workspace CRD's `spec.maxActiveSessions`, default 5), the proxy returns 429 with a `Retry-After` header.

There is no limit on total/inactive sessions — they're just JSON files on the PVC. Only concurrent active sessions are bounded.

### ProxyHandler struct

```go
type ProxyHandler struct {
    k8sClient  kubernetes.KubernetesClient
    httpClient *http.Client
    logger     logger.Logger

    // Password cache: sandboxID → server password
    pwCache    map[string]string
    pwCacheMu  sync.RWMutex

    // Active session tracking: sandboxID → set of active sessionIDs
    activeSess map[string]map[string]bool
    activeMu   sync.Mutex

    // Connection counting: sandboxID → in-flight connection count
    connCount  map[string]int
    connMu     sync.Mutex
}
```

### Two-tier rate limiting

1. **Active session limit** (from `workspace.spec.maxActiveSessions`, default 5):
   - Only write operations (message, prompt) to sessions not already in the active set are checked
   - If limit reached → 429 Too Many Requests with `Retry-After` header and body: `{"error": "active session limit reached", "maxActiveSessions": 5, "retryAfter": 10}`
   - Read operations (GET history, GET sessions, GET events) always pass through
   - A session already in the active set can always receive new messages (it's already counted)

2. **Hard connection ceiling** (10 concurrent connections per sandbox):
   - Transport-level limit on total in-flight HTTP connections
   - Prevents any single caller from overwhelming the opencode instance at the network level
   - Exceeded → 429 with `Retry-After`

Both limits are per-API-replica (not globally coordinated). For V1 this is acceptable.

### Active session lifecycle

```
User sends message to session X:
  1. Check: is session X already in activeSess[sandboxID]?
     - Yes → proxy the request, done
     - No → check: len(activeSess[sandboxID]) >= maxActiveSessions?
       - Yes → return 429 with Retry-After
       - No → add session X to activeSess[sandboxID], proxy the request
  2. When streaming response completes:
     - Do NOT remove from active set yet — agent may still be processing
     - Wait for session.status SSE event: "idle" for session X
     - On "idle": remove session X from activeSess[sandboxID]
```

The proxy subscribes to opencode's `GET /event` SSE stream for each sandbox that has active sessions. This provides the `session.status` events needed to track busy→idle transitions. If the SSE connection drops, all sessions for that sandbox remain in the active set until either:
- A new SSE connection is established and idle events are received
- A configurable timeout expires (default: 5 minutes) — sessions are cleared from active set
- The sandbox transitions to Suspending/Suspended — cache is invalidated

### Password caching

The server password is generated at sandbox creation and never changes. The proxy reads it from the K8s Secret once, caches it by sandbox ID, and reuses it for all subsequent requests to that sandbox. Cache is invalidated when the sandbox transitions to Suspending/Suspended/Terminated phases (watch CRD status changes via informer event handler).

### Core method

```go
func (h *ProxyHandler) ProxyToSandbox(c *gin.Context) {
    sandboxID := c.Param("id")
    // 1. Extract sandbox ID from URL param (c.Param("id"))
    // 2. Get pod IP from Sandbox CRD status (informer-cached)
    // 3. Get password from cache (or read from K8s Secret on first access)
    // 4. If write operation (message/prompt):
    //    a. Extract sessionID from URL
    //    b. Check active session limit (skip if session already active)
    //    c. If limit reached → 429 with Retry-After
    //    d. Add sessionID to active set
    // 5. Check hard connection ceiling → 429 if exceeded
    // 6. Build target: http://{podIP}:4096{path}
    // 7. Clone request, set Authorization: Basic header
    // 8. Proxy request (handle streaming responses and SSE)
    // 9. On connection error: refresh IP, retry once
    // 10. On streaming completion: keep session in active set until SSE idle event
}
```

### Endpoint mapping (verified against opencode source)

| LLMSafeSpace | Opencode | Method | Session check |
|---|---|---|---|
| `POST /api/v1/sandboxes/{id}/sessions` | `POST /session` | Create | None (creates new session) |
| `GET /api/v1/sandboxes/{id}/sessions` | `GET /session` | List | None (read-only) |
| `POST .../sessions/{sid}/message` | `POST /session/{sid}/message` | Send msg | **Active limit enforced** |
| `POST .../sessions/{sid}/prompt` | `POST /session/{sid}/prompt_async` | Send async | **Active limit enforced** |
| `GET .../sessions/{sid}/message` | `GET /session/{sid}/message` | History | None (read-only) |
| `POST .../sessions/{sid}/abort` | `POST /session/{sid}/abort` | Abort | None (ends processing) |
| `GET .../events` | `GET /event` | SSE stream | None (read-only) |

## Design Reference

Section 6.1 (Active vs Inactive Sessions), 7.1a (Verified Contract), 7.3-7.4, 7.12 (Rate Limiting), 11.4 (Proxy Handler)

## Effort

Large (8-10 hours — the core of V2, active session tracking adds complexity beyond simple proxy)
