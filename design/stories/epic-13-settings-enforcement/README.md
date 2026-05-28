# Epic 13: Settings Enforcement

**Status:** Ready for Implementation
**Depends on:** Epic 9 (Configuration & Settings) — schema, services, API, and UI are complete
**Priority:** High — settings exist but 29/35 produce no side effects

---

## Problem Statement

Epic 9 delivered the settings infrastructure: schema definitions, DB storage, admin/user APIs, and frontend UI. However, most settings are **inert** — changing them in the UI has no effect on system behavior. Users can toggle "Auto-Suspend" or change "Default CPU" and nothing happens.

This epic wires every defined setting to its intended side effect.

---

## Current State (Post-Epic 9)

| Category | Defined | Wired | Gap |
|----------|---------|-------|-----|
| Instance (Tier 2) | 27 | 3 | 24 |
| User (Tier 3) | 8 | 3 | 5 |
| **Total** | **35** | **6** | **29** |

**Already wired:**
- `auth.registrationEnabled` → `/auth/config` endpoint
- `workspace.maxActiveWorkspacesPerUser` → enforced on activate
- `workspace.maxStorageSize` → enforced on create
- `theme` → ThemeProvider applies to DOM
- `fontSize` → ThemeProvider sets root font size
- `sendOnEnter` → Composer.tsx Enter key behavior

---

## Phases

### Phase A: Workspace Defaults (Tier 1 — highest ROI)

Every workspace creation should read these from instance settings instead of using hardcoded values.

### Phase B: User Preferences (Tier 1 — trivial frontend)

Frontend components consume user settings that are already stored.

### Phase C: Security Hot-Reload (Tier 2 — auth/rate limiting)

Auth and rate limiting middleware read from DB at runtime instead of static startup config.

### Phase D: Lifecycle Automation (Tier 3 — new components)

Background jobs that enforce auto-suspend, TTL cleanup, and auto-provisioning.

### Phase E: Cosmetic & Network (Tier 4 — low priority)

Branding, network defaults, and notification infrastructure.

---

## User Stories

### Phase A: Workspace Defaults

#### US-13.0: Wire workspace.defaultImage

**As an** admin, **I want** the "Default Image" setting to control which container image new workspaces use, **so that** I can change the runtime without redeploying.

**Acceptance Criteria:**
- Workspace service reads `workspace.defaultImage` from instance settings on `CreateWorkspace`
- If setting unavailable, falls back to hardcoded default (graceful degradation)
- Existing workspaces are not affected (only new creates)

**Implementation:**
- File: `api/internal/services/workspace/workspace_service.go` (or equivalent create path)
- Read: `s.instanceSettings.GetString(ctx, "workspace.defaultImage")`
- Use as the image when building the Workspace CRD spec

---

#### US-13.1: Wire workspace.defaultStorageSize

**As an** admin, **I want** the "Default Storage" setting to control PVC size for new workspaces.

**Acceptance Criteria:**
- Workspace service reads `workspace.defaultStorageSize` on create
- Value used as PVC `storage` request in the Workspace CRD
- Validated against `workspace.maxStorageSize` (already enforced)

**Implementation:**
- Same file as US-13.0
- Read: `s.instanceSettings.GetString(ctx, "workspace.defaultStorageSize")`

---

#### US-13.2: Wire workspace.defaultResources (cpu, memory, ephemeralStorage)

**As an** admin, **I want** default resource limits to apply to new workspaces.

**Acceptance Criteria:**
- Reads `workspace.defaultResources.cpu`, `.memory`, `.ephemeralStorage`
- Applied as container resource limits/requests in the pod spec
- Graceful degradation if settings unavailable

**Implementation:**
- Read three settings, build `ResourceRequirements` struct
- Apply when constructing the Sandbox/Workspace CRD

---

#### US-13.3: Wire workspace.defaultMaxActiveSessions

**As an** admin, **I want** the max sessions setting to control concurrent sessions per workspace.

**Acceptance Criteria:**
- `ActiveSessionsResponse.MaxActive` reads from settings instead of hardcoded `5`
- Session creation rejects if at limit

**Implementation:**
- File: `api/internal/server/router.go` (the `/sessions/active` handler)
- Read: `instanceSettings.GetInt(ctx, "workspace.defaultMaxActiveSessions")`

---

#### US-13.4: Wire workspace.defaultSecurityLevel

**As an** admin, **I want** the default security level to apply to new workspaces.

**Acceptance Criteria:**
- New workspaces use the configured security level when no explicit level is specified in the create request
- Maps to SandboxProfile selection

**Implementation:**
- Read on workspace/sandbox create, use as default when `req.SecurityLevel == ""`

---

#### US-13.5: Wire workspace.defaultStorageClass

**As an** admin, **I want** to configure which Kubernetes StorageClass is used for workspace PVCs.

**Acceptance Criteria:**
- If non-empty, set as `storageClassName` on the PVC spec
- If empty, omit (cluster default applies)

**Implementation:**
- Read: `s.instanceSettings.GetString(ctx, "workspace.defaultStorageClass")`
- Set on PVC spec in controller or workspace service

---

### Phase B: User Preferences

#### US-13.6: Wire user preferredModel

**As a** user, **I want** my preferred model setting to pre-select in the model picker.

**Acceptance Criteria:**
- Model picker dropdown reads `preferredModel` from user settings
- If set and available in credential set allowlist, pre-selects it
- If not available, falls back to first available model

**Implementation:**
- Frontend: read via `useUserSetting("preferredModel", "")` in model picker component

---

#### US-13.7: Wire user codeBlockWordWrap

**As a** user, **I want** code blocks to wrap long lines when I enable word wrap.

**Acceptance Criteria:**
- Code block renderer applies `white-space: pre-wrap` when setting is true
- Default: `pre` (no wrap)

**Implementation:**
- Frontend: `useUserSetting("codeBlockWordWrap", false)` in message/code-block component
- Apply CSS class conditionally

---

#### US-13.8: Wire user compactMode

**As a** user, **I want** compact mode to reduce spacing throughout the UI.

**Acceptance Criteria:**
- App shell adds a `compact` class to root element when enabled
- CSS reduces padding/margins globally

**Implementation:**
- Frontend: `useUserSetting("compactMode", false)` in AppShell or ThemeProvider
- Add `data-compact="true"` attribute, define CSS rules

---

#### US-13.9: Wire user notifyOnSessionComplete and notifyOnWorkspaceReady

**As a** user, **I want** to control which browser notifications I receive.

**Acceptance Criteria:**
- When session completes (SSE event), check setting before showing notification
- When workspace becomes ready, check setting before showing notification
- Settings default to true

**Implementation:**
- Frontend: read settings in the SSE event handler / notification dispatch
- Gate `new Notification(...)` or toast behind the setting check

---

### Phase C: Security Hot-Reload

#### US-13.10: Wire auth.lockout* settings at runtime

**As an** admin, **I want** lockout settings to take effect immediately without pod restart.

**Acceptance Criteria:**
- Auth service reads `auth.lockoutEnabled`, `auth.lockoutAttempts`, `auth.lockoutDurationMinutes` from instance settings on each login attempt
- Falls back to schema defaults if DB unavailable
- Config file values are used only for initial seed

**Implementation:**
- File: `api/internal/services/auth/auth.go`
- Replace `s.config.Auth.LockoutEnabled` reads with `s.instanceSettings.GetBool(ctx, "auth.lockoutEnabled")`
- Inject `instanceSettings` into auth service (same pattern as workspace service)

---

#### US-13.11: Wire rateLimiting.* settings at runtime

**As an** admin, **I want** rate limiting changes to take effect without pod restart.

**Acceptance Criteria:**
- Rate limiter reads `rateLimiting.enabled`, `rateLimiting.defaultLimit`, `rateLimiting.windowMinutes`, `rateLimiting.burstSize`, `rateLimiting.strategy` from instance settings
- Changes apply within the cache TTL (60s)
- If settings unavailable, uses last known good values

**Implementation:**
- Option A: Rate limiter middleware reads settings per-request (simple, slight overhead)
- Option B: Rate limiter has a `Reload()` method called on a timer or cache invalidation
- Inject `instanceSettings` into rate limiter service

---

### Phase D: Lifecycle Automation

#### US-13.12: Wire workspace.autoSuspend (enabled + idleTimeoutMinutes)

**As an** admin, **I want** idle workspaces to be automatically suspended.

**Acceptance Criteria:**
- A background goroutine (or controller reconciliation loop) periodically checks active workspaces
- If `workspace.autoSuspend.enabled` is true and a workspace's `lastActivityAt` exceeds `idleTimeoutMinutes`, suspend it
- Check interval: every 60 seconds
- Graceful: logs suspension, doesn't race with active requests

**Implementation:**
- New file: `api/internal/services/workspace/auto_suspend.go`
- Goroutine started in `app.Run()`, stopped in `app.Shutdown()`
- Reads settings each cycle (respects admin changes without restart)

---

#### US-13.13: Wire workspace.ttlDaysAfterSuspended

**As an** admin, **I want** long-suspended workspaces to be automatically deleted.

**Acceptance Criteria:**
- If `workspace.ttlDaysAfterSuspended > 0`, workspaces suspended longer than N days are terminated (PVC deleted)
- If 0, no auto-deletion (default)
- Runs as part of the same background loop as auto-suspend

**Implementation:**
- Add to the auto-suspend goroutine: after checking idle, check suspended workspaces against TTL

---

#### US-13.14: Wire credentials.autoProvision

**As an** admin, **I want** new workspaces to automatically receive the default credential set.

**Acceptance Criteria:**
- On workspace create, if `credentials.autoProvision` is true and a default credential set exists, inject it
- Uses existing `SetCredentials` / secret injection path
- If no default credential set, skip silently

**Implementation:**
- File: workspace service create path
- After workspace CRD creation, read setting, get default credential set, inject if present

---

### Phase E: Cosmetic & Network

#### US-13.15: Wire instance.name and instance.motd

**As an** admin, **I want** the instance name and MOTD to appear in the UI.

**Acceptance Criteria:**
- Frontend reads `instance.name` from admin settings API (or a public config endpoint)
- Displays in page title and/or header
- `instance.motd` displayed on login page if non-empty

**Implementation:**
- Add a public `/api/v1/config` endpoint that returns instance name + motd (no auth required)
- Frontend reads on app init

---

#### US-13.16: Wire workspace.defaultNetworkAccess (ingress, egressDomains)

**As an** admin, **I want** default network policies to apply to new workspaces.

**Acceptance Criteria:**
- New workspaces get `ingress` and `egressDomains` from settings when not specified in create request
- Controller builds NetworkPolicy from these values

**Implementation:**
- Read in workspace service on create
- Pass to CRD spec → controller reads from CRD and builds NetworkPolicy

---

## Implementation Order

| Priority | Stories | Effort | Impact |
|----------|---------|--------|--------|
| **P0 — Do first** | US-13.0 through US-13.5 | ~1 hour | Every workspace creation uses correct defaults |
| **P1 — Same day** | US-13.6 through US-13.8 | ~30 min | Users see their preferences take effect |
| **P2 — This week** | US-13.10, US-13.11, US-13.14 | ~2 hours | Security settings work without restart |
| **P3 — Next sprint** | US-13.12, US-13.13 | ~4 hours | Lifecycle automation (new component) |
| **P4 — Whenever** | US-13.9, US-13.15, US-13.16 | ~2 hours | Notifications, branding, network |

---

## Testing Requirements

Per README-LLM.md rules:
- TDD for all backend changes
- Each story needs: happy path + unhappy path (settings unavailable → graceful degradation) + integration test
- Frontend: `tsc --noEmit` clean + manual verification

## Definition of Done

A setting is "wired" when:
1. Changing it via the admin/user API produces a measurable behavior change
2. A test proves the behavior change
3. Graceful degradation is tested (DB down → falls back to schema default)
