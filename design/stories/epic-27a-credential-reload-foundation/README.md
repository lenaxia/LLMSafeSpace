# Epic 27a: Credential Reload Foundation

**Status:** Planning
**Created:** 2026-06-03
**Priority:** Medium
**Depends on:** Epic 10 (llm-provider secret type, materializer, auth-store injection — shipped)
**Companion epic:** Epic 27b (Credential Reload Polish) — drain mode, bulk reload, error enrichment, docs, SDK design.

**This epic supersedes:** the original combined Epic 27 draft (deleted 2026-06-03 after critical-review passes identified factual errors, scope creep, and internal contradictions).

---

## Problem Statement

Today, when a user adds, updates, or deletes an `llm-provider` credential bound to a workspace, agentd's `reloadSecretsHandler` automatically calls `pkg/agent/opencode.Client.RefreshCredentials`, which writes to opencode's `auth.json` and **immediately calls `POST /instance/dispose`**. Verified at `cmd/workspace-agentd/secrets.go:268-290`. The dispose call aborts every in-flight LLM stream in the workspace because opencode's `InstanceState`-scoped `AbortController` is registered as a disposer. Verified against `~/personal/opencode/packages/opencode/src/session/llm.ts:355-362` and `effect/instance-state.ts:39-42`.

This is the wrong default: a user adding credentials for a future need should not interrupt unrelated agent activity already running in their workspace. Worklog 0125 documented production fallout from this behavior (Bug 2: managedProcess crash loop on the post-dispose restart fallback). Worklog 0127 fixed Bug 1 + Bug 2 mechanically but did not change the underlying "automatic dispose-on-credential-change" semantic.

**Failure modes the current behaviour exhibits:**

1. User has a long-running agent task in Session A; adds a new OpenAI credential intending to use it for Session B later → Session A's stream is aborted mid-response.
2. Programmatic API caller adds a credential as part of workspace setup automation → any concurrent inference jobs in that workspace get killed.
3. Multi-tab user adds a credential in Tab A → Tab B's active session loses its current LLM call with no UI explanation.

**Architectural cause:** opencode's only credential-invalidation primitive today is the workspace-wide `instance.dispose`. There is no targeted "refresh provider state without touching session scopes" operation. Our agentd flow conflates "stage the credential" with "activate the credential" in one `RefreshCredentials` call, leaving no opportunity for the user or caller to choose when activation happens.

---

## Scope of Epic 27a

This epic ships the **foundation** that fixes the auto-dispose problem and gives users an explicit affordance to apply staged credentials. The minimum viable behavior change.

**In Epic 27a:**
- Schema for tracking credential-staging state per workspace
- Single-workspace reload endpoint (immediate-dispose mode only)
- Frontend banner + confirmation modal for triggering reload
- Removal of automatic dispose from agentd's secret-reload path
- Pre-existing Bug 11 (user_secret_bindings type mismatch / missing FK) fixed as a side-effect of the schema work
- Integration test that re-runs worklog 0125's credflow exercise with the banner step replacing auto-dispose

**Deferred to Epic 27b:**
- Drain mode (`?drain=true` query parameter, event-driven via snapshot + SSETracker)
- Bulk reload across all of a user's workspaces
- Error response enrichment with the "did you mean to reload?" hint on chat-proxy errors
- API reference documentation
- SDK ergonomic helpers and typed exception classes

**Out of scope entirely:**
- Catalog merge between our DB credentials and opencode's catalog (rejected: creates phantom-state bugs, doesn't avoid the dispose problem; see "Refuted Hypotheses" below)
- Automatic dispose-on-idle scheduler in agentd (rejected: silent state changes, no user visibility, agent tasks could indefinitely defer reload)
- First-use lazy dispose at the proxy layer (rejected: workspace-wide blast radius means this disrupts unrelated sessions)
- Per-session model overrides (already supported by opencode's `PromptInput.model`; not a credential-reload concern)
- Workspace-default model storage (replaced by user-default model in the model-management discussion; tracked separately)
- DynamoDB-style storage migration (out of scope; called out as future work — see "Future Work" below)

---

## Design Principles

1. **No silent interruption of in-flight calls.** Activation of new credentials must be an explicit user or API-caller action.
2. **Honest catalog.** `GET /workspaces/:id/models` reflects opencode's currently-loaded providers. No DB-side merge.
3. **Explicit reload primitive.** A single `POST /api/v1/workspaces/:id/agent/reload` endpoint is the only way to cause a dispose. UI-driven banners and (future, in 27b) API callers both flow through it.
4. **Server-derived staging signal, not client-tracked.** A `pending_refresh BOOL` on `workspace_agent_state` is the authoritative signal for "this workspace has staged credentials that need reload."
5. **Best-effort staging signal.** The binding mutation (via `secrets.PgSecretStore`, which uses its own `pgxpool.Pool` transaction internally) and the `pending_refresh = true` write (via `database.Service`, which uses a separate `*sql.DB` pool) cannot share a single database transaction — they live on different, incompatible connection pools. The design therefore makes the credential-state write **best-effort and sequential**: `SetBindings` runs first; if it succeeds, `MarkCredentialChanged` runs in its own short transaction immediately after. If `MarkCredentialChanged` fails, the credential is correctly bound but the banner may not appear until the next binding mutation retriggers it. This eventual-consistency window is narrow and the failure mode is non-catastrophic (see F.10). The reload path (`last_agent_disposed_at` update + `pending_refresh = false`) runs entirely within `database.Service` and IS fully transactional.

---

## Stated Assumptions

Each verified against a specific file and line in the codebase or in `~/personal/opencode`.

| # | Assumption | Verification |
|---|---|---|
| A1 | A single `opencode serve` process runs per workspace pod, hosting all sessions for that workspace. | `runtimes/base/tools/entrypoints/entrypoint-opencode.sh:25` — `exec workspace-agentd --supervise`. agentd spawns one opencode child via `defaultOpencodeCmdFactory` at `cmd/workspace-agentd/main.go:884-895` (`opencode serve --hostname 0.0.0.0 --port <agentd.AgentPort>`). |
| A2 | Within that single process, each workspace directory has one `InstanceContext`. All sessions in the workspace share one provider-state, auth-store, MCP, tool registry, and LSP cache via `InstanceState`. | `~/personal/opencode/packages/opencode/src/project/instance-store.ts:106-122` — `load(input)` is keyed by directory and returns the same InstanceContext for repeated calls within the same process lifetime. |
| A3 | `POST /instance/dispose` ultimately calls `runDisposers(directory)`, which invalidates every `InstanceState` cache registered for that workspace. The opencode HTTP server stays running; the next request triggers a fresh `InstanceState.make` lookup that re-reads `auth.json` and rebuilds the provider list. | `instance-store.ts:92-96` (`disposeContext` → `runDisposers`); `effect/instance-state.ts:39-42` (every `InstanceState.make` registers a disposer that calls `ScopedCache.invalidate`). |
| A4 | Disposers include the LLM stream's `AbortController`, registered via `Effect.acquireRelease` on `Stream.scoped` (`session/llm.ts:355-362`). When the InstanceContext disposes, every in-flight LLM HTTP request in that workspace is cut. Sessions persist in SQLite and can be resumed by sending a new prompt. | `session/llm.ts:359-362` (acquireRelease → `ctrl.abort()`) plus the disposer registration chain in `instance-state.ts:39-42`. |
| A5 | opencode's auth Control API (`PUT /auth/:providerID`, `DELETE /auth/:providerID`) writes to `auth.json` at mode `0o600` and does NOT invalidate any InstanceState cache. The next provider-state read after the file write needs an invalidation step (dispose) to actually pick up the change. | Handler `~/personal/opencode/packages/opencode/src/server/routes/instance/httpapi/handlers/control.ts:14-26` calls `auth.set` and `auth.remove`. Implementation `packages/opencode/src/auth/index.ts:9,72-88` writes to `path.join(Global.Path.data, "auth.json")` with mode `0o600` and does not interact with InstanceState. |
| A6 | All opencode endpoints (including auth Control API and instance dispose) require Basic auth with username `opencode` (verified: `pkg/agentd/types.go:23`, constant `AuthUsername`) and the per-pod password. agentd reads this password from `/sandbox-cfg/password` at boot. | `pkg/agent/opencode/client.go:43-51,84-86,143-144` (Basic auth on every request). Worklog 0127 documents this as Bug 1's root cause. |
| A7 | The API server's `SSETracker` (`api/internal/handlers/session_tracker.go`) maintains a long-lived SSE subscription per workspace that consumes opencode's `session.status` events and dispatches via `onSessionIdle` / `onSessionActive` callbacks. | `session_tracker.go:79` (`EnsureWatching`); `session_tracker.go:246-271` (`dispatchProperties` switches on `session.status` events). Used by `proxy.go:167-171` to maintain `activeSess` map. |
| A8 | The API server reaches the workspace pod via two distinct ports: opencode directly at `agentd.AgentPort` (4096), and agentd at `agentd.AgentdPort` (4097). The constants are defined at `pkg/agentd/types.go:18-19`. The reload endpoint goes through agentd at `4097/v1/agent/reload`; agentd calls opencode locally. | `pkg/agentd/types.go:18-19`; existing usage at `secrets.go:452` (hardcoded `4097` — a defect this epic fixes to use `agentd.AgentdPort`). |
| A9 | The existing `RestartWorkspace` action (`workspace_service.go:484`) bumps `spec.RestartGeneration` and triggers a controller-driven full pod rebuild. This is **distinct from** Epic 27a's reload, which calls `POST /instance/dispose` on the running opencode and does not touch the pod. The user-facing terminology must distinguish these two operations. | `workspace_service.go:462-526`; controller logic in Epic 21 / Epic 24. |
| A10 | The `user_secret_bindings` table has `workspace_id VARCHAR(36)` while `workspaces.id` is `UUID`, with no FK. This is "Bug 11" from worklog 0085. Epic 27a fixes this as part of its schema work, by adding the new `workspace_agent_state` table with proper UUID + FK and also migrating `user_secret_bindings` to match. | `api/migrations/000002_workspaces.up.sql:2` (UUID); `api/migrations/000008_user_secrets.up.sql:18-23` (VARCHAR(36), no FK); commentary at `api/internal/services/database/database.go:450-457`. |
| A10b | **Bug 12 (pre-existing, fixed in this epic):** `workspaces.user_id` is `VARCHAR(255)` (`000002_workspaces.up.sql:4`) while `users.id` is `VARCHAR(36)` (`000001_initial_schema.up.sql:2`), with no FK. At runtime string equality works, but there is no referential integrity enforcement. Epic 27a adds the missing FK with `ON DELETE RESTRICT` (not CASCADE). `ON DELETE CASCADE` would be wrong here because workspaces use soft-delete (`deleted_at` column; `database.go:477`) while users are hard-deleted (`database.go:250`). If CASCADE were used, deleting a user would hard-delete workspace rows, bypassing the soft-delete path, losing audit records, and orphaning live Kubernetes CRD objects. `ON DELETE RESTRICT` means `DeleteUser()` fails if any workspace rows still reference the user, enforcing the correct application-level ordering: delete/reassign all workspaces first, then delete the user. | `api/migrations/000002_workspaces.up.sql:4`; `api/migrations/000001_initial_schema.up.sql:2`; `database.go:250` (hard delete users); `database.go:477` (soft delete workspaces). |
| A11 | `workspace_service.GetWorkspace(ctx, userID, workspaceID)` verifies ownership and returns `*types.Workspace` with `Phase` populated from the CRD. The phase check in US-27a.7 reads this field. Ownership is verified by the service (lines 244-251 check `meta.UserID != userID`), so the handler does not need a separate `verifyOwner` call. | `workspace_service.go:231-279`. |
| A12 | New workspace routes follow the pattern established by `/restart` (`router.go:620-631`): inline closures in `registerWorkspaceRoutes` calling `wsSvc` and other services directly, with errors rendered via `respondWithError(c, err)` (`router.go:743`). Handlers that require more than ~3 dependencies use a struct (`RouterConfig.SecretsHandler` pattern at `router.go:52-53`). The reload handler has 4 runtime dependencies and uses the struct pattern via `RouterConfig`. **Auth-extraction pattern note:** Inline route closures in `router.go` use `authSvc.GetUserID(c)` — a `package server` function not accessible from `package handlers`. Struct handlers in `package handlers` use `extractAuth(c)` (`secrets.go:795`). Both read the same JWT claims; the difference is package boundary. `AgentReloadHandler` is a struct handler and uses `extractAuth`. **Error-rendering note:** `respondWithError` is defined in `package server` (`router.go:743`) and is not accessible from `package handlers`. US-27a.7 (below) moves it to `package handlers` as `RespondWithError`, resolving the cross-package gap permanently. All call sites in `router.go` are updated to call `handlers.RespondWithError`. **`BeginTx` testability note:** `AgentStateStore.BeginTx` returns `*sql.Tx` — a concrete `database/sql` type. Unit tests for `AgentReloadHandler` must use `github.com/DATA-DOG/go-sqlmock` or a test PostgreSQL instance rather than a hand-rolled mock struct. **`apierrors` import note:** `agent_reload.go` must explicitly import `apierrors "github.com/lenaxia/llmsafespace/api/internal/errors"`. No existing file in `api/internal/handlers/` imports this package today; it must be added to `agent_reload.go`'s import block. | `router.go:44-63` (RouterConfig struct); `router.go:407-579` (inline routes use `GetUserID`); `router.go:178-187` (struct handler pattern for SecretsHandler); `secrets.go:795` (`extractAuth`); `api/internal/errors/errors.go` (apierrors package). |
| A13 | The model-catalog handler at `api/internal/handlers/models.go` has a 5s in-process per-workspace cache. `clearModelCache()` (line 77) clears all entries globally — not per-workspace. The reload handler calls this after successful dispose. The global clear is broader than necessary but acceptable given the 5s TTL. | `models.go:54-81`. |
| A14 | `WorkspaceMetadata` (`pkg/types/types.go:493-503`) is the DB row type populated by `database.GetWorkspace`. It is distinct from `types.Workspace` (the API-facing type, `types.go:389`). The two new fields (`AgentNeedsRefresh`, `CredentialsPendingSince`) are added to BOTH `WorkspaceMetadata` (via the LEFT JOIN in DB queries) AND `types.Workspace`/`WorkspaceListItem` (for API serialisation). `workspace_service.GetWorkspace` maps from `WorkspaceMetadata` to `types.Workspace` at lines 265-278 — the mapping must include the new fields. | `types.go:389,493`; `database.go:339-366`; `workspace_service.go:265-278`. |
| A15 | agentd's `/v1/reload-secrets` endpoint on the user port (4097) has NO application-layer authentication. The trust model relies exclusively on Kubernetes NetworkPolicy (`charts/llmsafespace/chart_test.go:128-232`) which allows only the API server pod to reach workspace pods on port 4097. The new `/v1/agent/reload` endpoint follows the same unauthenticated pattern. This is a documented tradeoff: app-layer auth would require a shared secret mechanism across pod restarts; NetworkPolicy provides the isolation boundary. If this tradeoff is ever revisited, the existing `requireBearerToken` helper at `cmd/workspace-agentd/main.go:907` can be applied to both endpoints atomically. | `cmd/workspace-agentd/main.go:517-519` (user port, no auth middleware); `main.go:474` (admin port, `requireBearerToken`). |
| A16 | No production users; API changes have no backwards-compatibility constraints. | Stated by user (Epic 21 A7 / Epic 24 A10 / Epic 25 A10 precedent). |
| A17 | `api/internal/interfaces/interfaces.go` defines the `DatabaseService` interface used by `workspace.Service` (`workspace_service.go:43`). The three new Epic 27a DB methods (`MarkCredentialChanged`, `GetLastCredentialChangedAt`, `MarkAgentReloaded`) are consumed via purpose-specific handler-layer interfaces (`CredentialStateWriter`, `AgentStateStore`) against the concrete `*database.Service` type — NOT through `workspace.Service.dbService`. Therefore `DatabaseService` and `MockDatabaseService` need **no changes** for Epic 27a. In Epic 27b, `ListPendingReloadWorkspaces` must be added to `DatabaseService` because `workspace.Service.ListPendingReloadWorkspaces` calls `s.dbService.ListPendingReloadWorkspaces` — and `s.dbService` is typed as `apiinterfaces.DatabaseService`. | `interfaces.go:47-79`; `workspace_service.go:43` (`dbService apiinterfaces.DatabaseService`); `app.go:87` (`dbSvc := svc.Database.(*database.Service)` — concrete type). |
| A18 | **Cross-pool transaction impossibility (verified).** The secrets binding layer (`pkg/secrets/PgSecretStore`) uses `pgxpool.Pool` (`app.go:46,123`; `pg_secret_store.go:23,280`). The agent-state layer (`database.Service`) uses `*sql.DB` via the pgx stdlib adapter (`database.go:37`; `sql.Open("pgx", ...)`). These are two separate connection pools to the same PostgreSQL server. PostgreSQL transactions are connection-scoped. There is no mechanism in Go, pgx, or PostgreSQL to join a `pgxpool.Tx` and a `*sql.Tx` into a single atomic unit. Therefore `MarkCredentialChanged` cannot share a transaction with `SetBindings`. See DP5. | `app.go:46,123` (separate secretsPool); `pg_secret_store.go:280` (pool.Begin(ctx)); `database.go:37` (sql.Open("pgx")). |

### Refuted Hypotheses

| # | Hypothesis | Refutation |
|---|---|---|
| R1 | "Catalog merge in our API handler can show new providers without disposing opencode." | Refuted as a primary mechanism: when the user actually picks a model from a not-yet-loaded provider, opencode returns "Model not found" because its provider snapshot is stale, so a dispose at first use is still needed. First-use dispose has the same workspace-wide blast radius as any other dispose (A4) — it would interrupt every concurrent session in the workspace. |
| R2 | "Wait for all sessions to be idle, then auto-dispose silently." | Refuted: a session can stay `busy` indefinitely on long agent tasks, and the user adding a credential expects to see it reflected somewhere. Silent waiting with no UI indication makes the system feel broken. The accepted design has the user (or, in 27b, the API caller) explicitly trigger reload. |
| R3 | "First-use dispose is fine because the user just sent a prompt — there's nothing in flight for them." | Refuted: the user's *other* concurrent sessions (other tabs, programmatic callers, long agent tasks) share the same InstanceContext (A2). Disposing for Session A's first-use of a new provider aborts every LLM stream in the workspace (A4). |
| R4 | "Pre-emptively dispose only when a prompt arrives with a `model.providerID` we've added since last dispose." | Refuted by R3 — the disruption is workspace-wide regardless of which session triggered the dispose. |
| R5 | "Inline `last_credential_changed_at` / `last_agent_disposed_at` columns on the `workspaces` table." | Refuted on separation-of-concerns grounds: workspace identity (immutable: id, user_id, runtime, name) and workspace agent-state (mutable: timestamps, future state) have different lifecycles. Co-locating them rewrites the workspace row on every credential mutation and conflates concerns. The accepted design uses a separate `workspace_agent_state` table. |

---

## User Stories

| Story | Title | Depends On |
|---|---|---|
| US-27a.1 | Schema: `workspace_agent_state` table + Bug 11 fix on `user_secret_bindings` | None |
| US-27a.2 | DB helpers: `MarkCredentialChanged`, `MarkAgentReloaded`, `GetLastCredentialChangedAt`; extend `WorkspaceMetadata` | US-27a.1 |
| US-27a.2b | `pkg/secrets`: `BindingsMutationResult` diff return from `SetBindings` and `AddBindings`; wire `MarkCredentialChanged` in `SecretsHandler`; `DeleteSecret` gap addressed. The `pkg/secrets` changes (new file, updated method signatures) have no dependency on US-27a.2 and can be started immediately. The handler wiring (`SetCredentialStateWriter`, `result.LLMProviderAffected` check) depends on US-27a.2 having `MarkCredentialChanged` on `database.Service`. | US-27a.2 (handler wiring only) |
| US-27a.3 | Expose `agentNeedsRefresh` on workspace API responses | US-27a.2 |
| US-27a.4 | `pkg/agent/opencode.Client`: rename `RefreshCredentials` → `StageCredentials`; remove dispose call | None |
| US-27a.5 | agentd `/v1/reload-secrets`: switch from `RefreshCredentials` to `StageCredentials` | US-27a.4 |
| US-27a.6 | agentd new endpoint `POST /v1/agent/reload` (calls `DisposeInstance` only) | US-27a.4 |
| US-27a.7 | API server new endpoint `POST /api/v1/workspaces/:id/agent/reload` | US-27a.3, US-27a.6 |
| US-27a.8 | Frontend banner + confirmation modal | US-27a.3, US-27a.7 |
| US-27a.9 | Integration test re-running worklog 0125's credflow exercise with banner step | US-27a.7, US-27a.8 |
| US-27a.10 | Worklog: design rationale, auto-dispose removal, retrospective | All implementation stories |

### Dependency Graph

```
US-27a.1 (schema) ── US-27a.2 (DB helpers + WorkspaceMetadata) ──┬── US-27a.3 (agentNeedsRefresh on API)
                                                                  │
                                                                  └── US-27a.2b (BindingsMutationResult + SecretsHandler wiring)

US-27a.4 (StageCredentials rename) ──┬── US-27a.5 (agentd uses StageCredentials)
                                     │
                                     └── US-27a.6 (agentd /v1/agent/reload)

US-27a.3, US-27a.6 ── US-27a.7 (API /workspaces/:id/agent/reload)
                          │
                          └── US-27a.8 (frontend banner) ── US-27a.9 (integration test)
                                                                │
                                                                └── US-27a.10 (worklog)

US-27a.2b feeds US-27a.9 (integration test must exercise MarkCredentialChanged triggering)
but does NOT block US-27a.7 (the reload endpoint has no dependency on SecretService).
```

### Critical Path

```
US-27a.1 → US-27a.2 → US-27a.3 ─┐
                                  ├─→ US-27a.7 → US-27a.8 ─┐
US-27a.4 → US-27a.5 → US-27a.6 ─┘                          ├─→ US-27a.9 → ship
                                                             │
US-27a.1 → US-27a.2 → US-27a.2b ───────────────────────────┘
           (pkg/secrets half has no upstream dependency; handler wiring half depends on US-27a.2)
```

US-27a.9 requires ALL of: US-27a.8 (banner) AND US-27a.2b (binding mutation signal). Both paths must complete before the integration test can run. US-27a.2b does NOT block US-27a.7 or US-27a.8 — it is a parallel track.

US-27a.5 (agentd no longer auto-disposes on secret reload) and US-27a.8 (banner exists for the user to trigger reload) **must ship together**. Until both land, there is either a regression (no way to reload after credential change) or no behaviour change (auto-dispose still happens). The integration test in US-27a.9 verifies the joined behaviour end-to-end before V1 ships.

---

## Detailed Design

### US-27a.1 — Schema

Migration `000014_workspace_agent_state_and_bug11_fix.up.sql`:

```sql
-- Bug 11 fix: align user_secret_bindings.workspace_id with workspaces.id type
-- and add the FK that should have existed from the start.
-- Existing rows already contain valid 36-char UUID strings (verified by
-- worklog 0085 follow-up); the cast is a no-op for valid data.
ALTER TABLE user_secret_bindings
    ALTER COLUMN workspace_id TYPE UUID USING workspace_id::uuid;

ALTER TABLE user_secret_bindings
    ADD CONSTRAINT user_secret_bindings_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;

-- Bug 12 fix: align workspaces.user_id with users.id type (VARCHAR(36))
-- and add the missing FK. workspaces.user_id was created as VARCHAR(255) in
-- migration 000002; users.id is VARCHAR(36) in migration 000001. No FK existed.
-- All existing workspaces.user_id values are valid user IDs of length <= 36
-- (they are JWT subject claims / UUID-formatted strings). The type reduction
-- is a no-op for valid data; the FK adds referential integrity going forward.
--
-- IMPORTANT: ON DELETE RESTRICT (not CASCADE) is intentional.
-- workspaces use soft-delete (deleted_at column); they are never hard-deleted
-- via the application layer. DeleteUser() runs DELETE FROM users WHERE id=$1
-- (a hard delete). If CASCADE were used, deleting a user would hard-delete all
-- their workspace ROWS -- bypassing the soft-delete path, losing audit records,
-- and orphaning any live Kubernetes CRD objects the controller still reconciles.
-- RESTRICT means DeleteUser() will fail with a FK violation if any workspace
-- rows still reference the user. The correct application flow is:
--   1. Delete or reassign all workspaces for the user first (soft-delete via
--      workspace_service.DeleteWorkspace, which fires the CRD deletion and
--      calls MarkWorkspaceDeleted to set deleted_at).
--   2. Only then call DeleteUser().
-- This invariant must be enforced at the application layer before this FK
-- allows the user row to be removed.
ALTER TABLE workspaces
    ALTER COLUMN user_id TYPE VARCHAR(36) USING user_id::varchar(36);

ALTER TABLE workspaces
    ADD CONSTRAINT workspaces_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

-- New per-workspace agent state, separate from workspace identity.
-- One row per workspace, created lazily on first credential mutation.
CREATE TABLE IF NOT EXISTS workspace_agent_state (
    workspace_id                UUID PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    last_credential_changed_at  TIMESTAMP WITH TIME ZONE,
    last_agent_disposed_at      TIMESTAMP WITH TIME ZONE,
    pending_refresh             BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at                  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_workspace_agent_state_pending
    ON workspace_agent_state (pending_refresh)
    WHERE pending_refresh = TRUE;

-- Backfill: any workspace that currently has llm-provider bindings is
-- treated as "credentials changed at migration time, never reloaded."
-- This will cause the banner to appear for existing users on first login
-- after the migration, prompting them to reload once to establish a clean
-- baseline. The string literal 'llm-provider' matches SecretTypeLLMProvider
-- in pkg/secrets/types.go:26.
INSERT INTO workspace_agent_state (workspace_id, last_credential_changed_at, pending_refresh)
SELECT DISTINCT b.workspace_id, NOW(), TRUE
FROM user_secret_bindings b
JOIN user_secrets s ON s.id = b.secret_id
WHERE s.type = 'llm-provider'
ON CONFLICT (workspace_id) DO NOTHING;
```

Migration `000014_workspace_agent_state_and_bug11_fix.down.sql`:

```sql
DROP TABLE IF EXISTS workspace_agent_state;
ALTER TABLE workspaces DROP CONSTRAINT IF EXISTS workspaces_user_id_fkey;
ALTER TABLE workspaces ALTER COLUMN user_id TYPE VARCHAR(255) USING user_id::text;
ALTER TABLE user_secret_bindings DROP CONSTRAINT IF EXISTS user_secret_bindings_workspace_id_fkey;
ALTER TABLE user_secret_bindings ALTER COLUMN workspace_id TYPE VARCHAR(36) USING workspace_id::text;
```

**Reasoning for `pending_refresh BOOL` redundancy with timestamps:**
Storing the boolean derived flag (rather than computing on read) makes bulk-pending queries trivial in SQL today (`WHERE pending_refresh = TRUE` with the partial index above) and forward-compatible with key-value stores that don't support attribute-comparison filters efficiently. The redundancy is mitigated by US-27a.2's single-writer constraint: only `MarkCredentialChanged` and the reload handler mutate this row. `MarkCredentialChanged` updates boolean and timestamps atomically in a single SQL UPSERT statement (auto-commit). `MarkAgentReloaded` updates them within an explicit transaction guarded by SELECT FOR UPDATE.

**Backfill note:** Users who had credentials loaded correctly via the auto-dispose path (pre-Epic 27a) will see a banner asking them to reload once. This is a one-time UX cost that establishes a clean `last_agent_disposed_at` baseline. The banner click is a no-op except for writing the timestamp — the credentials are already loaded.

### US-27a.2 — DB Helpers and `WorkspaceMetadata` extension

**Extension to `pkg/types/types.go`:**

```go
// WorkspaceMetadata is the database record for a workspace.
type WorkspaceMetadata struct {
    ID                       string     `json:"id" db:"id"`
    UserID                   string     `json:"userId" db:"user_id"`
    Name                     string     `json:"name" db:"name"`
    Runtime                  string     `json:"runtime" db:"runtime"`
    StorageSize               string     `json:"storageSize" db:"storage_size"`
    ImageTag                 string     `json:"imageTag" db:"image_tag"`
    AgentVersion             string     `json:"agentVersion" db:"agent_version"`
    CreatedAt                time.Time  `json:"createdAt" db:"created_at"`
    UpdatedAt                time.Time  `json:"updatedAt" db:"updated_at"`
    // New in Epic 27a (LEFT JOIN workspace_agent_state):
    AgentNeedsRefresh        bool       `json:"agentNeedsRefresh" db:"agent_needs_refresh"`
    CredentialsPendingSince  *time.Time `json:"credentialsPendingSince,omitempty" db:"credentials_pending_since"`
}
```

`types.Workspace` and `types.WorkspaceListItem` also gain these fields, populated by `workspace_service.GetWorkspace` from the metadata:

```go
ws := &types.Workspace{
    // ... existing fields ...
    AgentNeedsRefresh:       meta.AgentNeedsRefresh,
    CredentialsPendingSince: meta.CredentialsPendingSince,
}
```

**Updated queries in `api/internal/services/database/database.go`:**

Both `GetWorkspace` and `ListWorkspaces` gain a LEFT JOIN and two new Scan targets:

```sql
-- GetWorkspace query (replaces line 343-346)
SELECT
    w.id, w.user_id, w.name, w.runtime, w.storage_size, w.image_tag,
    w.agent_version, w.created_at, w.updated_at,
    COALESCE(s.pending_refresh, FALSE)  AS agent_needs_refresh,
    s.last_credential_changed_at        AS credentials_pending_since
FROM workspaces w
LEFT JOIN workspace_agent_state s ON s.workspace_id = w.id
WHERE w.id = $1
```

The `Scan` at line 349 gains two more destination fields:
```go
rows.Scan(...existing..., &ws.AgentNeedsRefresh, &ws.CredentialsPendingSince)
```

`ListWorkspaces` gets the same LEFT JOIN. The `CredentialsPendingSince` scan target is `*time.Time`; PostgreSQL NULL scans to nil correctly.

**New DB helper methods in `api/internal/services/database/database.go`:**

```go
// MarkCredentialChanged is the single writer that flips a workspace into the
// "credentials staged, reload needed" state. It opens its own short transaction
// internally because the binding mutation (secrets.PgSecretStore) uses a separate
// pgxpool.Pool that cannot share a *sql.Tx. The two writes are therefore
// best-effort sequential: the caller (SecretsHandler) runs SetBindings first;
// if it succeeds, MarkCredentialChanged is called immediately after.
//
// If MarkCredentialChanged fails, the credential IS correctly bound but the banner
// may not appear. The operator can safely retry the binding operation or manually
// call MarkCredentialChanged to recover. This failure mode is non-catastrophic.
func (s *Service) MarkCredentialChanged(ctx context.Context, workspaceID string) error {
    _, err := s.DB.ExecContext(ctx, `
        INSERT INTO workspace_agent_state
            (workspace_id, last_credential_changed_at, pending_refresh, updated_at)
        VALUES ($1, NOW(), TRUE, NOW())
        ON CONFLICT (workspace_id) DO UPDATE SET
            last_credential_changed_at = NOW(),
            pending_refresh = TRUE,
            updated_at = NOW()
    `, workspaceID)
    if err != nil {
        return fmt.Errorf("mark credential changed: %w", err)
    }
    return nil
}

// GetLastCredentialChangedAt returns the most recent credential-changed
// timestamp for the workspace, or the zero time if no row exists.
// Used by the reload handler to capture state BEFORE calling dispose,
// enabling the CAS check in MarkAgentReloaded.
func (s *Service) GetLastCredentialChangedAt(ctx context.Context, workspaceID string) (time.Time, error) {
    var t time.Time
    err := s.DB.QueryRowContext(ctx,
        `SELECT COALESCE(last_credential_changed_at, '1970-01-01') FROM workspace_agent_state WHERE workspace_id = $1`,
        workspaceID,
    ).Scan(&t)
    if err == sql.ErrNoRows {
        return time.Time{}, nil
    }
    if err != nil {
        return time.Time{}, fmt.Errorf("get last credential changed at: %w", err)
    }
    return t, nil
}

// MarkAgentReloaded clears the pending_refresh flag after a successful
// dispose. Uses SELECT FOR UPDATE to serialise against concurrent
// MarkCredentialChanged auto-commit writes, preventing the READ COMMITTED
// race where a concurrent UPSERT completing between our snapshot read and
// our pending_refresh=false write would be silently overwritten.
//
// SELECT FOR UPDATE ensures any concurrent MarkCredentialChanged UPSERT is
// either fully committed and visible before we read last_credential_changed_at,
// or is blocked until this transaction commits (after which MarkCredentialChanged
// will re-set pending_refresh=true correctly).
//
// Note: MarkCredentialChanged uses auto-commit (ExecContext, no explicit tx).
// There is no "uncommitted" MarkCredentialChanged write — the SELECT FOR UPDATE
// serialises against the implicit single-statement transaction of the UPSERT.
//
// The priorChangedAt parameter is the timestamp captured BEFORE the dispose
// call. If a new credential was staged DURING the dispose window
// (last_credential_changed_at > priorChangedAt in the updated row),
// pending_refresh is left true so the banner reappears for the new credential.
//
// Returns the timestamp written to last_agent_disposed_at (DB clock).
//
// All timestamps in workspace_agent_state use the DB server clock (NOW()).
// last_credential_changed_at is written by MarkCredentialChanged via NOW().
// last_agent_disposed_at is written here via NOW() (not the application clock)
// to ensure both columns use the same clock source and comparisons are valid.
func (s *Service) MarkAgentReloaded(
    ctx context.Context,
    tx *sql.Tx,
    workspaceID string,
    priorChangedAt time.Time,
) (time.Time, error) {
    // SELECT FOR UPDATE locks the row, ensuring any concurrent
    // MarkCredentialChanged transaction has committed before we read
    // last_credential_changed_at. Without this lock, READ COMMITTED
    // isolation would not see an uncommitted concurrent write.
    //
    // Invariant: this function is only called after the handler verified
    // agentNeedsRefresh = true, which guarantees a workspace_agent_state
    // row already exists (pending_refresh = TRUE). The SELECT FOR UPDATE
    // will therefore always find the row. The only deletion path for the
    // row is CASCADE from workspace deletion, which the phase guard (Active
    // check) rules out before we reach this point.
    //
    // If the row is NOT found (invariant violated — migration error, test
    // environment, future code change), we return apierrors.ErrNoAgentStateRow rather
    // than silently skipping the lock and proceeding. The handler maps this to
    // HTTP 409 "workspace has no pending credentials to reload", turning a
    // silent correctness hole into a loud, diagnosable failure.
    var currentChangedAt time.Time
    err := tx.QueryRowContext(ctx,
        `SELECT COALESCE(last_credential_changed_at, '1970-01-01')
         FROM workspace_agent_state
         WHERE workspace_id = $1
         FOR UPDATE`,
        workspaceID,
    ).Scan(&currentChangedAt)
    if err == sql.ErrNoRows {
        // Return the shared sentinel from apierrors (api/internal/errors/errors.go).
        // Both this function and the handler check it via errors.Is(err, apierrors.ErrNoAgentStateRow).
        // Neither handlers nor database imports the other; the sentinel lives in the
        // shared apierrors package that both already import.
        return time.Time{}, apierrors.ErrNoAgentStateRow
    }
    if err != nil {
        return time.Time{}, fmt.Errorf("lock workspace_agent_state: %w", err)
    }

    // pending_refresh stays true if a credential was staged during the dispose
    // window (currentChangedAt > priorChangedAt).
    newPendingRefresh := currentChangedAt.After(priorChangedAt)

    // Use NOW() (DB server clock) for last_agent_disposed_at so that both
    // timestamp columns in workspace_agent_state use the same clock source.
    // RETURNING gives us the exact value the DB wrote.
    var disposedAt time.Time
    err = tx.QueryRowContext(ctx, `
        INSERT INTO workspace_agent_state
            (workspace_id, last_agent_disposed_at, pending_refresh, updated_at)
        VALUES ($1, NOW(), $2, NOW())
        ON CONFLICT (workspace_id) DO UPDATE SET
            last_agent_disposed_at = NOW(),
            pending_refresh = $2,
            updated_at = NOW()
        RETURNING last_agent_disposed_at
    `, workspaceID, newPendingRefresh).Scan(&disposedAt)
    if err != nil {
        return time.Time{}, fmt.Errorf("mark agent reloaded: %w", err)
    }
    return disposedAt, nil
}

// Note: ErrNoAgentStateRow is NOT defined here. It lives in api/internal/errors/errors.go
// (the shared apierrors package) so both the database layer (which returns it) and the
// handler layer (which checks it) can reference it without a circular import.
// See the apierrors.ErrNoAgentStateRow declaration note in US-27a.7.
```

**`api/internal/interfaces/interfaces.go` — no changes required for Epic 27a:**

The three new `database.Service` methods (`MarkCredentialChanged`, `GetLastCredentialChangedAt`, `MarkAgentReloaded`) are consumed via `CredentialStateWriter` and `AgentStateStore` — handler-layer interfaces satisfied by the concrete `*database.Service` type passed directly from `app.go`. They do NOT flow through `workspace.Service.dbService`, which is typed as `apiinterfaces.DatabaseService`. Therefore:

- `api/internal/interfaces/interfaces.go` — **no changes needed for Epic 27a**.
- `api/internal/mocks/database.go` — **no changes needed for Epic 27a**.

In Epic 27b, `ListPendingReloadWorkspaces` must be added to `DatabaseService` (it is called by `workspace.Service.ListPendingReloadWorkspaces` via `s.dbService`). That change belongs in the 27b PR.

**Wired into:**

- `api/internal/handlers/secrets.go` — `SetBindings` and `SetWorkspaceEnv` (via `AddBindings`) call `MarkCredentialChanged` when `result.LLMProviderAffected` is true. `GetBindings` is read-only and never triggers the signal. The `SetBindings` and `GetBindings` methods are on `SecretsHandler` (`secrets.go:273-310`) — there is no separate `workspaces.go` in `package handlers`.
- `database.MarkWorkspaceDeleted` — the workspace_agent_state row cascades via FK on workspace delete; no explicit delete needed but the test asserts it.

**How `secrets.go` accesses `MarkCredentialChanged` — new `CredentialStateWriter` interface:**

`WorkspaceMetadataUpdater` (defined in `api/internal/handlers/models.go:23`, **not** `secrets.go`) has one method: `UpdateWorkspace(ctx, workspaceID, updates types.WorkspaceUpdates) error`. Its purpose is updating non-sensitive workspace display fields. Adding a credential-state method to it would violate ISP. Instead, `MarkCredentialChanged` is exposed through a new, purpose-specific interface:

```go
// CredentialStateWriter is the dependency interface for handlers that need
// to record that workspace credentials have changed. Defined in
// api/internal/handlers/secrets.go alongside SecretsHandler.
//
// Satisfied by *database.Service, which implements MarkCredentialChanged
// in US-27a.2.
//
// NOTE: MarkCredentialChanged does NOT take a *sql.Tx. The secrets binding
// write (PgSecretStore) and the credential-state write (database.Service) use
// incompatible connection pools (pgxpool.Pool vs *sql.DB). Cross-pool transactions
// are impossible in PostgreSQL. The writes are best-effort sequential — see DP5.
type CredentialStateWriter interface {
    MarkCredentialChanged(ctx context.Context, workspaceID string) error
}

// SecretsHandler gains one new field:
type SecretsHandler struct {
    // ... existing fields ...
    credStateWriter CredentialStateWriter // nil-safe; see SetCredentialStateWriter
}

// SetCredentialStateWriter installs the writer. If left nil,
// MarkCredentialChanged is silently skipped (banner won't appear but no crash).
// This matches the nil-safe pattern used for SetPasswordVerifier and
// SetPodIPResolver on the same handler.
func (h *SecretsHandler) SetCredentialStateWriter(w CredentialStateWriter) {
    h.credStateWriter = w
}
```

Wired in `api/internal/app/app.go`:

```go
secretsHandler.SetCredentialStateWriter(dbSvc) // database.Service satisfies CredentialStateWriter
```

The call pattern in both mutation handlers uses `result.LLMProviderAffected` from `BindingsMutationResult` (defined in US-27a.2b below). The undefined `isLLMProvider` variable from prior design drafts is replaced by this result field — see US-27a.2b for the full `SetBindings` and `SetWorkspaceEnv` handler pseudocode.

### US-27a.2b — `pkg/secrets`: `BindingsMutationResult` and `SecretsHandler` wiring

**Scope:** Two new files in `pkg/secrets/`, changes to `pkg/secrets/secret_service.go`, and changes to `api/internal/handlers/secrets.go`. Depends on US-27a.2 (handler wiring only — see story table note). `bindings_diff_test.go` declares `package secrets` (not `package secrets_test`) to access the unexported `computeBindingsDiff` and `sortedKeys` functions directly.

**File placement:** `BindingsMutationResult`, `computeBindingsDiff`, and `sortedKeys` live in a new dedicated file `pkg/secrets/bindings_diff.go`. Pure, stateless helpers belong in a focused file rather than growing `secret_service.go`. `CredentialStateWriter` lives in `api/internal/handlers/secrets.go` alongside `SecretsHandler`.

**`pkg/secrets/bindings_diff.go` — new file:**

```go
package secrets

import "sort"

// BindingsMutationResult describes what changed in a SetBindings or AddBindings
// call. Returned as a value (not pointer) so callers never need a nil check —
// on any error return path the zero value is returned and LLMProviderAffected
// is always false.
//
// AddedTypes and RemovedTypes are deduplicated and sorted alphabetically.
// Ordering is deterministic; tests may use assert.Equal on these slices.
// nil (not []string{}) means no secrets of that direction were affected;
// callers must use len() not == nil to check emptiness.
//
// When LLMProviderAffected is true but both AddedTypes and RemovedTypes are nil,
// the diff could not be computed (pre-read of existing bindings failed) and the
// conservative true value was used. Callers can distinguish this case but are
// not required to — treating it the same as a confirmed llm-provider change is
// correct behaviour.
type BindingsMutationResult struct {
    // LLMProviderAffected is true if any llm-provider secret was added or removed,
    // OR if the diff could not be computed (conservative fallback).
    // Callers that trigger MarkCredentialChanged check only this field.
    // SecretTypeLLMProvider is defined in pkg/secrets/types.go:26.
    LLMProviderAffected bool
    // AddedTypes lists the secret types added (deduplicated, sorted alphabetically).
    // nil when no secrets were added or when diff was unavailable.
    AddedTypes []string
    // RemovedTypes lists the secret types removed (deduplicated, sorted alphabetically).
    // Always nil for AddBindings (additive-only operation).
    // nil when no secrets were removed or when diff was unavailable.
    RemovedTypes []string
}

// computeBindingsDiff computes the BindingsMutationResult from the pre-write
// existing binding set and the post-write new binding set.
//
// Pass nil (or empty) for existing when computing AddBindings results —
// additive-only operations have no removals. nil iterates safely in Go.
//
// Both slices contain validated *UserSecret objects with type information,
// accumulated during SecretService's per-ID validation loops before the store
// write. No ADDITIONAL DB queries are needed by this function — both callers
// provide slices already populated from pre-existing service logic:
// SetBindings reads `existing` via store.GetBindings (pre-existing audit logic)
// and accumulates `newSecrets` during its per-ID validation loop.
// AddBindings passes nil for `existing` (additive-only, no pre-read needed)
// and accumulates `newSecrets` during its per-ID validation loop.
// This function only processes slices already in memory.
//
// SecretTypeLLMProvider ("llm-provider") is defined in pkg/secrets/types.go:26
// and is in the same package, so it is directly accessible here.
//
// Note: SetBindings also builds existingByID and newByID maps for its audit
// loop. These are separate map[string]bool instances (different value type from
// the map[string]*UserSecret maps here). The duplication is intentional —
// merging the maps would couple the diff logic to the audit logic, making both
// harder to change independently. The cost is O(n) over a small n (typically
// 1-10 secrets).
func computeBindingsDiff(existing, newSecrets []*UserSecret) BindingsMutationResult {
    existingByID := make(map[string]*UserSecret, len(existing))
    for _, s := range existing {
        existingByID[s.ID] = s
    }
    newByID := make(map[string]*UserSecret, len(newSecrets))
    for _, s := range newSecrets {
        newByID[s.ID] = s
    }

    addedTypes := map[string]struct{}{}
    for _, s := range newSecrets {
        if _, wasPresent := existingByID[s.ID]; !wasPresent {
            addedTypes[s.Type] = struct{}{}
        }
    }

    removedTypes := map[string]struct{}{}
    for _, s := range existing {
        if _, stillPresent := newByID[s.ID]; !stillPresent {
            removedTypes[s.Type] = struct{}{}
        }
    }

    _, llmAdded   := addedTypes[SecretTypeLLMProvider]
    _, llmRemoved := removedTypes[SecretTypeLLMProvider]

    return BindingsMutationResult{
        LLMProviderAffected: llmAdded || llmRemoved,
        AddedTypes:          sortedKeys(addedTypes),
        RemovedTypes:        sortedKeys(removedTypes),
    }
}

// sortedKeys returns the keys of a map[string]struct{} as a sorted slice,
// or nil if the map is empty. Deterministic ordering makes test assertions
// reliable with assert.Equal. Callers must use len() not == nil to check
// emptiness — nil and []string{} are semantically equivalent here.
func sortedKeys(m map[string]struct{}) []string {
    if len(m) == 0 {
        return nil
    }
    keys := make([]string, 0, len(m))
    for k := range m {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    return keys
}
```

**`sort` import:** `bindings_diff.go` imports `"sort"` (standard library). `secret_service.go` does not need a new import for this change.

**Updated `SetBindings` signature and body in `pkg/secrets/secret_service.go`:**

```go
// SetBindings signature changes from:
//   func (s *SecretService) SetBindings(...) error
// to:
//   func (s *SecretService) SetBindings(...) (BindingsMutationResult, error)
func (s *SecretService) SetBindings(
    ctx context.Context,
    userID, workspaceID string,
    secretIDs []string,
) (BindingsMutationResult, error) {
    if err := s.verifyWorkspaceOwner(ctx, userID, workspaceID); err != nil {
        return BindingsMutationResult{}, err
    }

    // Validate ownership and accumulate full secret objects for diff computation.
    // newSecrets is in the same order as secretIDs — this invariant is relied upon
    // by the audit loop below, which uses newSecrets instead of secretIDs.
    var newSecrets []*UserSecret
    for _, sid := range secretIDs {
        secret, err := s.store.GetSecret(ctx, userID, sid)
        if err != nil {
            return BindingsMutationResult{}, err
        }
        if secret == nil {
            return BindingsMutationResult{}, fmt.Errorf("%w: %s", ErrSecretNotFound, sid)
        }
        newSecrets = append(newSecrets, secret)
    }

    // Read existing bindings for diff and audit.
    // If GetBindings fails: proceed with the write (the binding mutation is
    // still valid), set existing = nil (audit will show no unbinds — acceptable
    // data loss on a DB error), and return a conservative result with
    // LLMProviderAffected = true so the banner appears even when we cannot
    // confirm it is needed. A false-positive banner is better than a missed one.
    // SecretService has no logger field; the error is surfaced to the caller
    // via the conservative result. Callers that care can inspect
    // result.AddedTypes == nil && result.RemovedTypes == nil && result.LLMProviderAffected
    // as the sentinel for "diff unavailable".
    existing, getErr := s.store.GetBindings(ctx, workspaceID)
    if getErr != nil {
        existing = nil // treat as empty for audit and diff
    }

    if err := s.store.SetBindings(ctx, workspaceID, secretIDs); err != nil {
        // If SetBindings fails, return the error and discard getErr.
        // A failed binding write needs no MarkCredentialChanged notification —
        // the credential was not bound, so no agent reload is required.
        // Discarding getErr here is correct, not a bug.
        return BindingsMutationResult{}, fmt.Errorf("set bindings: %w", err)
    }

    // Audit removed and added bindings (existing behaviour, unchanged).
    // Note: existingByID and newByID here are map[string]bool, distinct from
    // the map[string]*UserSecret maps inside computeBindingsDiff. See the
    // comment in computeBindingsDiff for why the duplication is intentional.
    existingByID := make(map[string]bool, len(existing))
    for _, sec := range existing {
        existingByID[sec.ID] = true
    }
    newByID := make(map[string]bool, len(newSecrets))
    for _, sec := range newSecrets {
        newByID[sec.ID] = true
    }
    for _, sec := range existing {
        if !newByID[sec.ID] {
            sid := sec.ID
            s.audit(ctx, userID, "unbind", &sid, &workspaceID, nil)
        }
    }
    for _, sec := range newSecrets {
        if !existingByID[sec.ID] {
            sid := sec.ID
            s.audit(ctx, userID, "bind", &sid, &workspaceID, nil)
        }
    }

    // If GetBindings failed, return the conservative result.
    // LLMProviderAffected = true, AddedTypes = nil, RemovedTypes = nil.
    // The nil types are the sentinel for "diff unavailable" (see BindingsMutationResult doc).
    if getErr != nil {
        return BindingsMutationResult{LLMProviderAffected: true}, nil
    }
    return computeBindingsDiff(existing, newSecrets), nil
}
```

**Updated `AddBindings` signature and body in `pkg/secrets/secret_service.go`:**

```go
// AddBindings signature changes from:
//   func (s *SecretService) AddBindings(...) error
// to:
//   func (s *SecretService) AddBindings(...) (BindingsMutationResult, error)
func (s *SecretService) AddBindings(
    ctx context.Context,
    userID, workspaceID string,
    secretIDs []string,
) (BindingsMutationResult, error) {
    if len(secretIDs) == 0 {
        return BindingsMutationResult{}, nil
    }
    if err := s.verifyWorkspaceOwner(ctx, userID, workspaceID); err != nil {
        return BindingsMutationResult{}, err
    }

    // Validate ownership and accumulate full secret objects for diff computation.
    var newSecrets []*UserSecret
    for _, sid := range secretIDs {
        secret, err := s.store.GetSecret(ctx, userID, sid)
        if err != nil {
            return BindingsMutationResult{}, err
        }
        if secret == nil {
            return BindingsMutationResult{}, fmt.Errorf("%w: %s", ErrSecretNotFound, sid)
        }
        newSecrets = append(newSecrets, secret)
    }

    if err := s.store.AddBindings(ctx, workspaceID, secretIDs); err != nil {
        return BindingsMutationResult{}, fmt.Errorf("add bindings: %w", err)
    }

    // Audit added bindings (existing behaviour, unchanged).
    for _, sec := range newSecrets {
        sid := sec.ID
        s.audit(ctx, userID, "bind", &sid, &workspaceID, nil)
    }

    // AddBindings is additive-only; pass nil for existing (no removals possible).
    // computeBindingsDiff iterates nil safely (zero iterations).
    return computeBindingsDiff(nil, newSecrets), nil
}
```

**Updated `SecretsHandler.SetBindings` call site in `api/internal/handlers/secrets.go`:**

```go
result, err := h.svc.SetBindings(c.Request.Context(), userID, workspaceID, req.SecretIDs)
if err != nil {
    handleSecretError(c, err)
    return
}

// result.LLMProviderAffected is true if any llm-provider secret was added or
// removed, or if the pre-read of existing bindings failed (conservative fallback).
// When true and AddedTypes/RemovedTypes are both nil, the diff was unavailable.
// Either way, calling MarkCredentialChanged is correct — a false-positive banner
// is safer than a missed one.
if result.LLMProviderAffected && h.credStateWriter != nil {
    if err := h.credStateWriter.MarkCredentialChanged(c.Request.Context(), workspaceID); err != nil {
        h.logger.Warn("mark credential changed: banner may not appear",
            zap.String("workspaceID", workspaceID), zap.Error(err))
        // Do NOT return an error: the binding succeeded. Only the staging signal is missing.
    }
}

h.pushSecretsToAgent(c, userID, workspaceID)
c.Status(http.StatusNoContent)
```

**Updated `SecretsHandler.SetWorkspaceEnv` call site in `api/internal/handlers/secrets.go`:**

```go
result, err := h.svc.AddBindings(ctx, userID, workspaceID, newBindings)
if err != nil {
    h.warn("SetWorkspaceEnv: AddBindings failed", ...)
    c.JSON(http.StatusInternalServerError, ...)
    return
}

// SetWorkspaceEnv creates env secrets — LLMProviderAffected will be false
// in practice today. The check is here for correctness should AddBindings
// ever be called with llm-provider IDs from a future code path.
if result.LLMProviderAffected && h.credStateWriter != nil {
    if err := h.credStateWriter.MarkCredentialChanged(ctx, workspaceID); err != nil {
        h.logger.Warn("mark credential changed: banner may not appear",
            zap.String("workspaceID", workspaceID), zap.Error(err))
    }
}

c.Status(http.StatusNoContent)
```

**`DeleteSecret` gap — scoped as follow-up, not Epic 27a:**

`SecretsHandler.DeleteSecret` deletes a secret that may currently be bound to one or more workspaces. If the secret is `llm-provider` type, those workspaces' opencode instances still have the credential loaded after deletion. The user should be prompted to reload. This is not addressed in Epic 27a.

**Risk:** A user who deletes their only OpenAI credential sees no banner. The stale credential stays loaded in opencode until the next explicit reload or pod restart.

**Disposition:** Tracked as `FOLLOW-UP-27a-1: MarkCredentialChanged on DeleteSecret`.

**Call sites:**
1. `SecretsHandler.DeleteSecret` (`secrets.go:186`) — deletes a user-chosen secret of any type including `llm-provider`. Needs the follow-up treatment.
2. `SecretsHandler.DeleteWorkspaceEnv` (`secrets.go:613`) — deletes an env-type secret. The type is always `env`, never `llm-provider`. No change needed.

**Implementation sequence for `SecretsHandler.DeleteSecret`:**

The `GetBindingsForSecret` call must happen **before** `DeleteSecret`. The FK `user_secret_bindings.secret_id → user_secrets.id ON DELETE CASCADE` (`000008_user_secrets.up.sql`) removes all binding rows when the secret row is deleted — calling `GetBindingsForSecret` after deletion returns empty.

The `BindingsMutationResult` / Option D pattern (return type info from the mutating call) cannot be applied here: `DeleteSecretResult.Type` would only be known after `DeleteSecret` runs, but workspace IDs must be captured before it runs. The two requirements conflict. A pre-flight `GetSecret` call in the handler is therefore unavoidable.

This means three `store.GetSecret` calls occur for one user action:
1. `h.svc.GetSecret` in the handler (pre-flight — gets the type)
2. `h.svc.GetBindingsForSecret` internally calls `store.GetSecret` for ownership (`secret_service.go:398-400`) — only reached if type is llm-provider
3. `h.svc.DeleteSecret` internally calls `store.GetSecret` for ownership (`secret_service.go:237`)

All three reads are within a single HTTP request, ownership is re-verified at each service call (correct security posture), and refactoring the service layer to avoid this is out of scope for a follow-up. The triple read is acceptable.

Correct handler sequence:

```go
// Step 1: pre-flight read to determine type.
// The handler has only secretID from the URL; it has no secret object in scope.
secret, err := h.svc.GetSecret(c.Request.Context(), userID, secretID)
if err != nil {
    handleSecretError(c, err)
    return
}

// Step 2: if llm-provider, capture workspace IDs BEFORE deletion.
// Must happen before step 3 — FK cascade removes binding rows on delete.
// secrets.SecretTypeLLMProvider is the package-qualified form used in package handlers,
// which imports pkg/secrets as "secrets" (secrets.go:18).
var affectedWorkspaces []string
if secret.Type == secrets.SecretTypeLLMProvider {
    affectedWorkspaces, err = h.svc.GetBindingsForSecret(c.Request.Context(), userID, secretID)
    if err != nil {
        // Non-fatal: proceed with deletion. Banner will not appear for affected workspaces.
        // Recovery: user must manually trigger POST /workspaces/:id/agent/reload to clear
        // the stale credential from opencode's in-memory provider state.
        h.warn("DeleteSecret: GetBindingsForSecret failed; banner may not appear",
            "secretID", secretID, "error", err.Error())
        // h.warn uses the handler's variadic string key-value helper (secrets.go:422);
        // zap is not imported in this file.
        affectedWorkspaces = nil
    }
}

// Step 3: delete. store.GetSecret is called again internally for ownership — triple read,
// acceptable (see note above).
if err := h.svc.DeleteSecret(c.Request.Context(), userID, secretID); err != nil {
    handleSecretError(c, err)
    return
}

// Step 4: notify affected workspaces. Best-effort; same pattern as SetBindings.
for _, wsID := range affectedWorkspaces {
    if h.credStateWriter != nil {
        if err := h.credStateWriter.MarkCredentialChanged(c.Request.Context(), wsID); err != nil {
            h.warn("DeleteSecret: MarkCredentialChanged failed",
                "workspaceID", wsID, "error", err.Error())
        }
    }
}

c.Status(http.StatusNoContent)
```

This is a handler-layer addition. Not in Epic 27a scope to keep the story atomic.

### US-27a.3 — Expose `agentNeedsRefresh`

`pkg/types/types.go` adds to `Workspace` and `WorkspaceListItem`:

```go
type Workspace struct {
    // ... existing fields ...
    AgentNeedsRefresh        bool       `json:"agentNeedsRefresh"`
    CredentialsPendingSince  *time.Time `json:"credentialsPendingSince,omitempty"`
}
```

`workspace_service.GetWorkspace` maps from `WorkspaceMetadata` to `types.Workspace` (line 265-278). The two new fields are propagated there:

```go
ws := &types.Workspace{
    // ... existing ...
    AgentNeedsRefresh:       meta.AgentNeedsRefresh,
    CredentialsPendingSince: meta.CredentialsPendingSince,
}
```

`workspace_service.ListWorkspaces` maps `WorkspaceListItem` similarly. `WorkspaceListItem` is defined at `pkg/types/types.go:424`; it also gains the two new fields. The mapping loop at `workspace_service.go:309-317` must explicitly populate them — omitting this while correctly extending the struct and DB query would silently return `agentNeedsRefresh: false` in every list response, breaking the workspace-list banner:

```go
// workspace_service.go — inside the ListWorkspaces mapping loop (lines 309-317 area)
item := &types.WorkspaceListItem{
    // ... existing fields ...
    AgentNeedsRefresh:       m.AgentNeedsRefresh,
    CredentialsPendingSince: m.CredentialsPendingSince,
}
```

The scan loop in `database.ListWorkspaces` (`database.go:540-545`) is extended identically to `GetWorkspace`. The `CredentialsPendingSince` scan target is `*time.Time`; PostgreSQL NULL scans to nil correctly.

### US-27a.4 — `pkg/agent/opencode.Client` rename

In `pkg/agent/opencode/client.go`:

- Rename `RefreshCredentials` → `StageCredentials`. New doc comment makes the contract explicit: writes to auth.json (via `PUT /auth/:providerID`) but does NOT trigger provider-state refresh; caller must invoke `DisposeInstance` separately to apply.
- Remove the `DisposeInstance(ctx)` call from inside `StageCredentials`. `StageCredentials` is now equivalent to `PushCredentials`.
- Keep `PushCredentials`, `DisposeInstance`, `setAuth` unchanged. `RefreshCredentials` is removed entirely; no caller in the repo will reference it after US-27a.5.
- Update `pkg/agent/opencode/client_test.go` and `client_integration_test.go`:
  - Drop the test that asserts dispose-after-push.
  - Add `TestStageCredentials_DoesNotCallDispose` verifying no dispose call against a mock that fails the test if it sees one.
  - Add `TestStageCredentials_PushFailure_NoSideEffects` verifying nothing is written or disposed if the auth PUT fails.

### US-27a.5 — agentd `/v1/reload-secrets` no longer auto-disposes

In `cmd/workspace-agentd/secrets.go` `reloadSecretsHandler`, replace lines 275-299:

```go
// Stage llm-provider credentials. StageCredentials writes to opencode's
// auth.json but does NOT dispose the instance. The user triggers reload
// explicitly via POST /api/v1/workspaces/:id/agent/reload (Epic 27a).
if hasLLMProviders(batch) {
    staged := m.StagedProviders()
    if len(staged) > 0 {
        oc := opencode.NewClient(
            fmt.Sprintf("http://localhost:%d", agentd.AgentPort),
            opencodePassword,
        )
        if err := oc.StageCredentials(r.Context(), staged); err != nil {
            log.Warn("reload-secrets: opencode stage failed; credentials remain in "+
                "auth.json on disk but in-memory provider state will not pick them up "+
                "until the next explicit reload or pod restart",
                zap.Error(err))
            // Stage failure is a soft error: credentials are already written
            // to auth.json by FlushProviders above; the next explicit reload
            // or pod restart will load them. No fallback proc.restart() because
            // we are deliberately not auto-disposing any more.
        }
    }
}

// Restart for env-secret changes (agent reads env at boot only).
// This path is independent of llm-provider staging — env-secret restart
// and llm-provider staging are orthogonal operations.
// Note: if a batch contains BOTH llm-provider and env secrets, StageCredentials
// runs first (writing to auth.json) and then proc.restart() runs. The restart
// re-reads auth.json on boot, picking up the staged LLM credentials as a
// side-effect. This makes StageCredentials redundant in that path but harmless.
if proc != nil && shouldRestart(batch) {
    log.Info("env secrets changed, restarting opencode")
    //nolint:contextcheck // restart() spawns its own goroutine with a fresh context
    proc.restart()
}
```

The `configReloaded` flag is **removed entirely**. It was previously used to prevent double-restart when both credential dispose and env restart happened in the same batch; since credential dispose no longer happens automatically, the flag serves no purpose. Env-secret restart now runs unconditionally when `shouldRestart(batch)` is true, regardless of llm-provider staging result.

The hardcoded `"4097"` at `api/internal/handlers/secrets.go:452` is replaced with `fmt.Sprintf("http://%s:%d/v1/reload-secrets", podIP, agentd.AgentdPort)`.

The hardcoded `"4096"` in `cmd/workspace-agentd/secrets.go:279` is already using `agentd.AgentPort` correctly; verify and keep.

### US-27a.6 — agentd new endpoint `POST /v1/agent/reload`

New file `cmd/workspace-agentd/agent_reload.go`:

```go
// agentReloadHandler triggers an opencode instance dispose. This is the
// only path in the system that calls dispose after Epic 27a ships.
// In-flight LLM streams are aborted; sessions persist in SQLite.
//
// Authentication: none at the application layer. The trust boundary is
// the Kubernetes NetworkPolicy which allows only the API server pod to
// reach the workspace pod on port agentd.AgentdPort (4097). See A15.
//
// Idempotent: opencode's InstanceStore short-circuits on already-disposed
// entries (instance-store.ts:145-153); concurrent calls are safe.
func agentReloadHandler(opencodePassword string, log *zap.Logger) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        if r.Method != http.MethodPost {
            w.WriteHeader(http.StatusMethodNotAllowed)
            return
        }

        oc := opencode.NewClient(
            fmt.Sprintf("http://localhost:%d", agentd.AgentPort),
            opencodePassword,
        )
        // 10s matches the dispose call's expected latency: opencode's dispose
        // is in-process cache invalidation, sub-100ms in practice.
        ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
        defer cancel()

        if err := oc.DisposeInstance(ctx); err != nil {
            log.Error("agent reload: dispose failed", zap.Error(err))
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]string{
                "error": "dispose failed: " + err.Error(),
            })
            return
        }

        log.Info("agent reload: dispose succeeded")
        w.WriteHeader(http.StatusOK)
        _ = json.NewEncoder(w).Encode(map[string]any{"disposed": true})
    }
}
```

Register in `cmd/workspace-agentd/main.go` alongside the existing `/v1/reload-secrets`:

```go
userMux.HandleFunc("/v1/reload-secrets", reloadSecretsHandler(loadMaterializeConfig(), proc, password))
userMux.HandleFunc("/v1/agent/reload", agentReloadHandler(password, log))
```

### US-27a.7 — API server `POST /api/v1/workspaces/:id/agent/reload`

**Handler in `api/internal/handlers/agent_reload.go`:**

```go
// ErrNoAgentStateRow is returned by AgentStateStore.MarkAgentReloaded when the
// workspace_agent_state row does not exist for the given workspace.
//
// Defined in api/internal/errors/errors.go (the shared apierrors package) so that
// both the handler layer (which checks it) and the database layer (which returns it)
// can reference it without creating a circular import.
//
// Layering:
//   handlers (agent_reload.go) imports apierrors -> checks errors.Is(err, apierrors.ErrNoAgentStateRow)
//   database (database.go)    imports apierrors -> returns apierrors.ErrNoAgentStateRow
//   Neither handlers nor database imports the other. app.go wires them together.
//
// In api/internal/errors/errors.go, add:
//   var ErrNoAgentStateRow = errors.New("workspace_agent_state row not found for workspace")

// AgentReloadHandler handles POST /api/v1/workspaces/:id/agent/reload.
// It is registered via RouterConfig.AgentReloadHandler and follows the
// SecretsHandler struct pattern (RouterConfig field, wired in app.go).
type AgentReloadHandler struct {
    workspaceSvc  WorkspaceServicer       // interface for testability
    db            AgentStateStore         // interface for testability
    podResolver   PodIPResolver           // existing interface, handlers/secrets.go:33
    httpClient    *http.Client            // 15s timeout: larger than agentd's 10s dispose timeout — see wiring note
    logger        *zap.Logger
    metricsService *metrics.Service
}

// WorkspaceServicer is the minimal workspace service surface needed by
// the reload handler.
type WorkspaceServicer interface {
    GetWorkspace(ctx context.Context, userID, workspaceID string) (*types.Workspace, error)
}

// AgentStateStore is the minimal DB surface needed by the reload handler.
type AgentStateStore interface {
    GetLastCredentialChangedAt(ctx context.Context, workspaceID string) (time.Time, error)
    MarkAgentReloaded(ctx context.Context, tx *sql.Tx, workspaceID string, priorChangedAt time.Time) (time.Time, error)
    BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// Reload handles POST /api/v1/workspaces/:id/agent/reload.
// It resolves ownership, verifies the workspace is Active, dispatches the
// dispose to agentd, updates agent state, and clears the model cache.
func (h *AgentReloadHandler) Reload(c *gin.Context) {
    start := time.Now()
    workspaceID := c.Param("id")
    // extractAuth is the package-level helper in package handlers (secrets.go:795).
    // It reads userID from the JWT claims set by the auth middleware.
    userID, _ := extractAuth(c)
    if userID == "" {
        c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
        return
    }

    ws, err := h.workspaceSvc.GetWorkspace(c.Request.Context(), userID, workspaceID)
    if err != nil {
        RespondWithError(c, err) // unqualified: RespondWithError is in the same package (handlers/errors.go)
        return
    }

    // Phase guard: only Active workspaces have a running pod to reload.
    // string(v1.WorkspacePhaseActive) == "Active" (workspace_types.go:152).
    if ws.Phase != string(v1.WorkspacePhaseActive) {
        c.JSON(http.StatusConflict, gin.H{
            "error": fmt.Sprintf("cannot reload agent: workspace is in phase %q (must be Active)", ws.Phase),
        })
        return
    }

    podIP, err := h.podResolver.GetWorkspacePodIP(c.Request.Context(), userID, workspaceID)
    if err != nil || podIP == "" {
        c.JSON(http.StatusConflict, gin.H{
            "error": "cannot reload agent: workspace pod is not reachable",
        })
        return
    }

    // Capture the credential-changed timestamp BEFORE dispose. Used by
    // MarkAgentReloaded's CAS to detect credentials staged during the
    // dispose window.
    priorChangedAt, err := h.db.GetLastCredentialChangedAt(c.Request.Context(), workspaceID)
    if err != nil {
        RespondWithError(c, apierrors.NewInternalError("agent_state_read_failed", err))
        return
    }

    // Dispatch to agentd (which calls opencode dispose locally).
    // 15s timeout: larger than agentd's own 10s dispose timeout so the agentd
    // deadline always fires first, giving a structured error rather than a
    // raw connection-timeout. See R6-9.
    agentdURL := fmt.Sprintf("http://%s:%d/v1/agent/reload", podIP, agentd.AgentdPort)
    req, _ := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, agentdURL, nil)
    resp, err := h.httpClient.Do(req)
    if err != nil {
        h.logger.Error("agent reload: agentd unreachable", zap.Error(err))
        RespondWithError(c, apierrors.NewInternalError("agent_unreachable", err))
        return
    }
    defer resp.Body.Close() //nolint:errcheck
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
        RespondWithError(c, apierrors.NewInternalError("dispose_failed",
            fmt.Errorf("agentd returned %d: %s", resp.StatusCode, string(body)),
        ))
        return
    }

    // Dispose succeeded. Update agent state in a transaction.
    // MarkAgentReloaded uses SELECT FOR UPDATE to prevent the READ COMMITTED
    // race with concurrent MarkCredentialChanged calls (see US-27a.2).
    tx, err := h.db.BeginTx(c.Request.Context(), nil)
    if err != nil {
        h.logger.Warn("agent reload: tx begin failed; dispose done, banner may persist",
            zap.Error(err))
        c.JSON(http.StatusOK, gin.H{
            "disposed":       true,
            "lastDisposedAt": time.Now().UTC().Format(time.RFC3339),
            "warning": "Agent was reloaded but state could not be updated. " +
                "The banner may reappear; clicking Reload again is safe.",
        })
        return
    }
    defer tx.Rollback() //nolint:errcheck

    disposedAt, err := h.db.MarkAgentReloaded(c.Request.Context(), tx, workspaceID, priorChangedAt)
    if err != nil {
        if errors.Is(err, apierrors.ErrNoAgentStateRow) {
            // Invariant violation: the workspace has no pending credentials row.
            // This should not happen if the handler correctly checks agentNeedsRefresh
            // before calling reload. Surface as 409 rather than silently proceeding.
            c.JSON(http.StatusConflict, gin.H{
                "error": "workspace has no pending credentials to reload",
            })
            return
        }
        h.logger.Warn("agent reload: MarkAgentReloaded failed", zap.Error(err))
        c.JSON(http.StatusOK, gin.H{
            "disposed":       true,
            "lastDisposedAt": time.Now().UTC().Format(time.RFC3339),
            "warning": "Agent was reloaded but state could not be updated. " +
                "The banner may reappear; clicking Reload again is safe.",
        })
        return
    }
    if err := tx.Commit(); err != nil {
        h.logger.Warn("agent reload: tx commit failed", zap.Error(err))
        c.JSON(http.StatusOK, gin.H{
            "disposed":       true,
            "lastDisposedAt": time.Now().UTC().Format(time.RFC3339),
            "warning": "Agent was reloaded but state could not be updated. " +
                "The banner may reappear; clicking Reload again is safe.",
        })
        return
    }

    // Clear model cache so next /models call reflects newly-loaded providers.
    // clearModelCache() clears all workspaces (it's a global cache); acceptable
    // given the 5s TTL — see A13.
    clearModelCache()

    if h.metricsService != nil {
        h.metricsService.RecordRequest("AgentReload", "", 0, time.Since(start), 0)
    }

    c.JSON(http.StatusOK, gin.H{
        "disposed":       true,
        "lastDisposedAt": disposedAt.Format(time.RFC3339),
    })
}
```

**Imports for `api/internal/handlers/agent_reload.go`:**

```go
import (
    "errors"
    "fmt"
    "io"
    "net/http"
    "time"

    "github.com/gin-gonic/gin"
    "go.uber.org/zap"

    apierrors "github.com/lenaxia/llmsafespace/api/internal/errors"
    "github.com/lenaxia/llmsafespace/pkg/agentd"
    v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
    "github.com/lenaxia/llmsafespace/pkg/types"
)
```

Note: the `database` package (`github.com/lenaxia/llmsafespace/api/internal/services/database`) is intentionally **not** imported here. `AgentReloadHandler` depends on the `AgentStateStore` interface only. `ErrNoAgentStateRow` lives in `api/internal/errors/errors.go` (the shared `apierrors` package) — both `database.go` (which returns it) and `agent_reload.go` (which checks it via `errors.Is`) import `apierrors`; neither imports the other. See the `ErrNoAgentStateRow` note at US-27a.2 and the declaration note above.

**Error rendering — `RespondWithError` moved to `package handlers` (A27a-5):**

As part of US-27a.7 implementation, `respondWithError` (`router.go:743`) is moved to `package handlers` and renamed `RespondWithError` (exported). The implementation is identical:

```go
// api/internal/handlers/errors.go (new file)

// RespondWithError maps API errors to HTTP responses. It is the single
// shared error-rendering helper for all struct handlers in package handlers.
// router.go's inline routes call this via handlers.RespondWithError.
func RespondWithError(c *gin.Context, err error) {
    type apiError interface {
        StatusCode() int
        Error() string
    }
    if ae, ok := err.(apiError); ok {
        c.JSON(ae.StatusCode(), gin.H{"error": ae.Error()})
        return
    }
    c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
```

`router.go:743` is updated to call `handlers.RespondWithError(c, err)` in all its inline route closures (grep: all `respondWithError` call sites in `router.go`). The local `respondWithError` function in `router.go` is removed. This eliminates the per-handler duplication and establishes the correct precedent for all future struct handlers.

**RouterConfig field in `api/internal/server/router.go`:**

```go
// RouterConfig (line 44-63 area):
// AgentReloadHandler handles POST /api/v1/workspaces/:id/agent/reload (optional)
AgentReloadHandler *handlers.AgentReloadHandler
```

**Route registration in `registerWorkspaceRoutes`:**

```go
if cfg.AgentReloadHandler != nil {
    rg.POST("/:id/agent/reload", cfg.AgentReloadHandler.Reload)
}
```

**Wiring in `api/internal/app/app.go`:**

```go
agentReloadHandler := handlers.NewAgentReloadHandler(
    wsSvc,
    dbSvc,
    newSecretsPodIPResolver(crdClient, dbSvc, log), // reuse existing resolver
    // 15s timeout: intentionally larger than agentd's own 10s dispose timeout
    // (set via context.WithTimeout in agentd's agentReloadHandler).
    // This ensures agentd's deadline fires first, returning a structured JSON
    // error body, rather than the API's http.Client cutting the connection
    // before agentd can report. A 5s API timeout (< 10s agentd) would cause
    // the API to return a raw connection-timeout error while agentd continues.
    &http.Client{Timeout: 15 * time.Second},
    log,
    metricsService,
)
// In the RouterConfig:
AgentReloadHandler: agentReloadHandler,
```

### US-27a.8 — Frontend banner + confirmation modal

Workspace detail page shows banner when `workspace.agentNeedsRefresh === true`:

```
┌─────────────────────────────────────────────────────────────────────┐
│ ⓘ New credentials available                                         │
│                                                                     │
│ You added or changed credentials at 12:34 PM. Reload the agent      │
│ to start using them.                                                │
│                                                                     │
│   [Reload agent]   [Dismiss]                                        │
└─────────────────────────────────────────────────────────────────────┘
```

Click "Reload agent" → confirmation modal:

```
┌─────────────────────────────────────────────────────────────────────┐
│ Reload agent for {workspace name}?                                  │
│                                                                     │
│ ⚠ This will abort any LLM call currently in progress in this        │
│   workspace. Your sessions and conversation history are preserved.  │
│                                                                     │
│   [Cancel]                                            [Reload]      │
└─────────────────────────────────────────────────────────────────────┘
```

In Epic 27a, the modal has no drain-checkbox option — drain mode ships in 27b. Modal is one warning, one button. Call goes to `POST /api/v1/workspaces/:id/agent/reload`, no query params.

**"Dismiss" behaviour:** Hides the banner using `sessionStorage` keyed by `{workspaceId}:{credentialsPendingSince}`. This persists across in-app SPA navigation (survives route changes that remount the component) but clears on tab close or full page reload. If `agentNeedsRefresh` is still true on next session start, the banner reappears. This is the correct default: the user who dismissed can still load the credentials by clicking reload on any subsequent visit until the credential is actually applied.

For workspace list views: a single page-level banner consolidates "X workspaces have new credentials" with a "Show me" link that filters the list. Per-workspace banners only appear on detail views. This prevents N-banner pages for users with many workspaces.

**Warning path:** If the reload API returns `{disposed: true, warning: "..."}`, the frontend shows a toast "Credentials applied, but status tracking may be delayed. The banner may reappear — clicking Reload again is safe." The banner itself is NOT optimistically hidden on the warning path; it will clear on the next workspace fetch after the DB eventually updates (e.g., next page refresh, next navigation).

### US-27a.9 — Integration test: credflow re-run

A new integration test in `tests/integration/credflow_test.go` that:

1. Creates a user, workspace, binds an llm-provider credential (Anthropic).
2. Verifies workspace pod boots and opencode loads the credential on first request.
3. Adds a SECOND llm-provider credential (OpenAI) via the API.
4. **Asserts no LLM stream interruption.** A long-running session is open; sending a prompt mid-test verifies the existing Anthropic stream is NOT aborted by the new credential add.
5. **Asserts the API response carries `agentNeedsRefresh: true`** for the workspace after the OpenAI add.
6. Calls `POST /api/v1/workspaces/:id/agent/reload`.
7. Asserts opencode disposes (verified by checking `GET /api/model` shows the OpenAI provider after dispose).
8. Asserts `agentNeedsRefresh: false` after reload.
9. Asserts cleanup of test workspace, secrets, user (worklog 0125 left orphans behind).

This test is the gate before US-27a is considered shipped.

### US-27a.10 — Worklog

After implementation, file `worklogs/01XX_YYYY-MM-DD_epic-27a-credential-reload-foundation.md` covering:

- The auto-dispose removal: what changed in agentd, what went away, why.
- The `configReloaded` flag removal: why it was correct to drop it entirely.
- The Bug 11 fix: data integrity restored, FK now enforced.
- The new `workspace_agent_state` table: design rationale, alternatives considered.
- The `SELECT FOR UPDATE` race fix in `MarkAgentReloaded` and why READ COMMITTED isolation alone is insufficient.
- Backfill banner: one-time UX cost, why it's acceptable.
- Open follow-ups handed off to 27b: drain mode, bulk reload, error enrichment, docs.

---

## Test Plan

US-27a.2b test file assignments: `[diff]` → `pkg/secrets/bindings_diff_test.go` (package secrets, tests pure functions, no mocks); `[service]` → `pkg/secrets/secret_service_test.go` (tests SecretService methods with mock store — note: `TestAddBindings_Empty_ZeroResult` is in this file but needs no mock store since the early-return fires before any store call); `[handler]` → `api/internal/handlers/secrets_test.go` (tests SecretsHandler with mock svc and credStateWriter).

| Story | Test | What It Proves |
|---|---|---|
| US-27a.1 | `TestMigration_000014_AppliesAndReverts` | Up + down migration runs on a fresh DB with realistic seed data |
| US-27a.1 | `TestMigration_000014_BackfillsExistingBindings` | Workspaces with pre-existing llm-provider bindings get `pending_refresh=true` |
| US-27a.1 | `TestMigration_000014_Bug11_FK_Enforced` | After migration, deleting a workspace cascades to user_secret_bindings |
| US-27a.1 | `TestMigration_000014_Bug11_TypeMatches` | After migration, `user_secret_bindings.workspace_id` is UUID |
| US-27a.1 | `TestMigration_000014_Bug12_FK_Enforced` | After migration, deleting a user while workspaces exist returns FK violation (RESTRICT) |
| US-27a.1 | `TestMigration_000014_Bug12_DeleteUser_SucceedsAfterWorkspacesDeleted` | After all workspaces soft-deleted, DeleteUser succeeds |
| US-27a.1 | `TestMigration_000014_Bug12_TypeMatches` | After migration, `workspaces.user_id` is VARCHAR(36) |
| US-27a.2 | `TestMarkCredentialChanged_NewWorkspace_InsertsRow` | First call inserts; pending_refresh=true |
| US-27a.2 | `TestMarkCredentialChanged_ExistingRow_UpdatesTimestamp` | Subsequent call updates last_credential_changed_at |
| US-27a.2 | `TestMarkCredentialChanged_NoExternalTx_OpensOwnTx` | MarkCredentialChanged does not require a *sql.Tx parameter; it manages its own connection |
| US-27a.2 | `TestMarkAgentReloaded_ForUpdatePreventsRace` | Concurrent MarkCredentialChanged tx blocked until MarkAgentReloaded commits; flag stays true |
| US-27a.2 | `TestMarkAgentReloaded_PriorChangedAtCapturesRace` | If credential staged after priorChangedAt, pending_refresh stays true |
| US-27a.2 | `TestMarkAgentReloaded_NoStagedChange_FlagFalse` | If priorChangedAt is current, flag clears |
| US-27a.2 | `TestMarkAgentReloaded_NoRow_ReturnsErrNoAgentStateRow` | Missing workspace_agent_state row → ErrNoAgentStateRow returned, no INSERT performed |
| US-27a.2 | `TestMarkAgentReloaded_UsesDBClock_NotAppClock` | RETURNING last_agent_disposed_at equals DB NOW(), not application time.Now() |
| US-27a.2 | `TestGetLastCredentialChangedAt_NoRow_ReturnsZero` | No workspace_agent_state row → zero time, no error |
| US-27a.2 | `TestGetWorkspace_AgentNeedsRefresh_ReflectsRow` | agentNeedsRefresh true when row says so |
| US-27a.2 | `TestGetWorkspace_NoAgentStateRow_FlagFalse` | LEFT JOIN returns FALSE when row doesn't exist |
| US-27a.3 | `TestListWorkspaces_AgentNeedsRefresh_PerWorkspace` | List response includes the flag for each workspace |
| US-27a.4 | `TestStageCredentials_DoesNotCallDispose` | Mock opencode rejects dispose; StageCredentials still returns nil |
| US-27a.4 | `TestStageCredentials_PushFailure_NoSideEffects` | If PUT /auth/:p returns 500, error returned; no dispose called |
| US-27a.4 | `TestStageCredentials_BasicAuth_Required` | Missing password → mock returns 401 → error returned |
| US-27a.2b | `TestComputeBindingsDiff_LLMProviderAdded_AffectedTrue` [diff] | computeBindingsDiff: llm-provider added → LLMProviderAffected=true, AddedTypes=["llm-provider"], RemovedTypes=nil |
| US-27a.2b | `TestComputeBindingsDiff_LLMProviderRemoved_AffectedTrue` [diff] | computeBindingsDiff: llm-provider removed → LLMProviderAffected=true, AddedTypes=nil, RemovedTypes=["llm-provider"] |
| US-27a.2b | `TestComputeBindingsDiff_EnvOnly_AffectedFalse` [diff] | computeBindingsDiff: only env secrets changed → LLMProviderAffected=false, AddedTypes/RemovedTypes contain only "env" |
| US-27a.2b | `TestSetBindings_StoreGetBindingsFails_ConservativeTrue` [service] | mock `store.GetBindings` (not `SecretsHandler.GetBindings`) returns error → SetBindings still succeeds, LLMProviderAffected=true, AddedTypes=nil, RemovedTypes=nil (all three asserted — nil types are the "diff unavailable" sentinel) |
| US-27a.2b | `TestComputeBindingsDiff_NilExisting_NoRemovals` [diff] | computeBindingsDiff(nil, newSecrets): RemovedTypes=nil, AddedTypes computed from newSecrets only |
| US-27a.2b | `TestAddBindings_LLMProviderAdded_AffectedTrue` [service] | AddBindings adds llm-provider → LLMProviderAffected=true, RemovedTypes=nil |
| US-27a.2b | `TestAddBindings_EnvOnly_AffectedFalse` [service] | AddBindings adds env secrets only → LLMProviderAffected=false |
| US-27a.2b | `TestAddBindings_Empty_ZeroResult` [service] | empty secretIDs → returns zero BindingsMutationResult without calling store.AddBindings or verifyWorkspaceOwner (asserted via mock expectations) |
| US-27a.2b | `TestSortedKeys_Deterministic` [diff] | sortedKeys: multiple calls on same map return identical ordering |
| US-27a.2b | `TestSortedKeys_EmptyMap_LenZero` [diff] | sortedKeys on empty map → len == 0 (contract: empty result; nil vs []string{} is an implementation detail, use len() not == nil per doc comment) |
| US-27a.2b | `TestSetBindings_LLMProviderAffected_CallsMarkCredentialChanged` [handler] | LLMProviderAffected=true → MarkCredentialChanged called once |
| US-27a.2b | `TestSetBindings_NotLLMProvider_NoMarkCredentialChanged` [handler] | LLMProviderAffected=false → MarkCredentialChanged not called |
| US-27a.2b | `TestSetBindings_MarkCredentialChangedFails_BindingStillApplied` [handler] | MarkCredentialChanged errors → 204 returned, binding applied, warning logged |
| US-27a.2b | `TestSetWorkspaceEnv_LLMProvider_CallsMarkCredentialChanged` [handler] | SetWorkspaceEnv with llm-provider (future-proofing): LLMProviderAffected=true → MarkCredentialChanged called |
| US-27a.5 | `TestReloadSecretsHandler_DoesNotDispose` | Full agentd reload-secrets flow; mock opencode rejects dispose; flow still returns 200 |
| US-27a.5 | `TestReloadSecretsHandler_StageFailure_NoFallbackRestart` | Stage error logged; proc.restart NOT called |
| US-27a.5 | `TestReloadSecretsHandler_EnvSecret_StillRestarts` | env-secret change still triggers proc.restart |
| US-27a.5 | `TestReloadSecretsHandler_EnvAndLLM_BothHandledOrthogonally` | Batch with both types: stage runs, env-restart runs, neither blocks the other |
| US-27a.6 | `TestAgentdReloadHandler_DisposeSucceeds_Returns200` | Mock opencode dispose 200 → handler 200 |
| US-27a.6 | `TestAgentdReloadHandler_DisposeFails_Returns502` | Mock opencode dispose 500 → handler 502 |
| US-27a.6 | `TestAgentdReloadHandler_MethodNotPost_Returns405` | GET → 405 |
| US-27a.6 | `TestAgentdReloadHandler_ConcurrentCalls_BothSucceed` | Two parallel POSTs both return 200 (idempotent at opencode) |
| US-27a.7 | `TestAgentReload_HappyPath_DispatchesAndUpdatesState` | Mock agentd 200 → API 200 → DB row updated → `lastDisposedAt` in response |
| US-27a.7 | `TestAgentReload_NotOwner_403` | Other user's workspace → 403 |
| US-27a.7 | `TestAgentReload_PhaseNotActive_409` | Suspended workspace → 409 with phase named |
| US-27a.7 | `TestAgentReload_PodIPNotResolved_409` | No pod → 409 |
| US-27a.7 | `TestAgentReload_AgentdUnreachable_500` | agentd connection refused → 500 |
| US-27a.7 | `TestAgentReload_AgentdReturns502_Returns500` | agentd reports dispose failure → API surfaces it |
| US-27a.7 | `TestAgentReload_DisposeOK_DBFails_Returns200WithWarning` | Dispose ok, DB tx fails → 200 with `warning` field |
| US-27a.7 | `TestAgentReload_WarningPath_BannerStaysUp` | After 200+warning, GET workspace still shows agentNeedsRefresh=true |
| US-27a.7 | `TestAgentReload_RaceWithConcurrentCredentialAdd_FlagStaysTrue` | FOR UPDATE lock ensures concurrent staged credential keeps flag true |
| US-27a.7 | `TestAgentReload_ClearsModelCache` | After 200, the in-process model cache is empty |
| US-27a.7 | `TestAgentReload_NoAgentStateRow_Returns409` | MarkAgentReloaded returns ErrNoAgentStateRow → 409 "no pending credentials" |
| US-27a.8 | `TestBanner_Renders_WhenAgentNeedsRefreshTrue` | UI test |
| US-27a.8 | `TestBanner_Hidden_WhenFlagFalse` | UI test |
| US-27a.8 | `TestBanner_Dismiss_PersistsViaSessionStorage` | Dismiss sets sessionStorage entry; banner hidden on remount |
| US-27a.8 | `TestBanner_Dismiss_ClearsOnNewCredentialAdd` | New credentialsPendingSince invalidates old sessionStorage entry |
| US-27a.8 | `TestBanner_Modal_RequiresExplicitConfirm` | Reload requires modal click-through |
| US-27a.8 | `TestBanner_WarningToast_OnWarningResponse` | 200+warning → toast shown; banner NOT hidden |
| US-27a.8 | `TestList_ConsolidatedBanner_LinksToFiltered` | Multi-workspace list shows single banner with filter link |
| US-27a.9 | `TestCredflow_AddCredentialMidStream_NoInterruption` | The worklog 0125 scenario, fixed |
| US-27a.9 | `TestCredflow_ExplicitReload_PicksUpNewCredential` | After reload, /models reflects new provider |
| US-27a.9 | `TestCredflow_Cleanup_NoOrphans` | Test tears down user, workspace, secret cleanly |

---

## Failure-Prone Areas (mitigations called out in design)

| # | Area | Mitigation |
|---|---|---|
| F.1 | DB tx commit fails after dispose succeeds | API returns 200 with `warning` field; frontend shows toast but does NOT hide banner; user re-clicks reload safely (idempotent at opencode) |
| F.2 | List view with many workspaces shows N banners | Single page-level banner on list view; per-workspace only on detail (US-27a.8) |
| F.3 | Concurrent credential add races the flag clear during dispose window | `SELECT FOR UPDATE` in MarkAgentReloaded ensures the concurrent write is committed and visible before we decide pending_refresh value |
| F.4 | agentd is unreachable | API returns 500 with clear error; banner stays up; user can retry |
| F.5 | Dispose is racy with another reload from a parallel tab | Idempotent at opencode (instance-store.ts:145-153 short-circuits on already-disposed); both API responses return 200 |
| F.6 | Bug 11 migration on existing data | Schema migration includes integration test on production-shaped data; ALTER COLUMN UUID cast is a no-op for valid 36-char strings |
| F.6b | Bug 12 migration on existing data | workspaces.user_id was VARCHAR(255) but all real values are <= 36 chars; type reduction is a no-op for valid data; FK is ON DELETE RESTRICT (not CASCADE) to preserve the soft-delete invariant; integration test asserts FK now enforced and that DeleteUser fails while workspaces exist |
| F.7 | `clearModelCache` clears across all workspaces | Acceptable: 5s TTL means cross-workspace impact is bounded; per-workspace clear is a future improvement |
| F.8 | Backfill causes spurious banner for users whose credentials were already loaded | One-time UX cost; the reload click on an already-loaded workspace is a no-op except updating the timestamp |
| F.9 | `MarkAgentReloaded` called when no `workspace_agent_state` row exists | Returns `ErrNoAgentStateRow`; handler maps to 409 "no pending credentials" rather than silently corrupting state |
| F.10 | `MarkCredentialChanged` succeeds but binding write already rolled back (impossible — binding runs first and succeeds before `MarkCredentialChanged` is called) | Not applicable: the call order is binding-first, then `MarkCredentialChanged`. `MarkCredentialChanged` is only called after `SetBindings` returns nil. |
| F.11 | `MarkCredentialChanged` fails after binding write succeeds | Credential IS correctly bound but banner may not appear. Logged as a warning; credential is usable. Next binding mutation (add/remove any llm-provider secret) will retrigger `MarkCredentialChanged`. User can also trigger banner by removing and re-adding any llm-provider binding. Non-catastrophic eventual consistency window (see A18, DP5). |

---

## Open Questions Carried into 27b

1. Drain mode timeout default and configurability.
2. Bulk reload concurrency / streaming response shape.
3. Error enrichment scope (chat-proxy routes only) and field shape.
4. SDK ergonomic helpers (typed exceptions, retry-with-drain wrappers).

---

## Future Work (Out of Scope)

- **DynamoDB-style storage migration.** Workspaces are independent units with no meaningful cross-row relational queries. Tracked as a separate cross-cutting epic.
- **Upstream PR for `POST /provider/refresh`.** When a targeted provider-state invalidation endpoint lands upstream, agentd's `/v1/agent/reload` handler can swap from `DisposeInstance` to `RefreshProvider` transparently, preserving in-flight LLM calls. Tracked in worklog 0121's "Next Steps."
- **App-layer authentication on agentd user port.** Currently relies on NetworkPolicy alone (see A15). If this is ever revisited, `requireBearerToken` at `main.go:907` can be applied to both `/v1/reload-secrets` and `/v1/agent/reload` atomically.

---

## Files Likely Affected

| Path | Change |
|---|---|
| `api/migrations/000014_workspace_agent_state_and_bug11_fix.up.sql` | NEW |
| `api/migrations/000014_workspace_agent_state_and_bug11_fix.down.sql` | NEW |
| `pkg/types/types.go` | Add `AgentNeedsRefresh`, `CredentialsPendingSince` to `WorkspaceMetadata`, `Workspace`, `WorkspaceListItem` |
| `api/internal/services/database/database.go` | Update `GetWorkspace`, `ListWorkspaces` SQL (LEFT JOIN); add `MarkCredentialChanged` (no tx param — opens its own internal tx), `GetLastCredentialChangedAt`, `MarkAgentReloaded` |
| `api/internal/services/database/database_test.go` | Tests for new methods and updated SQL |
| `api/internal/services/workspace/workspace_service.go` | Map new fields from `WorkspaceMetadata` to `types.Workspace`/`WorkspaceListItem` |
| `api/internal/handlers/secrets.go` | Add `CredentialStateWriter` interface and `SetCredentialStateWriter` setter. Wrap llm-provider binding writes in tx with `MarkCredentialChanged`. Replace hardcoded `"4097"` with `agentd.AgentdPort`. |
| `api/internal/handlers/models.go` | **No change to `WorkspaceMetadataUpdater` interface** — `MarkCredentialChanged` is NOT added here; it goes into the new `CredentialStateWriter` interface in `secrets.go`. |
| `api/internal/handlers/errors.go` | NEW: `RespondWithError` (moved from `respondWithError` in `router.go`) |
| `api/internal/handlers/agent_reload.go` | NEW: `AgentReloadHandler`, `WorkspaceServicer`, `AgentStateStore` interfaces |
| `api/internal/handlers/agent_reload_test.go` | NEW |
| `api/internal/server/router.go` | Add `AgentReloadHandler` field to `RouterConfig`; register route in `registerWorkspaceRoutes` |
| `api/openapi.yaml` | Document the new endpoint and response fields |
| `api/internal/app/app.go` | Wire `AgentReloadHandler` into `RouterConfig`; call `secretsHandler.SetCredentialStateWriter(dbSvc)` |
| `pkg/secrets/bindings_diff.go` | NEW: `BindingsMutationResult` type; `computeBindingsDiff` and `sortedKeys` helpers; imports `"sort"` |
| `pkg/secrets/bindings_diff_test.go` | NEW: `package secrets`; unit tests for `computeBindingsDiff` and `sortedKeys` (see test plan US-27a.2b rows marked `[diff]`); no mocks required — pure functions |
| `pkg/secrets/secret_service.go` | Update `SetBindings` and `AddBindings` return signatures; accumulate `newSecrets` in both validation loops; handle `GetBindings` error conservatively. `"sort"` is imported by `bindings_diff.go` only; no new import needed here. |
| `pkg/secrets/secret_service_test.go` | Update all `SetBindings`/`AddBindings` call sites to `result, err :=`; add `TestSetBindings_LLMProvider*` and `TestAddBindings_LLMProvider*` tests |
| `pkg/secrets/pg_integration_test.go` | Update `AddBindings` call site to `_, err :=` |
| `pkg/agent/opencode/client.go` | Rename `RefreshCredentials` → `StageCredentials`; remove dispose call |
| `pkg/agent/opencode/client_test.go` | Update tests |
| `pkg/agent/opencode/client_integration_test.go` | Update tests |
| `cmd/workspace-agentd/secrets.go` | Use `StageCredentials`; remove `configReloaded`; remove dispose fallback; replace hardcoded port with `agentd.AgentPort` |
| `cmd/workspace-agentd/secrets_test.go` | Update tests; add env+llm mixed batch test |
| `cmd/workspace-agentd/agent_reload.go` | NEW: `agentReloadHandler` |
| `cmd/workspace-agentd/agent_reload_test.go` | NEW |
| `cmd/workspace-agentd/main.go` | Register `/v1/agent/reload` route |
| `frontend/src/lib/components/AgentReloadBanner.svelte` | NEW |
| `frontend/src/lib/components/AgentReloadModal.svelte` | NEW |
| `frontend/src/lib/components/WorkspaceListAgentReloadBanner.svelte` | NEW |
| `frontend/src/routes/workspace/[id]/+page.svelte` | Mount detail-view banner |
| `frontend/src/routes/workspaces/+page.svelte` | Mount list-view consolidated banner |
| `tests/integration/credflow_test.go` | NEW |
| `worklogs/01XX_YYYY-MM-DD_epic-27a-credential-reload-foundation.md` | NEW (post-impl) |

---

## Success Criteria

1. Adding, updating, or deleting an llm-provider credential does NOT abort any in-flight LLM call. Verified by integration test US-27a.9.
2. The user sees a banner (per-workspace on detail, consolidated on list) when credentials are staged.
3. Clicking "Reload agent" with explicit confirmation triggers `POST /workspaces/:id/agent/reload`, which disposes opencode and clears the staging flag.
4. Bug 11 (`user_secret_bindings.workspace_id` type mismatch and missing FK) is resolved.
5. Bug 12 (`workspaces.user_id` type mismatch and missing FK to `users.id`) is resolved with `ON DELETE RESTRICT`; `DeleteUser` now fails if workspaces still exist, enforcing correct application-level ordering.
6. The `RestartWorkspace` action is unaffected (different verb, different mechanism, still does the heavy pod-rebuild for its own use cases).
7. No production bug like worklog 0125's Bug 2 (managedProcess crash loop) can recur via the credflow path.
8. Worklog 0127's Bug 1 + Bug 2 fixes are preserved — Epic 27a removes some surfaces that exercised them but does not regress them.
