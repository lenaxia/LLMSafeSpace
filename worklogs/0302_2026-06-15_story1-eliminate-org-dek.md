# Worklog: Story 1 — Eliminate Org DEK

**Date:** 2026-06-15
**Session:** Implement Story 1 from design 0031 — eliminate org DEK, commit, push, PR, iterate CI
**Status:** In Progress — PR #188 open, CI cycle 3 running after lint/idempotency fixes

---

## Objective

Implement Story 1 from `design/0031_2026-06-15_org-access-control-portal-architecture.md`: eliminate the org DEK entirely. Org credentials (LLM provider keys) move from per-org DEK encryption to server-side KEK (`deriveServerKey("org-credentials")`). This fixes a latent production bug where org credentials silently stop injecting after 24h without admin login.

---

## Work Completed

### Migration created
- `api/migrations/000035_drop_org_dek.up.sql` — drops `org_key_members` table, drops `pending_key_wrap` column, drops partial index
- `api/migrations/000035_drop_org_dek.down.sql` — restores schema (not data — warns that existing org credentials can't be re-encrypted)

### Production code changes (BUILDS CLEAN — `go build ./...` passes)

**Deleted files (5 files, ~1800 lines removed):**
- `pkg/secrets/org_key_service.go` — OrgKeyService (GenerateDEK, WrapDEK, UnlockDEK, RotateOrgDEK, etc.)
- `pkg/secrets/org_aware_key_service.go` — OrgAwareKeyService wrapper (decorator that added org DEK unlock to KeyService)
- `pkg/secrets/org_key_store.go` — OrgKeyStore interface + PgOrgKeyStore implementation (all org_key_members CRUD)
- `pkg/secrets/org_key_service_test.go` — 16 unit tests + mock store
- `pkg/secrets/org_lifecycle_integration_test.go` — full lifecycle integration tests

**Modified — credential injection path (the bug fix):**
- `pkg/secrets/injection.go` — `decryptBinding` now takes `adminKEK, orgKEK []byte` instead of using `orgKeySvc.GetOrgDEK()`. The upstream derivation loop derives both `"provider-credentials"` (admin) and `"org-credentials"` (org) keys via `s.deriveAdminKey(label)`. Org credentials now always decrypt — no cache dependency.
- `pkg/secrets/secret_service.go` — removed `orgKeySvc` field and `SetOrgKeyService` method

**Modified — auth/login path:**
- `pkg/secrets/key_service.go` — removed `UnlockAllOrgDEKs` stub method
- `api/internal/services/auth/auth.go` — removed `UnlockAllOrgDEKs` from `KeyServiceInterface`, removed the call from the login path

**Modified — handlers:**
- `api/internal/handlers/orgs.go` — removed `orgKeySvc` field from OrgsHandler, removed from constructor, deleted `AcceptKey` and `RotateKey` handlers, removed DEK/KEK derivation from `Create`, removed `pendingKeyWrap` from `AddMember`/`ChangeMemberRole`
- `api/internal/handlers/org_credentials.go` — replaced `orgKeySvc.GetOrgDEK` with `orgKeyDeriver("org-credentials")` in Create and Update handlers. Constructor now takes `AdminKeyDeriver` instead of `*OrgKeyService`
- `api/internal/handlers/secrets.go` — removed `orgKeyService` field, `SetOrgKeyService` method, and `RewrapAllOrgDEKsForAdmin` call from `ChangePassword`
- `api/internal/handlers/invitations.go` — removed `pendingKeyWrap` block from `Accept` handler

**Modified — database layer:**
- `api/internal/services/database/pg_org_store.go` — removed all `pending_key_wrap` from SQL queries (INSERT/SELECT/UPDATE), removed `org_key_members` DELETE statements, removed `SetPendingKeyWrap`/`SetPendingKeyWrapForOtherAdmins`/`DeleteOrgKeyMember` methods, removed `adminWrappedDEK` param from `CreateOrgWithAdmin`, removed `pendingKeyWrap` param from `AddOrgMember`, updated `IsOrgAdmin` to not check `pending_key_wrap = false`, updated `AcceptInvitationTx` to not set `pending_key_wrap`

**Modified — routing:**
- `api/internal/server/router.go` — removed `/accept-key` and `/rotate-key` org routes

**Modified — wiring:**
- `api/internal/app/app.go` — removed pgOrgKeyStore construction, removed orgKeyService construction, removed OrgAwareKeyService wrapper, wired base KeyService directly to auth, removed `secretService.SetOrgKeyService`, updated handler constructors

**Modified — types:**
- `pkg/types/types.go` — removed `PendingKeyWrap` from `OrgMember`, removed `UserPendingKeyWrap` from `OrgResponse`

**Modified — frontend:**
- `frontend/src/api/orgs.ts` — removed `acceptKey` and `rotateKey` API methods

---

## Test fixes completed

All 7 test files updated and passing:
1. **`api/internal/handlers/orgs_test.go`** — removed `PendingKeyWrap` field references, `secrets.NewPgOrgKeyStore`/`NewOrgKeyService`, updated `NewOrgsHandler` to 2-arg signature, removed `accept-key`/`rotate-key` routes, removed `SetPendingKeyWrap`/`DeleteOrgKeyMember` mock methods, removed `pendingKeyWrap` param from `AddOrgMember`, removed `keyMembers`/`pendingKeyWrap` maps from mock store, updated `CreateOrgWithAdmin` mock signature, renamed tests that tested deleted behavior
2. **`api/internal/handlers/org_create_billing_test.go`** — updated `NewOrgsHandler` call, removed unused `secrets` import
3. **`api/internal/handlers/invitations_test.go`** — removed `PendingKeyWrap` from mock and assertions
4. **`api/internal/services/auth/auth_e2e_all_test.go`** — removed `UnlockAllOrgDEKs` from `capturingKeyService`
5. **`api/internal/services/auth/auth_test.go`** — removed `UnlockAllOrgDEKs` from `fakeKeyService`
6. **`api/internal/services/auth/auth_sessionid_test.go`** — removed `UnlockAllOrgDEKs` from `trackingKeyService`
7. **`api/internal/services/auth/auth_apikey_dek_test.go`** — removed `UnlockAllOrgDEKs` from `dekJKeyService`

### CI fixes (3 iterations):
- **Iteration 1:** Chart migration mirror missing migration 035; migration 029 idempotency failure (CREATE INDEX on dropped `pending_key_wrap` column)
- **Iteration 2:** Chart mirror 029 content drift after idempotency fix; worklog 298 collision (pre-existing on main from parallel merges)
- **Iteration 3:** Synced migration 029 to chart mirror; renumbered worklog 0299→0300; migration 029 guarded with DO block for column existence check

---

## Key Decisions

1. **Separate HKDF labels for domain separation:** `"provider-credentials"` for admin credentials, `"org-credentials"` for org credentials. Per F4 in design review 0033.
2. **`AdminKeyDeriver` type reused:** The existing `func(label string) []byte` type (already used for admin credentials) serves org credentials with a different label. No new type needed.
3. **Base `KeyService` wired directly:** `OrgAwareKeyService` wrapper deleted. The auth service gets the base `KeyService` which implements `KeyServiceInterface` without the org DEK unlock decorator.

---

## Blockers

None. CI cycle 3 running after fixing lint (chart mirror sync, migration 029 idempotency guard, worklog renumber). Pre-existing worklog 0298 collision on main (two files from parallel merges) — not actionable from this branch.

---

## Tests Run

- `go build ./...` — **PASS**
- `go vet ./...` — **PASS**
- `go test -timeout 60s -race ./pkg/secrets/...` — **PASS** (29.9s)
- `go test -timeout 60s -race ./api/internal/handlers/...` — **PASS** (29.2s)
- `go test -timeout 60s -race ./api/internal/services/auth/...` — **PASS** (17.0s)
- `go test -timeout 60s ./api/internal/server/... ./api/internal/app/...` — **PASS**
- `gofmt` — clean
- CI cycle 1: Lint FAIL (chart mirror missing migration 035), Migration idempotency FAIL (migration 029 CREATE INDEX on dropped column), Frontend FAIL (downstream of Lint)
- CI cycle 2: Lint FAIL (chart mirror 029 content drift after idempotency fix), worklog 298 collision on main
- CI cycle 3: Running (fixed both issues — synced 029 to chart mirror, renumbered worklog to 0300)

---

## Next Steps

1. Monitor CI cycle 3 — should pass all checks
2. Monitor automated reviewer — iterate on findings
3. Merge PR #188 when approved
4. Start Story 2 (restrict org creation + email resolution) or Story 3 (single-org enforcement) — both unblocked by Story 1

---

## Files Modified

### New files
- `api/migrations/000035_drop_org_dek.up.sql`
- `api/migrations/000035_drop_org_dek.down.sql`

### Deleted files (5 files, ~1800 lines)
- `pkg/secrets/org_key_service.go`
- `pkg/secrets/org_aware_key_service.go`
- `pkg/secrets/org_key_store.go`
- `pkg/secrets/org_key_service_test.go`
- `pkg/secrets/org_lifecycle_integration_test.go`

### Modified files (14 files)
- `api/internal/app/app.go`
- `api/internal/handlers/invitations.go`
- `api/internal/handlers/org_credentials.go`
- `api/internal/handlers/orgs.go`
- `api/internal/handlers/secrets.go`
- `api/internal/server/router.go`
- `api/internal/services/auth/auth.go`
- `api/internal/services/database/pg_org_store.go`
- `frontend/src/api/orgs.ts`
- `pkg/secrets/injection.go`
- `pkg/secrets/key_service.go`
- `pkg/secrets/secret_service.go`
- `pkg/types/types.go`
- `api/internal/middleware/org_guard.go` (comment only)

### Branch
`feat/org-access-control-story1-eliminate-org-dek` (uncommitted — all changes in working tree)
