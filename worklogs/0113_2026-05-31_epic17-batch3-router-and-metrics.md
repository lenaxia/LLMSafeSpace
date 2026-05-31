# 0113 — Epic 17 batch 3: F1.1.1, F1.1.3, F1.1.4, F1.4.3

**Date:** 2026-05-31
**Status:** Code-level fix complete; awaiting CI build + live re-pentest

---

## Summary

| ID | Title | Severity | Fix |
|---|---|---|---|
| F1.1.1 | `/readyz` leaks driver error strings | Medium | Sanitized component status |
| F1.1.3 | API `/metrics` unauthenticated | Medium | Optional Bearer token via env |
| F1.1.4 | dead `/api/v1/workspaces/:id/stream` group | Low | Removed dead code |
| F1.4.3 | Controller `/metrics` unauthenticated | Medium | Loopback default bind |

(F1.1.5 + F1.1.6 + RT-2.17 covered by global rate-limit-on-by-default
in worklog 0112.)

---

## Changes

### F1.1.1 — readyz sanitization

`api/internal/server/router.go::/readyz`: pre-fix returned
`failures: ["database: connection refused dial tcp 10.0.0.5:5432:..."]`,
leaking host + port + driver internals. Now returns
`failures: ["database: unreachable"]` and logs the detailed error
server-side via `logger.Warn`.

### F1.1.3 — API metrics token

`api/internal/server/router.go::/metrics`: when env var
`LLMSAFESPACE_METRICS_TOKEN` is set, requires
`Authorization: Bearer <token>`. Empty env = unauthenticated (legacy
behavior, opt-in to auth).

### F1.1.4 — dead route group removed

`/api/v1/workspaces/:id/stream` was created with middleware but
never had a handler. Removed entirely.

### F1.4.3 — Controller metrics loopback

`charts/llmsafespace/values.yaml::controller.metricsAddr`:
default flipped from `:8080` → `127.0.0.1:8080`. Operators wanting
Prometheus scrape should run a kube-rbac-proxy sidecar that
forwards to loopback.

---

## Tests

`go test ./api/... ./charts/llmsafespace/...` all green.

---

## Tracker update

`MASTER-TRACKER.md`:
- F1.1.1, F1.1.3, F1.1.4, F1.4.3 → MINE / live-pending
- F1.1.5, F1.1.6 → resolved as duplicate-of (RT-2.4 + global rate-limit)
