# Epic 9: Configuration & Settings

**Status:** Planning
**Created:** 2026-05-27
**Priority:** High
**Depends on:** Epic 6 (Collapse Sandbox into Workspace)

## Rationale

The platform has configuration scattered across `values.yaml`, `config.yaml`, env vars, and hardcoded constants. There is no runtime-mutable settings layer, no admin panel, and no user preferences beyond localStorage theme. Operators must redeploy to change any behavior.

This epic introduces a tiered configuration system:

- **Tier 1 (Platform):** Immutable at runtime. Set via config file / Helm / env vars. Displayed read-only in admin UI.
- **Tier 2 (Instance):** Admin-mutable at runtime. Stored in DB. Seeded from Helm on install (`ON CONFLICT DO NOTHING` + log warning).
- **Tier 3 (User):** Per-user preferences. Stored in DB, cached in localStorage for instant UI.
- **Tier 4 (Workspace):** Per-workspace overrides. Stored in CRD spec.

Everything configurable through a config file or env var (GitOps), with optional clickops via the UI for operators who prefer it.

## Architecture

### Configuration Resolution Order

```
Workspace setting (Tier 4)
  → User preference (Tier 3)
    → Instance setting (Tier 2)
      → Hardcoded default
```

Not all settings exist at all tiers. Most Tier 2 settings have no user override (admin-only). Tier 3 is purely UX preferences.

### API Route Separation

Admin and user settings are completely separate route groups with independent auth middleware:

```
User:   GET/PUT  /api/v1/users/me/settings[/:key]
Admin:  GET/PUT  /api/v1/admin/settings[/:key]
Admin:  GET      /api/v1/admin/settings/platform
Admin:  CRUD     /api/v1/admin/credentials[/:id]
User:   GET      /api/v1/credentials  (accessible sets only, no keys)
```

Admin routes return 404 (not 403) for non-admin users — don't reveal route existence.

### Helm Seeding Behavior

On install/upgrade, a seed job inserts Tier 2 defaults:

```sql
INSERT INTO instance_settings (key, value) VALUES ($1, $2)
ON CONFLICT (key) DO NOTHING;
```

When a row already exists and differs from the Helm value, log:
```
WARN instance_setting_seed_skipped key=<key> current=<db_value> helm=<helm_value>
```

### User Settings Sync

- Frontend reads localStorage first (instant UI, no flash)
- On mount, fetches from API and reconciles
- Writes go to both localStorage (optimistic) and API (async persist)
- DB is source of truth; localStorage is cache

## Settings Inventory

### Tier 1: Platform (Read-only)

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
| `auth.cookieName` | `lsp_session` | string | ✓ | config.yaml (hardcoded in router) |
| `logging.level` | `info` | `debug` `info` `warn` `error` | ✓ | config.yaml/env |
| `logging.encoding` | `json` | `json` `console` | ✓ | config.yaml |
| `logging.development` | `false` | bool | | config.yaml |
| `mcp.enabled` | `true` | bool | | values.yaml |
| `mcp.transport` | `sse` | `sse` `stdio` | | values.yaml |
| `mcp.timeout` | `300s` | Go duration | | values.yaml |

### Tier 2: Instance (Admin-mutable)

| Key | Default | Valid Values | Req | Description |
|-----|---------|-------------|-----|-------------|
| **Auth** |
| `auth.registrationEnabled` | `true` | bool | ✓ | Allow new user sign-ups |
| `auth.lockoutEnabled` | `false` | bool | | Account lockout on failed attempts |
| `auth.lockoutAttempts` | `5` | 1–100 | | Failed attempts before lockout |
| `auth.lockoutDurationMinutes` | `15` | 1–1440 | | Lockout duration |
| **Rate Limiting** |
| `rateLimiting.enabled` | `true` | bool | ✓ | Global rate limiting |
| `rateLimiting.defaultLimit` | `100` | 1–100000 | | Requests per window |
| `rateLimiting.windowMinutes` | `1` | 1–1440 | | Rate limit window |
| `rateLimiting.burstSize` | `20` | 1–1000 | | Token bucket burst |
| `rateLimiting.strategy` | `token_bucket` | `token_bucket` `fixed_window` `sliding_window` | | Rate limit algorithm |
| **Workspace Defaults** |
| `workspace.defaultImage` | `ghcr.io/lenaxia/llmsafespace/base:latest` | container image ref | ✓ | Image for new workspaces |
| `workspace.defaultStorageSize` | `1Gi` | `^[0-9]+(Gi\|Mi)$` | ✓ | Default PVC size |
| `workspace.maxStorageSize` | `10Gi` | `^[0-9]+(Gi\|Mi)$` | ✓ | Max PVC size users can request |
| `workspace.defaultStorageClass` | `""` | StorageClass name or `""` | | K8s StorageClass |
| `workspace.maxActiveWorkspacesPerUser` | `3` | 1–50 | ✓ | Max running pods per user; oldest auto-suspended when exceeded |
| `workspace.defaultMaxActiveSessions` | `5` | 1–20 | ✓ | Default concurrent sessions per workspace |
| `workspace.defaultResources.cpu` | `500m` | K8s quantity | ✓ | Default CPU limit |
| `workspace.defaultResources.memory` | `512Mi` | K8s quantity | ✓ | Default memory limit |
| `workspace.defaultResources.ephemeralStorage` | `1Gi` | K8s quantity | | Default ephemeral storage |
| **Auto-Suspend** |
| `workspace.autoSuspend.enabled` | `true` | bool | ✓ | Global auto-suspend |
| `workspace.autoSuspend.idleTimeoutMinutes` | `60` | 5–10080 | ✓ | Idle timeout |
| `workspace.ttlDaysAfterSuspended` | `0` | 0–365 (0 = never) | | Auto-delete suspended workspaces |
| **Credentials** |
| `credentials.autoProvision` | `false` | bool | | Auto-copy default credential set to new workspaces |
| `credentials.defaultSetId` | `""` | UUID or `""` | | Which set to auto-provision |
| **Network** |
| `workspace.defaultNetworkAccess.ingress` | `false` | bool | | Allow inbound by default |
| `workspace.defaultNetworkAccess.egressDomains` | `[]` | string[] | | Default allowed egress |
| **Security** |
| `workspace.defaultSecurityLevel` | `standard` | `standard` `high` | | Pod security posture |
| **Branding** |
| `instance.name` | `LLMSafeSpace` | string 1–64 | | Instance display name |
| `instance.motd` | `""` | string 0–500 | | Login page message |

### Tier 3: User (Per-user)

| Key | Default | Valid Values | Req | Description |
|-----|---------|-------------|-----|-------------|
| `theme` | `system` | `light` `dark` `system` | ✓ | Color theme |
| `fontSize` | `14` | 10–24 | | Font size (px) |
| `compactMode` | `false` | bool | | Compact layout |
| `streamingEnabled` | `true` | bool | | Stream tokens |
| `showThinkingBlocks` | `true` | bool | | Show reasoning |
| `codeBlockWordWrap` | `false` | bool | | Wrap code lines |
| `sendOnEnter` | `true` | bool | | Enter sends |
| `preferredModel` | `""` | model ID or `""` | | Preferred model |
| `notifyOnSessionComplete` | `true` | bool | | Notify when agent finishes |
| `notifyOnWorkspaceReady` | `true` | bool | | Notify on workspace resume |
| `sidebarCollapsed` | `false` | bool | | Sidebar state |
| `sidebarWidth` | `280` | 200–600 | | Sidebar width (px) |

### Tier 4: Workspace-level

| Setting | CRD Field | Default | Valid Values | Req |
|---------|-----------|---------|-------------|-----|
| `name` | DB | auto-generated | string 1–64 | ✓ |
| `storageSize` | `spec.storage.size` | (instance) | `^[0-9]+(Gi\|Mi)$` ≤ max | ✓ |
| `storageClass` | `spec.storage.storageClassName` | (instance) | StorageClass or `""` | |
| `maxActiveSessions` | `spec.maxActiveSessions` | (instance: 5) | 1–20 | |
| `autoSuspend.enabled` | `spec.autoSuspend.enabled` | (instance) | bool | |
| `autoSuspend.idleTimeoutMinutes` | `spec.autoSuspend.idleTimeoutSeconds` | (instance) | 5–10080 | |
| `ttlDaysAfterSuspended` | `spec.ttlSecondsAfterSuspended` | (instance: 0) | 0–365 | |
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

## Credential Sets

New entity for managing multiple provider credential configurations.

### Schema

```sql
CREATE TABLE credential_sets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT UNIQUE NOT NULL,
  is_default BOOLEAN NOT NULL DEFAULT false,
  providers JSONB NOT NULL DEFAULT '{}',
  model_allowlist TEXT[] NOT NULL DEFAULT '{}',
  assigned_to JSONB NOT NULL DEFAULT '"all"',
  created_at TIMESTAMP NOT NULL DEFAULT now(),
  updated_at TIMESTAMP NOT NULL DEFAULT now()
);

-- Only one default at a time
CREATE UNIQUE INDEX idx_credential_sets_default ON credential_sets (is_default) WHERE is_default = true;
```

### Provider Config Format

```json
{
  "openai": { "apiKey": "sk-...", "baseUrl": "https://api.openai.com/v1" },
  "anthropic": { "apiKey": "sk-ant-..." },
  "deepseek": { "apiKey": "sk-...", "baseUrl": "https://api.deepseek.com" }
}
```

### Access Control

- `assigned_to`: `"all"` (everyone) or `["user-id-1", "user-id-2"]`
- Users only see set names + model allowlist, never API keys
- Admin sees everything

### UX Flow

1. Admin creates credential sets with provider keys + model allowlist
2. New workspaces get the default set auto-provisioned (if `credentials.autoProvision` is true)
3. Users can switch their workspace to any set they have access to
4. Chat model picker shows only models from the workspace's active credential set allowlist

## UI Component Library

### Decision: Radix UI Primitives

Hand-rolled components lack accessibility (focus trapping, keyboard nav, ARIA). Adding Radix for interactive primitives:

```
@radix-ui/react-dialog          — Dialog + Drawer
@radix-ui/react-dropdown-menu   — Replaces hand-rolled KebabMenu
@radix-ui/react-switch          — Toggle for boolean settings
@radix-ui/react-select          — Select dropdown
@radix-ui/react-slider          — Range slider
@radix-ui/react-tabs            — Tab navigation
@radix-ui/react-toast           — Save feedback, replaces UpdateAvailableToast
@radix-ui/react-tooltip         — Info icons on settings
```

### Components to Swap

| Current | Replace With |
|---------|-------------|
| `KebabMenu` (hand-rolled portal + click-outside) | `@radix-ui/react-dropdown-menu` |
| `RenameWorkspaceDialog` / `RenameSessionDialog` / `NewWorkspaceDialog` (inline forms) | `@radix-ui/react-dialog` |
| `UpdateAvailableToast` (one-off fixed div) | `@radix-ui/react-toast` |

### New Primitives to Build

| Component | Library | Description |
|-----------|---------|-------------|
| `Toggle` | Radix Switch | Boolean on/off |
| `Select` | Radix Select | Dropdown with options |
| `Slider` | Radix Slider | Range with value display |
| `Dialog` | Radix Dialog | Modal with focus trap |
| `Drawer` | Radix Dialog (side-positioned) | Slide-in panel |
| `Toast` + `useToast()` | Radix Toast | Notification system |
| `Tabs` | Radix Tabs | Tab bar + panels |
| `DropdownMenu` | Radix Dropdown Menu | Replaces KebabMenu |
| `Tooltip` | Radix Tooltip | Hover info |
| `NumberInput` | Hand-rolled | Input + min/max/step |
| `Textarea` | Hand-rolled | Styled multi-line |
| `TagInput` | Hand-rolled | Multi-value chips |

### Settings Layout Patterns

| Component | Description |
|-----------|-------------|
| `SettingsSection` | Group heading + description + children |
| `SettingsRow` | Label + description (left) + control (right) |
| `ReadOnlyField` | Label + value + source badge ("Config file") |

## Story List

| Story | Title | Size | Phase |
|-------|-------|------|-------|
| US-9.0 | UI Primitives — Radix integration + settings components | Large | A |
| US-9.1 | DB Schema — Settings tables | Small | A |
| US-9.2 | Settings Service — Backend CRUD + validation | Medium | A |
| US-9.3 | Settings Seed Job — Helm-driven seeding | Small | A |
| US-9.4 | Admin API Endpoints | Medium | A |
| US-9.5 | User Settings API Endpoints | Small | A |
| US-9.6 | Frontend — Admin Settings Page (Instance + Platform) | Large | B |
| US-9.7 | Frontend — User Settings Page Rework | Large | B |
| US-9.8 | Frontend — Workspace Settings Drawer | Medium | B |
| US-9.9 | Max Active Workspaces Enforcement | Small | B |
| US-9.10 | Max Storage Size Enforcement | Small | B |
| US-9.11 | Credential Sets Entity (full stack) | Large | C |
| US-9.12 | Frontend — Admin Credentials Page | Medium | C |
| US-9.13 | Preferred Model (User Setting + Chat Integration) | Medium | C |

## Dependency Graph

```
US-9.0 (UI primitives) ─────────────────────────────────┐
                                                          │
US-9.1 (DB schema) ──┐                                   │
                      ├── US-9.2 (service) ──┐            │
                      │                       ├── US-9.3 (seed)
                      │                       ├── US-9.4 (admin API) ──── US-9.6 (admin frontend) ←─┤
                      │                       ├── US-9.5 (user API) ───── US-9.7 (user frontend) ←──┤
                      │                       ├── US-9.9 (max workspaces)                           │
                      │                       └── US-9.10 (max storage)                             │
                      │                                                                             │
                      └── US-9.11 (credential sets) ──── US-9.12 (creds frontend) ←────────────────┤
                                                     └── US-9.13 (model picker) ←──────────────────┘

US-9.8 (workspace drawer) depends on US-9.0 + US-9.4 + US-9.5
```

## Phases

**Phase A (Foundation):** US-9.0, US-9.1, US-9.2, US-9.3, US-9.4, US-9.5
**Phase B (UI + Enforcement):** US-9.6, US-9.7, US-9.8, US-9.9, US-9.10
**Phase C (Credentials):** US-9.11, US-9.12, US-9.13

## Key Design Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Separate admin/user API routes (not role-branching in handlers) | Security — admin routes return 404 for non-admins, no information leakage |
| D2 | `ON CONFLICT DO NOTHING` for Helm seeding | GitOps sets initial defaults; UI overrides persist across upgrades |
| D3 | Radix UI for interactive primitives | Accessibility, focus management, keyboard nav — too error-prone to hand-roll |
| D4 | User settings in DB with localStorage cache | Sync across devices (DB) + instant UI (localStorage) |
| D5 | Credential sets as separate entity (not single secret) | Multi-provider support, model allowlisting, per-user access control |
| D6 | All durations stored/displayed in minutes | Consistent UX. CRD uses seconds internally; API converts at boundary. |
| D7 | Workspace timeout stays in seconds | Technical pod-level setting (max 86400s), not user-facing idle concept |
| D8 | No OIDC/SSO settings yet | Only a DTO field exists, no implementation. Add when built. |
| D9 | No Docker-mode settings yet | Epic 7 not implemented. Add when Docker deployment is built. |
