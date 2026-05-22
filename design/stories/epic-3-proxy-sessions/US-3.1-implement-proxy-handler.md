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
- [ ] Per-sandbox connection limit (10 concurrent)
- [ ] 503 with Retry-After on persistent failure

## Technical Details

**Note:** The API uses **Gin** (`github.com/gin-gonic/gin`). All handler code uses `gin.Context`.

**WebSocket ↔ SSE bridge is deferred to V2.1.** SSE is sufficient for V1 browsers.

**New file:** `api/internal/handlers/proxy.go`

**ProxyHandler struct:**

```go
type ProxyHandler struct {
    k8sClient  kubernetes.KubernetesClient
    httpClient *http.Client
    logger     logger.Logger
    limits     map[string]int   // sandboxID → active connections
    mu         sync.Mutex
    pwCache    map[string]string // sandboxID → cached password (read from Secret once)
    pwCacheMu  sync.RWMutex
}
```

**Password caching:** The server password is generated at sandbox creation and never changes. The proxy reads it from the K8s Secret once, caches it by sandbox ID, and reuses it for all subsequent requests to that sandbox. Cache is invalidated when the sandbox transitions to Suspending/Suspended/Terminated phases (watch CRD status changes via informer event handler).

**Core method:**

```go
func (h *ProxyHandler) ProxyToSandbox(c *gin.Context) {
    // 1. Extract sandbox ID from URL param (c.Param("id"))
    // 2. Get pod IP from Sandbox CRD status (informer-cached)
    // 3. Get password from cache (or read from K8s Secret on first access)
    // 4. Build target: http://{podIP}:4096{path}
    // 5. Clone request, set Authorization: Basic header
    // 6. Proxy request (handle streaming responses and SSE)
    // 7. On connection error: refresh IP, retry once
}
```

**Endpoint mapping (verified against opencode source):**

| LLMSafeSpace | Opencode | Notes |
|---|---|---|
| `POST /api/v1/sandboxes/{id}/sessions` | `POST /session` | No trailing slash! |
| `GET /api/v1/sandboxes/{id}/sessions` | `GET /session` | |
| `POST .../sessions/{sid}/message` | `POST /session/{sid}/message` | HTTP streaming |
| `POST .../sessions/{sid}/prompt` | `POST /session/{sid}/prompt_async` | Async (204) |
| `GET .../sessions/{sid}/message` | `GET /session/{sid}/message` | |
| `POST .../sessions/{sid}/abort` | `POST /session/{sid}/abort` | |
| `GET .../events` | `GET /event` | SSE stream |

## Design Reference

Section 7.1a (Verified Contract), 7.3-7.4, 11.4

## Effort

Large (6-8 hours — the core of V2, simplified by removing WebSocket bridge)
