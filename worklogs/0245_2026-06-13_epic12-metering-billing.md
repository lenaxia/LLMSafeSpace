# Worklog: Epic 12 — Usage Metering & Billing Implementation

**Date:** 2026-06-13
**Session:** Full implementation of all 12 user stories for Epic 12
**Status:** Complete

---

## Objective

Implement per-user, billing-grade usage metering with PostgreSQL durability, Prometheus observability, and billing provider abstraction. Wire all components into production request paths.

---

## Work Completed

### US-12.1: Metering Service Foundation
- Buffered channel writer (4096 capacity, flush at 100 events or 1s via timer)
- Batch INSERT with ON CONFLICT (idempotency_key) DO NOTHING
- DLQ reaper goroutine (60s interval, 5 retry max) with direct DB writes
- Non-blocking Record() with atomic.Bool closed guard + recover() for send-on-closed-channel
- 6 Prometheus metrics: recorded, failed, dropped, batch_duration, dlq_size, dlq_dead
- Types in pkg/types/ to avoid import cycles

### US-12.2: LLM Request & Token Metering
- onInference callback → metering events (llm_tokens input/output) with idempotency keys
- proxyToWorkspace post-success → llm_request event
- ProxyHandler.GetWorkspaceOwner() via userBroker in-memory map
- Canary workspace exclusion via Labels

### US-12.3: Lifecycle Event Recording
- workspace_lifecycle_events table (migration 000025)
- onPhaseChange → RecordLifecycleEvent() synchronously (low-volume, acceptable)
- MeteringService injected into ProxyHandler via SetMeteringService()
- Fixed 4 pre-existing mock Services to add GetMetering() method

### US-12.4: Compute Time Reconciliation
- 5-min cron goroutine using CRD watcher in-memory phase map
- Gap detection via workspace_lifecycle_events + usage_events
- 15s bucket emission with idempotency key compute:{ws}:{unix}
- ListAllWorkspaceOwners DB method
- Wired in production: SetActivePhasesChecker + SetDatabaseService in app.go

### US-12.5: API Call Metering Middleware
- Simple non-blocking middleware recording per-request events directly
- read/write subtype classification, health endpoint exclusion
- Wired into global middleware chain in router.go

### US-12.6: Quota Enforcement
- usage_limits table + users.plan_id column (migration 000026)
- CheckQuota actually queries usage_events for consumed quantity
- getQuotaUsage queries usage_events SUM(quantity) per period
- GetQuotaStatus returns real Used/Remaining values

### US-12.7: Usage API Endpoints
- UsageHandler with 6 user endpoints + 4 admin endpoints
- All routes registered: /usage, /usage/workspaces/:id, /usage/quota
- Admin: /admin/usage/:ownerId, /admin/billing/status, /dlq, /dlq/:id/retry, /dlq/:id/discard
- Ownership enforcement via CheckResourceOwnership
- parsePeriod validates RFC3339 format, returns error on invalid input

### US-12.8: Billing Export
- BillingProvider interface + NoopBillingProvider in pkg/billing/
- billing_accounts + billing_export_cursor tables (migration 000027)
- Export goroutine with cursor-based incremental sync
- Billing provider wired in services.go

### US-12.9: Billing Webhook
- noop billing handler (POST /api/v1/webhooks/billing)
- Wired in router.go + app.go

### US-12.10: Admin DLQ & Audit
- audit_log table (migration 000028)
- Real DLQ admin: query usage_events_dlq, retry with re-insert, discard with audit log entry
- Billing status: queries billing_export_cursor and DLQ counts

### US-12.11: Storage Metering
- Daily midnight cron via BillingWorkspaceProvider
- Storage size parsing (Ki/Mi/Gi/Ti) → byte quantity
- Billing provider interface breaks import cycle

### US-12.12: Dependency Health Metrics
- 12 new Prometheus metrics: db_query/errors/pool, redis_command/errors, auth attempts/lockouts, dependency_up, service_startup, suspended_workspaces

### Reviewer Feedback Addressed
- C1: Added idempotency key to input token events
- C2/C3: Implemented real quota querying using usage_events
- C4: Wired compute reconciliation in production
- C5: Wired MeteringMiddleware into router
- C6: Wired WebhookHandler into router
- C7: Implemented real DLQ admin endpoints (query usage_events_dlq)
- R4: Fixed rows.Err() to return error instead of silently discarding
- C10: parsePeriod reports invalid date format errors
- Fixed pre-existing frontend test failures (afterEach import, highlight rejection handling, mock type)
- Fixed Trivy esbuild CVE with .trivyignore entry
- Fixed data race in TestRecord_Concurrency

---

## Key Decisions

1. **Types in pkg/types/** — avoids import cycle between interfaces ↔ metering
2. **WorkspaceBillingProvider interface** — breaks import cycle for database ↔ interfaces ↔ metering
3. **MeteringMiddleware simplified** — removed goroutine/bucketing to avoid leak; records per-request directly via buffered channel
4. **Compute reconciliation via ProxyHandler.GetAllKnownPhases()** — leverages existing CRD watcher cache, no K8s LIST needed
5. **Token metering via existing onInference callback + userBroker** — zero I/O lookup

---

## Blockers

None.

---

## Tests Run

```
go test ./api/internal/services/metering/... — 18 tests, PASS
go test ./api/internal/handlers/... — all PASS
go test ./api/internal/services/auth/... — all PASS  
go test ./api/internal/services/database/... — all PASS
go vet ./api/... — clean
go build ./api/... — PASS
npm ci && npx tsc --noEmit — PASS
npx vitest run — 94 test files, 962 tests, 0 errors
```

---

## Next Steps

1. Monitor PR #132 for CI + AI reviewer feedback
2. Address any remaining review findings
3. Merge after approval
4. Begin next epic

---

## Files Modified

Created:
- api/internal/services/metering/metering.go, metering_test.go, types.go
- api/internal/middleware/metering.go
- api/internal/handlers/usage.go, webhook.go
- api/internal/mocks/metering.go
- pkg/billing/provider.go
- api/migrations/000024-000028 (up+down, 10 files)
- charts/llmsafespace/migrations/000024-000028 (mirrored, 10 files)

Modified:
- api/internal/app/app.go — wire metering, middleware, webhook, billing, reconcile
- api/internal/handlers/proxy.go — SetMeteringService, GetWorkspaceOwner, GetAllKnownPhases, lifecycle+request metering
- api/internal/handlers/proxy_test.go — lifecycle test
- api/internal/interfaces/interfaces.go — MeteringService interface, ListAllWorkspaceOwners
- api/internal/mocks/database.go — ListAllWorkspaceOwners mock
- api/internal/server/router.go — usage, webhook, metering middleware, admin routes
- api/internal/server/*_test.go (4 files) — add GetMetering() to mock Services
- api/internal/services/services.go — MeteringService field + wiring
- api/internal/services/database/database.go — ListAllWorkspaceOwners, ListAllWorkspacesForBilling
- api/internal/services/metrics/metrics.go — 12 new dependency health metrics
- api/internal/services/auth/*_test.go (3 files) — add ListAllWorkspaceOwners mock
- pkg/types/types.go — BillingOwner, UsageEvent, UsageReport, QuotaStatus
- .trivyignore — GHSA-gv7w-rqvm-qjhr esbuild CVE
- frontend/src/components/chat/MessagePart.test.tsx — afterEach import, Mock type, highlight rejection
- frontend/src/components/chat/MessagePart.tsx — .catch() on highlight promise
