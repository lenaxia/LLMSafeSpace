# Epic 11: Organizations

**Status:** Ready to Implement
**Depends On:** Epic 10 (user DEK infrastructure — confirmed in place), Epic 30 (Unified Credential Model — complete, PR #39)
**Estimated Effort:** ~40 hours
**Blocks:** Epic 12 org billing rollup (`resolveBillingOwner`)

---

## Validated Assumptions

Every claim is verified against live code. No unvalidated claims are made.

| # | Assumption | Verified At | Result |
|---|---|---|---|
| A1 | `OwnerTypeOrg = "org"` constant and `SecretOwner{ID, Type}` struct already exist | `pkg/secrets/provider.go:13,18` | Confirmed |
| A2 | `provider_credentials.owner_type IN ('user','org','admin')` constraint already in schema | `api/migrations/000015_unified_credential_model.up.sql:15` | Confirmed |
| A3 | `credential_auto_apply.target_type IN ('user','org','all')` constraint and `idx_cred_auto_apply_org` index already in schema | `api/migrations/000015_unified_credential_model.up.sql:51,68` | Confirmed |
| A4 | `decryptBinding` currently returns `fmt.Errorf("unsupported owner_type %q", b.OwnerType)` for `owner_type='org'` — no panic, just skip | `pkg/secrets/injection.go:154-155` | Confirmed |
| A5 | `SeedWorkspaceCredentials` has a documented placeholder: `OR (caa.target_type = 'org' AND caa.target_id = $2)` — uses `userID` as org_id stand-in | `pkg/secrets/pg_credential_store.go:79,91` | Confirmed |
| A6 | `KeyServiceInterface` in auth takes `userID string` for all methods — no `SecretOwner` variant | `api/internal/services/auth/auth.go:37-43` | Confirmed |
| A7 | `user_keys` table PK is `user_id VARCHAR(36)` — org keys cannot be stored here | `api/migrations/000007_user_keys.up.sql:4` | Confirmed |
| A8 | `KeyService` is entirely `userID string`-based: `InitializeUserKeys`, `UnlockDEK`, `HasKeys`, `ChangePassword`, `RotateKeyWithPassword` | `pkg/secrets/key_service.go:114,177,394,250,440` | Confirmed |
| A9 | `WorkspaceOwner` CRD struct has `UserID string` only — no `OrgID` field | `pkg/apis/llmsafespace/v1/workspace_types.go:11-13` | Confirmed |
| A10 | `workspaces` DB table has no `org_id` column — all migrations 000001–000023 confirmed | `api/migrations/000002_workspaces.up.sql` + ALTERs in 5,9,11,13,14,23 | Confirmed |
| A11 | `verifyOwner` does a direct `meta.UserID != userID` equality check — no org fallback | `api/internal/services/workspace/workspace_service.go:606` | Confirmed |
| A12 | `ListWorkspaces` DB query filters `WHERE user_id = $1` only | `api/internal/services/database/database.go:556` | Confirmed |
| A13 | `DeriveKEK`, `WrapDEK`, `UnwrapDEK`, `GenerateDEK`, `GenerateSalt` are all exported from `pkg/secrets` and usable by new org key code | `pkg/secrets/key_service.go` imports confirmed | Confirmed |
| A14 | `DEKCache.CacheDEK` is keyed by an arbitrary `sessionID string` — using `"org:<orgID>"` as the cache key is valid and already described in the design comments | `pkg/secrets/key_service.go:38-42` | Confirmed |
| A15 | `users.role` is a flat string `'user'` or `'admin'` — there is no per-org role concept in the schema | `api/migrations/000001_initial_schema.up.sql:6` | Confirmed |
| A16 | `AdminGuard` middleware checks `c.Get("userRole") == "admin"` — a clean pattern to follow for `OrgMemberGuard` and `OrgAdminGuard` | `api/internal/server/admin_guard.go:14-22` | Confirmed |
| A17 | Frontend `AdminProviderCredentialsTab.tsx:442` has `<option value="org" disabled>Organisation (coming soon)</option>` — placeholder waiting for real org IDs | `frontend/src/components/settings/AdminProviderCredentialsTab.tsx:442` | Confirmed |
| A18 | `zz_generated.deepcopy.go` exists and was generated from `workspace_types.go` — adding `OrgID *string` to `WorkspaceOwner` requires re-running code generation | `pkg/apis/llmsafespace/v1/zz_generated.deepcopy.go` | Confirmed |
| A19 | `buildWorkspaceCRD` in workspace_service.go sets `Owner: v1.WorkspaceOwner{UserID: userID}` — must be extended to also set `OrgID` when present | `api/internal/services/workspace/workspace_service.go:626` | Confirmed |
| A20 | The `DeriveKEK` function signature is `DeriveKEK(secret, salt []byte, info string) ([]byte, error)` — the `info` parameter provides domain separation, so org KEK derivation uses a distinct info string | `pkg/secrets/key_service.go:125` (calls `DeriveKEK(password, salt, kekInfo)`) | Confirmed |

---

## Problem Statement

### Current State

1. **Single ownership dimension.** Every workspace, secret, and credential is owned by exactly one user. A team of engineers sharing an AI workspace environment must each re-enter the same Anthropic key, the same GitHub PAT, and the same environment configuration. There is no way to share credentials or workspaces at a group level.

2. **No org-level LLM credentials.** The `provider_credentials` table has `owner_type IN ('user','org','admin')` ready, but no code writes or reads `owner_type='org'` rows. The `decryptBinding` function returns an error for org-owned credentials (`injection.go:154`).

3. **No org-level auto-apply.** `credential_auto_apply` supports `target_type='org'` in the schema, but `SeedWorkspaceCredentials` uses `userID` as a stand-in for `org_id` (`pg_credential_store.go:79`) — a documented placeholder that produces incorrect bindings once real org IDs exist.

4. **No org membership concept.** There are no `organizations` or `org_memberships` tables. The `users.role` field is a flat global binary — there is no per-org role.

5. **No org DEK.** The `user_keys` table stores one DEK per user. There is no mechanism to encrypt org-level secrets with a key shared among org admins.

6. **Billing attribution is user-only.** Epic 12's `resolveBillingOwner` is designed to return `{ID: orgID, Type: "org"}` when a workspace has an org — but there is no `workspaces.org_id` column to read from.

---

## Design

### Org DEK Cryptography

The org DEK model mirrors the user DEK model from Epic 10, with one critical difference: an org DEK must be accessible to any org admin, not just the user who created it. This is solved by storing one wrapped copy of the org DEK per admin in the `org_key_members` table.

```
Org Creation (by founding admin):
  1. Generate random 256-bit org DEK
  2. For the founding admin:
     a. Derive admin's KEK = DeriveKEK(admin_password, admin_user_salt, "llmsafespace-org-kek")
        (admin_user_salt comes from user_keys.salt — the admin's existing salt)
        NOTE: "llmsafespace-org-kek" is the HKDF info string for org KEK derivation.
        This MUST be different from the user KEK info string ("llmsafespace-kek" in
        pkg/secrets/crypto.go:20). HKDF with the same secret+salt but a different info
        string produces a different key — this is the domain separation that prevents
        the org KEK from being identical to the user KEK. Using "llmsafespace-kek" here
        would be a critical crypto bug: the org_key_members wrapped DEK would be
        decryptable by anyone who already holds the user's KEK.
     b. wrapped_dek = WrapDEK(admin_kek, org_dek)
     c. Insert row into org_key_members: (org_id, user_id=admin, wrapped_dek)
  3. org_dek exists only in memory; discarded after wrapping

Login (for any org admin):
  1. Standard user login completes — user DEK unlocked, cached under sessionID
  2. For each org where user has role='admin':
     a. Load org_key_members row for (org_id, user_id)
     b. Derive user's KEK = DeriveKEK(password, user_keys.salt, "llmsafespace-org-kek")
        (same info string as used at org creation — must match)
     c. Unwrap org DEK from wrapped_dek
     d. Cache org DEK under "org:<orgID>" in Redis with TTL = tokenDur
     e. Zero KEK from memory

Adding an admin to an org:
  1. Existing org admin calls POST /api/v1/orgs/:id/members with role='admin'
  2. Server retrieves org DEK from "org:<orgID>" cache (caller must be logged-in admin)
  3. Derive new admin's KEK from new_admin_user_keys.salt + new_admin_password... 
     PROBLEM: server does not know new admin's password.
  SOLUTION: two-step handshake — see US-11.3 for the accepted design.

Removing an admin from an org:
  1. Delete org_key_members row for (org_id, removed_user_id)
  2. org DEK does NOT change — removed admin's wrapped copy is gone; they cannot unwrap
     the org DEK going forward. Existing sessions expire naturally (TTL).
  NOTE: If the removed admin is suspected of malicious use, org DEK rotation (US-11.9)
  must be triggered. Removal alone is sufficient for normal offboarding.

Password change for an org admin:
  1. User changes password (existing ChangePassword flow)
  2. On success, server must re-wrap the org DEK for this admin's new KEK
  3. This requires the org DEK — which is available in "org:<orgID>" cache if the user
     is currently logged in (the session has both the old cached DEK and the new password)
  4. For each org where user is admin: unwrap with old KEK, re-wrap with new KEK, update row
  5. If the session has expired by the time password change is processed, the org_key_members
     row becomes stale (wrapped with old KEK). On next login with new password, the unwrap
     will fail. Recovery: org DEK rotation by another admin, or remove+re-add this admin.
     This edge case is documented, not silently swallowed.
```

**Cache key convention:** `"org:<orgID>"` in Redis, same `DEKCache` interface as user DEKs. TTL matches the admin's session TTL on normal login. After `RotateOrgDEK`, the TTL is extended to 7 days regardless of session TTL — see US-11.9 for rationale.

**Cache key namespace safety:** `sessionID` values are JWT `jti` claims — standard UUID format (`xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx`). `"org:<orgID>"` has the `"org:"` prefix followed by a UUID. These two key spaces cannot collide: no UUID string starts with `"org:"`.

If multiple admins are logged in concurrently, the org DEK is written to the same `"org:<orgID>"` key multiple times (identical value, last writer's TTL wins — not a problem since the value is identical).

**Why not re-derive the org DEK from a shared password?** A shared org password creates a single point of compromise and cannot be rotated without re-distributing the password. Per-admin wrapping gives each admin an independent copy, enabling clean admin removal.

**Why not use the admin's existing `user_keys.wrapped_dek` as the org key?** User DEKs are private to each user. The org DEK must be independently rotatable without affecting user secrets. Separate keys maintain clear audit trails and minimal blast radius.

### Data Model

#### New tables

```sql
CREATE TABLE organizations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL UNIQUE,    -- URL-safe identifier, e.g. "acme-corp"
    created_by  TEXT NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ             -- soft delete
);

CREATE INDEX idx_orgs_slug ON organizations(slug) WHERE deleted_at IS NULL;

CREATE TABLE org_memberships (
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role        TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);

CREATE INDEX idx_org_memberships_user ON org_memberships(user_id);

-- One row per (org, admin). Members do not have org_key_members rows —
-- they cannot decrypt org-level secrets without admin involvement.
CREATE TABLE org_key_members (
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wrapped_dek BYTEA NOT NULL,   -- WrapDEK(admin_kek, org_dek)
    key_version INTEGER NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
```

#### Modified tables

```sql
-- workspaces: add nullable org_id
ALTER TABLE workspaces ADD COLUMN org_id UUID REFERENCES organizations(id) ON DELETE SET NULL;
CREATE INDEX idx_workspaces_org ON workspaces(org_id) WHERE org_id IS NOT NULL;
```

**Soft-delete behaviour:** `organizations` uses soft delete (`deleted_at`). `ON DELETE SET NULL` on `workspaces.org_id` only fires on hard delete. When an org is soft-deleted, `workspaces.org_id` remains set. `SoftDeleteOrg` must therefore explicitly null the column:

```sql
-- Inside SoftDeleteOrg transaction:
UPDATE workspaces SET org_id = NULL WHERE org_id = $1;
UPDATE organizations SET deleted_at = now() WHERE id = $1;
```

All membership queries (`IsOrgMember`, `IsOrgAdmin`, `ListAdminOrgIDs`) must JOIN `organizations` and filter `organizations.deleted_at IS NULL`. This prevents members of soft-deleted orgs from retaining access via the org path in `verifyOwner`.

#### No changes to existing tables
- `users` — no org fields; membership is in `org_memberships`
- `user_keys` — unchanged; org keys live in `org_key_members`
- `provider_credentials` — schema already supports `owner_type='org'`; no DDL change
- `credential_auto_apply` — schema already supports `target_type='org'`; no DDL change
- `workspace_credential_bindings` — unchanged

### Adding an Admin: Two-Step Key Handshake

The server cannot derive a new admin's KEK because it does not have their plaintext password at admin-addition time. The accepted solution:

**Step 1 — Invitation:** The inviting admin calls `POST /api/v1/orgs/:id/members` with `{userID, role: "admin"}`. This creates an `org_memberships` row with a `pending_key_wrap: true` flag. No `org_key_members` row is created yet.

**Step 2 — Acceptance:** The invited user calls `POST /api/v1/orgs/:id/accept-key` with their `{password}`. The server:
  1. Verifies the user has a pending `org_memberships` row for this org
  2. Looks up `"org:<orgID>"` in the DEK cache — this requires any currently logged-in admin to have the org DEK cached. If not cached: the inviting admin must be active or must re-login first (documented constraint).
  3. Derives the invited user's KEK from their password + `user_keys.salt`
  4. Wraps the org DEK with the invited user's KEK → inserts `org_key_members` row
  5. Clears `pending_key_wrap` flag on `org_memberships`

**If org DEK not in cache at Step 2:** The server returns `409 Conflict` with message `"org DEK not currently available — an org admin must be logged in to complete key wrapping"`. The invited user must retry later. This is an operational constraint documented in the API.

**Alternative for member role:** Members (`role='member'`) do not get `org_key_members` rows. They can access org workspaces and see org credential names but cannot decrypt org secrets directly — org credentials are injected server-side during workspace activation (the server holds the org DEK cache, not the member).

### Workspace Org Attribution

A workspace can be created under an org by passing `orgID` in `CreateWorkspaceRequest`. The creating user must be an org member (any role). The workspace CRD `spec.owner` carries both `userID` (the creator) and `orgID` (the org, if set).

```go
// pkg/apis/llmsafespace/v1/workspace_types.go
type WorkspaceOwner struct {
    UserID string  `json:"userID"`
    OrgID  string  `json:"orgID,omitempty"`  // empty for personal workspaces
}
```

Ownership rules:
- A workspace with `orgID` set is accessible by all org members (read) and all org admins (full control)
- `verifyOwner` grants access if `userID == meta.UserID` OR user is an org member of `meta.OrgID`
- Workspace deletion is restricted to the creating user or org admins

### Credential Priority with Orgs

The `PrepareSecretsForInjection` merge order (from Epic 30) gains a new tier:

```
Explicit user binding (source_type='explicit', within_priority desc)
  beats
Auto user binding   (source_type='auto', owner_type='user', within_priority desc)
  beats
Explicit org binding (source_type='explicit', owner_type='org', within_priority desc)
  beats
Auto org binding    (source_type='auto', owner_type='org', within_priority desc)
  beats
Admin auto binding  (source_type='auto', owner_type='admin', within_priority desc)
```

The existing `GetWorkspaceCredentials` query in `pg_credential_store.go` already sorts by `(source_type='explicit') DESC, within_priority DESC` — the org tier slots between user-auto and admin-auto by setting appropriate `within_priority` values at seeding time (org auto-apply seeded with `within_priority=5`, user auto seeded with `within_priority=10`, admin auto seeded with `within_priority=0`).

### API Surface

```
Organizations:
  POST   /api/v1/orgs                           — create org (authenticated user becomes founding admin)
  GET    /api/v1/orgs                           — list orgs for current user
  GET    /api/v1/orgs/:id                       — get org (any member)
  PUT    /api/v1/orgs/:id                       — update org name/slug (admin only)
  DELETE /api/v1/orgs/:id                       — soft-delete org (admin only; must have no active workspaces)

Members:
  GET    /api/v1/orgs/:id/members               — list members (any member)
  POST   /api/v1/orgs/:id/members               — add member/admin (admin only; creates pending_key_wrap if role=admin)
  DELETE /api/v1/orgs/:id/members/:userID       — remove member (admin only; cannot remove last admin)
  POST   /api/v1/orgs/:id/accept-key            — complete admin key handshake (invited user + password)

Org Credentials:
  POST   /api/v1/orgs/:id/credentials           — create org LLM credential (admin only)
  GET    /api/v1/orgs/:id/credentials           — list org credentials (admin only; names + providers, never values)
  PUT    /api/v1/orgs/:id/credentials/:credID   — update org credential (admin only)
  DELETE /api/v1/orgs/:id/credentials/:credID   — delete org credential (admin only)

Org Auto-Apply:
  POST   /api/v1/orgs/:id/credentials/:credID/auto-apply   — create auto-apply rule for this org (admin only)
  GET    /api/v1/orgs/:id/credentials/:credID/auto-apply   — list auto-apply rules
  DELETE /api/v1/orgs/:id/credentials/:credID/auto-apply   — remove auto-apply rule

Workspaces (extensions):
  POST   /api/v1/workspaces                     — existing; accepts optional orgID in request body
  GET    /api/v1/orgs/:id/workspaces            — list workspaces for an org (any member)
```

---

## User Stories

### US-11.1: Schema Migration
### US-11.2: Org Key Service (OrgKeyService)
### US-11.3: Login — Unlock Org DEKs for Admin Members
### US-11.4: Org CRUD API
### US-11.5: Org Member Management + Key Handshake
### US-11.6: Org Credential CRUD
### US-11.7: `decryptBinding` Org Branch + `SeedWorkspaceCredentials` Fix
### US-11.8: Workspace Org Attribution (CRD + DB + Service + `verifyOwner`)
### US-11.9: Org DEK Rotation
### US-11.10: Password Change — Re-wrap Org DEK
### US-11.11: Frontend — Org Management UI
### US-11.12: Integration Tests + Canary

---

## Dependency Graph

```
US-11.1 (Schema)
  │
  ├─► US-11.2 (OrgKeyService)
  │       │
  │       ├─► US-11.9 (Org DEK rotation)
  │       └─► US-11.10 (Password change re-wrap)
  │
  ├─► US-11.4 (Org CRUD API)
  │       │
  │       ├─► US-11.3 (Login unlocks org DEKs)  ← depends on US-11.2 AND US-11.4
  │       │       (US-11.3 needs pgOrgStore from US-11.4 for SetOrgMemberStore)
  │       │
  │       └─► US-11.5 (Member management + key handshake)
  │               │
  │               └─► US-11.6 (Org credentials)
  │                       │
  │                       └─► US-11.7 (decryptBinding + SeedWorkspaceCredentials fix)
  │
  └─► US-11.8 (Workspace org attribution)

All of the above ─► US-11.11 (Frontend)
All of the above ─► US-11.12 (Integration tests)
```

**Critical path:** US-11.1 → US-11.2 + US-11.4 (parallel) → US-11.3 + US-11.5 (parallel, both need US-11.4) → US-11.6 → US-11.7

**Note:** US-11.3 depends on BOTH US-11.2 (OrgKeyService, which provides `UnlockAllOrgDEKs`) and US-11.4 (PgOrgStore, which `PgOrgKeyStore` queries for `GetOrgKeyMembersForUser`). US-11.3 does NOT require `SetOrgMemberStore` on the auth service — the batch unlock is internal to `OrgKeyService` and `PgOrgKeyStore`. US-11.3 and US-11.5 can be implemented in parallel once US-11.4 is complete.

---

## Non-Requirements (Explicitly Out of Scope)

| Item | Rationale |
|---|---|
| Org-level S3 shared folder | Deferred; US-10.7 (user S3) not built yet; org variant follows after |
| Org-level resource quotas | Epic 12 concern; billing must land first |
| Workspace-level access control beyond org membership | Future RBAC epic |
| Org-to-org federation / SSO | Enterprise tier |
| Transferring workspace ownership from user to org | Complex migration; separate story if needed |
| Audit log for org key events | Uses existing `secret_audit_log` table with `user_id` for now; add `org_id` column later |
| `virtual namespace` isolation per org-member | **⛔ Superseded by Epic 51** — tenant isolation now uses gVisor + admission webhook quotas in a shared namespace; no per-tenant namespaces. Org members are isolated by gVisor (container escape) + network policy (pod-to-pod) + per-tenant quotas, not namespace topology |
| Billing rollup by org | Epic 12 concern; `resolveBillingOwner` implementation is in Epic 12 and uses `workspaces.org_id` column added here |

---

## Threat Model Additions

| Threat | Mitigation |
|---|---|
| Compromised admin session retrieves org DEK from Redis | Redis DEK cache is AES-256-GCM encrypted with `LLMSAFESPACE_MASTER_SECRET` (Epic 34 US-34.2). Cache key `"org:<orgID>"` has same protections as `sessionID` keys. |
| Admin removed but still has org DEK in their session cache | Cache TTL matches session TTL (24h default, 30d with remember-me). Removal only prevents re-unlock on next login. For immediate revocation: org DEK rotation (US-11.9) generates a new org DEK, re-wraps for remaining admins, and the removed admin's cached copy is useless. |
| Member adds themselves as admin without existing admin approval | `POST /orgs/:id/members` requires `OrgAdminGuard`. New admin cannot complete Step 2 of key handshake without server having the org DEK in cache (requires existing admin to be active). |
| Org DEK leaked via `accept-key` endpoint (man-in-middle) | Endpoint is authenticated (JWT required) and validates both the membership invitation and the user's password-derived KEK. The org DEK is never returned in the response — it is only used server-side to wrap and store. |
| Password change invalidates org_key_members without org rotation | US-11.10 re-wraps the org DEK with the new KEK immediately on password change. If session expires between password change and re-wrap, the stale `org_key_members` row is detected at next admin login and returns an error, directing the admin to trigger org DEK rotation. |
| org_id collision in DEK cache (`"org:<orgID>"` clashes with `sessionID`) | `sessionID` values are JWT `jti` claims — UUID format (`xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx`). `"org:<orgID>"` is `"org:"` prefix + UUID. These namespaces cannot collide. |
