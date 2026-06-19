# Worklog: Epic 49 — US-49.2 Tier-1 Helm-Precedence + US-49.3 Admin UX Read-Only

**Date:** 2026-06-19
**Session:** Implement the helm-precedence settings model (Tier-1: settings immutable when declared via helm) and the frontend read-only rendering. Follow-up to PR #285.
**Status:** In Progress (PR #289 open for review)

---

## Objective

Build on the email foundation (PR #285) by adding the Tier-1 helm-precedence model: when email config is declared in helm, the email.* instance settings must be read-only in the admin UX (disabled with a "Managed by Helm" badge). When not declared in helm, they remain admin-mutable. This is the operator-config-vs-runtime-config separation.

---

## Work Completed

### US-49.2 — Tier-1 helm-precedence settings model (backend)

- **Schema change**: Added `ReadOnly bool` to `SettingDef` (`schema.go`) with JSON tag so the frontend receives it. Added 4 `email.*` instance settings (provider/sesRegion/fromAddress/baseUrl).
- **ErrReadOnly sentinel**: `Set()` returns it for helm-managed keys before any DB access. Settings handler maps it to 409 Conflict.
- **SetHelmOverrides()**: New method on `InstanceService`. Called once at boot from `app.go` when the email config block is present. Pins values and marks keys as read-only. Protected by `s.mu` for race-free concurrent reads.
- **Precedence chain**: helm override > DB value > schema default. Implemented in both `GetAll()` and `get()` (the typed-getter path).
- **Schema()**: Returns defs with `ReadOnly=true` for helm-managed keys, computed fresh each call (does not mutate the shared index).
- **SchemaVersion bumped** to 5 (new field + 4 new keys).
- **app.go wiring**: When `cfg.Email.Provider != "" || cfg.Email.FromAddress != "" || cfg.Email.BaseURL != ""`, calls `SetHelmOverrides` with the email config values.

### US-49.3 — Admin UX read-only rendering (frontend)

- **SettingDef type**: Added `readOnly?: boolean` to the frontend TypeScript interface.
- **SettingsForm.tsx**: When `def.readOnly === true`, shows a "Managed by Helm" badge next to the label, disables the control (`disabled={... || isReadOnly}`), and guards `handleChange` with an early return.
- **4 frontend tests**: badge shown for readOnly, control disabled, onSave not called with attempted interaction, badge absent for non-readOnly.

### Design deviation: email.provider as TypeString not TypeEnum

The original design (US-49.2) specified `email.provider` as `TypeEnum` with values `["", "ses"]`. Radix UI's `Select.Item` rejects empty-string values (reserved for "clear selection"). Changed to `TypeString` — a text input is correct: the value is "ses" or empty; when helm-managed it's disabled regardless.

---

## Key Decisions

1. **SetHelmOverrides called once at boot** (not hot-reloadable). The email provider is constructed once at boot (US-49.1); changing it requires a restart. The read-only model is consistent with this.

2. **Trigger condition** (`Provider != "" || FromAddress != "" || BaseURL != ""`): any non-empty email config value signals helm-managed. This is more robust than checking `email.enabled` alone (which doesn't exist in the Config struct — it's a helm template condition, not a runtime config key).

3. **Locking**: `helmOverrides` is always accessed under `s.mu` (RLock for reads, Lock for writes). The `index` map remains lock-free (immutable after construction). Both patterns coexist correctly.

---

## Tests Run

- `go test -race ./pkg/settings/...` — PASS (6 helm-precedence tests + existing settings tests)
- `go test -race -run "TestIntegration_HelmManaged" ./api/internal/handlers/...` — PASS (2 handler integration tests)
- `npx vitest run src/components/settings/SettingsForm.test.tsx` — PASS (30 tests, 4 new)
- `go build ./...` — clean

---

## Blockers

None.

---

## Next Steps

1. Get PR #289 through review + merge.
2. US-49.5 — Password reset via email (email_tokens migration + RevokeAllUserSessions + handler + interstitial frontend).
3. US-49.6 — Email verification on signup (**blocked on gate-scope decision**).
4. US-49.8 — E2E + integration tests across all flows.

---

## Files Modified

- `pkg/settings/schema.go` — ReadOnly field, email.* settings, SchemaVersion=5
- `pkg/settings/instance_service.go` — SetHelmOverrides, ErrReadOnly, helm-precedence in GetAll/get/Set/Schema
- `pkg/settings/helm_precedence_test.go` — NEW: 6 tests
- `api/internal/handlers/settings.go` — 409 for ErrReadOnly
- `api/internal/handlers/settings_integration_test.go` — 2 integration tests (409 + schema readOnly)
- `api/internal/app/app.go` — wire SetHelmOverrides when email config present
- `frontend/src/api/settings.ts` — readOnly on SettingDef
- `frontend/src/components/settings/SettingsForm.tsx` — read-only badge + disabled control
- `frontend/src/components/settings/SettingsForm.test.tsx` — 4 readOnly rendering tests
