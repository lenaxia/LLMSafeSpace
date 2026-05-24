# Worklog: E2E Auth Tests Fix and Run

**Date:** 2026-05-24
**Session:** Run the 5 skipped Playwright E2E tests requiring authentication, fix failures
**Status:** Complete

---

## Objective

Run the 5 previously-skipped Playwright E2E tests in `frontend/tests/e2e/chat.spec.ts` against the live k8s cluster, fixing any issues blocking them.

---

## Work Completed

### Infrastructure setup
- Pulled latest from main (`7a6ebc1` → `7b72d28` via two pulls)
- API accessed via `kubectl port-forward svc/llmsafespace-api 18080:8080 --namespace default` (port 8080 occupied by system `webproc` process on pid 602930, unkillable)
- Downloaded Node.js 22.16.0 to `/tmp/node-v22.16.0-linux-x64/` — system Node 18.19.1 is incompatible with Vite 8 (requires Node 20.19+)
- Reinstalled `node_modules` and Playwright chromium under Node 22
- Created test user via `POST /api/v1/auth/register`: username `e2e-test`, email `e2e@test.local`, password `TestPass123!`
- Updated `frontend/vite.config.ts` proxy target from `localhost:8080` to `localhost:18080`

### Bug 1: Login request field mismatch
- **Root cause:** Frontend `LoginRequest` type sent `{ username, password }` but Go API `LoginRequest` struct requires `{ email, password }` with `binding:"required,email"`. Login silently failed, `waitForURL(/\/chat/)` timed out and was swallowed by `.catch(() => {})`, so tests proceeded unauthenticated.
- **Fix:** Changed `LoginRequest.username` → `email` in `frontend/src/api/types.ts:25`. Updated `AuthProvider.login` to map `{ email: username, password }` in `frontend/src/providers/AuthProvider.tsx:25`.
- **Note:** The updated test file from git pull (0038) already improved `loginAs()` to throw on login failure instead of silently swallowing, which made diagnosing this much easier.

### Bug 2: Playwright strict mode violation in settings test
- **Root cause:** `getByText("API Keys")` matched 4 elements (tab button, section heading, description text, empty-state text). Playwright's strict mode rejects locators matching multiple elements.
- **Fix:** Changed to `getByRole("button", { name: "API Keys" })` and `getByRole("button", { name: "Appearance" })` in `frontend/tests/e2e/chat.spec.ts:41-42`.

### Test results
- **15/15 Playwright E2E tests pass** (5 authenticated + 10 unauthenticated)
- **237/237 vitest unit tests pass** (verified LoginRequest type change doesn't break existing tests)

### Assumptions stated and validated
1. API is reachable from the dev machine — validated via `curl http://localhost:18080/api/v1/auth/config`
2. Test user can be created — validated, registration returned 201
3. API login requires `email` not `username` — validated via curl: `{"username":"e2e-test"}` → 400, `{"email":"e2e@test.local"}` → 200
4. Unit tests unaffected by LoginRequest type change — validated, 237/237 pass

---

## Key Decisions

- Mapped `email` in AuthProvider rather than changing the form UI. The form placeholder still says "Username" — this is acceptable because the E2E test now passes the email address as the username value. A future UX improvement could change the placeholder to "Email or username" and add backend support for username-based login.
- Did not attempt to kill the `webproc` process on port 8080 — it's a system process. Used port 18080 instead.

---

## Blockers

None.

---

## Tests Run

```
export PATH="/tmp/node-v22.16.0-linux-x64/bin:$PATH"
npx vitest run                                                          # 237 passed
E2E_USERNAME='e2e@test.local' E2E_PASSWORD='TestPass123!' npx playwright test --reporter=list  # 15 passed
```

---

## Next Steps

- Consider reverting `vite.config.ts` proxy target change before committing (it's environment-specific)
- Consider adding backend support for username-or-email login to match the UI's "Username" placeholder
- The `vite.config.ts` change should not be committed to main as port 18080 is specific to this environment

---

## Files Modified

- `frontend/src/api/types.ts` — `LoginRequest.username` → `email`
- `frontend/src/providers/AuthProvider.tsx` — login sends `{ email: username, password }`
- `frontend/tests/e2e/chat.spec.ts` — `getByText` → `getByRole("button", ...)` for settings tabs
- `frontend/vite.config.ts` — proxy target `localhost:8080` → `localhost:18080` (environment-specific, consider reverting)
