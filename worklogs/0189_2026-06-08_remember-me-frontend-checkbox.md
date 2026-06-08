# Worklog 0189 — Frontend: Remember-Me Checkbox on Login Form

**Date:** 2026-06-08
**PR:** #67 (squash-merged into main, revision 180 deployed)
**Refs:** Epic 34 US-34.1 / PR #68 (backend)

---

## Summary

The backend `rememberMe` flag (Epic 34, PR #68) was implemented and deployed but
the login page had no checkbox — the flag was always `false`, meaning all sessions
were standard 24-hour regardless of user preference. This worklog covers wiring
the UI.

---

## Problem

`POST /auth/login` accepted `{"rememberMe": true}` but `AuthProvider.login` never
passed it — the payload was always `{ email, password }`. Users had no way to opt
into a 30-day session from the UI.

---

## Changes

### `frontend/src/api/types.ts`
Added `rememberMe?: boolean` to `LoginRequest`. Optional with no default — omitting
it is equivalent to `false`, preserving wire compatibility with existing clients.

### `frontend/src/components/auth/LoginForm.tsx`
- Added `rememberMe: boolean` state (default `false`)
- Added checkbox: `<input type="checkbox">` with Tailwind `accent-primary` styling,
  label "Remember me for 30 days"
- `onSubmit` prop signature changed from `(username, password)` to
  `(username, password, rememberMe)` — third arg is always passed (not optional),
  keeping the call contract explicit
- No new dependencies — native `<input type="checkbox">` styled with Tailwind

### `frontend/src/providers/AuthProvider.tsx`
- `AuthContextValue.login` signature: `(username, password, rememberMe?)` 
- `login` callback forwards `rememberMe` to `authApi.login({ email, password, rememberMe })`

### `frontend/src/pages/LoginPage.tsx`
- `<LoginForm onSubmit={login} />` replaced with explicit lambda
  `(username, password, rememberMe) => login(username, password, rememberMe)`
  to make the three-arg threading explicit and visible

### `frontend/src/components/auth/LoginForm.test.tsx`
- Updated `TestCallsOnSubmitWithUsernameAndPassword` to assert third arg `false`
- Added `TestCallsOnSubmitWithRememberMeTrue` — checks checkbox, verifies third arg `true`
- Added `TestRenderRememberMeCheckboxUncheckedByDefault` — checkbox exists and is unchecked

---

## Behaviour

| User action | JWT TTL | Cookie Max-Age |
|---|---|---|
| Login without checking box (default) | 24h | 86400s |
| Login with box checked | 30 days | 2592000s |

---

## Test results

694 frontend tests pass (3 new, 1 updated). TypeScript clean (`tsc --noEmit`).
All CI checks green on PR #67 including `Frontend (unit + typecheck + e2e)`.

---

## Deploy

Helm revision 180, image `ts-1780960366`. API `/readyz` healthy post-deploy.
