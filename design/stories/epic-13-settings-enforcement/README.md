# Epic 13: Settings Enforcement

**Status:** Ready for Implementation
**Depends on:** Epic 9 (Configuration & Settings)
**Priority:** High — 29/35 settings produce no side effects

---

## Problem Statement

Epic 9 delivered settings infrastructure (schema, DB, API, UI). Most settings are inert — changing them has no effect. This epic wires each setting to its intended behavior.

---

## Validated Assumptions

| # | Assumption | Validation |
|---|-----------|-----------|
| A1 | `buildWorkspaceCRD()` is the single point where CRD spec is constructed | Verified: `workspace_service.go:168` — only call site for workspace creation |
| A2 | `WorkspaceSpec` has `Resources *ResourceRequirements` field | Verified: `workspace_types.go:89` — `Resources *ResourceRequirements` |
| A3 | `WorkspaceSpec` has `SecurityLevel string` field | Verified: `workspace_types.go:77` — `SecurityLevel string` |
| A4 | `WorkspaceSpec.Storage.StorageClassName` exists | Verified: `workspace_types.go:18` |
| A5 | `WorkspaceSpec.AutoSuspend` has `Enabled` and `IdleTimeoutSeconds` | Verified: `workspace_types.go:43-47` |
| A6 | `WorkspaceSpec.TTLSecondsAfterSuspended` exists | Verified: `workspace_types.go:84` |
| A7 | `WorkspaceSpec.MaxActiveSessions` exists | Verified: `workspace_types.go:92` |
| A8 | `WorkspaceSpec.NetworkAccess` has `Ingress bool` and `Egress []WorkspaceEgressRule` | Verified: `workspace_types.go:30-35` |
| A9 | Auth service reads lockout config from `s.config.Auth.LockoutEnabled` (line 409) | Verified: `auth.go:409,413,468,478` |
| A10 | Rate limit middleware reads `config.Enabled`, `config.DefaultLimit`, `config.BurstSize` per-request from closure | Verified: `rate_limit.go:40,62,63` — config is captured in closure at startup |
| A11 | `CreateWorkspaceRequest` has `StorageSize`, `Runtime`, `StorageClass` but NOT `Resources`, `SecurityLevel`, `NetworkAccess`, `Image` | Verified: `types.go` — only `Name`, `Runtime`, `StorageSize`, `StorageClass`, `Labels` |
| A12 | `instanceSettings` is already injected into workspace service via `SetInstanceSettings` | Verified: `max_active.go:14` |
| A13 | Frontend has `useUserSetting(key, default)` hook | Verified: `useUserSettings.ts:45` |
| A14 | Frontend code blocks use `whitespace-pre-wrap` in `MessagePart.tsx` (hardcoded) | Verified: `MessagePart.tsx:28,59` |
| A15 | No model picker component exists yet | Verified: no files matching `*model*` or `*Model*` in frontend/src |
| A16 | `Composer.tsx` already uses `useUserSetting("sendOnEnter", true)` | Verified: `Composer.tsx:17` |
| A17 | `ThemeProvider.tsx` reads `theme` and `fontSize` from API | Verified: lines 33-40 |
| A18 | `WorkspaceStatus.SuspendedAt` exists for TTL calculation | Verified: `workspace_types.go` — `SuspendedAt *metav1.Time` |

---

## Phase A: Workspace Defaults

### US-13.0: Wire workspace.defaultImage

**File:** `api/internal/services/workspace/workspace_service.go`
**Function:** `buildWorkspaceCRD` (line 168)

**Current behavior:** `spec.Runtime` is set directly from `req.Runtime`. If `req.Runtime` is empty, the CRD has an empty runtime field and the controller uses whatever image is in the RuntimeEnvironment CRD.

**Desired behavior:** If `req.Runtime` is empty, read `workspace.defaultImage` from instance settings and use it as the runtime.

**Implementation:**
```go
// In CreateWorkspace, before calling buildWorkspaceCRD:
if req.Runtime == "" && s.instanceSettings != nil {
    if img, err := s.instanceSettings.GetString(ctx, "workspace.defaultImage"); err == nil && img != "" {
        req.Runtime = img
    }
}
```

**Tests:**
1. Happy: CreateWorkspace with empty runtime → CRD has default image from settings
2. Happy: CreateWorkspace with explicit runtime → CRD uses explicit (not overridden)
3. Unhappy: instanceSettings nil → no panic, empty runtime passes through
4. Unhappy: instanceSettings returns error → graceful degradation, empty runtime

**Acceptance criteria:**
- New workspaces with no explicit runtime use the admin-configured default image
- Existing workspaces unaffected
- No panic if settings service unavailable

---

### US-13.1: Wire workspace.defaultStorageSize

**File:** `api/internal/services/workspace/workspace_service.go`
**Function:** `CreateWorkspace` (line 140)

**Current behavior:** `req.StorageSize` is required — returns validation error if empty (line 147).

**Desired behavior:** If `req.StorageSize` is empty, read `workspace.defaultStorageSize` from instance settings. Only error if both are empty.

**Implementation:**
```go
// Replace the existing validation at line 147:
if req.StorageSize == "" && s.instanceSettings != nil {
    if size, err := s.instanceSettings.GetString(ctx, "workspace.defaultStorageSize"); err == nil && size != "" {
        req.StorageSize = size
    }
}
if req.StorageSize == "" {
    return nil, apierrors.NewValidationError(...)
}
```

**Tests:**
1. Happy: Empty storageSize + setting "2Gi" → CRD has "2Gi"
2. Happy: Explicit "5Gi" → uses "5Gi" regardless of setting
3. Unhappy: Empty storageSize + settings unavailable → validation error (existing behavior)
4. Integration: Setting "2Gi" + maxStorageSize "10Gi" → passes; setting "20Gi" + max "10Gi" → blocked by existing enforcement

**Acceptance criteria:**
- `storageSize` field becomes optional in the API (was required)
- Default comes from admin settings
- Still validated against `workspace.maxStorageSize`

---

### US-13.2: Wire workspace.defaultResources

**File:** `api/internal/services/workspace/workspace_service.go`
**Function:** `buildWorkspaceCRD` (line 168)

**Current behavior:** `spec.Resources` is nil (never set from `CreateWorkspaceRequest` — the request type doesn't have a Resources field per A11). The controller/scheduler uses pod defaults.

**Desired behavior:** If no resources specified, read defaults from settings and set on the CRD spec.

**Implementation:**
```go
// In buildWorkspaceCRD or CreateWorkspace, after building the CRD:
// Note: instanceSettings must be passed to buildWorkspaceCRD or applied after
if crd.Spec.Resources == nil && s.instanceSettings != nil {
    cpu, _ := s.instanceSettings.GetString(ctx, "workspace.defaultResources.cpu")
    mem, _ := s.instanceSettings.GetString(ctx, "workspace.defaultResources.memory")
    eph, _ := s.instanceSettings.GetString(ctx, "workspace.defaultResources.ephemeralStorage")
    if cpu != "" || mem != "" || eph != "" {
        crd.Spec.Resources = &v1.ResourceRequirements{
            CPU: cpu, Memory: mem, EphemeralStorage: eph,
        }
    }
}
```

**Design decision:** `buildWorkspaceCRD` is currently a pure function (no service access). Options:
- (A) Pass instanceSettings to it — breaks purity, adds parameter
- (B) Apply defaults after `buildWorkspaceCRD` returns — keeps function pure

**Recommendation:** Option B — mutate the CRD after construction. Keeps `buildWorkspaceCRD` testable without mocks.

**Tests:**
1. Happy: No resources in request + settings configured → CRD has resources
2. Happy: Settings partially configured (only cpu) → only cpu set, others empty
3. Unhappy: Settings unavailable → CRD.Spec.Resources remains nil (controller defaults apply)

---

### US-13.3: Wire workspace.defaultMaxActiveSessions

**File:** `api/internal/server/router.go`
**Function:** Anonymous handler at line ~113 (the `/sessions/active` endpoint)

**Current behavior:** Hardcoded `MaxActive: 5` (line 119 in router.go).

**Desired behavior:** Read from instance settings.

**Implementation:**
```go
// In the /sessions/active handler:
maxActive := 5
if cfg.InstanceSettings != nil {
    if v, err := cfg.InstanceSettings.GetInt(c.Request.Context(), "workspace.defaultMaxActiveSessions"); err == nil && v > 0 {
        maxActive = v
    }
}
c.JSON(http.StatusOK, types.ActiveSessionsResponse{
    Active:    active,
    MaxActive: maxActive,
})
```

Also set on the CRD spec in `buildWorkspaceCRD`:
```go
if crd.Spec.MaxActiveSessions == 0 && s.instanceSettings != nil {
    if v, err := s.instanceSettings.GetInt(ctx, "workspace.defaultMaxActiveSessions"); err == nil && v > 0 {
        crd.Spec.MaxActiveSessions = int32(v)
    }
}
```

**Tests:**
1. Happy: Setting = 10 → response shows MaxActive: 10
2. Happy: Setting = 10 → CRD spec has MaxActiveSessions: 10
3. Unhappy: Settings unavailable → falls back to 5

---

### US-13.4: Wire workspace.defaultSecurityLevel

**File:** `api/internal/services/workspace/workspace_service.go`
**Function:** `buildWorkspaceCRD`

**Current behavior:** `spec.SecurityLevel` is empty string (never set from request — A11 confirms request doesn't have this field).

**Desired behavior:** Read default from settings.

**Implementation:**
```go
if crd.Spec.SecurityLevel == "" && s.instanceSettings != nil {
    if level, err := s.instanceSettings.GetString(ctx, "workspace.defaultSecurityLevel"); err == nil && level != "" {
        crd.Spec.SecurityLevel = level
    }
}
```

**Tests:**
1. Happy: Setting "high" → CRD has SecurityLevel "high"
2. Unhappy: Settings unavailable → empty string (controller uses "standard" default per kubebuilder annotation)

---

### US-13.5: Wire workspace.defaultStorageClass

**File:** `api/internal/services/workspace/workspace_service.go`
**Function:** `buildWorkspaceCRD`

**Current behavior:** `spec.Storage.StorageClassName` is set from `req.StorageClass` (which is optional in the request).

**Desired behavior:** If `req.StorageClass` is empty, read from settings.

**Implementation:**
```go
if crd.Spec.Storage.StorageClassName == "" && s.instanceSettings != nil {
    if sc, err := s.instanceSettings.GetString(ctx, "workspace.defaultStorageClass"); err == nil && sc != "" {
        crd.Spec.Storage.StorageClassName = sc
    }
}
```

**Tests:**
1. Happy: Empty storageClass + setting "fast-ssd" → CRD has "fast-ssd"
2. Happy: Explicit "slow-hdd" → uses explicit
3. Unhappy: Settings unavailable → empty (cluster default)

---

### US-13.6: Wire workspace.autoSuspend defaults

**File:** `api/internal/services/workspace/workspace_service.go`
**Function:** `buildWorkspaceCRD`

**Current behavior:** Hardcoded `AutoSuspend: &v1.WorkspaceAutoSuspend{Enabled: true, IdleTimeoutSeconds: 86400}` (line 178).

**Desired behavior:** Read from settings.

**Implementation:**
```go
// Replace hardcoded values:
autoSuspendEnabled := true
idleTimeout := int64(86400)
if s.instanceSettings != nil {
    if v, err := s.instanceSettings.GetBool(ctx, "workspace.autoSuspend.enabled"); err == nil {
        autoSuspendEnabled = v
    }
    if v, err := s.instanceSettings.GetInt(ctx, "workspace.autoSuspend.idleTimeoutMinutes"); err == nil && v > 0 {
        idleTimeout = int64(v) * 60 // convert minutes to seconds
    }
}
spec.AutoSuspend = &v1.WorkspaceAutoSuspend{
    Enabled: autoSuspendEnabled, IdleTimeoutSeconds: idleTimeout,
}
```

**Note:** The schema defines `idleTimeoutMinutes` but the CRD field is `IdleTimeoutSeconds`. Conversion needed.

**Tests:**
1. Happy: Setting enabled=false → CRD has AutoSuspend.Enabled=false
2. Happy: Setting timeout=30 (minutes) → CRD has IdleTimeoutSeconds=1800
3. Unhappy: Settings unavailable → defaults (true, 86400)

---

### US-13.7: Wire workspace.defaultNetworkAccess

**File:** `api/internal/services/workspace/workspace_service.go`
**Function:** `buildWorkspaceCRD`

**Current behavior:** `spec.NetworkAccess` is nil (never set).

**Desired behavior:** Read defaults from settings.

**Implementation:**
```go
if crd.Spec.NetworkAccess == nil && s.instanceSettings != nil {
    ingress, _ := s.instanceSettings.GetBool(ctx, "workspace.defaultNetworkAccess.ingress")
    domains, _ := s.instanceSettings.GetStrings(ctx, "workspace.defaultNetworkAccess.egressDomains")
    if ingress || len(domains) > 0 {
        egress := make([]v1.WorkspaceEgressRule, len(domains))
        for i, d := range domains {
            egress[i] = v1.WorkspaceEgressRule{Domain: d}
        }
        crd.Spec.NetworkAccess = &v1.WorkspaceNetworkAccess{
            Ingress: ingress, Egress: egress,
        }
    }
}
```

**Tests:**
1. Happy: Ingress=true, domains=["api.openai.com"] → CRD has NetworkAccess set
2. Happy: Both false/empty → NetworkAccess remains nil
3. Unhappy: Settings unavailable → nil (no network restrictions)

---

## Phase B: User Preferences

### US-13.8: Wire user codeBlockWordWrap

**File:** `frontend/src/components/chat/MessagePart.tsx`

**Current behavior:** Code blocks use hardcoded `whitespace-pre-wrap` (line 28, 59).

**Desired behavior:** Use `pre` by default, `pre-wrap` when setting is true.

**Implementation:**
```tsx
// In the component or a parent:
const wordWrap = useUserSetting("codeBlockWordWrap", false);
const preClass = wordWrap ? "whitespace-pre-wrap" : "whitespace-pre overflow-x-auto";
```

**Tests:**
- Frontend typecheck passes
- Manual: toggle setting → code blocks change wrapping behavior

---

### US-13.9: Wire user compactMode

**File:** `frontend/src/components/layout/AppShell.tsx` or `frontend/src/providers/ThemeProvider.tsx`

**Current behavior:** No compact mode applied.

**Desired behavior:** When enabled, add `data-compact="true"` to root element. CSS reduces spacing.

**Implementation:**
```tsx
// In ThemeProvider or AppShell:
const compact = useUserSetting("compactMode", false);
useEffect(() => {
  document.documentElement.setAttribute("data-compact", String(compact));
}, [compact]);
```

CSS in `index.css`:
```css
[data-compact="true"] { --spacing-unit: 0.5; }
```

**Tests:**
- Frontend typecheck passes
- Manual: toggle → spacing reduces

---

### US-13.10: Wire user preferredModel

**File:** Does not exist yet — no model picker component (A15).

**Current behavior:** No model picker exists. The model is determined by the credential set's provider.

**Desired behavior:** When a model picker is built (future), it reads this setting to pre-select.

**Status:** **DEFERRED** — cannot wire until a model picker component exists. The setting is correctly defined in the schema for future use.

**Prerequisite:** Build a model picker component (separate story, likely Epic 14 or a frontend epic).

---

## Phase C: Security Hot-Reload

### US-13.11: Wire auth.lockout* at runtime

**File:** `api/internal/services/auth/auth.go`
**Lines:** 409, 413, 468, 478

**Current behavior:** Reads `s.config.Auth.LockoutEnabled`, `s.config.Auth.LockoutAttempts`, `s.config.Auth.LockoutDuration` — static values set at startup.

**Desired behavior:** Read from instance settings on each login attempt.

**Implementation:**
1. Add `instanceSettings *settings.InstanceService` field to auth `Service` struct
2. Add `SetInstanceSettings(svc)` method (same pattern as workspace service)
3. Wire in `app.go` after creating instanceSettings
4. Replace reads:
```go
// Before (line 409):
if s.config.Auth.LockoutEnabled {
// After:
lockoutEnabled := s.config.Auth.LockoutEnabled // fallback
if s.instanceSettings != nil {
    if v, err := s.instanceSettings.GetBool(ctx, "auth.lockoutEnabled"); err == nil {
        lockoutEnabled = v
    }
}
if lockoutEnabled {
```

Same pattern for `LockoutAttempts` (GetInt) and `LockoutDuration` (GetInt → minutes → time.Duration).

**Tests:**
1. Happy: Setting lockoutEnabled=true, attempts=3 → locks after 3 failures
2. Happy: Setting lockoutEnabled=false → never locks regardless of failures
3. Happy: Change setting from false→true → next login attempt uses new value (no restart)
4. Unhappy: instanceSettings nil → falls back to config file values
5. Unhappy: instanceSettings error → falls back to config file values

---

### US-13.12: Wire rateLimiting.* at runtime

**File:** `api/internal/middleware/rate_limit.go`

**Current behavior:** `RateLimitMiddleware` captures `config RateLimitConfig` in a closure at startup (line 38). The closure reads `config.Enabled`, `config.DefaultLimit`, `config.BurstSize` on every request — but the config struct is static.

**Desired behavior:** Read from instance settings per-request.

**Design options:**
- **(A) Inject instanceSettings into middleware closure** — middleware reads settings each request. Simple but adds ~1 cache-hit read per request (60s TTL cache means it's a map lookup, not a DB call).
- **(B) Periodic reload** — background goroutine updates the config struct every 60s. More complex, same effective latency.

**Recommendation:** Option A — the singleflight cache makes reads essentially free.

**Implementation:**
```go
func RateLimitMiddleware(rl interfaces.RateLimiterService, log pkginterfaces.LoggerInterface, config RateLimitConfig, instanceSettings *settings.InstanceService) gin.HandlerFunc {
    return func(c *gin.Context) {
        enabled := config.Enabled
        limit := config.DefaultLimit
        burst := config.BurstSize
        if instanceSettings != nil {
            if v, err := instanceSettings.GetBool(c.Request.Context(), "rateLimiting.enabled"); err == nil {
                enabled = v
            }
            if v, err := instanceSettings.GetInt(c.Request.Context(), "rateLimiting.defaultLimit"); err == nil && v > 0 {
                limit = v
            }
            if v, err := instanceSettings.GetInt(c.Request.Context(), "rateLimiting.burstSize"); err == nil && v > 0 {
                burst = v
            }
        }
        if !enabled { c.Next(); return }
        // ... rest uses limit, burst
    }
}
```

**Breaking change:** Function signature changes. All callers must be updated (only `router.go`).

**Tests:**
1. Happy: Setting enabled=false → requests pass without rate limiting
2. Happy: Setting limit=5 → 6th request in window gets 429
3. Happy: Change limit from 100→5 → takes effect within 60s (cache TTL)
4. Unhappy: instanceSettings nil → uses static config (existing behavior)
5. Integration: Full request flow with rate limiting from DB settings

---

## Phase D: Lifecycle Automation

### US-13.13: Auto-suspend background job

**New file:** `api/internal/services/workspace/auto_suspend.go`

**Current behavior:** No background job exists. `WorkspaceSpec.AutoSuspend` is set on the CRD but nothing acts on it (the controller may or may not implement this — needs verification).

**Assumption to validate before implementation:** Does the controller already implement auto-suspend based on `spec.autoSuspend`? Check `controller/internal/workspace/controller.go`.

**If controller does NOT implement it:**
- API server needs a background goroutine that:
  1. Lists all Active workspaces
  2. For each, checks `lastActivityAt` against `idleTimeoutSeconds`
  3. If idle > timeout, calls SuspendWorkspace

**If controller DOES implement it:**
- This story reduces to just wiring the settings into `buildWorkspaceCRD` (already covered in US-13.6).

**Implementation (if API-side):**
```go
type AutoSuspendWorker struct {
    logger           pkginterfaces.LoggerInterface
    k8sClient        pkginterfaces.KubernetesClient
    instanceSettings *settings.InstanceService
    namespace        string
    stopCh           chan struct{}
}

func (w *AutoSuspendWorker) Start() {
    go w.run()
}

func (w *AutoSuspendWorker) run() {
    ticker := time.NewTicker(60 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-w.stopCh: return
        case <-ticker.C: w.sweep()
        }
    }
}

func (w *AutoSuspendWorker) sweep() {
    enabled, _ := w.instanceSettings.GetBool(context.Background(), "workspace.autoSuspend.enabled")
    if !enabled { return }
    // List active workspaces, check lastActivityAt, suspend idle ones
}
```

**Tests:**
1. Happy: Workspace idle > timeout → suspended
2. Happy: Workspace active (recent activity) → not suspended
3. Happy: Setting enabled=false → no suspensions
4. Unhappy: K8s API error → logged, continues next cycle

---

### US-13.14: TTL cleanup for suspended workspaces

**File:** Same as US-13.13 (part of the sweep loop)

**Current behavior:** Suspended workspaces persist indefinitely.

**Desired behavior:** If `workspace.ttlDaysAfterSuspended > 0` and workspace has been suspended for > N days, delete it.

**Implementation:**
```go
// In the sweep loop, after auto-suspend check:
ttlDays, _ := w.instanceSettings.GetInt(ctx, "workspace.ttlDaysAfterSuspended")
if ttlDays > 0 {
    // List suspended workspaces where SuspendedAt + ttlDays < now
    // Delete each (PVC included)
}
```

**Uses:** `WorkspaceStatus.SuspendedAt` (validated in A18).

**Tests:**
1. Happy: Suspended 10 days ago, TTL=7 → deleted
2. Happy: Suspended 3 days ago, TTL=7 → not deleted
3. Happy: TTL=0 → no deletions ever
4. Unhappy: SuspendedAt nil → skip (don't delete)

---

### US-13.15: Wire credentials.autoProvision

**File:** `api/internal/services/workspace/workspace_service.go`
**Function:** `CreateWorkspace`

**Current behavior:** New workspaces have no credentials unless explicitly set via `PUT /workspaces/:id/credentials`.

**Desired behavior:** If `credentials.autoProvision` is true and a default credential set exists, auto-inject on create.

**Implementation:**
```go
// After successful workspace creation, before returning:
if s.instanceSettings != nil {
    if autoProvision, err := s.instanceSettings.GetBool(ctx, "credentials.autoProvision"); err == nil && autoProvision {
        // Get default credential set and inject
        // This requires access to the credential service — needs injection
    }
}
```

**Design decision:** The workspace service doesn't currently have access to the credential service. Options:
- (A) Inject credential service into workspace service
- (B) Handle in the handler layer (after CreateWorkspace returns)
- (C) Use an event/hook pattern

**Recommendation:** Option A — inject a `CredentialProvisioner` interface to keep it testable.

**Tests:**
1. Happy: autoProvision=true + default credential set exists → workspace gets credentials
2. Happy: autoProvision=true + no default → no error, no credentials
3. Happy: autoProvision=false → no credentials regardless
4. Unhappy: Credential injection fails → workspace still created (non-fatal), error logged

---

## Phase E: Cosmetic

### US-13.16: Wire instance.name and instance.motd

**File:** `api/internal/server/router.go` (add to existing `/auth/config` endpoint or new `/api/v1/config`)

**Current behavior:** No public endpoint exposes instance name or MOTD.

**Desired behavior:** Public endpoint returns instance branding.

**Implementation:**
```go
// Extend the existing /auth/config endpoint:
rg.GET("/config", func(c *gin.Context) {
    regEnabled := true
    instanceName := "LLMSafeSpace"
    motd := ""
    if instanceSettings != nil {
        if v, err := instanceSettings.GetBool(ctx, "auth.registrationEnabled"); err == nil { regEnabled = v }
        if v, err := instanceSettings.GetString(ctx, "instance.name"); err == nil && v != "" { instanceName = v }
        if v, err := instanceSettings.GetString(ctx, "instance.motd"); err == nil { motd = v }
    }
    c.JSON(http.StatusOK, gin.H{
        "registrationEnabled": regEnabled,
        "oidcEnabled": false,
        "instanceName": instanceName,
        "motd": motd,
    })
})
```

Frontend reads on app init and displays in header/login page.

**Tests:**
1. Happy: Setting name="My Corp AI" → /auth/config returns it
2. Happy: Empty motd → field present but empty string
3. Unhappy: Settings unavailable → defaults ("LLMSafeSpace", "")

---

## Implementation Order

| Priority | Stories | Effort | Impact |
|----------|---------|--------|--------|
| **P0** | US-13.0 – US-13.7 | ~2 hrs | Every workspace creation uses correct defaults |
| **P1** | US-13.8, US-13.9 | ~30 min | Users see preferences take effect |
| **P2** | US-13.11, US-13.12, US-13.15 | ~3 hrs | Security hot-reload + auto-provision |
| **P3** | US-13.13, US-13.14 | ~4 hrs | Lifecycle automation (new component) |
| **P4** | US-13.16 | ~30 min | Branding |
| **Deferred** | US-13.10 | — | Blocked on model picker component |

---

## Open Questions (must validate before implementing Phase C/D)

1. **Does the controller already implement auto-suspend?** Check `controller/internal/workspace/controller.go` for idle timeout logic. If yes, US-13.13 is just "wire settings into CRD" (already done in US-13.6).
2. **Does the controller read `TTLSecondsAfterSuspended` from the CRD?** If yes, US-13.14 reduces to wiring the setting into `buildWorkspaceCRD`.
3. **Rate limit middleware signature change** — is there a way to inject instanceSettings without changing the function signature? Could use a wrapper struct instead.
