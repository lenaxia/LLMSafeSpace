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

Also update `api/internal/app/app.go` to create ProxyHandler and pass to router.

## Design Reference

Section 7.4, 11.1

## Effort

Small (2-3 hours)
