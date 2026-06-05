# US-1.1: Fix API Compile Errors

**Epic:** 1 - Foundation
**Priority:** Critical
**Depends on:** US-0.1
**Blocks:** All other API stories

## User Story

As a developer, I want the API service to compile and start, so that I can build new features on a working foundation.

## Acceptance Criteria

- [ ] `go build ./api/...` succeeds with zero errors
- [ ] `go test ./api/...` passes (existing tests)
- [ ] API server starts and responds to `GET /health`

## Technical Details

**Note:** The API uses **Gin** (`github.com/gin-gonic/gin`), not Chi. All handler code uses `gin.Context`.

**Prerequisite:** US-0.1 must be completed first — the broken `pkg/types/zz_generated.deepcopy.go` blocks the entire monorepo build.

**Files to fix:**

| File | Issue | Fix |
|------|-------|-----|
| `api/internal/app/app.go:44` | `kubernetes.New(cfg, log)` — type mismatch | Pass `cfg.Kubernetes` not `cfg` |
| `api/internal/app/app.go:51` | `services.New(cfg, log, k8sClient)` — interface mismatch | Wrap `*kubernetes.Client` in interface adapter |
| `api/internal/app/app.go:69` | `h.RegisterRoutes(router)` — `h` undefined | Create handler instance, pass services |
| `api/internal/server/router.go:81` | `h.RegisterRoutes(router)` — `h` undefined | Receive handler via parameter or create in NewRouter |
| `api/internal/services/services.go:77` | `metrics.New()` — missing arg | Pass `log` |
| `api/internal/services/services.go:110` | `execution.New(log, k8sClient)` — missing arg | Add metrics arg |
| `pkg/logger/logger.go` | `With()` returns `*Logger` not `LoggerInterface` | Change return type to interface |
| `api/internal/server/router.go:74` | CacheService passed as RateLimiterService | Use separate rate limiter or add missing method |

## Design Reference

N/A — this is fixing existing code, not new design.

## Effort

Small (2-3 hours)
