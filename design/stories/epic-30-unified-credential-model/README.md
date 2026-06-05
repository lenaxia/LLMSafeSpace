# Epic 30: Unified Credential Model

**Status:** Planning
**Created:** 2026-06-04
**Priority:** High
**Depends on:** Epic 10 (user secret store, materializer), Epic 27a (explicit reload, `workspace_agent_state`)
**Companion epics:** Epic 27b (bulk reload, drain mode — may run in parallel after US-30.2)

---

## Problem Statement

The credential system has diverged into two isolated, non-interacting subsystems that each solve part of the problem but never connect:

### System 1: User `llm-provider` secrets (Epic 10)

Per-user, zero-knowledge encrypted secrets. Users create them, bind them to workspaces, and they flow through the full pipeline: `PrepareSecretsForInjection` → `EnsureSecretsManifest` → agentd materializer → `FormatOpenCodeConfig` → `/tmp/agent-config.json` → opencode. This path works end-to-end. However, there is currently no UI for a user to create one — the `SecretsTab` lists generic secret types but `llm-provider` is absent from the user-facing settings.

### System 2: Admin `credential_sets` (Epic 9)

Platform-operator-managed credential sets. Admins create them, configure providers, set model allowlists, assign to users or all users. They are stored encrypted in `credential_sets` and managed via `AdminCredentialsTab`. However, nothing downstream reads from `credential_sets`. `PrepareSecretsForInjection` only reads `user_secret_bindings`. The auto-provision comment at `workspace_service.go:214` is a comment with no code. `credSvc` is wired only to `credentialsHandler` — it has zero connection to the pod injection pipeline. An admin spending an hour configuring credential sets today achieves nothing for workspace users.

### System 3 (implicit): The free-tier opencode key

`OPENCODE_AUTH_CONTENT = '{"opencode":{"type":"api","key":"public"}}'` is hardcoded in `pod_builder.go:487`. This feeds opencode's `AccountPlugin`, which runs independently of `FormatOpenCodeConfig` (which feeds `ConfigProviderPlugin`). Two plugin paths now process the same provider, with undefined precedence. When a user adds their own opencode credential, or when the free-tier key changes, there is no way to update it without redeploying the controller. And because `AccountPlugin` sets `hasKey=true`, all opencode models (including paid ones) appear in the catalog, requiring the `ListModels` handler to filter them out manually.

### The missing layer: Organizations

When organizations are added, org-level credential sets need to work. If orgs create a fourth separate subsystem, the complexity becomes unmanageable. The design must accommodate org credentials without structural changes.

### Root cause

The three systems use different tables (`user_secrets`, `credential_sets`), different encryption schemes (user DEK vs server KEK), different injection paths (one wired, one dead), and different semantics. There is no shared abstraction. Adding an org level would require a third table and third injection path.

---

## Goal

Replace the three-system split with a single unified `provider_credentials` table and a single injection pipeline that merges all sources by priority at `PrepareSecretsForInjection` time. Admin credentials auto-apply to workspaces. User credentials override admin ones for the same provider. Org credentials (when added) slot between the two. The materializer and formatter are unchanged — they already accept `[]LLMProviderData` from any source.

**What does NOT change:**
- `pkg/agentd/secrets` materializer — correct, well-tested, unchanged
- `pkg/agent/opencode/format.go` — pure function, unchanged
- The `LLMProviderFormatter` callback pattern — correct extension point for future agents
- The Epic 10 user-DEK crypto stack — correct, unchanged
- The `workspace_agent_state` / reload flow from Epic 27a — unchanged
- The `LLMProviderData` struct — unchanged

---

## Non-Goals

- Org-level credentials (designed for but not built — extension point only)
- Vault / HSM backend (interface is forward-compatible, not built here)
- Per-session model overrides (already supported via `PromptInput.model`)
- Model discovery from provider APIs (separate concern)
- Backwards compatibility with `credential_sets` table or `user_secrets` of type `api-key` for LLM use — straight cutover

---

## Stated Assumptions

Each will be verified at implementation time against specific files and lines.

| # | Assumption | Needs verification at |
|---|---|---|
| A1 | `PrepareSecretsForInjection` is implemented on `*SecretService` in `pkg/secrets/injection.go` and satisfies the `SecretInjector` interface declared in `workspace_service.go`. All injection paths call it via `s.secretInjector.PrepareSecretsForInjection(...)` at `workspace_service.go:1061`. | Verified in session. |
| A2 | `reset()` (called by `Materialize`) deletes `AgentConfigPath` unconditionally (line 408). `FlushProviders` is a no-op when no LLM providers are staged. `applyAPIKey` writes to `SecretsEnvPath`, not `AgentConfigPath`. Three writers exist for `AgentConfigPath`: `FlushProviders`, `injectRelayConfig`, and `applyWorkspaceConfig`. US-30.4 must add relay re-injection after every `Materialize` call. | Verified in session: `pkg/agentd/secrets/secrets.go:377,408,612`. |
| A3 | `credential_sets` has no FK reference from any other table except via `assigned_to JSONB`. Dropping it is a clean operation. | `api/migrations/000006_settings.up.sql` |
| A4 | `user_secrets` rows of `type='api-key'` that were created for LLM use are not in production (no users). Dropping them is safe. | Verified in session: `SELECT DISTINCT type FROM user_secrets` shows no `api-key` rows used for LLM credentials. |
| A5 | The `pkg/credentials` package (service, crypto, types) has no callers outside `api/internal/handlers/credentials.go` and `api/internal/app/app.go`. It can be deleted entirely. | `grep -r "pkg/credentials" --include="*.go"` |
| A6 | `OPENCODE_AUTH_CONTENT` is set in exactly one place: `pod_builder.go:487`. | `grep -r "OPENCODE_AUTH_CONTENT" --include="*.go"` |
| A7 | The `secrets.DeriveKEK` function used by the user-DEK path can also be used to derive the server KEK for admin credentials, keeping one crypto primitive. | `pkg/secrets/key_service.go` |
| A8 | `workspace_agent_state` (Epic 27a) has `workspace_id UUID` FK to `workspaces`. The new `workspace_credential_bindings` table follows the same pattern. | `api/migrations/000014_*` |
| A9 | No prior data exists. Straight cutover — no data migration needed. Old tables are dropped, new tables are created empty. Seeding happens at API startup. | Stated by user. |
| A10 | `AdminCredentialsTab` currently shows credential sets that have zero runtime effect. It will be replaced by the new admin credentials UI in this epic. | Verified in session. |

---

## Design

### Core abstraction: `ProviderCredential`

```go
type OwnerType string

const (
    OwnerTypeUser  OwnerType = "user"   // per-user DEK (zero-knowledge)
    OwnerTypeOrg   OwnerType = "org"    // per-org DEK (zero-knowledge, Epic 29+)
    OwnerTypeAdmin OwnerType = "admin"  // server KEK (not zero-knowledge by design)
)
```

One table. `provider` is stored in plaintext as a DB column (not only inside the ciphertext) for two reasons: (a) the UI needs to show "you already have an anthropic credential" without decrypting, and (b) `PrepareSecretsForInjection` needs to deduplicate by provider without decrypting everything.

```sql
CREATE TABLE provider_credentials (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_type      TEXT NOT NULL CHECK (owner_type IN ('user','org','admin')),
    owner_id        TEXT NOT NULL,
    name            TEXT NOT NULL,
    provider        TEXT NOT NULL,   -- plaintext provider ID, e.g. 'anthropic', 'openai'
    ciphertext      BYTEA NOT NULL,  -- AES-256-GCM encrypted LLMProviderData JSON
    key_version     INTEGER NOT NULL DEFAULT 1,
    model_allowlist TEXT[] NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One credential per provider per owner. name is a human label only and
    -- has no uniqueness constraint — the provider column enforces the real
    -- semantic uniqueness. A separate UNIQUE(name) would be redundant and
    -- confusing since users can't have two credentials for the same provider anyway.
    UNIQUE(owner_type, owner_id, provider)
);

CREATE INDEX idx_provider_creds_owner ON provider_credentials(owner_type, owner_id);
```

One binding table. Source type is the primary sort key; within_priority is secondary:

```sql
CREATE TABLE workspace_credential_bindings (
    credential_id    UUID NOT NULL REFERENCES provider_credentials(id) ON DELETE CASCADE,
    workspace_id     UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    source_type      TEXT NOT NULL DEFAULT 'explicit'
                         CHECK (source_type IN ('explicit', 'auto')),
    within_priority  INTEGER NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(credential_id, workspace_id)
);

CREATE INDEX idx_ws_cred_bindings_workspace ON workspace_credential_bindings(workspace_id);
```

Auto-apply rules:

```sql
CREATE TABLE credential_auto_apply (
    credential_id   UUID NOT NULL REFERENCES provider_credentials(id) ON DELETE CASCADE,
    target_type     TEXT NOT NULL CHECK (target_type IN ('user','org','all')),
    target_id       TEXT,           -- NULL when target_type='all'
    within_priority INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Two partial unique indexes handle NULL target_id correctly
-- (standard UNIQUE constraint treats NULL != NULL).
CREATE UNIQUE INDEX idx_cred_auto_apply_unique_targeted
    ON credential_auto_apply(credential_id, target_type, target_id)
    WHERE target_id IS NOT NULL;

CREATE UNIQUE INDEX idx_cred_auto_apply_unique_all
    ON credential_auto_apply(credential_id, target_type)
    WHERE target_id IS NULL;
```

### Encryption model

| Owner type | Encryption key | Requires active session? | Zero-knowledge? |
|---|---|---|---|
| `user` | HKDF(user_password, salt) → KEK → wraps DEK, cached in Redis on login | Yes — DEK in session cache | Yes |
| `org` | HKDF(org_admin_password, salt) → org KEK → wraps org DEK (Epic 29+) | Yes — org DEK cached on org admin login | Yes |
| `admin` | `deriveServerKey("provider-credentials")` from `LLMSAFESPACE_MASTER_SECRET` | No | No — operator with `MASTER_SECRET` can decrypt |

Both `user` and `admin` encryption are already implemented (`secrets.DeriveKEK` and `secrets_adapters.deriveServerKey` respectively). No new crypto code is needed.

### Priority merge at `PrepareSecretsForInjection`

The function receives `(ctx, userID, sessionID, workspaceID string)` and returns `[]byte` (secrets JSON for the materializer). The merge uses a **two-key sort**, not a single priority integer:

**Primary key: source type** — `explicit` always beats `auto`. An explicitly-bound credential with `within_priority=0` always wins over any auto-applied credential regardless of its `within_priority`.

**Secondary key: `within_priority`** — tie-breaks within the same source type. Higher integer wins.

```
Merge order (highest to lowest):
  1. explicit, within_priority DESC
  2. auto,     within_priority DESC

For each provider ID: first credential in merge order wins. Rest discarded.
```

**Single source of truth: `workspace_credential_bindings`.**

Both `explicit` and `auto` rows live in `workspace_credential_bindings`. `auto` rows are seeded at workspace-creation time (US-30.3) and via the backfill endpoint. `PrepareSecretsForInjection` reads **only** from `workspace_credential_bindings` — it never queries `credential_auto_apply` directly at injection time. This removes the dual-path ambiguity: the binding table is the sole authoritative state, and `credential_auto_apply` is a configuration-only table that drives seeding, not runtime reads.

This means:
- A user explicitly binding their own anthropic key always overrides any admin-provisioned anthropic key.
- An admin can set `within_priority` on auto-apply rules to control which admin credential seeds into new workspaces when multiple admins provision the same provider.
- `sessionID` is only needed when a `user`-owned credential survives the merge. If only `admin` credentials are present, the injection succeeds with no active session.

### Free-tier opencode key

Becomes a real admin credential, seeded by `EnsureFreeTierCredential` called from `app.go` on API startup (after DB connected and schema migrations complete):

```
owner_type: 'admin'
owner_id: '_platform'
name: 'opencode-free-tier'
provider: 'opencode'
value: JSON{provider:"opencode", apiKey:"public"}
```

With a `credential_auto_apply` row (`target_type='all'`, `within_priority=0`). Both rows are upserted atomically in a single transaction. Idempotent — safe on every API restart. Skips with a Warn log if `LLMSAFESPACE_MASTER_SECRET` is not set.

`OPENCODE_AUTH_CONTENT` is removed from `pod_builder.go`. The free-tier key now flows through the same `FormatOpenCodeConfig` path as everything else. No more AccountPlugin / ConfigProviderPlugin conflict.

### `classifyTier` → availability-aware

`classifyTier` currently hardcodes `providerID == "opencode"` to identify free-tier models. This breaks once users add their own credentials: their anthropic models should appear as "available", not "paid/unavailable".

The new approach: `ListModels` derives `loadedProviders` directly from the live catalog response (not the DB), then passes it to `annotateModels`. A model is classified by `classifyAvailability`:

```go
type ModelAvailability string

const (
    ModelAvailable   ModelAvailability = "available"  // provider loaded, model usable
    ModelUnavailable ModelAvailability = "unavailable" // provider not loaded in pod
    ModelFreeTier    ModelAvailability = "free"        // zero-cost opencode model, proxied
)

// isZeroCostOpencode returns true for opencode-provider models where all cost
// entries are zero AND the workspace is using the platform's free-tier key
// (apiKey="public"). Returns false when the user has their own opencode key —
// in that case zero-cost models must NOT trigger relay injection since the relay
// is the platform's proxy, not a path the user's own key should traverse.
//
// platformOpencode is true when the workspace has no user-owned credential for
// the "opencode" provider (meaning the platform free-tier key is in use).
func isZeroCostOpencode(providerID string, cost []opencodeCost, platformOpencode bool) bool {
    if providerID != "opencode" || !platformOpencode {
        return false
    }
    for _, c := range cost {
        if c.Input > 0 || c.Output > 0 {
            return false
        }
    }
    return true
}
```

The `annotatedModel` response gains `Availability ModelAvailability`. The existing `Tier string` and `FreeTier bool` fields are kept for one release cycle for frontend compatibility, then deprecated.

### `SetModel` serial round-trips fix

`SetModel` currently makes 3–4 sequential HTTP calls to the pod. Collapse to 1 fetch + fire-and-forget pushes:

```
1. Fetch catalog once (GET /api/model) → derive: modelExists, isFreeTier
2. Validate model exists — return 400 if not
3. Store model selection in DB (synchronous, authoritative)
4. Fire-and-forget goroutine:
   a. PATCH /global/config {model: req.Model}
   b. if isFreeTier: PUT /auth/opencode {baseURL: relayURL}
      else:          PUT /auth/opencode {baseURL: ""}
5. Return 200 immediately (applied: true when pod was running)
```

The fire-and-forget failures are tolerable: the model selection is durable in the DB, and the next pod boot or explicit reload will pick it up.

### Model cache

Replace the in-process `modelCacheMap` (process-local, replica-unaware) with a Redis key:

```
key: "model-catalog:{workspaceID}"
TTL: 5s (unchanged)
value: raw bytes from /api/model response
```

On `SetModel` success, `DEL model-catalog:{workspaceID}` (instead of the current `evictModelCache`).

The `modelHTTPClient` and `passwordGetter` remain unchanged. Only the cache storage moves to Redis.

---

## User Stories

| Story | Title | Depends On |
|---|---|---|
| US-30.1 | Schema: `provider_credentials`, `workspace_credential_bindings`, `credential_auto_apply`; drop `credential_sets`; drop `pkg/credentials` package and all wiring in `app.go` | None |
| US-30.2 | Admin credential CRUD API (`/admin/provider-credentials`) + server-KEK encryption | US-30.1 |
| US-30.3 | Admin credential auto-apply: create/delete `credential_auto_apply` rows; hook into workspace creation to seed `workspace_credential_bindings` | US-30.2 |
| US-30.4 | Replace free-tier opencode key: API startup seeds `opencode-free-tier` admin credential + auto-apply; remove `OPENCODE_AUTH_CONTENT` from `pod_builder.go`. **Gate: must verify `apiKey:"public"` via ConfigProviderPlugin produces same free-tier model catalog as OPENCODE_AUTH_CONTENT before shipping.** | US-30.2, US-30.3 |
| US-30.5 | Rewrite `PrepareSecretsForInjection`: multi-source query, priority merge, multi-key decrypt | US-30.1, US-30.4 |
| US-30.6 | User LLM provider CRUD API (`/api/v1/provider-credentials`) + user-DEK encryption | US-30.1 |
| US-30.7 | User LLM provider UI: new "LLM Providers" tab in user settings (create, list, bind to workspace) | US-30.6 |
| US-30.8 | Admin credential set UI: replace `AdminCredentialsTab` with unified admin provider credentials view | US-30.2, US-30.3 |
| US-30.9 | `classifyAvailability`: replace `classifyTier` hardcode; derive loaded provider set from live catalog response | US-30.4 |
| US-30.10 | `SetModel` serial round-trip fix: single catalog fetch, fire-and-forget pod pushes | US-30.11 |
| US-30.11 | Model cache: move from process-local map to Redis | None (independent) |
| US-30.12 | Integration test: full credential precedence flow (admin free-tier < user credential; same provider override) | US-30.4, US-30.5, US-30.6 |
| US-30.13 | Canary scenario: `s-cred-provider-precedence` — verifies admin, user, and override paths end-to-end in cluster | US-30.12 |
| US-30.14 | Worklog: design rationale, cutover decisions, retrospective | All |

### Dependency graph

```
US-30.1 (schema + drop old tables)
  ├─→ US-30.2 (admin CRUD API)
  │     ├─→ US-30.3 (auto-apply + workspace seeding hook)
  │     │     ├─→ US-30.4 (free-tier key at startup; remove OPENCODE_AUTH_CONTENT)
  │     │     │     ├─→ US-30.5 (rewrite PrepareSecretsForInjection)
  │     │     │     │     └─→ US-30.12 (integration test)
  │     │     │     ├─→ US-30.9 (classifyAvailability)
  │     │     │     └─→ US-30.8 (admin UI)
  │     │     └─→ US-30.8 (admin UI)
  └─→ US-30.6 (user CRUD API)
        └─→ US-30.7 (user UI)
        └─→ US-30.12 (integration test)

US-30.11 (Redis cache)  ── independent, can start any time
US-30.11 → US-30.10 (SetModel fix uses Redis eviction)

US-30.12 ── US-30.13 (canary)
US-30.13 ── US-30.14 (worklog)
```

### Critical path

```
US-30.1 → US-30.2 → US-30.3 → US-30.4 → US-30.5
                                         → US-30.9
US-30.1 → US-30.6 → US-30.7

US-30.4 + US-30.5 + US-30.6 → US-30.12 → US-30.13 → US-30.14
```

US-30.10 and US-30.11 are independent improvements that can ship ahead of or alongside the main track.

US-30.7 (user LLM provider UI) can ship as soon as US-30.6 is done — it does not need US-30.4 or US-30.5. This means users can add their own credentials and see them in the model list before the admin/free-tier unification lands.

---

## Cutover Runbook

The deployment order matters. Shipping stories out of order causes regressions. This section prescribes the exact order and the actions needed after each step.

### Required pre-flight checks

Before deploying ANY Epic 30 story:
1. **Epic 27a must be deployed.** `workspace_agent_state` table must exist. Verify: `SELECT COUNT(*) FROM workspace_agent_state LIMIT 1;`
2. **`LLMSAFESPACE_MASTER_SECRET` must be set in the K8s credentials Secret.** The startup backfill calls `EnsureFreeTierCredential` which returns early silently if the master secret is absent — leaving all existing workspaces without free-tier bindings. Verify: `kubectl get secret llmsafespace-credentials -o jsonpath='{.data.master-secret}'` returns a non-empty value.
3. **`SELECT COUNT(*) FROM user_secrets WHERE type='llm-provider'`** must return 0. If non-zero, a migration step is required before US-30.5 deploys.

### Required deployment order

```
Step 1: US-30.1  — Schema migration (new tables, drop credential_sets)
Step 2: US-30.2  — Admin credential CRUD API
Step 3: US-30.3  — Auto-apply + CreateWorkspace seeding hook
Step 4: US-30.11 — Redis model cache (independent; can land before step 1)
Step 5: US-30.4  — Remove OPENCODE_AUTH_CONTENT from controller; API startup seeding
                   (EnsureFreeTierCredential seeds DB + auto-backfills existing workspaces)
Step 6: US-30.5  — Rewrite PrepareSecretsForInjection (wires credStore + deriveAdminKey)
Step 7: US-30.9  — classifyAvailability
Step 8: US-30.6  — User LLM provider CRUD API
Step 9: US-30.10 — SetModel round-trip fix
```

**US-30.4 MUST be deployed before US-30.5.** US-30.5 activates the new injection path. If deployed before US-30.4, the free-tier credential row does not exist, all workspaces get empty `providerData` — a regression. US-30.4 is now step 5 and US-30.5 is step 6, correcting the prior ordering error.

**US-30.4 (removing `OPENCODE_AUTH_CONTENT`) should deploy as a coordinated Helm upgrade** that includes both the updated controller (no `OPENCODE_AUTH_CONTENT`) and the updated API (with `EnsureFreeTierCredential` at startup). Note: Helm uses rolling updates, not atomic pod replacement. During the rollout there is a brief window where old and new controller pods coexist. A workspace created by a new controller pod before any new API pod has run `EnsureFreeTierCredential` will have neither `OPENCODE_AUTH_CONTENT` nor a free-tier binding. These are self-healing — the startup backfill on the next API pod start (typically within seconds) covers them. The Helm upgrade sequence is:
1. Migration job runs (pre-upgrade hook) — schema already exists from step 1, no-op.
2. API pod starts — `EnsureFreeTierCredential` seeds the credential + auto-apply row. It also automatically backfills `workspace_credential_bindings` for all existing workspaces (up to 1000 synchronously, remainder queued). This ensures existing workspaces have the free-tier binding before the controller deploys.
3. Controller pod starts — pods built after this point get no `OPENCODE_AUTH_CONTENT`.
4. New workspaces created after step 3 get the free-tier binding via `CreateWorkspace` seeding.

**Window between steps 3 and 6 (US-30.3 through US-30.4 deployment gap):** Between when `CreateWorkspace` seeding is wired (step 3) and when `EnsureFreeTierCredential` seeds the credential row (step 6), `SeedWorkspaceCredentials` will query `credential_auto_apply` and find zero rows (the free-tier `credential_auto_apply` row doesn't exist yet). It silently inserts zero bindings — not a bug, just a no-op. Workspaces created in this window are handled by the automatic startup backfill at step 6. No manual action required.

### Existing workspace pods after cutover

Workspace pods running before step 6 still have `OPENCODE_AUTH_CONTENT` in their process environment (it was injected at pod creation time and is not retroactively removed). They continue to work via `AccountPlugin` until they restart.

When an existing workspace pod restarts after step 6 (new workspace created, pod deleted, workspace resumed from suspend):
- It gets no `OPENCODE_AUTH_CONTENT` (controller no longer injects it).
- `PrepareSecretsForInjection` runs at pod activation to build `secrets.json`.
- For the pod to get the free-tier opencode key, its workspace must have a `workspace_credential_bindings` row for the `opencode-free-tier` credential.

**Existing workspaces created before step 3 (CreateWorkspace seeding hook) have no such row.** After cutover, when these pods restart, they get no free-tier models.

**Recovery action (automatic):** `EnsureFreeTierCredential` at API startup automatically backfills existing workspaces. No manual admin action is required post-cutover. The startup backfill also triggers `MarkCredentialChanged` for each affected workspace so the reload banner appears. After users reload (or pods restart), free-tier models appear.

---

### US-30.1 — Schema

Migration `000015_unified_credential_model.up.sql`:

```sql
-- Drop the legacy admin credential sets system entirely.
-- credential_sets has no FK references from other tables (assigned_to is JSONB, not a FK).
DROP TABLE IF EXISTS credential_sets;

-- New unified table for all LLM provider credentials.
-- One row = one provider credential for one owner.
-- Admin creates one row per provider; user creates one row per provider.
-- There is no "credential set" grouping at the DB level — that is a UI concern.
--
-- `provider` is stored in plaintext (not only in the ciphertext) so that:
--   (a) the UI can show "you already have an anthropic credential" without decryption.
--   (b) PrepareSecretsForInjection can deduplicate by provider before decrypting.
--   (c) mergeCredentials can sort and skip duplicate providers cheaply.
-- The unique constraint is on (owner_type, owner_id, provider).
-- `name` has no separate unique constraint — it is a human label only.
-- A user can have two credentials with the same name if they are for different providers
-- (though the provider uniqueness constraint already prevents two credentials for the same
-- provider from the same owner). No name-level uniqueness is enforced or needed.
CREATE TABLE provider_credentials (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_type      TEXT NOT NULL CHECK (owner_type IN ('user', 'org', 'admin')),
    owner_id        TEXT NOT NULL,
    -- 'user'  → user_id (FK to users.id)
    -- 'org'   → org_id (FK to organizations.id, Epic 29+)
    -- 'admin' → '_platform' (literal sentinel, not a FK)
    name            TEXT NOT NULL,
    provider        TEXT NOT NULL,  -- plaintext provider ID, e.g. 'anthropic', 'openai'
    ciphertext      BYTEA NOT NULL,        -- AES-256-GCM encrypted LLMProviderData JSON
    key_version     INTEGER NOT NULL DEFAULT 1,
    -- model_allowlist: non-empty means only these model IDs are included when this
    -- credential is merged into a secrets batch. Empty array = all models allowed.
    -- The merge step in PrepareSecretsForInjection maps this to LLMProviderData.Models
    -- before passing to FormatOpenCodeConfig. See US-30.5 for the mapping logic.
    model_allowlist TEXT[] NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(owner_type, owner_id, provider)  -- one credential per provider per owner
);

CREATE INDEX idx_provider_creds_owner ON provider_credentials(owner_type, owner_id);

CREATE TRIGGER trg_provider_credentials_updated_at
    BEFORE UPDATE ON provider_credentials
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Explicit workspace bindings (user or admin chooses to bind a specific credential
-- to a specific workspace).
-- Priority scheme (two-key sort, not a single integer):
--   1. source_type: 'explicit' always beats 'auto' in mergeCredentials.
--   2. within_priority: tie-breaks within the same source_type (higher wins).
-- Using two columns rather than a single integer prevents the ambiguity where
-- an explicit binding with within_priority=0 could be outranked by an auto-apply
-- entry with within_priority=50.
CREATE TABLE workspace_credential_bindings (
    credential_id    UUID NOT NULL REFERENCES provider_credentials(id) ON DELETE CASCADE,
    workspace_id     UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    source_type      TEXT NOT NULL DEFAULT 'explicit'
                         CHECK (source_type IN ('explicit', 'auto')),
    within_priority  INTEGER NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(credential_id, workspace_id)
);

CREATE INDEX idx_ws_cred_bindings_workspace ON workspace_credential_bindings(workspace_id);
CREATE INDEX idx_ws_cred_bindings_credential ON workspace_credential_bindings(credential_id);

-- Auto-apply rules: admin or org credentials that apply to a target automatically.
-- When a new workspace is created, CreateWorkspace seeds workspace_credential_bindings
-- with source_type='auto' for each matching auto-apply rule.
-- When a credential is updated after workspace creation, existing auto-applied bindings
-- are NOT retroactively updated — the user must trigger a reload (Epic 27a banner).
CREATE TABLE credential_auto_apply (
    credential_id   UUID NOT NULL REFERENCES provider_credentials(id) ON DELETE CASCADE,
    target_type     TEXT NOT NULL CHECK (target_type IN ('user', 'org', 'all')),
    target_id       TEXT,
    -- target_type='user'  → user_id; only that user's workspaces get this credential
    -- target_type='org'   → org_id; all org-member workspaces get this credential
    -- target_type='all'   → NULL; every workspace gets this credential
    within_priority INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Unique constraint on (credential_id, target_type, target_id).
-- NULL target_id (target_type='all') requires a partial unique index because
-- NULL != NULL in standard SQL UNIQUE constraints.
CREATE UNIQUE INDEX idx_cred_auto_apply_unique_targeted
    ON credential_auto_apply(credential_id, target_type, target_id)
    WHERE target_id IS NOT NULL;

CREATE UNIQUE INDEX idx_cred_auto_apply_unique_all
    ON credential_auto_apply(credential_id, target_type)
    WHERE target_id IS NULL;

CREATE INDEX idx_cred_auto_apply_all  ON credential_auto_apply(target_type) WHERE target_type = 'all';
CREATE INDEX idx_cred_auto_apply_user ON credential_auto_apply(target_id)   WHERE target_type = 'user';
CREATE INDEX idx_cred_auto_apply_org  ON credential_auto_apply(target_id)   WHERE target_type = 'org';

-- Job state for async backfill operations.
CREATE TABLE credential_backfill_jobs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    credential_id UUID NOT NULL REFERENCES provider_credentials(id) ON DELETE CASCADE,
    status        TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running','complete','failed')),
    processed     INTEGER NOT NULL DEFAULT 0,
    errors        JSONB NOT NULL DEFAULT '[]',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Since no production users or data exist at cutover, the migration comment
-- describing a two-phase Go-based migration is REMOVED. There is no cmd/migrate
-- binary in this codebase and no migration tooling capable of running Go code.
-- The migrate/migrate runner is SQL-only.
--
-- All credential seeding (including the free-tier opencode key) happens at
-- API server startup via EnsureFreeTierCredential (see US-30.4).
-- No SQL INSERT for existing rows is needed here.
```

**Down migration** (`000015_unified_credential_model.down.sql`):

```sql
-- WARNING: DESTRUCTIVE. This down migration does NOT restore credential data.
DROP TABLE IF EXISTS credential_backfill_jobs;  -- must drop before provider_credentials (FK)
DROP TABLE IF EXISTS credential_auto_apply;
DROP TABLE IF EXISTS workspace_credential_bindings;
DROP TABLE IF EXISTS provider_credentials;

-- Recreate credential_sets shell (empty — no data restored).
CREATE TABLE credential_sets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT UNIQUE NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT false,
    providers_encrypted BYTEA NOT NULL,
    key_version SMALLINT NOT NULL DEFAULT 1,
    model_allowlist TEXT[] NOT NULL DEFAULT '{}',
    assigned_to JSONB NOT NULL DEFAULT '"all"',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Note on `user_secrets` table:** The `user_secrets` table is NOT dropped. It continues to hold `ssh-key`, `git-credential`, `secret-file`, and `env-secret` rows. No tombstones — there are no LLM credentials in production to migrate.

**Critical: `user_secrets.llm-provider` rows must NOT be silently abandoned.** `buildNonLLMSecrets` filters out `SecretTypeLLMProvider` type rows from `user_secrets`. This is correct for the transition once US-30.5 is deployed — LLM credentials now come from `provider_credentials`. However, if any user created an `llm-provider` secret in `user_secrets` before US-30.5 deploys, those entries are silently excluded after cutover. US-30.5 must ensure that `user_secrets.llm-provider` rows are either: (a) migrated to `provider_credentials` before US-30.5 ships (requires a one-time migration step), or (b) read by `buildNonLLMSecrets` and forwarded through the new path as a compatibility shim. Since A4 asserts no LLM credentials exist in production, option (a) applies — the migration is a no-op. But this assumption must be verified before US-30.5 deploys via: `SELECT COUNT(*) FROM user_secrets WHERE type='llm-provider'`. If non-zero, a migration step is required before US-30.5 goes live.

**Deletion of `pkg/credentials` and old wiring in `app.go`:** US-30.1 includes deleting the entire `pkg/credentials` package (service, crypto, types). This has **three** import sites that must all be cleaned up:
1. `api/internal/handlers/credentials.go` — the old `AdminCredentialsHandler`; delete file, remove routes
2. `api/internal/app/app.go` — `credSvc`, `loadCredentialKeySet`, `credentialsHandler` variables; remove construction and wiring
3. `api/internal/services/database/credentials.go` — implements `credentials.Store` interface with `GetCredentialSet`, `ListCredentialSets`, etc.; **delete this file entirely**. The new credential DB methods go in `pkg/secrets/pg_secret_store.go`, not here.

Missing the `database/credentials.go` deletion causes a compile error when `pkg/credentials` is deleted.

### US-30.2 — Admin Credential CRUD API

**One row per provider, one request per provider.** There is no "credential set" grouping at the API level. If an admin wants to configure both anthropic and openai, they make two POST calls. The admin UI (US-30.8) presents a polished multi-provider form that fires multiple requests — grouping is a UI concern, not an API or DB concern.

New routes under `AdminGuard`:
```
POST   /api/v1/admin/provider-credentials
GET    /api/v1/admin/provider-credentials
GET    /api/v1/admin/provider-credentials/:id
PUT    /api/v1/admin/provider-credentials/:id
DELETE /api/v1/admin/provider-credentials/:id
```

Handler: `AdminProviderCredentialsHandler` in `api/internal/handlers/admin_provider_credentials.go`.

Encryption: `deriveServerKey("provider-credentials")` → 32-byte server KEK → `secrets.EncryptSecret(kek, plaintext)`. Decryption uses the same derived key. No session required.

**Nil KEK behavior (missing or too-short `LLMSAFESPACE_MASTER_SECRET`):** `deriveServerKey` returns `nil` for both absent and too-short keys (< 32 hex chars) — both are indistinguishable at runtime. The handler MUST check for nil KEK explicitly and return `503 Service Unavailable` with a message indicating the master secret is not configured. Returning 500 or propagating the `aes.NewCipher` error would leak internal details. GET/LIST operations must also degrade gracefully: `BaseURL` is omitted, other fields are returned normally (they come from plaintext columns).

Request body:
```go
type CreateAdminCredentialRequest struct {
    Name           string   `json:"name" binding:"required"`
    Provider       string   `json:"provider" binding:"required"`  // e.g. "anthropic", "openai"
    APIKey         string   `json:"apiKey" binding:"required"`
    BaseURL        string   `json:"baseURL,omitempty"`
    ModelAllowlist []string `json:"modelAllowlist,omitempty"`
}
```

Response:
```go
type AdminCredentialResponse struct {
    ID             string   `json:"id"`
    Name           string   `json:"name"`
    Provider       string   `json:"provider"`
    BaseURL        string   `json:"baseURL,omitempty"`  // requires decrypt — see note
    ModelAllowlist []string `json:"modelAllowlist"`
    CreatedAt      string   `json:"createdAt"`
    UpdatedAt      string   `json:"updatedAt"`
    // APIKey is NEVER returned. Not in GET, not in list, not in create response.
}
```

**Note on `BaseURL`:** `BaseURL` is stored inside the AES-GCM ciphertext as part of `LLMProviderData` — it is not a plaintext column. Every GET and list response therefore requires decryption using `deriveServerKey("provider-credentials")`. If `LLMSAFESPACE_MASTER_SECRET` is absent, `BaseURL` is omitted from the response (field is `omitempty`). The handler must tolerate a nil KEK gracefully: log a Warn and return the response without `BaseURL`. This is consistent with other admin endpoints that degrade when the master secret is absent.

`DELETE /api/v1/admin/provider-credentials/:id` deletes one row. Cascades to `workspace_credential_bindings` and `credential_auto_apply` via FK. Running workspaces are NOT automatically notified — they retain the credential until next pod boot or explicit reload.

### US-30.3 — Auto-apply

New routes:
```
POST   /api/v1/admin/provider-credentials/:id/auto-apply
DELETE /api/v1/admin/provider-credentials/:id/auto-apply/:target_type/:target_id
GET    /api/v1/admin/provider-credentials/:id/auto-apply
```

**Seeding at workspace creation:** On `CreateWorkspace`, the workspace service queries `credential_auto_apply` for rows matching `target_type='all'` OR `target_type='user' AND target_id=userID` and inserts `workspace_credential_bindings` rows with `source_type='auto'` for each match.

This requires a new field on `workspace.Service` and wiring in `app.go`:

```go
// api/internal/services/workspace/workspace_service.go
type Service struct {
    // ... existing fields ...
    credProvisioner CredentialProvisioner  // NEW — nil-safe
}

// CredentialProvisioner seeds workspace_credential_bindings from credential_auto_apply.
// Satisfied by the credential DB layer (pkg/secrets/pg_secret_store.go).
type CredentialProvisioner interface {
    SeedWorkspaceCredentials(ctx context.Context, workspaceID, userID string) error
}

func (s *Service) SetCredentialProvisioner(cp CredentialProvisioner) {
    s.credProvisioner = cp
}
```

In `CreateWorkspace` (at the existing `// Auto-provision default credentials if enabled` comment, line 214):

```go
if s.credProvisioner != nil {
    if err := s.credProvisioner.SeedWorkspaceCredentials(ctx, workspaceID, userID); err != nil {
        // Non-fatal: workspace is created, binding is best-effort.
        // Missing binding is recovered via backfill endpoint.
        s.logger.Warn("credential auto-apply seeding failed", "workspaceID", workspaceID, "error", err)
    }
}
```

Wired in `api/internal/app/app.go` (inside the secrets `{}` block where `pgStore` is in scope — same location as `wsSvc.SetSecretInjector(secretService)`):

```go
wsSvc.SetCredentialProvisioner(pgStore)  // *PgSecretStore satisfies CredentialProvisioner
```

`SeedWorkspaceCredentials` is implemented on `*PgSecretStore` — it queries `credential_auto_apply` and bulk-inserts `workspace_credential_bindings` with `source_type='auto'`.

**Explicit bind overwrites auto:** If a user calls `POST /api/v1/provider-credentials/:id/bind/:workspaceId` for a credential that already has a `source_type='auto'` binding (seeded at creation), the bind endpoint performs an `INSERT ... ON CONFLICT (credential_id, workspace_id) DO UPDATE SET source_type='explicit', within_priority=$within_priority`. This is intentional: the user explicitly choosing a credential is a stronger signal than the auto-apply default. The `source_type` column transitions from `auto` → `explicit` and the slot can no longer be reclaimed by auto-apply without an explicit unbind.

**Remediation for existing workspaces when a new auto-apply rule is added:** Workspaces created before the rule was added do not get the new credential automatically — only new workspaces do. Retroactive application requires an admin action. This is by design (avoid surprising workspace owners with unexpected credential additions). The admin API exposes a backfill endpoint:

```
POST /api/v1/admin/provider-credentials/:id/auto-apply/backfill
```

This endpoint is **asynchronous** to avoid holding an HTTP connection while iterating potentially thousands of workspaces. The handler:
1. Validates the credential and auto-apply rule exist.
2. Creates a job row in a new `credential_backfill_jobs` table (`id UUID, credential_id UUID, status TEXT, processed INT, errors JSONB, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ`).
3. Returns `202 Accepted` with `{"jobId": "<uuid>"}`.
4. Enqueues a background goroutine that:
   - Queries all workspaces matching the auto-apply target in pages of 100.
   - For each page: inserts `source_type='auto'` bindings for workspaces that don't already have any binding for this credential.
   - Calls `MarkCredentialChanged` for each affected workspace.
   - Updates the job row's `processed` count and appends to `errors` on failure.
   - Sets `status='complete'` or `status='failed'` when done.
5. Job status is queryable via `GET /api/v1/admin/provider-credentials/:id/auto-apply/backfill/:jobId`.

The `credential_backfill_jobs` table is defined in the US-30.1 migration. Job rows are retained for 7 days (a cleanup cron or TTL job is out of scope; manual deletion via `DELETE` is acceptable).

**Automatic startup backfill:** Rather than requiring a manual admin call, `EnsureFreeTierCredential` (called at API startup) also triggers a backfill for any workspace that lacks the free-tier binding. This is idempotent — it uses the same `SeedWorkspaceCredentials` logic with a batch query. On first boot after cutover, this runs synchronously in the startup path (capped at 1000 workspaces before switching to async) and seeds all existing workspaces before the API begins serving requests. For deployments with > 1000 workspaces, the remainder is queued as a background backfill job automatically.

If `MarkCredentialChanged` fails for a workspace (e.g. DB blip), the job logs the failure and continues — partial backfill is acceptable since the credential binding itself succeeded and the affected workspace will receive the credential on next pod boot regardless.

The `CredentialProvisioner` interface proposed in Epic 13 is not needed. The workspace service calls the credential DB layer directly, gated by a nil-safe interface following the existing pattern.

### US-30.4 — Free-tier key migration

**Remove from `pod_builder.go`:**
```go
// DELETE this line:
{Name: "OPENCODE_AUTH_CONTENT", Value: `{"opencode":{"type":"api","key":"public"}}`},
```

**Seeding mechanism — API server startup, not migration binary:**

The migration runner is `migrate/migrate` — a SQL-only Docker image. It cannot execute Go code. There is no `cmd/migrate/` binary in this codebase. `deriveServerKey` is a package-private function in `api/internal/app/secrets_adapters.go` — it cannot be called from `pkg/secrets` or from any binary outside `api/internal/app`.

`LLMSAFESPACE_MASTER_SECRET` is also not guaranteed to be set — `deriveServerKey` returns `nil` when unset, and `NewRedisDEKCache` treats a nil master key as "no encryption" rather than an error. Admin credential encryption therefore must degrade gracefully when the key is absent.

**The correct approach: seed on API startup, then backfill existing workspaces.**

These are two distinct operations that must both be called at startup:

```go
// In api/internal/app/app.go, inside the secrets {} block after DB init:

// 1. Seed the free-tier credential row + auto-apply row (idempotent upsert).
if pgStore != nil {
    if err := EnsureFreeTierCredential(ctx, pgStore, log); err != nil {
        log.Warn("free-tier credential seeding skipped", "error", err)
    } else {
        // 2. Backfill workspace_credential_bindings for existing workspaces.
        // Synchronous for <= 1000 workspaces, async beyond that.
        // Only runs if EnsureFreeTierCredential succeeded (credential row exists).
        if err := BackfillFreeTierBindings(ctx, pgStore, log); err != nil {
            log.Warn("free-tier workspace backfill failed", "error", err)
        }
    }
}
```

`EnsureFreeTierCredential` only upserts one `provider_credentials` row and one `credential_auto_apply` row. It does NOT touch `workspace_credential_bindings`. `BackfillFreeTierBindings` is a separate function that queries all workspaces lacking the free-tier binding, inserts `source_type='auto'` rows, and calls `MarkCredentialChanged` per affected workspace. It is idempotent.

Note: the wiring above replaces the old `credSvc` variable (the now-deleted `credentials.Service`). The new seeder uses `pgStore` directly, not `asyncAudit`, because `UpsertFreeTierCredential` is a platform operation that does not need audit logging.

```go
// In api/internal/app/secrets_adapters.go — co-located with deriveServerKey.
func EnsureFreeTierCredential(ctx context.Context, credSvc CredentialSeeder, log LoggerInterface) error {
    kek := deriveServerKey("provider-credentials")
    if kek == nil {
        return fmt.Errorf("LLMSAFESPACE_MASTER_SECRET not set; skipping free-tier credential seed")
    }
    plaintext := `{"provider":"opencode","apiKey":"public"}`
    ciphertext, err := secrets.EncryptSecret(kek, []byte(plaintext))
    if err != nil {
        return fmt.Errorf("encrypt free-tier key: %w", err)
    }
    return credSvc.UpsertFreeTierCredential(ctx, ciphertext)
}

// CredentialSeeder is a narrow interface satisfied by the credential DB layer.
// UpsertFreeTierCredential MUST wrap its two INSERTs (provider_credentials +
// credential_auto_apply) in a single transaction. This is an implementation
// contract, not just a recommendation — concurrent API replicas calling this
// simultaneously must not produce orphaned rows. PostgreSQL row-level locking
// serializes the upsert races safely, but only if both INSERTs are atomic.
type CredentialSeeder interface {
    UpsertFreeTierCredential(ctx context.Context, ciphertext []byte) error
}
```

`UpsertFreeTierCredential` is implemented on `*PgSecretStore` in `pkg/secrets/pg_secret_store.go` — the same file that implements all other credential DB operations. It is NOT placed in `api/internal/services/database/credentials.go` (that package is for workspace/user/config and does not implement `SecretStore`). `AsyncAuditLogger` gets a pass-through delegation for this method, as it does for all `SecretStore` methods.

**Why startup, not migration:**
- The API server is the only process that has `deriveServerKey` in scope.
- `LLMSAFESPACE_MASTER_SECRET` is already injected into the API container (it's the same secret used for Redis DEK cache encryption).
- Startup seeding is idempotent: `ON CONFLICT DO UPDATE` makes it safe to call on every boot.
- Failure is non-fatal: workspaces boot without the free-tier key, log a warning, and recover automatically on the next boot after the operator sets the master secret.

**Note on LLMSAFESPACE_MASTER_SECRET in the Helm chart:**
Currently `LLMSAFESPACE_MASTER_SECRET` is read by `deriveServerKey` via `os.Getenv` but is not injected in `api-deployment.yaml`. US-30.4 must add it as an optional env var referencing the `llmsafespace-credentials` K8s Secret:

```yaml
# In charts/llmsafespace/templates/api-deployment.yaml, add to env:
- name: LLMSAFESPACE_MASTER_SECRET
  valueFrom:
    secretKeyRef:
      name: {{ include "llmsafespace.secretName" . }}
      key: master-secret
      optional: true  # absence is tolerated; free-tier seeding is skipped with a warning
```

And add `master-secret` to the credentials Secret template (`secret.yaml`) with the same rotation-safe pattern used for `jwt-secret`.

**Unvalidated assumption requiring pre-implementation verification:** The design assumes that passing `apiKey:"public"` via `FormatOpenCodeConfig` → `ConfigProviderPlugin` produces the same model catalog behavior as passing `{"opencode":{"type":"api","key":"public"}}` via `OPENCODE_AUTH_CONTENT` → `AccountPlugin`. These are different plugin paths in opencode. **Before shipping US-30.4, this MUST be verified in a live test environment** by:
1. Removing `OPENCODE_AUTH_CONTENT` from a test pod.
2. Writing a `config.json` with `apiKey:"public"` for the opencode provider via `FormatOpenCodeConfig`.
3. Querying `GET /api/model` and confirming free-tier models appear.

If the behavior differs (e.g., `ConfigProviderPlugin` does not enable the opencode provider's free-tier models), US-30.4 will ship a regression. This verification is a gate for US-30.4, not an assumption.

**IMPORTANT — relay config wipe on every `Materialize` call:** `reset()` (called by `Materialize` at `pkg/agentd/secrets/secrets.go:377`) calls `m.FS.Remove(m.Paths.AgentConfigPath)` at line 408. This happens on EVERY `Materialize` call — not just when there are LLM providers, and not in `FlushProviders`. It wipes `AgentConfigPath` unconditionally. Then `FlushProviders` is a no-op if `len(m.stagedProviders) == 0` (no LLM providers in the batch). Result: after any credential reload that contains only non-LLM secrets (e.g., just an SSH key refresh), the opencode config file is gone and the relay config is gone — permanently, until the next pod restart runs `injectRelayConfig` at startup.

The relay re-injection in `secrets.go` must therefore run after EVERY `Materialize` call, not just when `FlushProviders` writes a config. The condition is: if `LLMSAFESPACE_RELAY_URL` is set, call `injectRelayConfig` unconditionally after any materialization. `injectRelayConfig` already handles the case where the file doesn't exist (creates a minimal config). This is safe to call even if `FlushProviders` was a no-op.

Also note: `AgentConfigPath` has THREE writers, not one as A2 claims:
1. `FlushProviders` (via `atomicWrite`) — only when LLM providers are staged
2. `injectRelayConfig` (`relay_config.go:31`) — at pod startup and now after each reload
3. `applyWorkspaceConfig` (`cmd/workspace-agentd/secrets.go:177-211`) — writes workspace-level opencode settings

A2's "only writer" claim is incorrect. The correctness argument for the relay fix doesn't depend on it being the only writer, but implementers should be aware all three functions touch the same file.

The fix is: the reload handler must re-read `LLMSAFESPACE_RELAY_URL` and call `injectRelayConfig` after any `FlushProviders` or after `Materialize` when staged providers exist. `relayBase` is a local variable in a different `main.go` scope — it is NOT available in `reloadSecretsHandler`. The handler must call `os.Getenv("LLMSAFESPACE_RELAY_URL")` directly:

```go
// In cmd/workspace-agentd/secrets.go, after FlushProviders:
if err := m.FlushProviders(opencode.FormatOpenCodeConfig); err != nil {
    // ... handle ...
}
// Re-inject relay config. Read env var directly since relayBase is not in scope here.
if relayURL := os.Getenv("LLMSAFESPACE_RELAY_URL"); relayURL != "" {
    relayBase := fmt.Sprintf("http://localhost:%d/relay/inference", agentd.AgentdPort)
    if injectErr := injectRelayConfig(agentd.AgentConfigPath, relayBase); injectErr != nil {
        log.Warn("relay config re-inject after reload failed", zap.Error(injectErr))
    }
}
```

Additionally, `reloadSecretsHandler` at `main.go:693` does NOT need a signature change — the relay re-injection reads the env var at call time. This is correct since `LLMSAFESPACE_RELAY_URL` is a stable env var set at pod creation, not a runtime variable.

### US-30.5 — Rewrite `PrepareSecretsForInjection`

New signature (same external interface, different implementation):

```go
func (s *Service) PrepareSecretsForInjection(
    ctx context.Context,
    userID, sessionID, workspaceID string,
) ([]byte, error)
```

**`CredentialStore` interface — added to `pkg/secrets`, not `api/internal`:**

`PrepareSecretsForInjection` is implemented on `*SecretService` in `pkg/secrets/injection.go` (verified: line 29). `pkg/` must not import from `api/internal/` so `CredentialStore` lives in `pkg/secrets/credential_store.go`.

```go
// pkg/secrets/credential_store.go
type CredentialStore interface {
    // GetWorkspaceCredentials returns all workspace_credential_bindings rows
    // for the given workspace, joined with provider_credentials, ordered by
    // source_type DESC ('explicit' > 'auto') THEN within_priority DESC THEN created_at ASC.
    // The ORDER BY is part of the interface contract — callers rely on it for priority.
    // Implementors MUST include: ORDER BY (source_type = 'explicit') DESC, within_priority DESC, created_at ASC
    GetWorkspaceCredentials(ctx context.Context, workspaceID string) ([]CredentialBinding, error)
}

type CredentialBinding struct {
    ID             string
    OwnerType      string   // 'user', 'org', 'admin'
    OwnerID        string   // user_id, org_id, or '_platform'.
                            // 'user': not used for DEK selection (sessionID used instead).
                            // 'org':  will be orgID when Epic 29+ implements org DEK lookup.
                            // Retained for logging and future org branch.
    Provider       string   // plaintext provider name e.g. "anthropic"
    Ciphertext     []byte
    KeyVersion     int
    ModelAllowlist []string
    SourceType     string   // 'explicit' or 'auto'
    WithinPriority int
}
```

**Wiring — `CredentialStore` is satisfied by `*PgSecretStore` and delegated by `AsyncAuditLogger`:**

`database.Service` (`api/internal/services/database`) is a workspace/user/config service — it does not implement `SecretStore` and would not implement `CredentialStore`. The credential DB methods belong in `pkg/secrets/pg_secret_store.go`, implemented on `*PgSecretStore`.

**The `AsyncAuditLogger` typed-store problem:** `AsyncAuditLogger.store` is typed as `SecretStore` (interface), not `*PgSecretStore` (concrete type). Adding `GetWorkspaceCredentials` to `*PgSecretStore` does NOT automatically make `AsyncAuditLogger` capable of delegating it via `l.store.GetWorkspaceCredentials(...)` — `SecretStore` doesn't have that method. Two clean solutions exist:

**Option A (preferred):** Define a combined interface `SecretAndCredentialStore` that embeds both `SecretStore` and `CredentialStore`. Change `AsyncAuditLogger.store` to this combined type. `*PgSecretStore` satisfies both. `AsyncAuditLogger` delegates `GetWorkspaceCredentials` and `UpsertFreeTierCredential` to `l.store`.

When Option A is used, the `SecretService.credStore` field can be eliminated — `s.store` already satisfies `CredentialStore` via the combined interface, so `s.store.GetWorkspaceCredentials(...)` works directly. The `SetCredentialStore` method and `credStore` field become unnecessary. The nil check in `PrepareSecretsForInjection` becomes `if s.store == nil || s.deriveAdminKey == nil` (store is always non-nil in production since `NewSecretService` requires it). Use `HasAdminKeyDeriver()` as the sole gate for the new path:

```go
// Simplified: with Option A, credStore field is dropped from SecretService.
// The new path is active when deriveAdminKey is wired.
if s.deriveAdminKey == nil {
    return s.prepareSecretsLegacy(ctx, userID, sessionID, workspaceID)
}
// Use s.store (which satisfies CredentialStore) directly:
bindings, err := s.store.(CredentialStore).GetWorkspaceCredentials(ctx, workspaceID)
```

Or more cleanly, change the `store` field type in `SecretService` to `SecretAndCredentialStore` directly, eliminating the type assertion.

**`deriveServerKey` is in `api/internal/app` — not callable from `pkg/secrets`:**

`deriveServerKey` is a package-private function in `api/internal/app/secrets_adapters.go`. `pkg/secrets/injection.go` cannot call it (import cycle). The solution: expose admin credential decryption via a **callback injected at wire time**, analogous to how `WorkspaceOwnerVerifier` is injected:

```go
// pkg/secrets/secret_service.go
// Option A is the chosen approach: SecretService.store is typed as SecretAndCredentialStore.
// The separate credStore field is ELIMINATED — s.store satisfies CredentialStore directly.
type SecretService struct {
    keys              *KeyService
    store             SecretAndCredentialStore  // was SecretStore; satisfies both interfaces
    wsOwners          WorkspaceOwnerVerifier
    deriveAdminKey    AdminKeyDeriver   // nil = admin creds not supported
    requireWsVerifier bool
    logger            LoggerInterface   // required for Warn calls in injection path
}

func (s *SecretService) SetAdminKeyDeriver(d AdminKeyDeriver)  { s.deriveAdminKey = d }
func (s *SecretService) HasAdminKeyDeriver() bool              { return s.deriveAdminKey != nil }
```

There is NO separate `credStore` field. `SetCredentialStore` is NOT needed. The new injection path activates when `deriveAdminKey` is non-nil:

```go
// PrepareSecretsForInjection: falls back to legacy when deriveAdminKey is nil.
if s.deriveAdminKey == nil {
    return s.prepareSecretsLegacy(ctx, userID, sessionID, workspaceID)
}
// Use s.store directly for credential queries:
bindings, err := s.store.GetWorkspaceCredentials(ctx, workspaceID)
```

Wired in `api/internal/app/app.go`:

```go
secretService.SetAdminKeyDeriver(deriveServerKey)  // passes the function itself
```

`decryptBinding` in `injection.go` then uses `s.deriveAdminKey`:

```go
case "admin":
    if s.deriveAdminKey == nil {
        return LLMProviderData{}, fmt.Errorf("admin key deriver not configured")
    }
    key := s.deriveAdminKey("provider-credentials")
    if key == nil {
        return LLMProviderData{}, fmt.Errorf("server KEK unavailable (LLMSAFESPACE_MASTER_SECRET not set)")
    }
    // key is a []byte, pass to DecryptSecret directly
```

This pattern: keeps `pkg/secrets` free of any import from `api/internal`; makes the admin key derivation testable (inject a stub `AdminKeyDeriver` in tests); keeps `deriveServerKey` private to `api/internal/app`; and is consistent with the existing injection patterns (`WorkspaceOwnerVerifier`, `SetCredentialStore`).

**`PrepareSecretsForInjection` — nil-safe fallback and legacy rename:**

The current implementation in `pkg/secrets/injection.go` is renamed to `prepareSecretsLegacy`. The new `PrepareSecretsForInjection` falls back to the legacy path when **either** `credStore` or `deriveAdminKey` is nil — i.e., when `s.credStore == nil || s.deriveAdminKey == nil`. The new path is only active when BOTH are non-nil. This is expressed in code as:

Partial wiring (one set but not the other) is almost certainly a bug. The API startup sequence in `app.go` must log an explicit `Error` — not a `Warn` — if `credStore` is set but `deriveAdminKey` is not, or vice versa, so the operator knows the new credential path is not active:

```go
// In app.go after wiring both:
if (secretService.HasCredentialStore()) != (secretService.HasAdminKeyDeriver()) {
    log.Error("partial credential store wiring: either both credStore and adminKeyDeriver must be set, or neither")
}
```

```go
func (s *SecretService) PrepareSecretsForInjection(
    ctx context.Context, userID, sessionID, workspaceID string,
) ([]byte, error) {
    if s.deriveAdminKey == nil {
        // Fall back to legacy path when admin key deriver not wired.
        return s.prepareSecretsLegacy(ctx, userID, sessionID, workspaceID)
    }

    if err := s.verifyWorkspaceOwner(ctx, userID, workspaceID); err != nil {
        return nil, err
    }

    // 1. Load all bound credentials via s.store (SecretAndCredentialStore).
    //    ORDER BY enforced by DB: (source_type='explicit') DESC, within_priority DESC, created_at ASC.
    bindings, err := s.store.GetWorkspaceCredentials(ctx, workspaceID)
    if err != nil {
        return nil, fmt.Errorf("get workspace credentials: %w", err)
    }
    // ... new multi-source path below ...
}

// prepareSecretsLegacy is the current PrepareSecretsForInjection body, renamed.
// The rename is mechanical — no logic changes.
func (s *SecretService) prepareSecretsLegacy(
    ctx context.Context, userID, sessionID, workspaceID string,
) ([]byte, error) {
    // current body of PrepareSecretsForInjection, verbatim
}
```

**Full implementation sketch:**

```go
func (s *SecretService) PrepareSecretsForInjection(
    ctx context.Context, userID, sessionID, workspaceID string,
) ([]byte, error) {
    if s.credStore == nil || s.deriveAdminKey == nil {
        return s.prepareSecretsLegacy(ctx, userID, sessionID, workspaceID)
    }

    if err := s.verifyWorkspaceOwner(ctx, userID, workspaceID); err != nil {
        return nil, err
    }

    // 1. Load all bound credentials.
    //    ORDER BY enforced by DB: (source_type='explicit') DESC, within_priority DESC, created_at ASC.
    bindings, err := s.credStore.GetWorkspaceCredentials(ctx, workspaceID)
    if err != nil {
        return nil, fmt.Errorf("get workspace credentials: %w", err)
    }

    // 2. Derive server KEK once if any admin credentials are present.
    //    s.deriveAdminKey is the injected deriveServerKey function from api/internal/app.
    //    Returns nil when LLMSAFESPACE_MASTER_SECRET is unset — admin creds are then skipped.
    var serverKEK []byte
    for _, b := range bindings {
        if b.OwnerType == "admin" {
            serverKEK = s.deriveAdminKey("provider-credentials")
            if serverKEK == nil {
                s.logger.Warn("server KEK unavailable; admin credentials will be skipped")
            }
            break
        }
    }

    // 3. Decrypt and deduplicate by provider.
    //    seen[provider] is set ONLY after successful decryption — failure on a
    //    higher-priority credential allows fallback to the next for the same provider.
    seen := make(map[string]bool)
    var providerData []LLMProviderData
    for _, b := range bindings {
        if seen[b.Provider] {
            continue
        }
        pd, err := s.decryptBinding(ctx, b, sessionID, serverKEK)
        if err != nil {
            s.logger.Warn("skipping credential",
                "id", b.ID, "ownerType", b.OwnerType, "provider", b.Provider, "error", err)
            continue // do NOT set seen[b.Provider]
        }
        if len(b.ModelAllowlist) > 0 {
            // FILTER, not replace — preserve existing model metadata.
            // Use a new slice (not pd.Models[:0]) to avoid a nil-slice panic
            // when pd.Models is nil (provider-level key with no model list).
            allowed := make(map[string]bool, len(b.ModelAllowlist))
            for _, id := range b.ModelAllowlist {
                allowed[id] = true
            }
            var filtered []LLMModelConfig
            for _, m := range pd.Models {  // safe: range over nil slice is a no-op
                if allowed[m.ID] {
                    filtered = append(filtered, m)
                }
            }
            if len(filtered) == 0 {
                // No existing model metadata matched allowlist (or pd.Models was nil/empty).
                // Populate stubs so FormatOpenCodeConfig restricts to the allowed IDs.
                filtered = make([]LLMModelConfig, 0, len(b.ModelAllowlist))
                for _, id := range b.ModelAllowlist {
                    filtered = append(filtered, LLMModelConfig{ID: id})
                }
            }
            pd.Models = filtered
        }
        seen[b.Provider] = true
        providerData = append(providerData, pd)
    }

    // 4. Non-LLM secrets unchanged — reads from user_secrets via s.store.GetBindings.
    nonLLM, err := s.buildNonLLMSecrets(ctx, userID, sessionID, workspaceID)
    if err != nil {
        return nil, err
    }
    return buildSecretsJSON(providerData, nonLLM)
}

// decryptBinding dispatches by owner_type.
func (s *SecretService) decryptBinding(
    ctx context.Context, b CredentialBinding, sessionID string, serverKEK []byte,
) (LLMProviderData, error) {
    var key []byte
    switch b.OwnerType {
    case "user":
        dek, err := s.keys.GetDEK(ctx, sessionID)
        if err != nil {
            return LLMProviderData{}, fmt.Errorf("get user DEK: %w", err)
        }
        key = dek
    case "admin":
        if serverKEK == nil {
            return LLMProviderData{}, fmt.Errorf("server KEK unavailable")
        }
        key = serverKEK
    case "org":
        return LLMProviderData{}, fmt.Errorf("org credentials not yet supported (Epic 29+)")
    default:
        return LLMProviderData{}, fmt.Errorf("unknown owner_type %q", b.OwnerType)
    }
    plaintext, err := DecryptSecret(key, b.Ciphertext)
    if err != nil {
        return LLMProviderData{}, err
    }
    var pd LLMProviderData
    if err := json.Unmarshal(plaintext, &pd); err != nil {
        return LLMProviderData{}, fmt.Errorf("unmarshal LLMProviderData: %w", err)
    }
    return pd, nil
}
```

**Pipeline boundary — `PrepareSecretsForInjection` returns `[]InjectedSecret` JSON, not `[]LLMProviderData`:**

The function's output format (`[]InjectedSecret`) is unchanged. The materializer in `pkg/agentd/secrets` receives and parses `[]InjectedSecret`. `LLMProviderData` is internal to the injection function — it is serialized into `InjectedSecret.Plaintext` as JSON before the array is marshaled. This is the current format: each `llm-provider` secret in `[]InjectedSecret` has `Plaintext = json.Marshal(LLMProviderData)`. Nothing about this changes.

`buildSecretsJSON` and `buildNonLLMSecrets` do not exist today. They must be defined as part of US-30.5:

```go
// buildNonLLMSecrets reads non-LLM secrets (ssh-key, git-credential, etc.)
// from the legacy user_secrets path via s.store.GetBindings.
// GetDEK is called lazily — only if at least one non-LLM secret exists — so
// workspaces with only admin LLM credentials and no non-LLM secrets do not
// require an active session.
func (s *SecretService) buildNonLLMSecrets(
    ctx context.Context, userID, sessionID, workspaceID string,
) ([]InjectedSecret, error) {
    bound, err := s.store.GetBindings(ctx, workspaceID)
    if err != nil {
        return nil, err
    }
    // Filter to non-LLM secrets owned by this user before fetching DEK.
    var relevant []*UserSecret
    for _, secret := range bound {
        if secret.UserID == userID && secret.Type != SecretTypeLLMProvider {
            relevant = append(relevant, secret)
        }
    }
    if len(relevant) == 0 {
        return nil, nil  // no non-LLM secrets; no DEK needed
    }
    dek, err := s.keys.GetDEK(ctx, sessionID)
    if err != nil {
        return nil, fmt.Errorf("get DEK for non-LLM secrets: %w", err)
    }
    var out []InjectedSecret
    for _, secret := range relevant {
        plaintext, err := DecryptSecret(dek, secret.Ciphertext)
        if err != nil {
            continue
        }
        out = append(out, InjectedSecret{
            Type:      secret.Type,
            Name:      secret.Name,
            Metadata:  secret.Metadata,
            Plaintext: string(plaintext),
        })
    }
    return out, nil
}

// buildSecretsJSON converts providerData + non-LLM secrets into the
// []InjectedSecret JSON format the materializer reads from /sandbox-cfg/secrets.json.
// providerData entries become InjectedSecret{Type:"llm-provider", Plaintext: json(LLMProviderData)}.
func buildSecretsJSON(providerData []LLMProviderData, nonLLM []InjectedSecret) ([]byte, error) {
    out := make([]InjectedSecret, 0, len(providerData)+len(nonLLM))
    for _, pd := range providerData {
        plaintext, err := json.Marshal(pd)
        if err != nil {
            return nil, err
        }
        out = append(out, InjectedSecret{
            Type:      SecretTypeLLMProvider,
            Name:      pd.Provider,  // name is the provider ID for LLM entries
            Plaintext: string(plaintext),
        })
    }
    out = append(out, nonLLM...)
    return json.Marshal(out)
}
```

**Session requirement:** `sessionID` is consumed lazily. `buildNonLLMSecrets` only calls `GetDEK` when at least one non-LLM secret exists. `decryptBinding` calls `GetDEK` only for `user`-owned provider credentials. A workspace with only admin provider credentials and no non-LLM secrets can inject without an active session.

**Sequencing note:** US-30.5 can be implemented and unit-tested immediately after US-30.1 lands. End-to-end testing requires US-30.4 (free-tier seeded at startup). US-30.12 enforces this dependency.

### US-30.6 — User LLM Provider CRUD API

New routes (user-scoped, no AdminGuard):
```
POST   /api/v1/provider-credentials
GET    /api/v1/provider-credentials
GET    /api/v1/provider-credentials/:id
PUT    /api/v1/provider-credentials/:id
DELETE /api/v1/provider-credentials/:id

POST   /api/v1/provider-credentials/:id/bind/:workspaceId
DELETE /api/v1/provider-credentials/:id/bind/:workspaceId
GET    /api/v1/workspaces/:id/provider-credentials  (list what's bound)
```

Encryption: user DEK (same as `user_secrets` today). Requires active session.

The handler is `UserProviderCredentialsHandler` in `api/internal/handlers/provider_credentials.go`. It is analogous to `SecretsHandler` but scoped to `owner_type='user'` rows in `provider_credentials`.

**Binding triggers `MarkCredentialChanged`** (Epic 27a) so the reload banner appears. Same pattern as `SetBindings` in `SecretsHandler`.

### US-30.7 — User LLM Provider UI

New tab in user Settings: "LLM Providers".

Component: `UserProviderCredentialsTab.tsx`. Separate from `SecretsTab.tsx` (which handles non-LLM secrets). The tab is not visible to admins in the admin panel — it appears in the regular user settings.

**Create form fields:**
- Name (text, required)
- Provider (select: anthropic, openai, google, deepseek, ollama, custom)
- API Key (password input, required)
- Base URL (text, optional — shown for ollama/custom)
- Workspace binding (multi-select of user's workspaces, optional)

**List view:** shows each credential with provider name, masked key (`sk-ant-...••••••••`), bound workspaces. Delete and reveal (password-gated) actions.

**Tests (TDD — write before component):**
- renders provider select and API key field
- submits correct JSON to `POST /api/v1/provider-credentials`
- binding flow calls `POST /api/v1/provider-credentials/:id/bind/:workspaceId`
- shows bound workspace count
- disabled submit when API key empty

### US-30.8 — Admin Credentials UI

Replace `AdminCredentialsTab.tsx`. The new UI is structurally similar but talks to the new `/api/v1/admin/provider-credentials` endpoints and shows auto-apply configuration.

**Changes from existing AdminCredentialsTab:**
- Remove model allowlist tab (moved to workspace settings in Epic 13)
- Add auto-apply configuration (target: all / specific user / org)
- Remove "key version" column (implementation detail, not user-relevant)
- Show "active workspaces" count (how many workspace pods currently have this credential loaded)

### US-30.9 — `classifyAvailability`

**`loadedProviders` is derived from the live catalog response, not the DB.** The live catalog (`GET /api/model` on the pod) reflects what opencode has actually loaded — which may differ from the DB state if a reload is pending (Epic 27a `pending_refresh=true`). Using the DB would show providers as "available" that opencode hasn't loaded yet, producing a misleading UI.

`annotateModels` gains a new parameter:

```go
func annotateModels(raw []byte, loadedProviders map[string]bool) ([]annotatedModel, error)
```

`loadedProviders` is built by `ListModels` from the catalog response itself, before annotation:

```go
// Build loadedProviders from the live catalog — which providers appear at all
// in the returned model list? A provider is "loaded" if opencode reports any
// enabled model for it, regardless of cost.
loadedProviders := make(map[string]bool)
for _, m := range parsedModels {
    if m.Enabled {
        loadedProviders[m.ProviderID] = true
    }
}
annotated, err := annotateModels(raw, loadedProviders)
```

`classifyTier` is replaced by:

```go
func classifyAvailability(
    providerID string,
    cost []opencodeCost,
    loadedProviders map[string]bool,
    platformOpencode bool, // true when workspace has no user-owned opencode credential
) ModelAvailability {
    if !loadedProviders[providerID] {
        return ModelUnavailable
    }
    if isZeroCostOpencode(providerID, cost, platformOpencode) {
        return ModelFreeTier
    }
    return ModelAvailable
}
```

`platformOpencode` is determined once in `ListModels` by checking whether the workspace has a user-owned `provider_credentials` row for `provider="opencode"`. This requires one DB query per `ListModels` call.

**Wiring:** `SecretsHandler` (which handles `ListModels`) needs access to the credential store for this query. The existing `ModelStore` interface (`models.go:24-28`) only covers workspace updates and model retrieval. Add `HasUserProviderCredential(ctx context.Context, userID, provider string) (bool, error)` to a new `ProviderCredentialChecker` interface, and wire it on `SecretsHandler` via a nil-safe setter. When nil (before US-30.6 is deployed), `platformOpencode` defaults to `true` (assume platform key — correct behavior before user credentials can exist).

```go
// api/internal/handlers/models.go
type ProviderCredentialChecker interface {
    HasUserProviderCredential(ctx context.Context, userID, provider string) (bool, error)
}

// In ListModels:
platformOpencode := true  // default: assume platform key
if h.credChecker != nil {
    has, err := h.credChecker.HasUserProviderCredential(ctx, userID, "opencode")
    if err == nil {
        platformOpencode = !has  // user has own key → not platform
    }
}
```

`HasUserProviderCredential` is implemented on `*PgSecretStore` (counts rows matching `owner_type='user' AND owner_id=userID AND provider=provider`). This query is cheap (indexed on `(owner_type, owner_id, provider)`).

The `annotatedModel` struct gains `Availability ModelAvailability`. The existing `Tier string` and `FreeTier bool` fields are kept for one release cycle for frontend compatibility, then deprecated.

The `ListModels` usable-model filter becomes:

```go
for _, m := range annotated {
    if m.Availability == ModelUnavailable {
        continue
    }
    usable = append(usable, m)
}
```

The existing special-case filter for paid opencode models (current lines 182–185) is subsumed: with the free-tier key flowing through `ConfigProviderPlugin`, paid opencode models appear in the catalog as `enabled=true` but with non-zero cost. `classifyAvailability` returns `ModelAvailable` for them (provider is loaded, non-zero cost). They are NOT filtered out. The frontend uses `Availability` to show them with a "paid — add your own key" indicator. This is a behavior change from the current filter — paid opencode models will now appear in the selector, greyed out rather than hidden. This is intentional: showing users what exists and why they can't use it is better UX than hiding it.

### US-30.10 — `SetModel` serial round-trip fix

Changes to `api/internal/handlers/models.go`:

```go
func (h *SecretsHandler) SetModel(c *gin.Context) {
    // ... auth and ownership checks unchanged ...

    // Determine if workspace uses platform free-tier opencode key.
    platformOpencode := true
    if h.credChecker != nil {
        if has, err := h.credChecker.HasUserProviderCredential(
            c.Request.Context(), userID, "opencode",
        ); err == nil {
            platformOpencode = !has
        }
    }

    var podIP string
    var isFreeTier bool

    if h.podIPResolver != nil {
        var err error
        podIP, err = h.podIPResolver.GetWorkspacePodIP(...)
        if err == nil && podIP != "" {
            catalog := h.fetchCatalogOnce(c.Request.Context(), podIP, workspaceID)
            model, found := findModel(catalog, req.Model)
            if !found {
                c.JSON(http.StatusBadRequest, gin.H{"error": "model not found in workspace catalog"})
                return
            }
            isFreeTier = isZeroCostOpencode(model.ProviderID, model.Cost, platformOpencode)
        }
    }

    // Persist to DB (synchronous — authoritative).
    if err := h.wsUpdater.UpdateWorkspace(...); err != nil { ... }
    // Evict Redis cache (US-30.11).
    h.redisClient.Del(c.Request.Context(), fmt.Sprintf("model-catalog:%s", workspaceID))

    // Persist to K8s Secret (best-effort).
    if h.manifestWriter != nil {
        _ = h.manifestWriter.EnsureWorkspaceConfig(...)
    }

    // Fire-and-forget pod pushes.
    if podIP != "" {
        password, _ := h.passwordGetter(c.Request.Context(), workspaceID)
        relayBaseURL := fmt.Sprintf("http://localhost:%d/relay/inference", agentd.AgentdPort)
        go func() {
            _ = h.patchAgentModel(context.Background(), podIP, password, req.Model)
            if isFreeTier {
                _ = h.pushRelayBaseURL(context.Background(), podIP, password, relayBaseURL)
            } else {
                _ = h.clearRelayBaseURL(context.Background(), podIP, password)
            }
        }()
    }

    applied := podIP != ""
    c.JSON(http.StatusOK, gin.H{"model": req.Model, "applied": applied})
}
```
                _ = h.clearRelayBaseURL(context.Background(), podIP, password)
            }
        }()
    }

    applied := podIP != ""
    c.JSON(http.StatusOK, gin.H{"model": req.Model, "applied": applied})
}
```

`fetchCatalogOnce` replaces the three separate catalog-fetching functions. `modelExistsInCatalog` and `isFreeTierModel` are deleted.

### US-30.11 — Model cache in Redis

```go
// In ListModels:
cacheKey := fmt.Sprintf("model-catalog:%s", workspaceID)

// Read:
body, err := h.redisClient.Get(ctx, cacheKey).Bytes()
if err != nil { body = nil } // cache miss

// Write:
h.redisClient.Set(ctx, cacheKey, body, 5*time.Second)

// Evict (SetModel):
h.redisClient.Del(ctx, cacheKey)
```

`SecretsHandler` gains a `redisClient redis.UniversalClient` field. The in-process `modelCacheMap` and its mutex are deleted.

---

## Failure Modes and Mitigations

| Failure | Impact | Mitigation |
|---|---|---|
| `LLMSAFESPACE_MASTER_SECRET` not set at API startup | `EnsureFreeTierCredential` logs Warn and skips seeding; all workspaces boot without free-tier models | Operator adds `master-secret` to the K8s credentials Secret and restarts the API pod. On next startup, seeding runs and free-tier models appear on next workspace pod restart. |
| `LLMSAFESPACE_MASTER_SECRET` set but too short (< 32 hex chars) | `deriveServerKey` returns `nil` silently — indistinguishable from "not set"; admin credentials silently fail | `EnsureFreeTierCredential` must check for nil KEK and log an `Error` (not just `Warn`) with a clear message: "LLMSAFESPACE_MASTER_SECRET is set but invalid (too short or not valid hex)". Operators who set a short key get no runtime error today — this must be made explicit. |
| `LLMSAFESPACE_MASTER_SECRET` rotated | ALL existing admin credentials in `provider_credentials` are encrypted with the old KEK and become undecryptable. `EnsureFreeTierCredential` re-seeds only the free-tier row. | **There is no automated recovery for user-created admin credentials.** This is a known limitation of server-KEK encryption. Operators must re-enter all admin credentials after key rotation. US-30.2 must include a `POST /api/v1/admin/provider-credentials/:id/re-encrypt` endpoint that takes the new API key value and re-encrypts. Alternatively, the design could store the KEK-encrypted ciphertext with a versioned key ID so rotation can be detected. This is deferred to a follow-up (Epic 31+) but **must be documented as a known operational gap** — operators must not rotate `LLMSAFESPACE_MASTER_SECRET` without a re-entry plan. |
| `LLMSAFESPACE_MASTER_SECRET` not set at runtime | Admin credentials cannot be decrypted in `decryptBinding` | Skipped with Warn log; workspace loads only user credentials. Free-tier key absent. Same outcome as unset at startup. |
| User DEK unavailable (session expired) | User credentials cannot be decrypted | Skipped with Warn log (same as admin failure above). If no other credentials present, workspace gets no provider config. |
| New auto-apply rule added but existing workspaces not seeded | Existing workspaces do not get the new credential automatically | Admin calls `POST /admin/provider-credentials/:id/auto-apply/backfill` (async, paginated, 202 Accepted). |
| Backfill job partially fails | Some workspaces get binding, some don't | Job logs failures per workspace. Credential binding itself succeeded; affected workspaces get credential on next pod boot. |
| Free-tier admin credential deleted | All workspaces lose free-tier models on next pod boot | Restart the API pod — `EnsureFreeTierCredential` re-seeds on every startup via `DO UPDATE`. |
| Two credentials for same provider, same source_type, equal within_priority | Non-deterministic if DB query omits stable tiebreaker | `GetWorkspaceCredentials` interface contract mandates `ORDER BY (source_type='explicit') DESC, within_priority DESC, created_at ASC`. `created_at ASC` (oldest wins) is the stable tiebreaker. Implementors must not omit it. |
| US-30.5 deployed before US-30.4 | `credStore` wired, free-tier credential row absent → all workspaces get empty provider list (regression) | Deploy in the order prescribed in the Cutover Runbook. Never deploy US-30.5 before US-30.4. |
| Existing workspaces after cutover have no free-tier binding | Pod restarts get no free-tier models | `EnsureFreeTierCredential` at API startup automatically backfills all existing workspaces. No manual action required. |
| Redis unavailable (US-30.11) | Model cache miss on every request | Fail open: fetch from pod directly. Degraded latency only. |
| Epic 27a not deployed before US-30.3 | `MarkCredentialChanged` fails with table-not-found on every workspace binding | Run the pre-flight checks in the Cutover Runbook before deploying any Epic 30 story. |
| Higher-priority credential fails to decrypt; lower-priority (admin) used silently | User's explicitly-bound key fails; admin credential substituted without notification | **Known and intentional fallback behavior.** The Warn log is server-side only. Users are not notified their explicit credential failed. This is acceptable for the free-tier fallback case. For user-controlled explicit credentials: the Epic 27a reload banner appears only when bindings change, not when decryption fails. A follow-up metric or error surface should be added if silent substitution is deemed unacceptable (Epic 31+). |

---

## Out of Scope (Future Epics)

| Item | When |
|---|---|
| Org-level credentials (`owner_type='org'`) | Epic 29 (Organizations) |
| `decryptCredential` org branch implementation | Epic 29 |
| `credential_auto_apply` target_type='org' | Epic 29 |
| Model discovery from provider APIs | Separate epic (Epic 31+) |
| Per-model allowlist per workspace binding | Epic 31+ |
| Vault / HSM backend for server KEK | Enterprise tier |
| Lazy DEK rotation for `provider_credentials` (Epic 10 US-10.8) | Can ship independently |
| `ListPendingReloadWorkspaces` bulk reload | Epic 27b |
