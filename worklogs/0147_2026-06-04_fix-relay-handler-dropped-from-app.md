# 0147 — Fix relay handler dropped from app.go

**Date:** 2026-06-04
**Status:** Complete

---

## What

The `relayHandler` and `relayFallbackHandler` instantiations (plus their
`RouterConfig` fields) were accidentally dropped when `eb1a7c4` squash-merged
model-selection code on top of Epic 26 (`98d157b`). As a result, every build
since `eb1a7c4` — including the current cluster image — silently omitted the
relay routes, causing all `GET /workspaces/:id/relay` and `POST /workspaces/:id/relay/fallback`
requests to return Gin's 404 "no route matched".

The regression was discovered during Epic 26 E2E validation when the relay
WebSocket upgrade returned 404 even on an Active workspace, despite the route
being correctly registered in `router.go`.

---

## Root cause

`eb1a7c4` ("feat(models): Add model selection code (missing from squash merge #20)")
added `modelsHandler` to `app.go` but was authored from a base that predated
`98d157b`, so the Epic 26 additions to `app.go` were not present in the diff
and were not included in the squash.

The `router.go` `RouterConfig` struct still had the `RelayHandler` /
`RelayFallbackHandler` fields (correct), but `app.go` never populated them,
so both `if cfg.RelayHandler != nil` guards in the router short-circuited and
the routes were never registered.

---

## Fix

Re-add to `api/internal/app/app.go`:

```go
// Epic 26: WebSocket relay for client-proxied inference (free-tier models).
relayHandler := handlers.NewRelayHandler(&k8sWorkspaceGetterAdapter{...})
relayFallbackHandler := handlers.NewRelayFallbackHandler()
```

And pass them into `RouterConfig`:

```go
RelayHandler:         relayHandler,
RelayFallbackHandler: relayFallbackHandler,
```

---

## Verification

`go build ./api/...` passes. E2E validation resumes once the new image is deployed.
