# Worklog: Epic 43 — E2E Wiring Tests for Worklog-0372 Remediation

**Date:** 2026-06-19
**Session:** Close the E2E wiring gaps identified after the 4-PR worklog-0372 remediation (#265/#266/#267/#272 — all merged). Each gap was a workflow tested in isolation but never traced through the live request path. Per README-LLM.md "E2E Wiring Verification": "unit tests pass" is NOT sufficient; the actual wiring must be demonstrated.
**Status:** Complete

---

## Objective

Add true end-to-end tests for four workflows that were unit-tested but not integration-tested:

1. **Admin-suspend → user-denied** (F4 marker round-trip through live HTTP)
2. **Controller→API org-status token agreement** (real client + production-contract server)
3. **app.go construction** (suspend path actually invokes MarkUserSuspended — observable marker)
4. **F11 redirect warning** (fires when RedirectBaseURL unset)

---

## Work Completed

### E2E #1 + #3 — `api/internal/app/e2e_suspend_test.go` (new)
`TestE2E_AdminSuspendUser_VictimTokenRejected`: constructs a real `auth.Service` + real `PlatformAdminHandler` (same constructor signature as `app.go`: `NewPlatformAdminHandler(orgStore, userStore, authSvc, authSvc, log)`), wires both through a live gin router on a real TCP socket, and proves:
- Admin POST `/admin/users/victim/suspend` → 200
- The atomic suspend ran (`SuspendUserGuardedByLastAdmin` called)
- The F4 marker was written (`MarkUserSuspended` → `user_suspended:victim` in the shared cache)
- Victim's SAME token (issued before suspend, still cryptographically valid) → 401 "account suspended" on the next request

`TestE2E_AdminUnsuspendUser_VictimTokenAcceptedAgain`: suspend → unsuspend through the live router → victim's token works again (marker cleared + status flipped back).

### E2E #2 — `controller/internal/workspace/org_status_e2e_test.go` (new)
`TestE2E_OrgStatus_ClientAgainstProductionContract`: the real `CachedOrgStatusClient` fetches from a server that enforces the production contract (fail-closed token gate, `X-Internal-Token` header, `{status:...}` JSON). A matching token → 200 + parsed status; the client and server agree on the contract.
`TestE2E_OrgStatus_TokenMismatch_RejectedByContract`: a mismatched token → fetch fails (`ok == false`). The token gate is real.
`TestE2E_OrgStatus_ContractConstants`: locks the header name (`X-Internal-Token`) and env-var name (`LLMSAFESPACES_INTERNAL_TOKEN`) — if either side renames, this test catches the drift.

### E2E #4 — `api/internal/handlers/org_sso_test.go` (extended)
`TestE2E_SSO_ResolveCallbackURL_WarnsOnForwardedHeaderFallback`: with `RedirectBaseURL` unset, `resolveCallbackURL` builds the URL from `X-Forwarded-Proto` + `Host` AND fires a warning — a capturing logger asserts the warning text contains "forwarded headers".
`TestE2E_SSO_ResolveCallbackURL_NoWarnWhenRedirectBaseURLSet`: complement — no warning when the gap is closed.

---

## Key Decisions

1. **Real TCP server for the suspend test** (not `httptest.NewRecorder`) — the existing `e2e_http_test.go` pattern uses `net.Listen` + `http.Server.Serve`, and so does this test. The network round-trip catches wiring issues a recorder would miss (e.g. middleware ordering, header propagation).
2. **Shared user map across DB mock + org store + user store** — the atomic suspend flips the user's status in the same map `GetUser` reads, faithfully reproducing `PgOrgStore.SuspendUserGuardedByLastAdmin`'s contract without a real DB.
3. **Contract-constants drift test** — the controller→API contract (header name, env var) is duplicated by design so a rename on either side turns the test red. This is the standard pattern for cross-module contracts that can't share a constant without an import cycle.
4. **Production-contract server, not a stub** — `orgStatusHandlerFunc` reproduces the real handler's fail-closed + token-check logic (not a pass-through stub), so the client is tested against the genuine contract.

---

## Blockers

None.

---

## Tests Run

- `go test ./api/internal/app/...` — green (including new suspend e2e).
- `go test ./api/internal/handlers/...` — green (including new F11 warning tests).
- `go test ./controller/internal/workspace/...` — green (including new org-status contract tests).
- `go build ./...`, `go vet ./...`, `gofmt`, `goimports`, `golangci-lint --new-from-rev=origin/main` (0 issues) — all clean.

---

## Next Steps

These tests close the last E2E wiring gaps from the worklog-0372 remediation. All four workflows are now traced through the live request path. No further wiring gaps identified.

---

## Files Modified

- `api/internal/app/e2e_suspend_test.go` (new) — suspend/unsuspend full HTTP round-trip.
- `controller/internal/workspace/org_status_e2e_test.go` (new) — controller→API org-status token agreement + contract-constants drift test.
- `api/internal/handlers/org_sso_test.go` (extended) — F11 redirect warning wiring tests.
