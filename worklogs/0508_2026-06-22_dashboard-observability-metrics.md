# Worklog 0508: Wire dashboard observability metrics (DB / Redis / dependency / auth)

**Date:** 2026-06-22
**Session:** Diagnose and fix ~10 Grafana dashboard panels showing "No data" by wiring previously-unused `Record*` helpers at the architecturally correct layer.
**Status:** PR #356 open; addressing reviewer feedback.

---

## Objective

The operational dashboard showed `No data` for: Dependency Status, DB Connection Pool Utilization, DB Query Latency p95 + Errors, Redis Latency p95 + Errors, Auth Failure Ratio + Reasons, and Agent Reload Failure Ratio.

Live verification (`up{job=~".*llmsafespace.*"}=1`, scraping healthy) confirmed the issue was at the application instrumentation layer, not Prometheus or service discovery.

The metrics were defined in code (`api/internal/services/metrics/metrics.go`) but the corresponding `Record*` helpers had **zero callers** outside of test files. Counters/histograms only emit a labeled time-series after first observation, so the metrics never appeared in `/metrics`.

---

## Work Completed

### DB tracing — `api/internal/services/database/tracer.go`

New pgx v5 `QueryTracer` attached to **both** the primary `*sql.DB` pool (via `stdlib.OpenDB(connConfig)` with `connConfig.Tracer`) and the secrets `*pgxpool.Pool` (via `pgxpool.NewWithConfig` + `cfg.ConnConfig.Tracer = database.NewQueryTracer()`). Every query in the binary flows through one tracer.

- `classifyOperation` strips leading whitespace, line comments, and block comments so pgx query-tag annotations don't poison the bucket label.
- `classifyAfterWith` (CTE handling) **tracks parenthesis depth** and returns the first DML keyword observed at depth 0 outside the CTE list. This correctly handles `WITH … INSERT … SELECT` (CreateUser), `WITH RECURSIVE … DELETE` (DeleteSessionTree), and `WITH … UPDATE`. Earlier first-keyword-wins / last-keyword-wins heuristics misclassified production queries as `"select"`.
- Tokenisation treats any whitespace (space/tab/newline) as a word boundary — earlier space-only matching missed multi-line queries entirely.
- `classifyError` buckets driver/server errors into `connection / timeout / constraint / deadlock / syntax / other`.
- `pgx.ErrNoRows` is **not** counted as an error (control-flow signal).

### Redis instrumentation — `api/internal/services/cache/metrics_hook.go`

New go-redis v8 `redis.Hook` attached to both the primary cache client (`cache.New`) and the DEK cache client (`app.go:dekCacheClient`). Pipeline-aware. `redis.Nil` is **not** counted as an error. Command names normalised to lowercase single-token to keep cardinality bounded.

### Health checker — `api/internal/services/health/checker.go`

Periodic dependency probe goroutine started in `App.Run()` and stopped in `App.Shutdown()`. Pings each registered dependency every 15s (configurable, 2s ping timeout) and refreshes `llmsafespaces_db_pool_*` gauges so the Connection Pool Utilization panel doesn't freeze on the boot-time snapshot. `Stop()` is idempotent via `sync.Once`. Nil dependencies are filtered.

### Auth attempts — `auth.go`, `middleware/auth.go`

`metrics.RecordAuthAttempt(method, result)` instrumented at every Login exit path (success, db_error, user_not_found, wrong_password, account_suspended, account_inactive, email_not_verified, token_generation_failure, **lockout_blocked**) and at the JWT/API-key middleware (success, missing_token, invalid_token). Method labels: `password / jwt / apikey / missing`. New helper `authMethodForToken` classifies tokens by prefix.

### `RecordAgentReload`

Already wired correctly at `agent_reload.go:179`. The empty panel was a Category B (no traffic), not missing instrumentation. No code change needed.

---

## TDD

Tests written before implementation:

- **`tracer_test.go`** — 8 cases including `pgx.ErrNoRows` carve-out, error-type classification, comment stripping, CTE folding (with the production `CreateUser` regression test).
- **`metrics_hook_test.go`** — 4 cases including `redis.Nil` carve-out, pipeline observation.
- **`checker_test.go`** — 4 cases including idempotent `Stop`, healthy/unhealthy reporting, pool refresh, nil-dependency safety.
- **`auth_attempts_metric_test.go`** — 4 cases (success, wrong password, user-not-found, lockout-blocked).
- **`auth_method_test.go`** — 4 cases (empty/apikey/jwt/fallback).

All `./api/...`, `./controller/...`, `./pkg/...` test suites pass.

---

## Reviewer Feedback Addressed

PR #356 received a `REQUEST CHANGES` review with three items:

1. **CTE classifier bug** — Fix described as "use last-position keyword" was a partial answer. Production `CreateUser` query is `INSERT INTO … SELECT $1, $2, …` so SELECT trails INSERT — last-position would still classify as `"select"`. Correct fix: track parenthesis depth and return the first keyword at depth 0 outside the CTE bodies. Added regression test for the actual production query.
2. **Missing `authMethodForToken` tests** — Added `auth_method_test.go` with all four classification branches.
3. **Missing worklog** — This file (worklog 0508).

Also fixed reviewer's secondary findings: the lockout early-exit path now calls `RecordAuthAttempt("password", "failure")` so the failure-ratio denominator is complete when lockout is enabled; the 4096-byte truncation for very long CTEs is documented in the function comment.

---

## Files Changed

```
new file: api/internal/middleware/auth_method_test.go
new file: api/internal/services/auth/auth_attempts_metric_test.go
new file: api/internal/services/cache/metrics_hook.go
new file: api/internal/services/cache/metrics_hook_test.go
new file: api/internal/services/database/tracer.go
new file: api/internal/services/database/tracer_test.go
new file: api/internal/services/health/checker.go
new file: api/internal/services/health/checker_test.go
modified: api/internal/app/app.go
modified: api/internal/middleware/auth.go
modified: api/internal/services/auth/auth.go
modified: api/internal/services/cache/cache.go
modified: api/internal/services/database/database.go
modified: api/internal/services/metrics/metrics.go
```
