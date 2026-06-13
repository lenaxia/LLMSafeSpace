# Worklog: US-38.7 + US-38.8 — Remove Dead Code, Consolidate Dual Patterns

**Date:** 2026-06-13
**Session:** Epic 38 architectural remediation, stories 38.7 (dead code) and 38.8 (dual patterns)
**Status:** Complete

---

## Objective

Remove dead code accumulated across the codebase and consolidate two divergent implementation patterns into single, canonical helpers. Address reviewer findings from PR #146, including a critical 404→500 regression in admin credential deletion.

---

## Work Completed

### US-38.7: Remove Dead Code

Deleted unused packages and helpers with no live references:
- `api/internal/middleware/cors.go` + tests (replaced by central router config)
- `api/internal/middleware/request_id.go` + tests (header propagation handled in router)
- `controller/internal/common/constants.go`
- `controller/internal/common/leader_election.go`
- `pkg/utilities/strings.go`
- `proxy.go`: removed the unused `workspaceConfig.workspaceID` field (never assigned or read; `wsConfig` map is keyed by workspaceID so the field was redundant)

### US-38.8: Consolidate Dual Patterns

**Pattern 1 — Auth Extraction:** Migrated 10 `c.GetString("userID")` call sites to the shared `extractAuth()` helper and added explicit 401 guards at each site.

**Pattern 2 — Error Classification:** Introduced `ClassifyPostgresError` plus `ErrDuplicateCredential` / `ErrCredentialNotFound` sentinels, replacing fragile `isDuplicateErr` / `isNotFound` substring matchers across 4 call sites.

**Pattern 4 — Secret Injection:** Removed `prepareSecretsLegacy` and the nil-`deriveAdminKey` branch, enforcing the audit-tracked injection path as the only path.

### Review Fix: Critical 404→500 Regression

`ClassifyPostgresError` originally only handled `*pgconn.PgError`, but the production `PgSecretStore.DeleteAdminCredential` returns `pgx.ErrNoRows` for not-found deletes (pg_credential_store.go:250). Since `pgx.ErrNoRows` is not a `PgError`, `errors.As` failed and the raw error propagated, causing `AdminProviderCredentialsHandler.Delete` to return **500 instead of 404**.

Fix:
- Added an `errors.Is(err, pgx.ErrNoRows)` branch at the top of `ClassifyPostgresError`.
- Corrected the `fakeAdminCredStore.DeleteAdminCredential` test mock to return `pgx.ErrNoRows` (it previously returned `&pgconn.PgError{Code: "P0002"}`, masking the bug by exercising a code path production never hits).

### Review Fix: Unrelated Worklog Rename

The US-38.8 commit had renamed `0245_…_epic41-message-queue-reliability-implementation.md` → `0246_…`, colliding with the existing `0246_…_small-proxy-fixes`. Reverted the rename back to `0245`.

---

## Tests Added

- `errors_test.go`: 5 unit tests for `ClassifyPostgresError`
  - `pgx.ErrNoRows` → `ErrCredentialNotFound` (regression guard for the 404→500 bug)
  - `&pgconn.PgError{Code: "23505"}` → `ErrDuplicateCredential`
  - `&pgconn.PgError{Code: "P0002"}` → `ErrCredentialNotFound`
  - generic `errors.New(...)` → returned unchanged
  - `nil` → `nil`
- `user_provider_credentials_test.go`: 401 auth guard tests
  - Table-driven test covering all 7 endpoints (Create, List, Get, Delete, ListBindings, Bind, Unbind) → 401 when unauthenticated
  - `Create`-specific test verifying userID-without-sessionID is also rejected (Create uniquely requires both)

---

## Verification

- `go build ./...` — passes
- `go test ./api/internal/handlers/... -timeout 120s` — passes (all existing + 16 new assertions)
