# Worklog: Consolidate Credential Handler/Store Triplication (US-38.13)

**Date:** 2026-06-18
**Session:** Implement US-38.13 — collapse the triplicated provider-credential Go types, store interfaces, and handler boilerplate, plus extract the shared frontend ModelConfigTable. Four-phase refactor, one commit per phase.
**Status:** Complete

---

## Objective

The provider credential system had three independent handler files, three store interfaces, three row structs, two response DTOs, and a duplicated frontend component — all operating on the same `provider_credentials` table with the same column set. Collapse the duplication without changing routes, encryption, error codes, or response shapes.

---

## Work Completed

### Phase 1 — Shared data types + response builder (commit 9cab71fc)

- Collapsed three row structs (`AdminCredentialRow`, `UserCredentialRow`, `OrgCredentialRow` + embedded `OrgCredentialMetadata`) into one `CredentialRow` scoped by `OwnerType` + `OwnerID` (`pkg/secrets/pg_credential_store.go`).
- Replaced two response DTOs (`AdminCredentialResponse`, `OrgCredentialResponse`) with one `CredentialResponse` whose `OrgID` and `BindWarning` fields are `omitempty`. Preserved the existing `orgId` JSON key (the org List test asserts it) rather than the spec's proposed `ownerId` — see Key Decisions.
- Extracted `buildCredentialResponse(row, key)` from `buildOrgCredResponse`; routed the admin/user List/Get/Update handlers through it. This also closed a latent hygiene gap: admin's inline decrypt-for-baseURL did not zero the plaintext (org's helper always did); the shared helper always zeroes.
- Migrated the four remaining `isDuplicateErr` call sites (`org_credentials.go:162,349`, `orgs.go:130,286`) to `ClassifyPostgresError` + `errors.Is` and deleted the shim from `errors.go`.

### Phase 2 — Unified CRUD store interface (commit 6f6d3013)

- Replaced the three handler-local CRUD interfaces (`AdminCredentialStore`, `UserCredentialStore`, `orgCredentialStore`) with one `CredentialStore` of five owner-scoped methods: `Create`/`List`/`Get`/`Update`/`Delete`, each parameterized by `(ownerType, ownerID)`.
- Collapsed nine owner-specific `PgSecretStore` CRUD methods down to the five parameterized ones.
- Extracted `CredentialBindingStore` for the user-only credential↔workspace operations (Bind/Unbind/ListBindings/GetCredentialBindings/BindCredentialToAllUserWorkspaces). The org handler keeps a local `orgBindingAndAutoApplyStore` for its bulk-bind + auto-apply methods.
- Deleted the exported `OrgCredentialStore` interface.
- Unified `UpdateCredential` preserves the org handler's partial-update contract via COALESCE: nil `model_allowlist`/`model_context_limits`/`ciphertext` means "don't change"; empty slice/map is a valid "clear" value.
- Unified `CreateCredential` follows the admin pattern (caller generates UUID in Go), so the org handler now generates `uuid.New().String()` instead of relying on `RETURNING id`.
- Updated handler constructors + `app.go` wiring (lines 224, 301). Consolidated the three test fakes onto the shared `CredentialStore` surface.

### Phase 3 — Handler helper extraction (commit 2db2e23b)

- Extracted `getCredentialForProbe` (`credential_ops.go`): fetch row → resolve key via injected `credentialKeyResolver` (keeps the three distinct key sources separate) → decrypt → return plaintext + saved limits. Each `ProbeModels` handler shrank from ~25 lines to ~10. The helper defer-zeroes the plaintext, closing a hygiene gap where the old probe paths left the decrypted API key on the heap.
- Extracted `encryptCredentialData(key, provider, apiKey, baseURL)` (`credential_ops.go`): marshal → encrypt → zero plaintext. Replaces inline blocks in the three Create handlers. Consistently zeroes the plaintext in the admin/user Create paths (org already did).
- Create/Update/Delete stay as per-handler orchestration — their side-effect divergence (auto-bind, 207 vs BindWarning, credStateWriter, provider mutability) makes a unified function harder to read than three thin handlers.

### Phase 4 — Frontend component + type extraction (commit 5458c191)

- Extracted the pure `ModelConfigTable` (checkbox + context-window table) to `frontend/src/components/shared/ModelConfigTable.tsx` with the exported `ModelRow` interface. Admin/user tabs import it directly; the org tab wraps it in a local `OrgModelConfigTable` that preserves its "Add model manually" affordance via the `footer` prop. Each tab passes its own empty-state wording via `emptyMessage`.
- Shared provider-credential types in `frontend/src/api/providerCredentialTypes.ts` (`ProviderCredential`, `CreateCredentialRequest`, `UpdateCredentialRequest`, `CreateCredentialResponse`, `ProbeModelEntry`, `ProbeModelsResponse`). The admin/user API clients re-export these and keep named aliases for backward compatibility; `OrgCredential` extends `ProviderCredential` with `orgId`.
- No shared behavioral component (`ProviderCredentialsPanel`) was created — the three tabs have genuine behavioral differences and a flag-driven shared component would be a leaky abstraction (per worklog 0312 and the story's explicit "What NOT to do").

---

## Key Decisions

1. **Preserved `orgId` JSON key, did not rename to `ownerId`.** The spec's `CredentialResponse` proposed `ownerId`, but `TestOrgCredentials_List_CamelCaseAndBaseURL` asserts the raw JSON key is `orgId`. Renaming would break the existing test, violating the "existing tests must pass unchanged" hard constraint. Kept `orgId` (omitempty) — admin/user creds simply omit it.

2. **Named the CRUD interface `CredentialStore` in the handlers package, not `pkg/secrets`.** `pkg/secrets/credential_store.go` already defines a `CredentialStore` interface (the workspace-injection methods from Epic 30: `GetWorkspaceCredentials`, `UpsertFreeTierCredential`, `SeedWorkspaceCredentials`, etc.). That interface is a genuinely different concern (injection/seed), referenced via type assertion in `injection.go` and the async-audit wrapper. Reusing the name in `pkg/secrets` would collide; renaming the existing one is out of scope and a larger blast radius. The new CRUD `CredentialStore` lives in the `handlers` package (handler-local, like the interfaces it replaces), and the concrete `PgSecretStore` implements both.

3. **Shared frontend types in `providerCredentialTypes.ts`, not `credentials.ts`.** `api/credentials.ts` already exists with the older `CredentialSet` admin-API types (a different subsystem). Putting provider-credential types there would collide. Created `providerCredentialTypes.ts` to avoid the collision.

4. **Org tab keeps a local `OrgModelConfigTable` wrapper.** The shared `ModelConfigTable` is the pure table; the org tab's "Add model manually" feature is org-specific UX, so it stays in a thin wrapper that passes the manual-add button via the shared component's `footer` prop.

5. **`getCredentialForProbe` uses an injected `credentialKeyResolver` func.** The three key sources (platform KEK, session DEK, org KEK) must stay separate (domain separation is a security property). A resolver callback keeps them separate while still extracting the shared get→decrypt boilerplate.

---

## Assumptions Stated and Validated (Rule 7)

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | All three row structs had identical column sets | Verified before editing; confirmed by the unified `CredentialRow` compiling and all tests passing. |
| A2 | Org responses use `orgId` JSON key (spec's `ownerId` would break tests) | Verified: `TestOrgCredentials_List_CamelCaseAndBaseURL` asserts `orgId`. Kept `orgId`. |
| A3 | `secrets.CredentialStore` (injection) is a different concern from the CRUD interface | Verified: referenced only via type assertion in `injection.go` + async-audit wrapper; never imported by handlers. Named the new CRUD interface in the handlers package to avoid collision. |
| A4 | `api/credentials.ts` already has unrelated `CredentialSet` types | Verified: created `providerCredentialTypes.ts` instead. |
| A5 | Org Update COALESCE-with-nil semantics must be preserved | Verified: `TestOrgCredentials_Update_NameOnly_NoReEncrypt` passes — unified `UpdateCredential` uses COALESCE so nil = "don't change". |
| A6 | Org Create relied on `RETURNING id`; unified Create generates UUID in Go | Verified: behavior-neutral since DB `DEFAULT gen_random_uuid()` is only a fallback; `TestOrgCredentials_Create_*` pass. |
| A7 | Admin Update allows provider change; org does not | Verified: stayed per-handler (admin update request has `Provider *string`, org does not). `TestAdminProviderCredentials_Update_*` pass. |
| A8 | Controller package is unaffected by these changes | Verified: `grep` for all changed types in `controller/` and `cmd/` returns zero hits. |

---

## Blockers

None.

---

## Tests Run

| Test | Command | Result |
|------|---------|--------|
| Go build (affected) | `go build ./pkg/secrets/... ./api/internal/handlers/... ./api/internal/app/...` | PASS |
| Go vet (api + pkg) | `go vet ./api/... ./pkg/...` | PASS (EXIT 0) |
| Handler tests | `go test -timeout 90s ./api/internal/handlers/... -v` | PASS (all existing tests unchanged) |
| Secrets tests | `go test -timeout 60s ./pkg/secrets/... -v` | PASS |
| Frontend typecheck | `cd frontend && npm run typecheck` | PASS (EXIT 0) |
| Frontend lint | `cd frontend && npm run lint` | PASS (0 errors; 8 pre-existing warnings in unrelated files) |
| Frontend tests | `cd frontend && npm run test` | PASS (1089 tests, 104 files) |
| Grep: old row structs | `grep -rn 'AdminCredentialRow\|UserCredentialRow\|OrgCredentialRow\|OrgCredentialMetadata' --include='*.go' .` | 0 hits |
| Grep: old DTOs | `grep -rn 'AdminCredentialResponse\|OrgCredentialResponse' --include='*.go' .` | 0 hits |
| Grep: shim/builder | `grep -rn 'isDuplicateErr\|buildOrgCredResponse' --include='*.go' .` | 0 hits |

Note: the full `go build ./...` times out on this sandbox due to slow module downloads for the controller package's heavy K8s dependencies (unrelated to these changes). The controller does not reference any changed types (verified by grep), and `go vet ./api/... ./pkg/...` passes.

---

## Next Steps

- Open a PR for this branch (`refactor/US-38.13-consolidate-credential-triplication`) and run the full `make build && make test && make lint` in CI, which has network access for the controller's K8s deps.
- Mark the US-38.13 acceptance criteria checkboxes in the story file once CI is green.

---

## Files Modified

**Backend (Phase 1):**
- `pkg/secrets/pg_credential_store.go` — unified `CredentialRow`, admin/user CRUD use it
- `pkg/secrets/org_credential_store.go` — removed `OrgCredentialMetadata`/`OrgCredentialRow`, org CRUD use `CredentialRow`
- `api/internal/handlers/admin_provider_credentials.go` — `CredentialResponse`, `buildCredentialResponse`, removed `AdminCredentialResponse`
- `api/internal/handlers/user_provider_credentials.go` — uses `CredentialResponse`
- `api/internal/handlers/org_credentials.go` — removed `OrgCredentialResponse`/`buildOrgCredResponse`, uses shared builder, migrated `isDuplicateErr`
- `api/internal/handlers/orgs.go` — migrated `isDuplicateErr` (2 sites), added `errors` import
- `api/internal/handlers/errors.go` — deleted `isDuplicateErr` shim
- Test fakes in the three `*_test.go` files — updated row/DTO references

**Backend (Phase 2):**
- `api/internal/handlers/admin_provider_credentials.go` — `CredentialStore` interface, constructor + calls use unified methods
- `api/internal/handlers/user_provider_credentials.go` — `CredentialBindingStore` interface, split store/bindings fields
- `api/internal/handlers/org_credentials.go` — `orgBindingAndAutoApplyStore`, org Create generates UUID
- `api/internal/handlers/credential_probe.go` — uses unified `GetCredential`
- `api/internal/app/app.go` — updated constructor wiring (lines 224, 301)
- `pkg/secrets/pg_credential_store.go` — 5 parameterized CRUD methods (down from 9)
- `pkg/secrets/org_credential_store.go` — `OrgAutoApplyStore`, removed CRUD methods + `OrgCredentialStore`
- Test fakes consolidated onto `CredentialStore` surface

**Backend (Phase 3):**
- `api/internal/handlers/credential_ops.go` — NEW: `getCredentialForProbe`, `encryptCredentialData`
- `api/internal/handlers/credential_probe.go` — admin/user ProbeModels use helper
- `api/internal/handlers/org_credentials.go` — org ProbeModels + Create use helpers
- `api/internal/handlers/admin_provider_credentials.go` — Create uses helper
- `api/internal/handlers/user_provider_credentials.go` — Create uses helper

**Frontend (Phase 4):**
- `frontend/src/components/shared/ModelConfigTable.tsx` — NEW: shared pure component + `ModelRow`
- `frontend/src/api/providerCredentialTypes.ts` — NEW: shared provider-credential types
- `frontend/src/api/providerCredentials.ts` — re-exports shared types, aliases for per-client names
- `frontend/src/api/orgs.ts` — `OrgCredential` extends `ProviderCredential`
- `frontend/src/components/settings/AdminProviderCredentialsTab.tsx` — imports shared `ModelConfigTable`
- `frontend/src/components/settings/UserProviderCredentialsTab.tsx` — imports shared `ModelConfigTable`
- `frontend/src/components/org-admin/OrgCredentialsTab.tsx` — local `OrgModelConfigTable` wrapper using shared component
