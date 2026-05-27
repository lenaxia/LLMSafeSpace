# Epic 9: Configuration & Settings

**Status:** Planning
**Created:** 2026-05-27
**Priority:** High
**Depends on:** Epic 6 (Collapse Sandbox into Workspace)

## Rationale

Configuration is scattered across `values.yaml`, `config.yaml`, env vars, and hardcoded constants. No runtime-mutable settings, no admin panel, no user preferences beyond localStorage theme. Operators must redeploy to change any behavior.

This epic introduces a tiered configuration system with a **declarative settings schema** as the single source of truth — driving validation, seeding, API responses, and frontend form generation from one definition.

## Architecture

### Tiered Configuration

| Tier | Mutability | Storage | Who |
|------|-----------|---------|-----|
| 1 (Platform) | Immutable at runtime | config.yaml / values.yaml / env | Operator (deploy-time) |
| 2 (Instance) | Mutable at runtime | PostgreSQL `instance_settings` | Admin (UI or API) |
| 3 (User) | Mutable at runtime | PostgreSQL `user_settings` + localStorage cache | User |
| 4 (Workspace) | Mutable at runtime | Workspace CRD spec | Workspace owner |

### Resolution Order

```
Workspace (Tier 4) → Instance (Tier 2) → Hardcoded default
User (Tier 3) is independent — UX preferences only, no overlap with Tier 2/4.
```

### Declarative Settings Schema

A single Go struct defines every setting. Validation, seeding, API serialization, and frontend form generation all derive from this schema. No separate validation registry, no manual sync between layers.

```go
// pkg/settings/schema.go

type SettingType string
const (
    TypeBool    SettingType = "bool"
    TypeInt     SettingType = "int"
    TypeString  SettingType = "string"
    TypeEnum    SettingType = "enum"
    TypeStrings SettingType = "strings"
)

type SettingDef struct {
    Key         string      `json:"key"`
    Tier        int         `json:"tier"`              // 2=instance, 3=user
    Type        SettingType `json:"type"`
    Default     any         `json:"default"`
    Required    bool        `json:"required"`
    Min         *int        `json:"min,omitempty"`         // int range
    Max         *int        `json:"max,omitempty"`         // int range
    Pattern     string      `json:"pattern,omitempty"`     // string regex
    Enum        []string    `json:"enum,omitempty"`        // enum values
    Category    string      `json:"category"`              // UI grouping
    Label       string      `json:"label"`                 // UI display name
    Description string      `json:"description"`           // UI help text
}
```

The schema is:
- Compiled into the API binary (no runtime file loading)
- Exposed via `GET /api/v1/admin/settings/schema` for frontend form generation
- Used by the seed job to know what to insert
- Used by the service layer to validate writes
- Used by the frontend to render controls (type → component mapping)

### API Route Separation

```
# User settings (authenticated)
GET    /api/v1/users/me/settings
GET    /api/v1/users/me/settings/schema
PUT    /api/v1/users/me/settings/:key

# Admin settings (admin only — returns 404 for non-admin)
GET    /api/v1/admin/settings
GET    /api/v1/admin/settings/schema
PUT    /api/v1/admin/settings/:key
GET    /api/v1/admin/settings/platform
CRUD   /api/v1/admin/credentials[/:id]

# User-facing credentials (authenticated)
GET    /api/v1/credentials
```

### Admin Guard Middleware

New middleware (not reusing existing `RequireRoles` which returns 403):

```go
func AdminGuard(dbService DatabaseService) gin.HandlerFunc {
    return func(c *gin.Context) {
        userID := c.GetString("userID")
        user, err := dbService.GetUser(c.Request.Context(), userID)
        if err != nil || user == nil || user.Role != "admin" {
            c.AbortWithStatus(404) // Don't reveal route exists
            return
        }
        c.Next()
    }
}
```

### Prerequisites (Code Fixes Required)

1. **Auth middleware must populate `userRole` in context.** Currently only sets `userID`. The `AdminGuard` above does its own DB lookup instead (avoids adding a DB call to every request's auth middleware).

2. **`router.go:208` hardcodes `RegistrationEnabled: false`.** US-9.4 must wire this to read from the settings service (inject `SettingsService` into the auth routes registration and call `GetInstanceSetting("auth.registrationEnabled")`).

3. **Admin endpoints must still be rate-limited.** The existing rate limiter should NOT exempt admin routes. Admin settings writes are low-volume but must be protected against brute-force probing. The standard per-user rate limit applies.

4. **Rate limiter middleware must read from SettingsService.** Currently takes a static `RateLimitConfig` struct at construction. US-9.2 must refactor it to accept `SettingsService` and read `rateLimiting.*` settings per-request (cached via 60s TTL, so no DB hit). Fallback to compiled defaults if settings service is unavailable.

### Cache Strategy

Instance settings are read on nearly every request (rate limit config, registration check). Strategy:

- In-memory cache in the settings service with **60-second TTL**
- `SetInstanceSetting` immediately invalidates local cache
- Multi-pod: each pod has its own cache; changes propagate within 60s
- No Redis pub/sub needed — 60s staleness is acceptable for admin config changes

### User Settings Sync

- Frontend reads localStorage first (instant, no flash)
- On mount, fetches from API; DB value overwrites local (DB wins on read)
- Writes go to localStorage (optimistic) + API (async persist)
- `updated_at` column enables future conflict detection if needed

### Credential Encryption

Provider API keys in `credential_sets.providers` are encrypted at the application layer:

- AES-256-GCM encryption
- Encryption key sourced from K8s Secret (same secret that holds JWT key)
- Encrypted before DB write, decrypted on read
- Only decrypted when copying to workspace secret — never returned to API responses

### Audit Logging

Every admin settings write produces a structured log:

```json
{"level":"info","msg":"instance_setting_changed","user_id":"...","key":"...","old_value":"...","new_value":"...","timestamp":"..."}
```

No separate audit table for V1 — structured logs are queryable via existing log infrastructure.

### SettingsService Interface

```go
type SettingsService interface {
    // Instance settings (Tier 2)
    GetInstanceSetting(ctx context.Context, key string) (any, error)
    GetAllInstanceSettings(ctx context.Context) (map[string]any, error)
    SetInstanceSetting(ctx context.Context, key string, value any) error

    // User settings (Tier 3)
    GetUserSettings(ctx context.Context, userID string) (map[string]any, error)
    SetUserSetting(ctx context.Context, userID, key string, value any) error

    // Schema
    GetSchema() []SettingDef
    GetInstanceSchema() []SettingDef
    GetUserSchema() []SettingDef

    Start() error
    Stop() error
}
```

Added to `Services` interface:
```go
GetSettings() SettingsService
GetCredentialSets() CredentialSetService
```

### Credential Sets Service Interface

```go
type CredentialSetService interface {
    Create(ctx context.Context, req CreateCredentialSetRequest) (*CredentialSet, error)
    Get(ctx context.Context, id string) (*CredentialSet, error)
    List(ctx context.Context) ([]*CredentialSet, error)
    ListForUser(ctx context.Context, userID string) ([]*CredentialSetSummary, error)
    Update(ctx context.Context, id string, req UpdateCredentialSetRequest) (*CredentialSet, error)
    Delete(ctx context.Context, id string) error
    SetDefault(ctx context.Context, id string) error
    GetDefault(ctx context.Context) (*CredentialSet, error)
    Start() error
    Stop() error
}
```

### Helm Seeding

On install/upgrade, seed job inserts Tier 2 defaults from schema:

```sql
INSERT INTO instance_settings (key, value) VALUES ($1, $2)
ON CONFLICT (key) DO NOTHING;
```

When skipped (row exists with different value):
```
WARN instance_setting_seed_skipped key=<key> current=<db_value> helm=<helm_value>
```

## Settings Inventory

### Tier 1: Platform (Read-only in UI)

| Key | Default | Valid Values | Req | Source |
|-----|---------|-------------|-----|--------|
| `server.host` | `0.0.0.0` | IP/hostname | ✓ | config.yaml |
| `server.port` | `8080` | 1–65535 | ✓ | config.yaml |
| `server.shutdownTimeout` | `30s` | Go duration | ✓ | config.yaml |
| `postgresql.host` | `postgres` | hostname/IP | ✓ | values.yaml |
| `postgresql.port` | `5432` | 1–65535 | ✓ | values.yaml |
| `postgresql.database` | `llmsafespace` | string | ✓ | values.yaml |
| `postgresql.user` | `llmsafespace` | string | ✓ | values.yaml |
| `postgresql.sslMode` | `disable` | `disable` `require` `verify-ca` `verify-full` | ✓ | values.yaml |
| `postgresql.maxOpenConns` | `25` | 1–1000 | | values.yaml |
| `postgresql.maxIdleConns` | `10` | 1–100 | | values.yaml |
| `postgresql.connMaxLifetime` | `5m` | Go duration | | values.yaml |
| `redis.host` | `redis-master` | hostname/IP | ✓ | values.yaml |
| `redis.port` | `6379` | 1–65535 | ✓ | values.yaml |
| `redis.db` | `0` | 0–15 | | values.yaml |
| `redis.poolSize` | `20` | 1–1000 | | values.yaml |
| `kubernetes.namespace` | (release ns) | valid namespace | ✓ | config.yaml |
| `kubernetes.inCluster` | `true` | bool | ✓ | config.yaml |
| `kubernetes.leaderElection.enabled` | `true` | bool | ✓ | config.yaml |
| `controller.watchNamespaces` | `""` | `""` `"*"` `"ns1,ns2"` | | values.yaml |
| `security.allowedOrigins` | `["https://safespace.thekao.cloud"]` | string[] | ✓ | config.yaml/env |
| `security.allowCredentials` | `false` | bool | | config.yaml/env |
| `auth.apiKeyPrefix` | `lsp_` | string 3–10 | ✓ | config.yaml |
| `auth.tokenDuration` | `24h` | Go duration | ✓ | config.yaml |
| `auth.cookieName` | `lsp_session` | string | ✓ | hardcoded in router |
| `logging.level` | `info` | `debug` `info` `warn` `error` | ✓ | config.yaml/env |
| `logging.encoding` | `json` | `json` `console` | ✓ | config.yaml |
| `logging.development` | `false` | bool | | config.yaml |
| `mcp.enabled` | `true` | bool | | values.yaml |
| `mcp.transport` | `sse` | `sse` `stdio` | | values.yaml |
| `mcp.timeout` | `300s` | Go duration | | values.yaml |

### Tier 2: Instance (Admin-mutable)

| Key | Default | Type | Valid Values | Req | Category | Description |
|-----|---------|------|-------------|-----|----------|-------------|
| `auth.registrationEnabled` | `true` | bool | | ✓ | Auth | Allow new user sign-ups |
| `auth.lockoutEnabled` | `false` | bool | | | Auth | Account lockout on failed attempts |
| `auth.lockoutAttempts` | `5` | int | 1–100 | | Auth | Failed attempts before lockout |
| `auth.lockoutDurationMinutes` | `15` | int | 1–1440 | | Auth | Lockout duration |
| `rateLimiting.enabled` | `true` | bool | | ✓ | Rate Limiting | Global rate limiting |
| `rateLimiting.defaultLimit` | `100` | int | 1–100000 | | Rate Limiting | Requests per window |
| `rateLimiting.windowMinutes` | `1` | int | 1–1440 | | Rate Limiting | Window duration |
| `rateLimiting.burstSize` | `20` | int | 1–1000 | | Rate Limiting | Burst size |
| `rateLimiting.strategy` | `token_bucket` | enum | `token_bucket` `fixed_window` `sliding_window` | | Rate Limiting | Algorithm |
| `workspace.defaultImage` | `ghcr.io/lenaxia/llmsafespace/base:latest` | string | container image ref | ✓ | Workspace | Image for new workspaces |
| `workspace.defaultStorageSize` | `1Gi` | string | `^[0-9]+(Gi\|Mi)$` | ✓ | Workspace | Default PVC size |
| `workspace.maxStorageSize` | `10Gi` | string | `^[0-9]+(Gi\|Mi)$` | ✓ | Workspace | Max PVC size |
| `workspace.defaultStorageClass` | `""` | string | StorageClass or `""` | | Workspace | K8s StorageClass |
| `workspace.maxActiveWorkspacesPerUser` | `3` | int | 1–50 | ✓ | Workspace | Max running pods; oldest auto-suspended |
| `workspace.defaultMaxActiveSessions` | `5` | int | 1–20 | ✓ | Workspace | Concurrent sessions per workspace |
| `workspace.defaultResources.cpu` | `500m` | string | K8s quantity | ✓ | Workspace | Default CPU limit |
| `workspace.defaultResources.memory` | `512Mi` | string | K8s quantity | ✓ | Workspace | Default memory limit |
| `workspace.defaultResources.ephemeralStorage` | `1Gi` | string | K8s quantity | | Workspace | Default ephemeral storage |
| `workspace.autoSuspend.enabled` | `true` | bool | | ✓ | Auto-Suspend | Global auto-suspend |
| `workspace.autoSuspend.idleTimeoutMinutes` | `60` | int | 5–10080 | ✓ | Auto-Suspend | Idle timeout |
| `workspace.ttlDaysAfterSuspended` | `0` | int | 0–365 | | Auto-Suspend | Auto-delete (0 = never) |
| `credentials.autoProvision` | `false` | bool | | | Credentials | Auto-copy default set to new workspaces |
| `workspace.defaultNetworkAccess.ingress` | `false` | bool | | | Network | Allow inbound by default |
| `workspace.defaultNetworkAccess.egressDomains` | `[]` | strings | domain names | | Network | Default allowed egress |
| `workspace.defaultSecurityLevel` | `standard` | enum | `standard` `high` | | Security | Pod security posture |
| `instance.name` | `LLMSafeSpace` | string | 1–64 chars | | Branding | Instance display name |
| `instance.motd` | `""` | string | 0–500 chars | | Branding | Login page message |

### Tier 3: User (Per-user)

| Key | Default | Type | Valid Values | Req | Category |
|-----|---------|------|-------------|-----|----------|
| `theme` | `system` | enum | `light` `dark` `system` | ✓ | Appearance |
| `fontSize` | `14` | int | 10–24 | | Appearance |
| `compactMode` | `false` | bool | | | Appearance |
| `streamingEnabled` | `true` | bool | | | Chat |
| `showThinkingBlocks` | `true` | bool | | | Chat |
| `codeBlockWordWrap` | `false` | bool | | | Chat |
| `sendOnEnter` | `true` | bool | | | Chat |
| `preferredModel` | `""` | string | model ID or `""` | | Chat |
| `notifyOnSessionComplete` | `true` | bool | | | Notifications |
| `notifyOnWorkspaceReady` | `true` | bool | | | Notifications |
| `sidebarCollapsed` | `false` | bool | | | Layout |
| `sidebarWidth` | `280` | int | 200–600 | | Layout |

### Tier 4: Workspace-level

| Setting | CRD Field | Default | Valid Values | Req |
|---------|-----------|---------|-------------|-----|
| `name` | DB | auto-generated | string 1–64 | ✓ |
| `storageSize` | `spec.storage.size` | (instance) | `^[0-9]+(Gi\|Mi)$` ≤ max | ✓ |
| `storageClass` | `spec.storage.storageClassName` | (instance) | StorageClass or `""` | |
| `maxActiveSessions` | `spec.maxActiveSessions` | (instance) | 1–20 | |
| `autoSuspend.enabled` | `spec.autoSuspend.enabled` | (instance) | bool | |
| `autoSuspend.idleTimeoutMinutes` | `spec.autoSuspend.idleTimeoutSeconds` ÷ 60 | (instance) | 5–10080 | |
| `ttlDaysAfterSuspended` | `spec.ttlSecondsAfterSuspended` ÷ 86400 | (instance) | 0–365 | |
| `timeout` | `spec.timeout` | `0` | 0–86400 (seconds) | |
| `maxRetries` | `spec.maxRetries` | `3` | 0–10 | |
| `resources.cpu` | `spec.resources.cpu` | (instance) | K8s quantity | |
| `resources.memory` | `spec.resources.memory` | (instance) | K8s quantity | |
| `networkAccess.ingress` | `spec.networkAccess.ingress` | (instance) | bool | |
| `networkAccess.egressDomains` | `spec.networkAccess.egress[]` | (instance) | string[] | |
| `packages` | `spec.packages[]` | `[]` | `[{runtime, requirements[]}]` | |
| `initScript` | `spec.initScript` | `""` | string 0–10000 | |
| `credentialSetId` | `spec.credentials.secretName` | (instance default) | secret name | |
| `securityLevel` | `spec.securityLevel` | (instance) | `standard` `high` | |

**Duration convention:** User-facing durations (idle timeout, TTL) stored/displayed in minutes or days. CRD stores seconds internally; the API converts at the boundary. Pod `timeout` stays in seconds (technical, not user-facing).

## Database Schema

### Settings Tables

```sql
CREATE TABLE instance_settings (
  key TEXT PRIMARY KEY,
  value JSONB NOT NULL,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE TABLE user_settings (
  user_id VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  value JSONB NOT NULL,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, key)
);
```

## Credential Sets

### Schema

```sql
CREATE TABLE credential_sets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT UNIQUE NOT NULL,
  is_default BOOLEAN NOT NULL DEFAULT false,
  providers_encrypted BYTEA NOT NULL,  -- AES-256-GCM encrypted JSONB
  model_allowlist TEXT[] NOT NULL DEFAULT '{}',
  assigned_to JSONB NOT NULL DEFAULT '"all"',  -- "all" or ["user-id-1", ...] (VARCHAR(36) user IDs)
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

-- Only one default at a time
CREATE UNIQUE INDEX idx_credential_sets_default ON credential_sets (is_default) WHERE is_default = true;

-- Auto-update updated_at on all settings/credential tables
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_instance_settings_updated_at
  BEFORE UPDATE ON instance_settings
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER trg_user_settings_updated_at
  BEFORE UPDATE ON user_settings
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER trg_credential_sets_updated_at
  BEFORE UPDATE ON credential_sets
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
```

### Default Toggle Transaction

Toggling `is_default` requires a transaction to prevent race conditions:

```sql
BEGIN;
UPDATE credential_sets SET is_default = false WHERE is_default = true;
UPDATE credential_sets SET is_default = true WHERE id = $1;
COMMIT;
```

The partial unique index prevents two defaults even if the transaction logic fails, but the service layer must use a transaction to avoid a window where no set is default.

### Provider Config (decrypted form)

```json
{
  "openai": { "apiKey": "sk-...", "baseUrl": "https://api.openai.com/v1" },
  "anthropic": { "apiKey": "sk-ant-..." },
  "deepseek": { "apiKey": "sk-...", "baseUrl": "https://api.deepseek.com" }
}
```

### Access Control

- `assigned_to`: `"all"` or `["user-id-1", "user-id-2"]`
- Users see: set name, model allowlist, provider names (not keys)
- Admin sees: everything (keys masked in UI as `sk-...xxxx`, full value on explicit reveal)

### Deletion Policy

Deleting a credential set that is referenced by active workspaces returns **409 Conflict** with the list of referencing workspace IDs. Admin must reassign those workspaces first.

### UX Flow

1. Admin creates credential sets with provider keys + model allowlist
2. New workspaces get the default set auto-provisioned (if `credentials.autoProvision` is true)
3. Users can switch their workspace to any set they have access to
4. Chat model picker shows only models from the workspace's active credential set allowlist

## UI Component Library

### Decision: Radix UI Primitives

```
@radix-ui/react-dialog          — Dialog + Drawer
@radix-ui/react-dropdown-menu   — Replaces KebabMenu
@radix-ui/react-switch          — Toggle
@radix-ui/react-select          — Select dropdown
@radix-ui/react-slider          — Range slider
@radix-ui/react-tabs            — Tabs
@radix-ui/react-toast           — Notifications
@radix-ui/react-tooltip         — Info icons
```

### Components to Swap

| Current | Replace With |
|---------|-------------|
| `KebabMenu` | `@radix-ui/react-dropdown-menu` |
| `Rename*Dialog` / `NewWorkspaceDialog` | `@radix-ui/react-dialog` |
| `UpdateAvailableToast` | `@radix-ui/react-toast` |

### New Primitives

| Component | Source | Description |
|-----------|--------|-------------|
| `Toggle` | Radix Switch | Boolean on/off |
| `Select` | Radix Select | Dropdown |
| `Slider` | Radix Slider | Range with value |
| `Dialog` | Radix Dialog | Modal with focus trap |
| `Drawer` | Radix Dialog (side) | Slide-in panel |
| `Toast` + `useToast()` | Radix Toast | Notification system |
| `Tabs` | Radix Tabs | Tab bar + panels |
| `DropdownMenu` | Radix Dropdown | Replaces KebabMenu |
| `Tooltip` | Radix Tooltip | Hover info |
| `NumberInput` | Hand-rolled | Input + min/max |
| `Textarea` | Hand-rolled | Multi-line |
| `TagInput` | Hand-rolled | Multi-value chips |

### Settings Layout (schema-driven)

Frontend fetches `GET /admin/settings/schema` and renders forms dynamically:

| `type` | Renders |
|--------|---------|
| `bool` | `<Toggle>` |
| `int` | `<NumberInput>` or `<Slider>` (if range is small) |
| `string` | `<Input>` |
| `enum` | `<Select>` |
| `strings` | `<TagInput>` |

Layout components:
- `SettingsSection` — groups by `category`
- `SettingsRow` — label + description + control
- `ReadOnlyField` — for Tier 1 display

## Story List

| Story | Title | Size | Phase |
|-------|-------|------|-------|
| US-9.0 | UI Primitives — Radix integration + settings layout components | Large | A |
| US-9.1 | Declarative Settings Schema + DB Migration | Medium | A |
| US-9.2 | Settings Service — CRUD, validation, caching | Medium | A |
| US-9.3 | Settings Seed Job | Small | A |
| US-9.4 | Admin API + AdminGuard Middleware | Medium | A |
| US-9.5 | User Settings API | Small | A |
| US-9.6 | Frontend — Admin Settings Page (schema-driven) | Large | B |
| US-9.7 | Frontend — User Settings Page | Large | B |
| US-9.8 | Frontend — Workspace Settings Drawer | Medium | B |
| US-9.9 | Max Active Workspaces Enforcement | Small | B |
| US-9.10 | Max Storage Size Enforcement | Small | B |
| US-9.11 | Credential Sets Entity (full stack) | Large | C |
| US-9.12 | Frontend — Admin Credentials Page | Medium | C |
| US-9.13 | Preferred Model + Chat Integration | Medium | C |

## Dependency Graph

```
US-9.0 (UI primitives) ──────────────────────────────────┐
                                                           │
US-9.1 (schema + DB) ──┐                                  │
                        ├── US-9.2 (service) ──┐           │
                        │                       ├── US-9.3 (seed)
                        │                       ├── US-9.4 (admin API) ─── US-9.6 (admin UI) ←──┤
                        │                       ├── US-9.5 (user API) ──── US-9.7 (user UI) ←───┤
                        │                       ├── US-9.9 (max workspaces)                     │
                        │                       └── US-9.10 (max storage)                       │
                        │                                                                       │
                        └── US-9.11 (credential sets) ─── US-9.12 (creds UI) ←─────────────────┤
                                                      └── US-9.13 (model picker) ←─────────────┘

US-9.8 (workspace drawer) depends on US-9.0 + US-9.4 + US-9.5
```

## Phases

**Phase A (Foundation):** US-9.0, US-9.1, US-9.2, US-9.3, US-9.4, US-9.5
**Phase B (UI + Enforcement):** US-9.6, US-9.7, US-9.8, US-9.9, US-9.10
**Phase C (Credentials):** US-9.11, US-9.12, US-9.13

## Design Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Declarative settings schema as single source of truth | Drives validation, seeding, API responses, and frontend form generation. Adding a setting = adding one struct entry. |
| D2 | Separate admin/user API routes with AdminGuard returning 404 | Security — no information leakage about admin routes |
| D3 | `ON CONFLICT DO NOTHING` + log warning for Helm seeding | GitOps sets initial defaults; UI overrides persist across upgrades |
| D4 | Radix UI for interactive primitives | Accessibility, focus management, keyboard nav |
| D5 | User settings: DB source of truth + localStorage cache | Cross-device sync + instant UI |
| D6 | Credential sets as separate entity with AES-256-GCM encryption | Multi-provider, model allowlisting, per-user access, keys never in API responses |
| D7 | In-memory cache with 60s TTL for instance settings | Hot-path reads (rate limiting) without DB per request |
| D8 | User-facing durations in minutes/days; pod timeout in seconds | Consistent UX for humans; technical precision for K8s |
| D9 | Credential set deletion blocked if referenced (409) | Prevent orphaned workspace references |
| D10 | Structured log audit (not audit table) for V1 | Sufficient for compliance; avoids schema complexity |
| D11 | Migration rollback drops settings tables entirely | Settings tables are additive; rollback loses settings data but no other tables are affected. Acceptable for V1. |
| D12 | Admin endpoints rate-limited (not exempt) | Prevents brute-force probing of admin routes even though they return 404 |

## Test Plan

### US-9.1 (Schema + DB)
- Migration up creates `instance_settings`, `user_settings`, and `credential_sets` with correct constraints
- Migration down drops cleanly
- PK on `instance_settings.key` prevents duplicates
- Composite PK `(user_id, key)` on `user_settings` works
- FK cascade: deleting user deletes their settings
- `updated_at` trigger fires on UPDATE (value changes → `updated_at` advances)
- Schema definition compiles and contains all expected keys

### US-9.2 (Settings Service)
- `GetInstanceSettings` returns all rows merged with schema defaults
- `SetInstanceSetting` valid value → succeeds, cache invalidated
- `SetInstanceSetting` invalid value (wrong type, out of range, bad enum) → validation error
- `SetInstanceSetting` unknown key → error
- `GetUserSettings` returns user values merged with schema defaults
- `SetUserSetting` valid → succeeds
- `SetUserSetting` invalid → validation error
- Cache: after set, immediate get returns new value (no stale read)
- Concurrent writes: no corruption (DB row-level locking)

### US-9.3 (Seed Job)
- Fresh DB: all schema defaults inserted
- Existing values: not overwritten, warning logged per skipped key
- Partial existing: only missing keys inserted
- Seed job is idempotent (run twice = same result)

### US-9.4 (Admin API + AdminGuard)
- Admin GET returns all instance settings with schema metadata
- Admin PUT valid value → 200, audit log emitted
- Admin PUT invalid → 400 with validation details
- Non-admin on any `/admin/*` → 404 (not 401, not 403)
- Unauthenticated → 401 (from auth middleware, before AdminGuard)
- `GET /admin/settings/platform` returns Tier 1 read-only values
- `GET /admin/settings/schema` returns full schema definition

### US-9.5 (User Settings API)
- Authenticated GET returns user settings merged with defaults
- PUT valid → 200
- PUT invalid → 400
- User A cannot access User B's settings
- Unset keys return schema defaults

### US-9.9 (Max Active Workspaces)
- User at cap: activate suspends stalest (oldest `lastActivityAt`)
- User below cap: activate works without suspending
- Response includes `resumed` and `suspended` fields (change `Suspended` from `string` to `[]string` to handle cap reduction scenarios)
- Stalest = oldest `lastActivityAt`; fallback to oldest `createdAt` if null
- Setting change immediately affects next activation (not retroactive — existing over-cap workspaces stay running until next activate)

### US-9.10 (Max Storage)
- Create with size ≤ max → succeeds
- Create with size > max → 400 with max in error message
- Setting change affects new creations only (not existing)

### US-9.11 (Credential Sets)
- Admin CRUD: create, read, update, delete
- Toggle `is_default`: previous default unset (transaction)
- User list: only sees assigned sets, no API keys in response
- Delete with active references → 409 with workspace IDs
- Auto-provision: new workspace gets default set copied as K8s Secret
- Encryption: providers stored encrypted, decrypted only for K8s Secret copy
- Model allowlist correctly filters available models

### Frontend (US-9.0, 9.6, 9.7, 9.8, 9.12)
- Radix components render correctly, keyboard navigable, focus trapped in dialogs
- Admin page: schema-driven form renders correct control per type
- Admin page: hidden from non-admin (404 route)
- User settings: localStorage provides instant render, API sync on mount
- User settings: write updates both local and remote
- Workspace drawer: opens from kebab, saves patch CRD
- Toast appears on save success/failure
- Read-only fields not editable, show source badge
