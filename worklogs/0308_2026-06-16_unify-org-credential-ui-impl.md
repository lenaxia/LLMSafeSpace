# Worklog: Unify Org Provider Credential UI — Backend + Frontend Implementation

**Date:** 2026-06-16
**Session:** Implement worklog 0307's scope — close 3 backend gaps (B-1/B-2/B-3) and 5 frontend gaps (F-1..F-5) so org credentials reach parity with admin/user credentials
**Status:** Complete — implemented, TDD tests pass (8 new backend, 11 new frontend), builds + vet + typecheck clean

---

## Objective

Close the gaps identified in worklog 0307 so the org credentials tab matches the capability of the admin and user provider credential tabs: model probing, context-limit editing, full create/update responses with baseURL, and an expandable row UI.

A latent bug surfaced during implementation: `OrgCredentialMetadata` had **no JSON tags**, so the List endpoint serialized PascalCase (`ID`, `ModelAllowlist`) while the frontend expected camelCase. This is fixed as part of B-2/B-3 via a dedicated response DTO (mirroring the long-standing `AdminCredentialResponse` pattern).

---

## Work Completed

### Backend — B-1: Org model probe route

Added `ProbeModels` to `OrgCredentialsHandler` and registered `GET /orgs/:id/credentials/:credID/models`.

- Pattern mirrors `AdminProviderCredentialsHandler.ProbeModels` (`credential_probe.go:243`): decrypt the stored ciphertext with the org KEK, call `probeCredentialModels` (shared helper), return the model list merged with saved context limits.
- Fail-closed contract: nil KEK → 503, not found → 404, decrypt failure → 500.
- Route wired at `router.go:1015` inside `orgAdminGroup` (org-admin gated).

### Backend — B-2/B-3: OrgCredentialResponse DTO + baseURL extraction + full responses

**Root cause of the latent serialization bug:** the old List handler returned `[]*OrgCredentialMetadata` directly. That struct has no JSON tags, so Go emitted PascalCase keys — incompatible with the camelCase frontend contract that admin/user credentials already followed.

**Fix:** introduced `OrgCredentialResponse` (camelCase JSON tags, mirrors `AdminCredentialResponse` + `OrgID` + optional `BindWarning`) and a `buildOrgCredResponse` helper that decrypts ciphertext to extract `baseURL` (decryption failure is non-fatal — baseURL omitted, metadata still returned; the credential remains usable).

- `List` now builds `[]OrgCredentialResponse` with baseURL extracted per row.
- `Create` now returns the full DTO (including `modelAllowlist`, `modelContextLimits`, `baseURL`, timestamps) instead of the sparse `{id,orgId,name,provider}`. Fetches the freshly-created row via `GetOrgCredential` to surface DB-generated timestamps. `BindWarning` still surfaced on auto-bind failure (contract preserved from worklog 0252).
- `Update` now returns the full DTO (including decrypted baseURL) instead of `{id,message}`. Fetches the updated row for accurate `updated_at` and post-rotation ciphertext.

### Backend — Store interface: ListOrgCredentials returns full rows

Changed `OrgCredentialStore.ListOrgCredentials` from `[]*OrgCredentialMetadata` → `[]*OrgCredentialRow`. Rationale: the handler needs ciphertext to extract baseURL, and fetching metadata-only then re-querying each row for ciphertext would be an N+1. The admin store already returns full rows for the same reason (`pg_credential_store.go:ListAdminCredentials`). Updated `PgSecretStore.ListOrgCredentials` SQL to also `SELECT ciphertext, key_version`.

### Frontend — F-1/F-2: API client + types

`frontend/src/api/orgs.ts`:
- Extended `OrgCredential` interface with `baseURL?`, `modelContextLimits`, `bindWarning?`.
- Extended `createCredential`/`updateCredential` request types with `modelContextLimits`.
- Added `probeCredentialModels(orgId, credId)` calling `GET /orgs/:id/credentials/:credId/models`.

### Frontend — F-3/F-4/F-5: OrgCredentialsTab rebuild

Rewrote `OrgCredentialsTab.tsx` (188 → ~440 lines):
- Expandable credential rows showing provider, baseURL, allowlist badges, context limits.
- Edit flow: opens a pre-populated form, supports name/apiKey rotation/model config updates.
- Post-create probe pattern (admin parity): a "Fetch models from provider" button in edit mode probes the stored credential and renders the discovered model list.
- `ModelConfigTable` component (mirrors the user/admin tab) with checkbox + per-model context-window input + "add model manually" fallback for first-party providers without a discoverable `/models` endpoint.
- Create form explains the post-create probe flow (save first, then fetch — org credentials probe after creation, unlike user credentials which support anonymous pre-create probing).
- Toast feedback on create/delete; inline error states.

---

## Key Decisions

1. **Dedicated response DTO over JSON tags on the store struct.** Adding tags to `OrgCredentialMetadata` would couple the DB-layer struct to the API contract and still leave `baseURL` absent (it lives in ciphertext, not a column). The `OrgCredentialResponse` DTO mirrors the proven `AdminCredentialResponse` pattern and keeps the store layer contract-pure.

2. **ListOrgCredentials returns full rows (with ciphertext).** Necessary for baseURL extraction without N+1; matches `ListAdminCredentials`. The metadata is still available via the embedded `OrgCredentialMetadata` field.

3. **Org probes post-create (admin pattern), not pre-create (user pattern).** Org credentials auto-bind to all org workspaces on creation, so probing after creation fits naturally. Adding anonymous pre-create probing would duplicate the admin flow for no UX gain.

4. **Decryption failure in List/Create/Update is non-fatal for baseURL.** A corrupt or legacy ciphertext shouldn't 500 a list/create response — the credential remains usable; only the display `baseURL` is omitted. This mirrors the admin handler's silent-skip behaviour (`admin_provider_credentials.go:181-188`).

5. **Create fetches the row after insert (extra round trip).** Matches the admin handler's RETURNING-timestamps approach conceptually; the org store's `CreateOrgCredential` returns only the ID, so a follow-up `GetOrgCredential` is the cleanest way to surface DB timestamps. Acceptable cost (one indexed SELECT by PK).

---

## Assumptions Validated (Rule 7)

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | `probeCredentialModels` is reusable for org creds (shared helper) | Verified: `credential_probe.go:171` takes plaintext + savedLimits — no admin/user coupling. Org handler decrypts then calls it identically. |
| A2 | Org handler already has the org KEK available | Verified: `OrgCredentialsHandler.orgKeyDeriver` (`org_credentials.go:32`) — used in Create (line 69) and Update (line 156). Added `orgKEK()` helper + `buildOrgCredResponse`. |
| A3 | `OrgCredentialMetadata` had no JSON tags (PascalCase serialization bug) | Verified: `org_credential_store.go:16-25` — no `json:"..."` tags. Old List returned this directly. Now all JSON goes through `OrgCredentialResponse`. |
| A4 | `ListOrgCredentials` change has no external mock consumers | Verified: `grep` for `ListOrgCredentials`/`OrgCredentialStore` found only the handler-local interface, the PgSecretStore impl, and the test fake. No mocks reference it. |
| A5 | `buildOrgCredResponse` baseURL-skip-on-decrypt-failure matches admin | Verified: `admin_provider_credentials.go:181-188` silently skips on `DecErr != nil`; org helper does the same. |
| A6 | `OrgCredentialRow` embeds `OrgCredentialMetadata` so the new List return type carries all fields | Verified: `org_credential_store.go:28-32` — embedded struct + Ciphertext + KeyVersion. |
| A7 | Org probe route must be org-admin gated | Verified: registered inside `orgAdminGroup` (`router.go:1015`), same group as Create/Update/Delete. |
| A8 | Frontend `probeCredentialModels` URL shape matches backend route | Verified: client calls `/orgs/${id}/credentials/${credId}/models`; backend route `GET /orgs/:id/credentials/:credID/models`. |

---

## Adversarial Self-Review (Rule 11)

### Phase 1 — Findings considered

1. **Create now does an extra SELECT after INSERT.** Real cost, but matches admin's RETURNING semantics and is a single PK lookup. Accepted; documented in Key Decisions #5.
2. **Does anything else consume the old sparse Create/Update response shape?** Checked: only the org credentials frontend consumed these responses; admin/user paths are unaffected (separate handlers). Frontend updated in lockstep.
3. **`Update_NameOnly_NoReEncrypt` test previously asserted the deriver is never called.** The new response builder legitimately derives the KEK for read-only baseURL display decryption (no ciphertext write). Updated the test to assert the real contract: no re-encryption (ciphertext write) and no key-version bump, verified via `lastUpdateCT == nil` and `KeyVersion == 3`.
4. **List decryption loop could be slow for orgs with many credentials.** Same O(n) decrypt as admin List; acceptable for the expected org-credential cardinality (low single digits).

### Phase 2 — Validation

All four findings validated as non-bugs (3) or accepted trade-offs (1). No real bugs found. The latent PascalCase serialization bug (Phase 1 finding #0, surfaced during impl) was the one real defect — fixed via the DTO.

### Phase 3 — Remediation

PascalCase bug fixed with regression tests: `TestOrgCredentials_List_CamelCaseAndBaseURL` asserts both camelCase keys present AND PascalCase keys absent.

---

## Tests Run

### Backend (Go)

```
go test -timeout 180s ./api/internal/handlers/        → ok (28s)
go test -timeout 180s ./pkg/secrets/                  → ok (20s)
go vet ./api/internal/handlers/ ./api/internal/server/ ./pkg/secrets/  → clean
gofmt -l <changed files>                              → clean
go build ./api/...                                    → clean
```

8 new backend tests:
- `TestOrgCredentials_ProbeModels_NotFound` (404)
- `TestOrgCredentials_ProbeModels_NilKEK_503` (fail-closed)
- `TestOrgCredentials_ProbeModels_NoBaseURL` (graceful warning)
- `TestOrgCredentials_ProbeModels_Success` (mock provider, merged limits)
- `TestOrgCredentials_List_CamelCaseAndBaseURL` (regression: camelCase keys + baseURL extraction)
- `TestOrgCredentials_List_Empty` (`[]` not null)
- `TestOrgCredentials_Create_FullResponse` (full DTO with baseURL + timestamps)
- `TestOrgCredentials_Update_FullResponse` (full DTO, decrypted baseURL)

3 existing tests updated for the richer response contract:
- `TestOrgCredentials_Create_Success` (map[string]string → typed DTO)
- `TestOrgCredentials_Create_BindFails_Returns201WithWarning` (typed DTO)
- `TestOrgCredentials_Update_NameOnly_NoReEncrypt` (assert no re-encryption, not no-deriver-call)

### Frontend (Vitest)

```
npx tsc --noEmit                                       → clean
npx vitest run src/components/org-admin/OrgCredentialsTab.test.tsx → 11 passed
npx vitest run (full suite)                            → 103 files, 1076 passed
```

11 new frontend tests covering: list render, baseURL display, expand/context-limits, empty state, error state, create form open, create + refresh, validation error, edit pre-fill + update, delete, post-create probe.

---

## Files Modified

### Backend
- `api/internal/handlers/org_credentials.go` — added `OrgCredentialResponse` DTO, `orgKEK()`, `buildOrgCredResponse`, `ProbeModels`; rewrote List/Create/Update to return full DTOs; interface return type updated.
- `api/internal/handlers/org_credentials_test.go` — 8 new tests + 3 updated for new contract; fake store returns full rows.
- `api/internal/server/router.go` — registered `GET /orgs/:id/credentials/:credID/models`.
- `pkg/secrets/org_credential_store.go` — `ListOrgCredentials` interface + impl now return `[]*OrgCredentialRow` (selects ciphertext + key_version).

### Frontend
- `frontend/src/api/orgs.ts` — extended `OrgCredential` type, create/update request types, added `probeCredentialModels`.
- `frontend/src/components/org-admin/OrgCredentialsTab.tsx` — full rebuild (expandable rows, edit/update, model fetch, context limits, ModelConfigTable).
- `frontend/src/components/org-admin/OrgCredentialsTab.test.tsx` — new (11 tests).

---

## Blockers

None.

---

## Next Steps

1. Commit on a `feat/` branch and open a PR.
2. (Optional, lower priority per worklog 0307) Extract `ModelConfigTable` + `CredentialRow` into `frontend/src/components/shared/` and rewire the admin and user tabs to consume the shared components — they already work, so this is pure de-duplication.
3. Verify against a live cluster: create an org credential with a baseURL, confirm the model probe returns the provider's model list, set context limits, and confirm they persist across reload.
