# Worklog: Epic 43 — User-Suspend Atomicity + active/status Sync (F6/F7)

**Date:** 2026-06-19
**Session:** Fix worklog 0372 findings F6 + F7 (the database + handler layer of user-suspension). Final PR of a 4-PR remediation split by subsystem; depends on #267 (auth), which added the `MarkUserSuspended`/`ClearUserSuspended` primitives this PR wires into the suspend handler. Each finding was independently re-verified against source (Rule 11 Phase 2) before fixing.
**Status:** Complete

---

## Objective

- **F6 (MEDIUM, latent)** — `users.active` and `users.status` represented one concept (suspended-or-not) across two columns that could drift; `SetUserStatus` updated only `status`, deliberately leaving `active` untouched.
- **F7 (MEDIUM)** — the last-admin deadlock check (`OrgsWhereUserIsLastActiveAdmin` SELECT) and the status update (`SetUserStatus` UPDATE) ran as separate statements with no transaction — a TOCTOU where two concurrent admin suspensions could both pass the check and orphan an org. The safe `SELECT … FOR UPDATE` pattern already existed in the same file but wasn't reused.

---

## Work Completed

### F6 — Mirror `active` from `status`
`SetUserStatus` (`database.go`) now updates `active = (status == 'active')` in the same statement, so the two columns cannot drift. The auth middleware authorizes on `status`; Login historically checks `active`; keeping them in lockstep removes the divergence vector.

### F7 — Atomic guarded suspend
Added `PgOrgStore.SuspendUserGuardedByLastAdmin(ctx, userID, force)` — one transaction that `SELECT … FOR UPDATE`s the admin rows of every org the user administers, re-runs the last-admin check inside the tx, then `UPDATE users SET status='suspended', active=false`. Refactored `OrgsWhereUserIsLastActiveAdmin` to share its SQL via a `queryer`-based helper (`lastActiveAdminOrgsQuery`/`scanLastActiveAdminOrgs`) — no duplication. The now-unused standalone read was removed from the handler's `platformAdminOrgStore` interface (Rule 5: no dead code).

### Handler wiring (F4 + F7)
`SuspendUser` now calls the atomic `SuspendUserGuardedByLastAdmin` and writes the F4 revocation marker (best-effort) via the new `platformUserRevoker` interface (`MarkUserSuspended`/`ClearUserSuspended`, satisfied by `*auth.Service` from #267). `UnsuspendUser` clears the marker. The revocation write is best-effort: a Redis blip logs a warning but does not roll back the admin action (the user is already suspended in the DB; the per-request `GetUser` gate from #267 enforces regardless).

---

## Key Decisions

1. **F7 reuses the existing `SELECT … FOR UPDATE` locking discipline** from `RemoveOrgAdminIfNotLast`/`DemoteOrgAdminIfNotLast`, generalized to the multi-org case (lock ALL admin rows of every org the user administers). Validated `PgOrgStore` and `database.Service` share the same `*sql.DB` (`app.go` `NewPgOrgStore(dbSvc.DB)`), so a cross-table transaction is sound.
2. **`force=true` skips the lock + check entirely** (straight to the UPDATE) — the documented D19 security-emergency escape hatch.
3. **Revocation write is best-effort, status flip is authoritative** — the marker (from #267) is resilience + precise labelling; `GetUser` is the load-bearing gate. A Redis blip during suspend must not surface a misleading 500.
4. **Removed `OrgsWhereUserIsLastActiveAdmin` from the handler interface** — after F7 the handler only uses the atomic method; keeping the standalone read in the interface was dead surface area.

---

## Blockers

None.

---

## Tests Run

- `go test ./api/internal/services/database/... ./api/internal/handlers/... ./api/internal/server/...` — green.
- `pg_org_store_test.go`: `SuspendUserGuardedByLastAdmin_NotLast_Suspends`, `..._LastAdmin_Refuses` (409 + no UPDATE), `..._Force_SkipsCheck`, `SetUserStatus_MirrorsActive_F6`.
- `platform_admin_test.go`: suspend happy (asserts atomic path + revocation), last-admin blocked (no revocation), force override, guarded-store error 500, revoker best-effort (Redis blip → warn not fail), nil revoker tolerated, unsuspend clears marker.

---

## Next Steps

Final PR of the 4-PR worklog-0372 remediation (#265 SSO, #266 chart+endpoint, #267 auth — all merged). With this, all 10 code/config findings (F1, F3–F11) are on main; F2/F12/F13 were process/documentation. Long-term F6 follow-up: drop the legacy `active` column entirely once all readers migrate to `status`.

---

## Files Modified

- `api/internal/services/database/database.go` — F6 `SetUserStatus` mirrors `active`.
- `api/internal/services/database/database_test.go` — updated `TestSetUserStatus` for the F6 query shape.
- `api/internal/services/database/pg_org_store.go` — F7 `SuspendUserGuardedByLastAdmin` + shared `lastActiveAdminOrgsQuery`/`scanLastActiveAdminOrgs` queryer helper.
- `api/internal/services/database/pg_org_store_test.go` — F6/F7 atomicity tests.
- `api/internal/handlers/platform_admin.go` — F4 revoker wiring + F7 atomic suspend path; removed dead `OrgsWhereUserIsLastActiveAdmin` from the interface.
- `api/internal/handlers/platform_admin_test.go` — F4/F7 handler tests + mock revoker.
- `api/internal/handlers/platform_admin_list_test.go`, `api/internal/server/router_admin_platform_list_test.go` — constructor-signature + interface updates.
- `api/internal/app/app.go` — wire the revoker (`svc.GetAuth()`) into `NewPlatformAdminHandler`.
