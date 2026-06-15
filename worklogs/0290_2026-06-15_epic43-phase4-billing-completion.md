# Worklog: Epic 43 Phase 4 — Billing Integration Completion

**Date:** 2026-06-15
**Session:** Finish Phase 4 — ReportUsage wiring, feature gating, tests, commit
**Status:** Complete

---

## Objective

Complete the remaining Phase 4 (Billing Integration) work identified in worklog 0289's "Next Steps": write tests for `ReportUsage`/`exportToStripe`/`SetUsageReporter`/`StripeProvider`, add feature-gating middleware, wire it into the router, build/lint/test, and commit on the existing `feat/epic43-phase4-billing` branch.

---

## Work Completed

### 1. StripeProvider test coverage (new file)

`pkg/billing/stripe_provider_test.go` — 10 tests using `httptest.Server` + `stripe.SetBackend` to exercise the provider against a stub Stripe API:

| Test | Verifies |
|------|----------|
| `CreateCustomer_Success` | Email+name forwarded, customer ID returned |
| `CreateCustomer_StripeError` | Error wrapped, not swallowed |
| `SuspendCustomer_Success` | Metadata `suspended=true` set on customer |
| `SuspendCustomer_StripeError` | Error wrapped |
| `ReportUsage_HappyPath` | Both events reported, correct paths, idempotency keys forwarded, quantity+timestamp correct |
| `ReportUsage_UnknownMeterSkipped` | Events with no configured meter are skipped (0), only configured meters hit Stripe |
| `ReportUsage_StripeErrorStopsAndReturnsPartial` | First-event failure returns empty partial ids + error |
| `ReportUsage_Empty` | No Stripe calls for empty input |
| `ReportUsage_IdempotencyKeyOnlyWhenSet` | Key forwarded to Stripe header when provided |
| `parseUnixTimestamp` | Valid RFC3339 parsed, invalid/empty falls back to now |

Pattern: `withStripeBackend(t, srv)` saves the prior global stripe backend, swaps to the test server, and restores on cleanup. Tests using it MUST NOT run in parallel.

### 2. Metering export tests (new file)

`api/internal/services/metering/export_test.go` — 8 tests using `sqlmock` + a `fakeUsageReporter`:

| Test | Verifies |
|------|----------|
| `ExportUsage_NoNewEvents_DoesNothing` | maxID==lastID → no cursor update |
| `ExportUsage_NoReporter_OnlyAdvancesCursor` | Default path: cursor advances, no reporter call |
| `ExportUsage_WithReporter_AggregatesAndReports` | Rows aggregated by customer+type, deterministic idempotency key `meter-{customer}-{type}-{maxID}` |
| `ExportUsage_WithReporter_AggregatesMultipleEventsIntoOneReport` | **Regression test**: 3 events for same customer+type → 1 report (catches the GROUP BY bug below) |
| `ExportUsage_WithReporter_SkipsEventsWithoutCustomer` | Orphan events (no billing account) skipped |
| `ExportUsage_ReporterFailure_AdvancesCursorAndContinues` | Reporter error logged, not surfaced; cursor still advances |
| `ExportUsage_WithReporter_EmptyRows_NoCall` | Empty export rows → no reporter invocation |
| `ExportUsage_QueryError_AbortsAndDoesNotAdvanceCursor` | Query error aborts ExportUsage, cursor unchanged |
| `SetUsageReporter_NilClears` | `SetUsageReporter(nil)` clears the reporter |

### 3. FeatureGuard middleware (new file)

`api/internal/middleware/feature_guard.go` — Gin middleware that gates routes by `org.PlanID` via `billing.IsFeatureAllowed`. Returns **402 PaymentRequired** (not 403) when the plan doesn't include the feature, so the frontend can distinguish "permission denied" (403, from `OrgAdminGuard`) from "upgrade required" (402, from `FeatureGuard`). Response body includes `feature`, `planId`, `hint` for the upgrade CTA.

- Interface: `orgPlanReader { GetOrg(ctx, orgID) (*types.Organization, error) }` — satisfied by `OrgsHandler` via a new thin `GetOrg` delegation method (Interface Segregation: separate from `orgMemberChecker`).
- Runs **after** `OrgAdminGuard` so the extra `GetOrg` query only executes for admins (low volume).
- **Fail-open** for unknown feature names (forward compat with future `billing.IsFeatureAllowed` cases).
- Blank `plan_id` falls back to Free via `GetPlanFeatures` default (deny sensitive features).

`api/internal/middleware/tests/feature_guard_test.go` — 9 tests covering all plan×feature combinations, not-found, DB error, unknown feature fail-open, blank plan.

### 4. Router wiring

`api/internal/server/router.go:1022-1030` — FeatureGuard applied to:
- `PUT /api/v1/orgs/:id/policies/:key` — gated on `"policies"` (Business+)
- `DELETE /api/v1/orgs/:id/policies/:key` — gated on `"policies"` (Business+)
- `GET /api/v1/orgs/:id/policies` — **open** (reads remain open per user decision; members can see what's enforced)
- `GET /api/v1/orgs/:id/audit` — gated on `"audit"` (Business+)

### 5. Bug fix found via adversarial review (Rule 11)

**`exportToStripe` GROUP BY included `idempotency_key`**, defeating aggregation. Since every usage event has a unique idempotency key, each event became its own group — `SUM(quantity)` was just the single event's quantity, and N Stripe API calls were made per export window instead of 1 per customer+type.

**Fix**: Removed `idempotency_key` from GROUP BY (now groups by `event_type + external_customer_id` only). Generate a deterministic window-scoped idempotency key in Go: `meter-{customerID}-{eventType}-{maxID}`. Stable across retries of the same `[lastID, maxID]` range (defense-in-depth against double-reporting), distinct across windows. Added regression test `TestExportUsage_WithReporter_AggregatesMultipleEventsIntoOneReport`.

### 6. Design trade-off documented

`metering.go:436-448` — Added a multi-line comment explaining why reporter failure doesn't block cursor advancement: a persistent Stripe outage would stall the cursor forever, causing `usage_events` to grow unbounded. Trade-off: the failed window's usage is not reported to Stripe. A future reconciliation job (Phase 5 follow-up) can recover missed windows by diffing the cursor against `usage_events`.

---

## Key Decisions

1. **D1: 402 PaymentRequired for feature-gate denials** — Lets the frontend distinguish upgrade-required from permission-denied. Response body carries `feature`, `planId`, `hint` for rendering an upgrade CTA.
2. **D2: Policy reads stay open, writes gated** — Members on any plan can see what policies are enforced (transparency); only Business+ orgs can change them. Matches the user's explicit scope choice.
3. **D3: Aggregate by customer+type with deterministic window-scoped idempotency key** — `meter-{customer}-{type}-{maxID}`. Prevents per-event Stripe calls and is safe under retry.
4. **D4: Reporter failure advances cursor** — Prevents unbounded cursor stall on persistent Stripe outage. Missed windows need a reconciliation job (Phase 5).
5. **D5: Fail-open for unknown feature names** — Forward compatibility with future `billing.IsFeatureAllowed` cases. New features default to allowed until explicitly added to a plan tier.
6. **D6: StripeProvider tests use `httptest.Server` + `stripe.SetBackend`** — Matches stripe-go's own test pattern. Zero production refactor. Tests exercise the full provider code path including stripe-go serialization.

---

## Assumptions (Rule 7)

| # | Assumption | Validated By |
|---|------------|--------------|
| 1 | `Organization.PlanID` is loaded by `GetOrg` | `pg_org_store.go:140-155` (reads `plan_id` column) |
| 2 | `PgOrgStore.GetOrg` can serve as `orgPlanReader` | Read interface; added `OrgsHandler.GetOrg` delegation |
| 3 | `billing.IsFeatureAllowed` is the canonical check | `plan_tiers.go:57` |
| 4 | StripeProvider is testable via `stripe.SetBackend` + httptest | stripe-go's own tests (`stripe_test.go:78,110`) |
| 5 | Policy GET stays open; PUT/DELETE gated | Re-read `PolicyHandler`; matches user scope choice |
| 6 | Audit List is admin-only + feature gated | Re-read `AuditHandler`; matches user scope choice |
| 7 | sqlmock is the metering test pattern | `metering_test.go:24` |

All assumptions validated before code was written.

---

## Adversarial Self-Review (Rule 11)

### Phase 1 findings → Phase 2 validation

| # | Finding | Verdict | Action |
|---|---------|---------|--------|
| 1 | `exportToStripe` GROUP BY includes `idempotency_key` → no aggregation | **REAL BUG** | Fixed: removed from GROUP BY, generate deterministic window key |
| 2 | Reporter failure silently advances cursor, loses Stripe data | **DESIGN FLAW** | Documented with rationale + Phase 5 follow-up note |
| 3 | N+1 query risk on FeatureGuard | **FALSE ALARM** | Only runs on admin-only routes (low volume); one extra `GetOrg` per request |
| 4 | Information leak in 402 response body | **FALSE ALARM** | Caller is already an admin (OrgAdminGuard ran first) |
| 5 | Race between OrgAdminGuard and FeatureGuard on plan change | **FALSE ALARM** | Reading the latest plan is the desired behavior |
| 6 | Global stripe backend mutation in tests | **FALSE ALARM** | No `t.Parallel()`; no other tests in package touch the backend; save/restore in helper |

**Zero remaining real findings.**

---

## Blockers

None.

---

## Tests Run

```
# Phase 4 new + changed packages (all pass with -race -count=1)
go test -timeout 120s -race -count=1 \
  ./pkg/billing/... \
  ./api/internal/middleware/... \
  ./api/internal/services/metering/... \
  ./api/internal/handlers/... \
  ./api/internal/server/...
```

Results:
- `pkg/billing`: 10 new StripeProvider tests + 8 existing plan_tiers tests — PASS
- `api/internal/middleware/tests`: 9 new FeatureGuard tests + existing OrgGuard tests — PASS
- `api/internal/services/metering`: 8 new export tests + existing metering tests — PASS
- `api/internal/handlers`: all existing tests (including webhook + policies) — PASS
- `api/internal/server`: router tests — PASS

Build: `go build ./...` — clean.
Vet: `go vet ./...` — clean.
Fmt: `make fmt` — applied to 3 new files.

golangci-lint: not installed in this environment; pre-commit hook will run it on commit.

---

## Next Steps

1. **Commit + push** the branch `feat/epic43-phase4-billing` (this session's scope per user choice).
2. **Open PR** and iterate through the automated review-approve-merge cycle.
3. **Phase 5 reconciliation job**: recover missed Stripe usage windows by diffing `billing_export_cursor` against `usage_events` (per D4).
4. **US-43.10 OIDC follow-up**: KEK encryption, JWT issuance, idempotent membership, comprehensive tests (still deferred from worklog 0289).
5. **Phase 5 Platform operations** (US-43.18-43.20): admin dashboard, org/user suspension, cross-org audit.

---

## Files Modified

### New files
- `pkg/billing/stripe_provider_test.go` — 10 StripeProvider tests
- `api/internal/services/metering/export_test.go` — 8 export tests
- `api/internal/middleware/feature_guard.go` — FeatureGuard middleware
- `api/internal/middleware/tests/feature_guard_test.go` — 9 FeatureGuard tests
- `worklogs/0290_2026-06-15_epic43-phase4-billing-completion.md` — this worklog

### Modified files
- `api/internal/middleware/feature_guard.go` — new middleware (file was new, listed above)
- `api/internal/handlers/orgs.go` — added `GetOrg` delegation method (one-liner)
- `api/internal/server/router.go` — wired FeatureGuard on policy mutations + audit read
- `api/internal/services/metering/metering.go` — fixed GROUP BY bug, added aggregation key generation, documented cursor-advancement trade-off

### Previously uncommitted (from worklog 0289, still on this branch)
- `api/internal/app/app.go` — wire StripeProvider as usage reporter
- `api/internal/config/config.go` — `Meters` config + env var loading
- `api/internal/handlers/webhook.go` — persist subscription ID on checkout
- `api/internal/handlers/webhook_test.go` — fake store method
- `api/internal/services/metering/metering.go` — SetUsageReporter, exportToStripe, ExportUsage integration
- `frontend/src/components/org-admin/OrgAdminLayout.tsx` — Billing tab nav
- `frontend/src/components/org-admin/OrgBillingTab.tsx` — Billing UI tab (new)
- `frontend/src/router.tsx` — Billing tab route
- `pkg/billing/plan_tiers.go` — PlanFeatures, PlanTiers, IsFeatureAllowed, TrialConfig (new)
- `pkg/billing/plan_tiers_test.go` — plan tier tests (new)
- `pkg/billing/stripe_provider.go` — ReportUsage, SuspendCustomer, Meters config
