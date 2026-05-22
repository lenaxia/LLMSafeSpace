# US-3.2: Add Session Proxy Routes

**Epic:** 3 - Proxy and Sessions
**Priority:** Critical
**Depends on:** US-3.1

## User Story

As a developer, I want proxy routes registered in the API router, so that external callers can reach opencode session endpoints.

## Acceptance Criteria

- [ ] All proxy endpoints registered in `router.go`
- [ ] Auth middleware applied to all proxy routes
- [ ] Sandbox ownership check before proxying
- [ ] `go build ./api/...` succeeds

## Technical Details

**Note:** The API uses **Gin**. Route params use `:param` syntax (not `{param}`).

**Edit:** `api/internal/server/router.go`

Add route group (Gin syntax):

```go
proxy := v1.Group("/sandboxes/:id")
proxy.Use(authMiddleware, ownershipCheck)
{
    proxy.POST("/sessions", proxyHandler.CreateSession)
    proxy.GET("/sessions", proxyHandler.ListSessions)
    proxy.POST("/sessions/:sessionId/message", proxyHandler.SendMessage)
    proxy.POST("/sessions/:sessionId/prompt", proxyHandler.SendPromptAsync)
    proxy.GET("/sessions/:sessionId/message", proxyHandler.GetHistory)
    proxy.POST("/sessions/:sessionId/abort", proxyHandler.AbortSession)
    proxy.GET("/events", proxyHandler.StreamEvents)
}
```

### Ownership check

Ownership is verified by reading the Sandbox CRD's `metadata.labels["user-id"]` label (set by `buildCRDFromRequest` in sandbox service). The check compares this label against the authenticated user ID from the auth middleware. This avoids an extra DB lookup — the CRD is already being read for the pod IP.

```go
func ownershipCheck(services interfaces.Services, proxyHandler *ProxyHandler) gin.HandlerFunc {
    return func(c *gin.Context) {
        userID := services.GetAuth().GetUserID(c)
        sandboxID := c.Param("id")
        // Read Sandbox CRD, check labels["user-id"] == userID
        // 403 if mismatch
    }
}
```

### App wiring

Update `api/internal/app/app.go` to:
1. Create `ProxyHandler` (needs k8sClient, logger, services)
2. Start/stop proxy handler lifecycle in `App.Run()` / `App.Shutdown()`
3. Pass proxy handler to router setup

The existing `app.go` creates the router inline (separate from `server/router.go`). Both need to be reconciled: either move all routing to `server/router.go` and call it from `app.go`, or add proxy routes to the inline router in `app.go`. The preferred approach is to consolidate: `app.go` creates the proxy handler, passes it to `server.NewRouter()` which registers all routes.

## Design Reference

Section 7.4, 11.1

## Effort

Small (2-3 hours)
