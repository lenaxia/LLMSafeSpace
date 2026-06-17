# Worklog: Unify Provider Credential UI Across Admin, User, and Org

**Date:** 2026-06-16
**Session:** Scope the unification of the three provider credential frontends into a single reusable component
**Status:** **Complete** — scoped and validated; implementation pending

---

## Objective

Investigate whether the three credential UIs (admin platform credentials, user provider keys, org credentials) can share a single reusable component. All three have near-identical backend schemas and frontend patterns — verify feasibility and identify exact gaps.

---

## Work Completed

### Investigation: Backend schema comparison

Source files examined:
- `api/internal/handlers/org_credentials.go:41-56` — create/update request types
- `api/internal/handlers/admin_provider_credentials.go:40-58` — create/update request types
- `api/internal/handlers/admin_provider_credentials.go:29-38` — response type
- `pkg/secrets/org_credential_store.go:16-32` — OrgCredentialMetadata/Row
- `pkg/secrets/pg_credential_store.go:199-209` — AdminCredentialRow
- `api/internal/server/router.go:1011-1017` — org credential routes
- `api/internal/server/router.go:252-257` — user credential routes

**Finding: Backend create/update request types are structurally identical.**

| Field | Admin (admin_provider_credentials.go:40) | Org (org_credentials.go:41) | User |
|-------|-----------|------|------|
| `name` | ✅ `string` `binding:"required"` | ✅ `string` `binding:"required,min=1,max=128"` | ✅ `string` |
| `provider` | ✅ `string` `binding:"required"` | ✅ `string` `binding:"required"` | ✅ `string` |
| `apiKey` | ✅ `string` `binding:"required"` | ✅ `string` `binding:"required"` | ✅ `string` |
| `baseURL` | ✅ `string` | ✅ `string` | ✅ `string` |
| `modelAllowlist` | ✅ `[]string` | ✅ `[]string` | ✅ `[]string` |
| `modelContextLimits` | ✅ `map[string]int` | ✅ `map[string]int` | ✅ `map[string]int` |

All three share the same underlying DB table (`provider_credentials`) with `owner_type` + `owner_id` discriminator. Encryption path: admin uses `deriveServerKey("provider-credentials")`, org uses `deriveServerKey("org-credentials")`, user uses session DEK — but this is internal to each handler and invisible to the frontend.

### Backend gaps identified

Three gaps prevent full frontend unification:

| # | Gap | Where | Detail |
|---|-----|-------|--------|
| **B-1** | No model probe endpoint | org routes only | Admin has `GET /admin/provider-credentials/:id/models` (router.go:255 user, ~line 400 admin). Org has no equivalent — the route `/orgs/:id/credentials/:credID/models` is NOT registered. |
| **B-2** | `baseURL` not in org List response | `org_credentials.go:118-129` `List` handler | Admin List (`admin_provider_credentials.go:164-190`) decrypts each credential's ciphertext and extracts `baseURL` from `LLMProviderData`. Org List returns `OrgCredentialMetadata` directly without decrypting — `baseURL` is always absent. |
| **B-3** | Org Create/Update responses are sparse | `org_credentials.go:106-114` `Create`, `org_credentials.go:196` `Update` | Return only `{id, orgId, name, provider}` (Create) or `{id, message}` (Update). Admin returns the full `AdminCredentialResponse` with `modelAllowlist`, `modelContextLimits`, `baseURL`, timestamps. |

### What works already

- `OrgCredentialMetadata` struct (`org_credential_store.go:16-25`) **already includes** `ModelContextLimits map[string]int` — the JSON response from List includes it
- Store interface already accepts `modelContextLimits map[string]int` in `CreateOrgCredential` and `UpdateOrgCredential`
- Auto-apply endpoints exist for org credentials (same pattern as admin)
- The DB schema stores `model_context_limits` as a JSONB column in `provider_credentials`

### Frontend gaps identified

| # | Gap | Where | Detail |
|---|-----|-------|--------|
| **F-1** | `OrgCredential` type incomplete | `frontend/src/api/orgs.ts:44-52` | Missing `modelContextLimits?: Record<string, number>` and `keyVersion?: number`. Backend already returns `modelContextLimits` in the list response. |
| **F-2** | No model probe API | `frontend/src/api/orgs.ts` | No `probeModels(orgId, credId)` method. Admin has `adminProviderCredentialsApi.probeModels(id)` calling `GET /admin/provider-credentials/:id/models`. User has `probeModelsAnon(apiKey, baseURL)` for pre-create anonymous probing. |
| **F-3** | OrgCredentialsTab is bare skeleton | `frontend/src/components/org-admin/OrgCredentialsTab.tsx` (188 lines) | Lacks: model fetching, context limit editing, expandable detail rows, update existing credential. Compare: `UserProviderCredentialsTab.tsx` (664 lines) and `AdminProviderCredentialsTab.tsx` (796 lines). |
| **F-4** | No edit/update for existing org creds | `OrgCredentialsTab.tsx` | Only create/delete. Update endpoint exists on backend (`PUT /orgs/:id/credentials/:credID`). |
| **F-5** | `baseURL` never returned for org creds | `OrgCredentialsTab.tsx:76` | The list item shows `Provider: {c.provider}` but can't show `baseURL` because backend B-2. |

---

## Unification Design

### Phase 1: Backend — close 3 gaps (~1.5h)

**B-1: Add model probe route for org credentials**
- Add `ProbeModels(c *gin.Context)` to `OrgCredentialsHandler` (same pattern as admin's probe)
- Register `GET /orgs/:id/credentials/:credID/models` in `registerOrgRoutes` (`router.go:1017+`)
- Route: `orgAdminGroup.GET("/credentials/:credID/models", credH.ProbeModels)`
- Implementation: decrypt ciphertext, parse `LLMProviderData`, call provider API to list models

**B-2: Include `baseURL` in org credential list response**
- Modify `OrgCredentialsHandler.List` to decrypt ciphertext and extract `baseURL` (same pattern as `admin_provider_credentials.go:181-188`)
- Note: list response is `[]*OrgCredentialMetadata` — `baseURL` is not a struct field. Either add it to the struct or transform the response.
- Simplest approach: create a response DTO similar to `AdminCredentialResponse` or add `BaseURL` to `OrgCredentialMetadata` (the field is optional, populated only in List/Get)

**B-3: Return full response in Create/Update**
- Create: return complete metadata (ID, name, provider, baseURL, modelAllowlist, modelContextLimits, timestamps)
- Update: return updated metadata (same fields)

### Phase 2: Frontend — extract shared component (~2h)

The richest implementation is `UserProviderCredentialsTab.tsx`. Extract three reusable components:

#### 2a: `ModelConfigTable` (already a standalone function at line 394) — MOVE to `src/components/shared/ModelConfigTable.tsx`
- Self-contained: takes `rows: ModelRow[]` + `onChange` callback
- Used identically in UserProviderCredentialsTab:394 and AdminProviderCredentialsTab
- No API dependencies — pure UI component

#### 2b: Extract `CreateProviderCredentialForm` as shared component
- Currently `CreateUserCredentialForm` at `UserProviderCredentialsTab.tsx:455`
- Parameterize with `apiClient` prop exposing `{ probeModels: (apiKey, baseURL) => Promise<ProbeResult> }`
- User variant: calls `probeModelsAnon(apiKey, baseURL)` before creation
- Admin variant: creates credential first, then calls `probeModels(credId)` after creation (Phase 1 pattern)
- Org variant: same as admin (create first, probe after) — reuse admin's pattern

#### 2c: Extract `CredentialRow` as shared component
- Currently `CredentialRow` at `UserProviderCredentialsTab.tsx:159`
- Parameterize with `apiClient` + optional panels:
  - `showBindings` (user only) — workspace binding panel
  - `showAutoApply` (admin + org) — auto-apply rules
  - `onEdit` callback — opens update form

### Crucial differences that remain separate

| Feature | Admin | User | Org |
|---------|-------|------|-----|
| API base path | `/admin/provider-credentials` | `/provider-credentials` | `/orgs/{orgId}/credentials` |
| Owner scoping | `owner_type='admin'` | `owner_type='user'` (session user) | `owner_type='org'` + `orgId` param |
| KEK derivation | `"provider-credentials"` | session DEK | `"org-credentials"` |
| Workspace bindings | Not applicable | Per-credential binding panel | Auto-bound to all org workspaces on create |
| Auto-apply | `targetType: all/user/org` | Not applicable | Org-scoped only |
| Model probing | `GET /admin/.../models` (post-create) | `POST /probe-models` (pre-create, anonymous) | `GET /orgs/.../models` (post-create) — needs B-1 |

### Shared component API shape

```typescript
interface ProviderCredentialApiClient {
  // CRUD
  list: () => Promise<CredentialType[]>;
  create: (req: CreateRequest) => Promise<CredentialType>;
  update: (id: string, req: UpdateRequest) => Promise<CredentialType>;
  delete: (id: string) => Promise<void>;
  // Model probing
  probeModels: (credId: string) => Promise<ProbeModelsResponse>;
  // Optional: bindings (user only)
  listBindings?: (credId: string) => Promise<BindingInfo[]>;
  bindToWorkspace?: (credId: string, wsId: string) => Promise<void>;
  unbindFromWorkspace?: (credId: string, wsId: string) => Promise<void>;
  // Optional: auto-apply (admin + org)
  createAutoApply?: (credId: string, req: AutoApplyRequest) => Promise<void>;
  listAutoApply?: (credId: string) => Promise<AutoApplyRule[]>;
  deleteAutoApply?: (credId: string, targetType: string, targetId?: string) => Promise<void>;
}
```

The base component is `ProviderCredentialsPanel` — renders the list with expandable rows, create form, model config. Each tab wraps it with the appropriate `apiClient` and toggles optional panels.

---

## Key Decisions

1. **One-way probe for org/admin, anonymous probe for user.** User credentials support pre-create probing (`POST /probe-models` with apiKey+baseURL) because the user hasn't stored a credential yet. Admin and org credentials probe after creation (they have a stored credential with an ID). This is a UX difference that stays but is handled by the apiClient — the shared component doesn't care.

2. **Org uses the admin probe pattern (post-create).** Org already has auto-bind on create, so probing after creation fits naturally. Adding anonymous pre-create probing would duplicate the admin probe flow.

3. **Not all three tabs become identical.** The shared component handles the common 90% (CRUD, model list, context limits). Each tab wraps it with its own API client and conditionally shows: workspace bindings (user), auto-apply rules (admin/org), and header text.

4. **`baseURL` extraction from ciphertext is consistent.** Admin List decrypts to get baseURL; org List should do the same (B-2). This requires the org KEK to be available in the List handler — it already is (`h.orgKeyDeriver`).

---

## Blockers

None. This is a scope/plan — not yet implemented.

---

## Tests Run

N/A — investigation only. No code changes made.

---

## Next Steps

1. Implement Phase 1 backend gaps (B-1, B-2, B-3) — model probe route, baseURL in list, full Create/Update responses
2. Implement Phase 2 frontend extraction — ModelConfigTable → shared/, CreateForm + CredentialRow → shared/
3. Wire OrgCredentialsTab with shared components
4. Optionally rewire AdminProviderCredentialsTab and UserProviderCredentialsTab to use shared components (lower priority — they already work)
5. Write tests for new backend route + frontend components

---

## Files Modified

None — investigation only. Files examined:
- `api/internal/handlers/org_credentials.go` (entire file, 275 lines)
- `api/internal/handlers/admin_provider_credentials.go` (entire file, 468 lines)
- `api/internal/server/router.go` (org routes at 978-1017, user routes at 243-257)
- `pkg/secrets/org_credential_store.go` (structs at 15-45, list at 66-90)
- `pkg/secrets/pg_credential_store.go` (AdminCredentialRow at 197-209)
- `frontend/src/components/org-admin/OrgCredentialsTab.tsx` (entire file, 188 lines)
- `frontend/src/components/settings/UserProviderCredentialsTab.tsx` (entire file, 664 lines)
- `frontend/src/components/settings/AdminProviderCredentialsTab.tsx` (CreateAdminCredentialForm at 549-796)
- `frontend/src/api/orgs.ts` (OrgCredential at 44-52, API methods at 131-149)
- `frontend/src/api/providerCredentials.ts` (types at 8-45, API methods)

**Design doc alignment:** This work is new scope not covered by `design/0031` — the credential UI unification is a UX improvement that spans all three credential types. Design 0031 only covers org access control (Stories 1-9).

---

## Assumptions Validated (Rule 7)

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | All three create request types are identical | Verified: `createAdminCredentialRequest` (`admin_provider_credentials.go:40-47`) and `createOrgCredentialRequest` (`org_credentials.go:41-48`) have the same 6 fields in the same order. |
| A2 | `OrgCredentialMetadata` already includes `modelContextLimits` | Verified: `org_credential_store.go:22` — `ModelContextLimits map[string]int`. The field IS present and returned in JSON. |
| A3 | Admin List decrypts ciphertext to extract `baseURL` | Verified: `admin_provider_credentials.go:181-188` — decrypts each credential, unmarshals `LLMProviderData`, sets `r.BaseURL`. |
| A4 | Org List does NOT extract `baseURL` | Verified: `org_credentials.go:118-129` — returns `creds` directly from `ListOrgCredentials` with no decryption step. |
| A5 | No `/models` route exists for org credentials | Verified: `router.go:1011-1017` — routes are POST/GET/PUT/DELETE `/credentials` + auto-apply. No `/models` sub-route registered. |
| A6 | Frontend `OrgCredential` type missing fields | Verified: `orgs.ts:44-52` — only `id, orgId, name, provider, modelAllowlist, createdAt, updatedAt`. Missing `modelContextLimits`. |
| A7 | `ModelConfigTable` has no API or store dependencies | Verified: `UserProviderCredentialsTab.tsx:394-453` — pure UI component taking `rows` + `onChange`. No imports beyond React. |
| A8 | `CredentialRow` can be parameterized by removing binding panel | Verified: binding panel (`UserProviderCredentialsTab.tsx:304-377`) is a conditional block inside the expanded panel — can be gated by `showBindings` prop. |
| A9 | Org handler already has access to org KEK | Verified: `OrgCredentialsHandler` struct (`org_credentials.go:32`) has `orgKeyDeriver secrets.AdminKeyDeriver`. Used in Create (line 69) and Update (line 156). Available for List decryption. |

## Integration Point Validation

| Integration | Location | Verified |
|------------|----------|----------|
| New `/models` route registration | `router.go:1017` — after `DELETE /credentials/:credID/auto-apply` | ✅ Wire path: add `orgAdminGroup.GET("/credentials/:credID/models", credH.ProbeModels)` |
| `baseURL` extraction in List | `org_credentials.go:118-129` `List` handler | ✅ Add decryption loop after `ListOrgCredentials` call; pattern from `admin_provider_credentials.go:181-188` |
| Full Create response | `org_credentials.go:106-114` | ✅ Replace `gin.H{...}` with struct containing all fields; same pattern as admin's `AdminCredentialResponse` |
| Full Update response | `org_credentials.go:196` | ✅ Replace `gin.H{"id": credID, "message": "..."}` with full metadata |
| Frontend `orgsApi` extension | `orgs.ts:131-149` `listCredentials` / `createCredential` | ✅ Add `probeModels(orgId, credId)` method; extend `OrgCredential` interface with `modelContextLimits` |
| Frontend shared component wiring | `OrgCredentialsTab.tsx` → shared `CreateProviderCredentialForm` + `CredentialRow` | ✅ Pass `orgsApi`-based apiClient; disable bindings panel; enable auto-apply panel |
