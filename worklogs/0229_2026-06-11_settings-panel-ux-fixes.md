# Worklog 0229 — Settings Panel UX Audit & Fixes

**Date:** 2026-06-11
**Branch:** fix/settings-panel-ux
**Epic:** Cross-cutting (settings, secrets, credentials)

## Summary

Deep-dive audit of all six settings panel tabs. Identified and fixed every broken
or unimplemented behaviour found. All fixes are verified end-to-end against the
backend code and covered by tests.

## Issues Fixed

### 1. Admin guard 404 detection (Breaking — both admin tabs)

`AdminGuard` returns HTTP 404 with an empty body for non-admin users (security
through obscurity). The API client falls back to `message = res.statusText =
"Not Found"`. Both tabs were checking `e.message.includes("404")` or
`e.message.includes("403")` — neither matched, so non-admin users saw a raw
error string rather than "Admin access required."

**Fix:** Both tabs now use `e instanceof ApiClientError && e.status === 404`.

Files: `AdminSettingsPage.tsx`, `AdminProviderCredentialsTab.tsx`, both test files.

### 2. Admin settings save errors silently swallowed (High)

`SettingsForm.tsx` catches errors and re-throws, expecting the parent to show a
toast. `UserSettingsTab` did; `AdminSettingsPage` did not — the catch block was
absent, so any save failure was fully silent.

**Fix:** Added `try/catch` with `toast(message, "error")` to `AdminSettingsPage.handleSave`,
matching the `UserSettingsTab` pattern exactly.

Files: `AdminSettingsPage.tsx`, `SettingsPage.test.tsx`.

### 3. API Keys tab was a non-functional placeholder (High)

The tab was 18 lines of static JSX. The "Create key" button had no `onClick`.
The backend (`POST/GET/DELETE /auth/api-keys`) is fully implemented and has been
since the initial schema.

**Fix:** Created `frontend/src/api/apiKeys.ts` (typed client). Rewrote
`ApiKeysTab.tsx` (347 lines) with: key list, create form, one-time key banner
with copy + warning, delete with confirmation, expand for metadata. Full test
coverage in `ApiKeysTab.test.tsx`.

Files: `frontend/src/api/apiKeys.ts` (new), `ApiKeysTab.tsx`, `ApiKeysTab.test.tsx`.

### 4. `api-key` legacy secret type invisible in UI (Medium)

`SecretTypeAPIKey = "api-key"` is a valid backend type (materialises as
`API_KEY_<NAME>` env var). It was absent from `SECRET_TYPES` so existing
api-key secrets returned by `GET /secrets` were silently dropped by the group
filter and never rendered.

**Fix:** Added as a display-only `legacyOnly: true` entry. Existing secrets are
now visible with a migration banner pointing to LLM Providers / Environment
Variables. Users cannot create new api-key secrets from the UI (the type is
excluded from the create dropdown).

Files: `SecretsTab.tsx`.

### 5. "Organisation" auto-apply option creates silently broken rules (Medium)

The seeding SQL for `org` auto-apply rules matches `target_id = userID` — not
real org membership — as a placeholder until org support is implemented. Admins
could create org-scoped rules that would only fire if `targetId` matched the
creating user's own ID.

**Fix:** Disabled the "Organisation" `<option>` with label "(coming soon)".

Files: `AdminProviderCredentialsTab.tsx`.

### 6. Backend mount_path validation test coverage (Low)

Added explicit test cases to `secret_service_test.go` verifying that absolute
paths (including valid-looking ones like `/home/sandbox/.secrets/cert.pem`)
are rejected at the API layer. The fix to the frontend submission (removing the
absolute-path prefix from `handleSubmit`) was already present; this adds the
regression tests.

Files: `pkg/secrets/secret_service_test.go`.

## Verified Non-Issues

- **compactMode CSS side effect** — 7 functional CSS overrides exist in
  `frontend/src/styles/index.css:98-108`. Not a no-op.
- **wsOwnerCheck wiring** — wired in `app.go:184`. Nil behaviour is fail-open
  (security concern in tests only; production always wires it).
- **autoApplyStore wiring** — wired in `app.go:182`. Nil returns 503. Tested.
- **SettingsHandler nil** — always constructed in production (`app.go:108`).
  Nil guard in router is for test harnesses only.
- **DB-down silent defaults** — intentional graceful degradation in
  `GetAdminSettings`. Documented but not changed.

## Test Results

- Frontend: **897 tests across 94 files — all passing**
- Backend (`pkg/secrets/...`, `api/...`): **all packages passing**
