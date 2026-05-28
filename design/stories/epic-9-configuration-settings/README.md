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
Workspace (Tier 4) → Instance (Tier 2) → Schema default
User (Tier 3) is independent — UX preferences only, no overlap with Tier 2/4.
```

### Declarative Settings Schema

A single Go struct defines every mutable setting. Validation, seeding, API serialization, and frontend form generation all derive from this schema. No separate validation registry, no manual sync between layers.

```go
// pkg/settings/schema.go

const SchemaVersion = 1  // Increment on any schema change (add/remove/modify keys)

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
    Min         *int        `json:"min,omitempty"`         // int range
    Max         *int        `json:"max,omitempty"`         // int range
    Pattern     string      `json:"pattern,omitempty"`     // string regex
    Enum        []string    `json:"enum,omitempty"`        // enum values
    Category    string      `json:"category"`              // UI grouping
    Label       string      `json:"label"`                 // UI display name
    Description string      `json:"description"`           // UI help text
}
```

Every setting has a default — there is no concept of a "required" setting that can be missing. The schema is:
- Compiled into the API binary (no runtime file loading)
- Versioned via `SchemaVersion` constant (incremented on any key add/remove/modify)
- Exposed via `GET /api/v1/admin/settings/schema` for frontend form generation
- Used by the seed job to insert defaults and detect orphaned keys
- Used by the service layer to validate writes with typed accessors
- Used by the frontend to render controls (type → component mapping)

### Config.yaml Deprecation

Settings that move to Tier 2 (DB-mutable) are **removed from `config.go`** and the runtime config struct. Their config.yaml values become **seed-only defaults** — read once by the seed job on first install, then ignored. The DB is the sole runtime authority for Tier 2 keys.

Fields to remove from `Config` struct: `Auth.RegistrationEnabled`, `Auth.LockoutEnabled`, `Auth.LockoutAttempts`, `Auth.LockoutDuration`, `RateLimiting.*` (entire struct).

The corresponding env var overrides (`LLMSAFESPACE_AUTH_LOCKOUT*`, `LLMSAFESPACE_RATELIMITING_*`) are also removed. Operators configure these via the admin UI or Helm seed values.

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
CRUD   /api/v1/admin/credentials[/:id]
POST   /api/v1/admin/credentials/rotate-key

# User-facing credentials (authenticated)
GET    /api/v1/credentials
```

No platform/Tier 1 endpoint. Infrastructure config (DB host, Redis port, K8s namespace) is not exposed via API — it has no user-facing value and leaks topology.

### Admin Guard Middleware

Auth middleware loads `userRole` into context on every authenticated request (single `SELECT role FROM users WHERE id=$1` — sub-ms by PK). AdminGuard reads from context:

```go
func AdminGuard() gin.HandlerFunc {
    return func(c *gin.Context) {
        if c.GetString("userRole") != "admin" {
            c.AbortWithStatus(404) // Don't reveal route exists
            return
        }
        c.Next()
    }
}
```

### Prerequisites (Code Fixes Required)

1. **Auth middleware loads `userRole` into context.** Add `SELECT role FROM users WHERE id=$1` after token validation. Sets `c.Set("userRole", role)` and `context.WithValue(ctx, types.ContextKeyUserRole, role)`. This replaces the dead-code `RequireRoles` middleware.

2. **Delete dead code:** Remove `RequireRoles` middleware, `ExemptRoles` field from `RateLimitConfig`, and the associated exempt-role check in the rate limiter. These are never used.

3. **Remove Tier 2 fields from `Config` struct.** After settings service exists, `config.go` no longer owns `Auth.RegistrationEnabled`, `Auth.Lockout*`, or `RateLimiting.*`. These move to DB-backed settings.

4. **Rate limiter middleware reads from `InstanceSettingsService`.** Refactor to accept the service and call typed accessors (`GetBool`, `GetInt`) per-request. Cached via singleflight — no DB hit. Fallback to schema defaults if service unavailable.

### Cache Strategy

Instance settings are read on nearly every request (rate limit config, registration check). Strategy:

- **Full-map cache:** Load all ~30 instance settings as one unit. Cache the entire map, not individual keys.
- **60-second TTL** with `singleflight.Group` to prevent thundering herd on expiry — only one goroutine fetches from DB, others wait.
- `SetInstanceSetting` immediately invalidates local cache and triggers a fresh load.
- Multi-pod: each pod has its own cache; changes propagate within 60s.
- No Redis pub/sub needed — 60s staleness is acceptable for admin config changes.
- **Readiness:** If DB is unreachable AND cache is empty (cold start failure), pod reports not-ready via `/readyz`.

```go
import "golang.org/x/sync/singleflight"

type settingsCache struct {
    mu       sync.RWMutex
    data     map[string]any
    loadedAt time.Time
    ttl      time.Duration
    sf       singleflight.Group
}
```

### User Settings Sync

- Frontend reads localStorage first (instant, no flash)
- On mount, fetches from API; DB value overwrites local (DB wins on read)
- Writes go to localStorage (optimistic) + API (async persist)
- `updated_at` column enables future conflict detection if needed

### Credential Encryption & Key Rotation

Provider API keys in `credential_sets` are encrypted at the application layer:

- **AES-256-GCM** encryption with authenticated additional data (AAD = credential set ID)
- Each encrypted blob is prefixed with a `key_version` byte (used for decryption — self-describing)
- `key_version` column mirrors the prefix (used for efficient `WHERE key_version < N` queries during rotation)
- Encryption keys stored in a K8s Secret as a JSON array: `[{"version":1,"key":"base64..."},{"version":2,"key":"base64..."}]`
- **Active key:** Highest version number — used for all new writes
- **Decryption:** Read `key_version` prefix from blob, select matching key from the array
- **Rotation workflow:**
  1. Admin generates new key, appends to K8s Secret array with incremented version
  2. Calls `POST /api/v1/admin/credentials/rotate-key` 
  3. API re-encrypts all credential sets with the new active key (background, idempotent)
  4. Once all rows use the new version, old key can be removed from the Secret
- Keys are **never** returned in API responses — only decrypted when copying to workspace K8s Secret

```go
type EncryptionKeySet struct {
    Keys []EncryptionKey `json:"keys"`
}

type EncryptionKey struct {
    Version int    `json:"version"`
    Key     []byte `json:"key"` // 32 bytes for AES-256
}
```

### Audit Logging

Every admin settings write produces a structured log:

```json
{"level":"info","msg":"instance_setting_changed","user_id":"...","key":"...","old_value":"...","new_value":"...","timestamp":"..."}
```

No separate audit table for V1 — structured logs are queryable via existing log infrastructure.

### Service Interfaces (Type-Safe)

Two separate services — different access patterns, different caching, different authorization:

```go
// pkg/settings/instance_service.go

type InstanceSettingsService interface {
    // Typed accessors — no type assertions needed by callers
    GetBool(ctx context.Context, key string) (bool, error)
    GetInt(ctx context.Context, key string) (int, error)
    GetString(ctx context.Context, key string) (string, error)
    GetStrings(ctx context.Context, key string) ([]string, error)

    // Bulk read (admin page)
    GetAll(ctx context.Context) (map[string]any, error)

    // Write (admin only)
    Set(ctx context.Context, key string, value any) error

    // Schema
    Schema() []SettingDef

    Start() error
    Stop() error
}
```

```go
// pkg/settings/user_service.go

type UserSettingsService interface {
    // Typed accessors
    GetBool(ctx context.Context, userID, key string) (bool, error)
    GetInt(ctx context.Context, userID, key string) (int, error)
    GetString(ctx context.Context, userID, key string) (string, error)

    // Bulk read (user settings page)
    GetAll(ctx context.Context, userID string) (map[string]any, error)

    // Write
    Set(ctx context.Context, userID, key string, value any) error

    // Schema
    Schema() []SettingDef

    Start() error
    Stop() error
}
```

```go
// pkg/credentials/service.go

type CredentialSetService interface {
    Create(ctx context.Context, req CreateCredentialSetRequest) (*CredentialSet, error)
    Get(ctx context.Context, id string) (*CredentialSet, error)
    List(ctx context.Context) ([]*CredentialSet, error)
    ListForUser(ctx context.Context, userID string) ([]*CredentialSetSummary, error)
    Update(ctx context.Context, id string, req UpdateCredentialSetRequest) (*CredentialSet, error)
    Delete(ctx context.Context, id string) error
    SetDefault(ctx context.Context, id string) error
    GetDefault(ctx context.Context) (*CredentialSet, error)
    RotateEncryptionKey(ctx context.Context) (*RotateKeyResult, error)
    Start() error
    Stop() error
}
```

Added to `Services` interface:
```go
GetInstanceSettings() InstanceSettingsService
GetUserSettings() UserSettingsService
GetCredentialSets() CredentialSetService
```

### Helm Seeding

On install/upgrade, seed job:

1. Inserts Tier 2 defaults from schema for any missing keys:
```sql
INSERT INTO instance_settings (key, value) VALUES ($1, $2)
ON CONFLICT (key) DO NOTHING;
```

2. Detects orphaned keys (in DB but not in current schema) and logs warnings:
```
WARN instance_setting_orphaned key=<key> hint="removed in schema v<N>, consider DELETE"
```

3. Logs skipped keys (existing value differs from schema default):
```
WARN instance_setting_seed_skipped key=<key> current=<db_value> schema_default=<default>
```

## Settings Inventory

### Tier 1: Platform (Not exposed via API)

Tier 1 settings live in `config.yaml` / `values.yaml` / env vars. They are **not** exposed via any API endpoint — operators manage them via Helm/kubectl. They are documented here for completeness but have no UI representation.

Sensitive infrastructure details (DB host, Redis credentials, K8s namespace) are never surfaced to the frontend.

### Tier 2: Instance (Admin-mutable)

| Key | Default | Type | Valid Values | Category | Description |
|-----|---------|------|-------------|----------|-------------|
| `auth.registrationEnabled` | `true` | bool | | Auth | Allow new user sign-ups |
| `auth.lockoutEnabled` | `false` | bool | | Auth | Account lockout on failed attempts |
| `auth.lockoutAttempts` | `5` | int | 1–100 | Auth | Failed attempts before lockout |
| `auth.lockoutDurationMinutes` | `15` | int | 1–1440 | Auth | Lockout duration |
| `rateLimiting.enabled` | `true` | bool | | Rate Limiting | Global rate limiting |
| `rateLimiting.defaultLimit` | `100` | int | 1–100000 | Rate Limiting | Requests per window |
| `rateLimiting.windowMinutes` | `1` | int | 1–1440 | Rate Limiting | Window duration |
| `rateLimiting.burstSize` | `20` | int | 1–1000 | Rate Limiting | Burst size |
| `rateLimiting.strategy` | `token_bucket` | enum | `token_bucket` `fixed_window` `sliding_window` | Rate Limiting | Algorithm |
| `workspace.defaultImage` | `ghcr.io/lenaxia/llmsafespace/base:latest` | string | container image ref | Workspace | Image for new workspaces |
| `workspace.defaultStorageSize` | `1Gi` | string | `^[0-9]+(Gi\|Mi)$` | Workspace | Default PVC size |
| `workspace.maxStorageSize` | `10Gi` | string | `^[0-9]+(Gi\|Mi)$` | Workspace | Max PVC size |
| `workspace.defaultStorageClass` | `""` | string | StorageClass or `""` | Workspace | K8s StorageClass |
| `workspace.maxActiveWorkspacesPerUser` | `3` | int | 1–50 | Workspace | Max running pods; oldest auto-suspended |
| `workspace.defaultMaxActiveSessions` | `5` | int | 1–20 | Workspace | Concurrent sessions per workspace |
| `workspace.defaultResources.cpu` | `500m` | string | K8s quantity | Workspace | Default CPU limit |
| `workspace.defaultResources.memory` | `512Mi` | string | K8s quantity | Workspace | Default memory limit |
| `workspace.defaultResources.ephemeralStorage` | `1Gi` | string | K8s quantity | Workspace | Default ephemeral storage |
| `workspace.autoSuspend.enabled` | `true` | bool | | Auto-Suspend | Global auto-suspend |
| `workspace.autoSuspend.idleTimeoutMinutes` | `60` | int | 5–10080 | Auto-Suspend | Idle timeout |
| `workspace.ttlDaysAfterSuspended` | `0` | int | 0–365 | Auto-Suspend | Auto-delete (0 = never) |
| `credentials.autoProvision` | `false` | bool | | Credentials | Auto-copy default set to new workspaces |
| `workspace.defaultNetworkAccess.ingress` | `false` | bool | | Network | Allow inbound by default |
| `workspace.defaultNetworkAccess.egressDomains` | `[]` | strings | domain names | Network | Default allowed egress |
| `workspace.defaultSecurityLevel` | `standard` | enum | `standard` `high` | Security | Pod security posture |
| `instance.name` | `LLMSafeSpace` | string | 1–64 chars | Branding | Instance display name |
| `instance.motd` | `""` | string | 0–500 chars | Branding | Login page message |

### Tier 3: User (Per-user)

| Key | Default | Type | Valid Values | Category |
|-----|---------|------|-------------|----------|
| `theme` | `system` | enum | `light` `dark` `system` | Appearance |
| `fontSize` | `14` | int | 10–24 | Appearance |
| `compactMode` | `false` | bool | | Appearance |
| `streamingEnabled` | `true` | bool | | Chat |
| `showThinkingBlocks` | `true` | bool | | Chat |
| `codeBlockWordWrap` | `false` | bool | | Chat |
| `sendOnEnter` | `true` | bool | | Chat |
| `preferredModel` | `""` | string | model ID or `""` | Chat |
| `notifyOnSessionComplete` | `true` | bool | | Notifications |
| `notifyOnWorkspaceReady` | `true` | bool | | Notifications |
| `sidebarCollapsed` | `false` | bool | | Layout |
| `sidebarWidth` | `280` | int | 200–600 | Layout |

### Tier 4: Workspace-level

| Setting | CRD Field | Default | Valid Values |
|---------|-----------|---------|-------------|
| `name` | DB | auto-generated | string 1–64 |
| `storageSize` | `spec.storage.size` | (instance) | `^[0-9]+(Gi\|Mi)$` ≤ max |
| `storageClass` | `spec.storage.storageClassName` | (instance) | StorageClass or `""` |
| `maxActiveSessions` | `spec.maxActiveSessions` | (instance) | 1–20 |
| `autoSuspend.enabled` | `spec.autoSuspend.enabled` | (instance) | bool |
| `autoSuspend.idleTimeoutMinutes` | `spec.autoSuspend.idleTimeoutSeconds` ÷ 60 | (instance) | 5–10080 |
| `ttlDaysAfterSuspended` | `spec.ttlSecondsAfterSuspended` ÷ 86400 | (instance) | 0–365 |
| `timeout` | `spec.timeout` | `0` | 0–86400 (seconds) |
| `maxRetries` | `spec.maxRetries` | `3` | 0–10 |
| `resources.cpu` | `spec.resources.cpu` | (instance) | K8s quantity |
| `resources.memory` | `spec.resources.memory` | (instance) | K8s quantity |
| `networkAccess.ingress` | `spec.networkAccess.ingress` | (instance) | bool |
| `networkAccess.egressDomains` | `spec.networkAccess.egress[]` | (instance) | string[] |
| `packages` | `spec.packages[]` | `[]` | `[{runtime, requirements[]}]` |
| `initScript` | `spec.initScript` | `""` | string 0–10000 |
| `credentialSetId` | `spec.credentials.secretName` | (instance default) | secret name |
| `securityLevel` | `spec.securityLevel` | (instance) | `standard` `high` |

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
  providers_encrypted BYTEA NOT NULL,  -- AES-256-GCM, prefixed with key_version byte
  key_version SMALLINT NOT NULL DEFAULT 1,
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

Toggling `is_default` requires a transaction:

```sql
BEGIN;
UPDATE credential_sets SET is_default = false WHERE is_default = true;
UPDATE credential_sets SET is_default = true WHERE id = $1;
COMMIT;
```

The partial unique index prevents two defaults even if the transaction logic fails.

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

Deleting a credential set referenced by active workspaces returns **409 Conflict** with the list of referencing workspace IDs. Admin must reassign those workspaces first.

### Key Rotation Flow

```
1. Generate new 32-byte key
2. Append to K8s Secret: {"version": N+1, "key": "base64..."}
3. POST /api/v1/admin/credentials/rotate-key
4. API iterates all credential_sets WHERE key_version < N+1:
   - Decrypt with old key (by version)
   - Re-encrypt with new key
   - UPDATE SET providers_encrypted=..., key_version=N+1
5. Response: {"rotated": 5, "alreadyCurrent": 2, "errors": 0}
6. Once all rows at new version, remove old key from Secret
```

Rotation is idempotent — safe to retry on partial failure. Rows already at the new version are skipped.

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

## Story List

| Story | Title | Size | Phase |
|-------|-------|------|-------|
| US-9.0 | UI Primitives — Radix integration + settings layout components | Large | A |
| US-9.1 | Declarative Settings Schema + DB Migration | Medium | A |
| US-9.2 | Instance Settings Service — typed accessors, singleflight cache | Medium | A |
| US-9.3 | User Settings Service | Small | A |
| US-9.4 | Settings Seed Job + schema versioning | Small | A |
| US-9.5 | Admin API + AdminGuard + auth middleware role loading | Medium | A |
| US-9.6 | User Settings API | Small | A |
| US-9.7 | Legacy cleanup — remove dead code from config/middleware | Small | A |
| US-9.8 | Frontend — Admin Settings Page (schema-driven) | Large | B |
| US-9.9 | Frontend — User Settings Page | Large | B |
| US-9.10 | Frontend — Workspace Settings Drawer | Medium | B |
| US-9.11 | Max Active Workspaces Enforcement | Small | B |
| US-9.12 | Max Storage Size Enforcement | Small | B |
| US-9.13 | Credential Sets Entity (full stack + encryption) | Large | C |
| US-9.14 | Credential Key Rotation | Medium | C |
| US-9.15 | Frontend — Admin Credentials Page | Medium | C |
| US-9.16 | Preferred Model + Chat Integration | Medium | C |

## Dependency Graph

```
US-9.0 (UI primitives) ─────────────────────────────────────────┐
                                                                  │
US-9.1 (schema + DB) ──┬── US-9.4 (seed)                         │
                        │                                         │
                        ├── US-9.2 (instance svc) ──┐             │
                        │                            │             │
                        ├── US-9.3 (user svc) ───────┤             │
                        │                            ├── US-9.5 (admin API) ── US-9.8 (admin UI) ←──┤
                        │                            ├── US-9.6 (user API) ─── US-9.9 (user UI) ←───┤
                        │                            ├── US-9.11 (max workspaces)                    │
                        │                            └── US-9.12 (max storage)                      │
                        │                                                                           │
                        └── US-9.13 (credential sets) ── US-9.14 (rotation)                        │
                                                     └── US-9.15 (creds UI) ←──────────────────────┤
                                                     └── US-9.16 (model picker) ←──────────────────┘

US-9.7 (legacy cleanup) depends on US-9.2 + US-9.5 (can only remove old code after new code works)
US-9.10 (workspace drawer) depends on US-9.0 + US-9.5 + US-9.6
```

## Phases

**Phase A (Foundation):** US-9.0, US-9.1, US-9.2, US-9.3, US-9.4, US-9.5, US-9.6, US-9.7
**Phase B (UI + Enforcement):** US-9.8, US-9.9, US-9.10, US-9.11, US-9.12
**Phase C (Credentials):** US-9.13, US-9.14, US-9.15, US-9.16

## Design Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Declarative settings schema as single source of truth | Drives validation, seeding, API responses, and frontend form generation. Adding a setting = adding one struct entry. |
| D2 | Schema versioning (`SchemaVersion` constant) | Seed job detects orphaned keys; frontend can cache-bust on version change |
| D3 | Typed accessors (`GetBool`, `GetInt`, `GetString`) | Type safety — no `any` returns, no panics from type assertions |
| D4 | Split `InstanceSettingsService` / `UserSettingsService` | SRP — different caching, auth, and access patterns |
| D5 | Full-map cache with `singleflight.Group` | Prevents thundering herd on TTL expiry; simpler than per-key caching |
| D6 | Auth middleware loads `userRole` into context | Available everywhere (AdminGuard, audit, rate limiting); one DB call amortized |
| D7 | AdminGuard returns 404 (not 403) | No information leakage about admin routes |
| D8 | Remove Tier 2 fields from `Config` struct | Single source of truth — DB is authoritative for mutable settings |
| D9 | Delete dead code (`RequireRoles`, `ExemptRoles`) | Reduce confusion; new AdminGuard replaces them |
| D10 | No Tier 1 / platform API endpoint | Infrastructure config has no user-facing value; leaks topology |
| D11 | `ON CONFLICT DO NOTHING` + orphan detection for seeding | GitOps sets initial defaults; UI overrides persist; stale keys surfaced |
| D12 | Radix UI for interactive primitives | Accessibility, focus management, keyboard nav |
| D13 | User settings: DB source of truth + localStorage cache | Cross-device sync + instant UI |
| D14 | AES-256-GCM with versioned key rotation | Keys never static; rotation without downtime; old keys removable |
| D15 | Credential set deletion blocked if referenced (409) | Prevent orphaned workspace references |
| D16 | Structured log audit (not audit table) for V1 | Sufficient for compliance; avoids schema complexity |
| D17 | Migration rollback drops settings tables entirely | Additive tables; rollback loses settings data but no other tables affected |
| D18 | Admin endpoints rate-limited (not exempt) | Prevents brute-force probing |
| D19 | User-facing durations in minutes/days; pod timeout in seconds | Consistent UX for humans; technical precision for K8s |

## Test Plan

### US-9.1 (Schema + DB)
- Migration up creates `instance_settings`, `user_settings`, and `credential_sets` with correct constraints
- Migration down drops cleanly
- PK on `instance_settings.key` prevents duplicates
- Composite PK `(user_id, key)` on `user_settings` works
- FK cascade: deleting user deletes their settings
- `updated_at` trigger fires on UPDATE (value changes → `updated_at` advances)
- `key_version` column on `credential_sets` defaults to 1
- Schema definition compiles and contains all expected keys
- `SchemaVersion` constant is an integer > 0

### US-9.2 (Instance Settings Service)
- `GetBool` returns correct typed value for bool keys
- `GetInt` returns correct typed value for int keys
- `GetString` returns correct typed value for string keys
- `GetBool` on non-bool key → type mismatch error (not panic)
- `Set` valid value → succeeds, cache invalidated, audit log emitted
- `Set` invalid value (wrong type, out of range, bad enum) → validation error
- `Set` unknown key → error
- `GetAll` returns all rows merged with schema defaults
- Singleflight: concurrent `GetBool` calls during cache miss result in single DB query
- Cache: after `Set`, immediate `Get` returns new value (no stale read)
- Cold start with DB down → service reports unhealthy

### US-9.3 (User Settings Service)
- `GetAll` returns user values merged with schema defaults
- `Set` valid → succeeds
- `Set` invalid → validation error
- `Set` unknown key → error
- User A cannot read/write User B's settings (enforced at service layer)
- Unset keys return schema defaults via typed accessors

### US-9.4 (Seed Job)
- Fresh DB: all Tier 2 schema defaults inserted
- Existing values: not overwritten, warning logged per skipped key
- Partial existing: only missing keys inserted
- Orphaned keys (in DB, not in schema): warning logged with schema version
- Seed job is idempotent (run twice = same result)

### US-9.5 (Admin API + AdminGuard)
- Auth middleware sets `userRole` in context for all authenticated requests
- Admin GET returns all instance settings with schema metadata
- Admin PUT valid value → 200, audit log emitted
- Admin PUT invalid → 400 with validation details
- Non-admin on any `/admin/*` → 404 (not 401, not 403)
- Unauthenticated → 401 (from auth middleware, before AdminGuard)
- `GET /admin/settings/schema` returns full schema definition with `SchemaVersion`

### US-9.6 (User Settings API)
- Authenticated GET returns user settings merged with defaults
- PUT valid → 200
- PUT invalid → 400
- User A cannot access User B's settings
- Unset keys return schema defaults

### US-9.7 (Legacy Cleanup)
- `RequireRoles` middleware deleted — no compilation errors
- `ExemptRoles` field removed from `RateLimitConfig` — no compilation errors
- `Config.Auth.RegistrationEnabled`, `Config.Auth.Lockout*`, `Config.RateLimiting.*` removed
- Env var overrides for removed fields no longer parsed
- Rate limiter reads from `InstanceSettingsService.GetBool("rateLimiting.enabled")` etc.
- All existing tests still pass after removal

### US-9.11 (Max Active Workspaces)
- User at cap: activate suspends stalest (oldest `lastActivityAt`)
- User below cap: activate works without suspending
- Response includes `resumed` and `suspended` fields (`Suspended []string`)
- Stalest = oldest `lastActivityAt`; fallback to oldest `createdAt` if null
- Setting change immediately affects next activation (not retroactive)

### US-9.12 (Max Storage)
- Create with size ≤ max → succeeds
- Create with size > max → 400 with max in error message
- Setting change affects new creations only (not existing)

### US-9.13 (Credential Sets)
- Admin CRUD: create, read, update, delete
- Toggle `is_default`: previous default unset (transaction)
- User list: only sees assigned sets, no API keys in response
- Delete with active references → 409 with workspace IDs
- Auto-provision: new workspace gets default set copied as K8s Secret
- Encryption: providers stored encrypted with `key_version`, decrypted only for K8s Secret copy
- Model allowlist correctly filters available models

### US-9.14 (Key Rotation)
- `POST /admin/credentials/rotate-key` re-encrypts all rows to latest key version
- Rows already at latest version are skipped (idempotent)
- Response includes count of rotated, skipped, and errored rows
- After rotation, all rows have `key_version` = latest
- Decryption still works for rows encrypted with any known key version
- Unknown key version → clear error (not silent corruption)

### Frontend (US-9.0, 9.8, 9.9, 9.10, 9.15)
- Radix components render correctly, keyboard navigable, focus trapped in dialogs
- Admin page: schema-driven form renders correct control per type
- Admin page: hidden from non-admin (404 route)
- User settings: localStorage provides instant render, API sync on mount
- User settings: write updates both local and remote
- Workspace drawer: opens from kebab, saves patch CRD
- Toast appears on save success/failure
- Key rotation button shows progress and result count
