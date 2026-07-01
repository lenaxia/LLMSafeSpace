# Worklog: Remove stale WireGuard endpoint field from relay setup wizard (#464)

**Date:** 2026-06-30
**Session:** Fix issue #464 — the relay setup wizard still rendered a required "WireGuard Endpoint" input after WireGuard was removed from the router↔relay path in worklog 0447.
**Status:** Complete
**Branch:** `fix/464-relay-wizard-stale-wireguard-field`
**Issue:** [#464](https://github.com/lenaxia/LLMSafeSpaces/issues/464)

---

## Objective

Remove the dead "WireGuard Endpoint" UI from the relay setup wizard Deploy step, along with the dead `routerEndpoint`/`wireGuardPort`/`wireGuardEndpoint` surface it depended on, so the frontend contract matches the backend (which dropped WireGuard in worklog 0447).

---

## Root cause

Worklog 0447 removed WireGuard from the router↔relay path and fixed the **backend** (`relay_admin.go`): `deployRequest` no longer has `RouterEndpoint`, `fillWireGuardEndpoint` became a no-op, and the `InferenceRelay` CR is built from providers + upstream only. The **frontend** was never updated to match. `RelaySetupWizard.tsx` kept rendering a required WG endpoint input, gated the Deploy button on `!deployConfig.routerEndpoint`, and sent `routerEndpoint` in the POST body — which the backend silently discards. The result: dead UI that blocks deploy until the admin types an arbitrary placeholder into a field whose value is thrown away.

The deeper root cause is **type drift**: `frontend/src/api/relay.ts` had drifted from the backend contracts (`RelaySetup` carried a `wireGuardEndpoint` the backend never returns; `DeployRequest` carried `routerEndpoint`/`wireGuardPort` the backend never reads).

---

## Assumptions stated and validated (per Rule 7)

1. **The backend deploy handler does not consume `routerEndpoint`.** Validated: `api/internal/handlers/relay_admin.go:422-425` — `deployRequest` is `{ UpstreamURL string; Providers []string }`. No WG field. `Deploy` (relay_admin.go:429) builds the CR from `req.UpstreamURL` + `req.Providers` only.
2. **The backend `setupResponse` does not return `wireGuardEndpoint`.** Validated: `relay_admin.go:58-65` — fields are `Deployed, RouterDeployed, CRDInstalled, AWS/OCI/GCPConfigured`. No WG field. So `RelaySetup.wireGuardEndpoint` in the frontend was always `undefined`.
3. **The backend deliberately tolerates a stale `routerEndpoint` for rollout compatibility.** Validated: `relay_admin_test.go:676` `TestRelayDeploy_IgnoresRouterEndpointIfExists` asserts a client sending `routerEndpoint:"legacy-gw:51820"` still gets 200. Worklog 0447 explicitly frames this as a rollout-window safety net.
4. **`upstreamURL` is optional and backend-defaulted.** Validated: `relay_admin.go:438-440` defaults empty `UpstreamURL` to `https://opencode.ai/zen/v1`. The wizard never collected it; omitting it is correct.
5. **No other frontend code reads the removed fields.** Validated by grep: `wireGuardEndpoint` was used only in `relay.ts` (type), the wizard test mock, and the e2e fixture. `routerEndpoint` only in the wizard + its tests. `RelayInstance.wgIP` (status dashboard, `RelayStatusDashboard.tsx:201`) is a *different* field and remains live — left untouched (out of scope).

---

## Work Completed

### Analysis — is the fix at the right level?

Yes — issue #464 is a "remove dead code / repair type drift" problem, and the correct fix is the simplest one that eliminates the dead surface completely (Rule 4: not over-engineered; Rule 5: zero tech debt). The issue's proposal was sound but **incomplete**: it targeted `routerEndpoint` and missed two equally-dead type members (`DeployRequest.wireGuardPort`, `RelaySetup.wireGuardEndpoint`). The complete fix removes all three so the frontend types mirror the backend contracts exactly.

On the issue's *optional* backend item #5 (tighten the deploy handler to strict-JSON-reject `routerEndpoint`): **deliberately NOT implemented.** Rationale:
- The backend already ignores the field harmlessly; there is exactly one client (this frontend), fixed in the same change.
- Strict rejection would break the deliberate backwards-compat test `TestRelayDeploy_IgnoresRouterEndpointIfExists` and the rollout-window contract it protects (cached browser tabs / in-flight deploys during the actual production rollout).
- Rule 12 (containment, no premature tightening) + the right-sized-complexity rubric: no second consumer, no recurring pain, no forcing event. The issue author's own guidance was "Defer if invasive."

### Changes

- **`frontend/src/api/relay.ts`** — removed `wireGuardEndpoint` from `RelaySetup` and `routerEndpoint`/`wireGuardPort` from `DeployRequest`. Both interfaces now match the backend (`setupResponse`, `deployRequest`) field-for-field.
- **`frontend/src/components/settings/RelaySetupWizard.tsx`** — removed the `deployConfig` state (held only the dead `routerEndpoint`); removed the "WireGuard endpoint is required" guard in `handleDeploy`; removed `routerEndpoint` from the deploy POST body; removed the WG endpoint label/helper/input JSX; changed the Deploy button disabled guard from `deploying || !deployConfig.routerEndpoint` to `deploying` (the Deploy section is already conditionally rendered only when ≥1 provider is configured, and `handleDeploy` keeps a defense-in-depth empty-providers guard).
- **`frontend/src/api/relay.test.ts`** — dropped `routerEndpoint` from the `deploy` test fixtures.
- **`frontend/src/components/settings/RelaySetupWizard.test.tsx`** — dropped `wireGuardEndpoint` from `mockSetup`; reworked the "shows configured providers after save and allows deploy" test to no longer type into the removed field and to assert `providers: ["aws"]` only; added a dedicated regression test ("does not render a WireGuard endpoint field and deploys without one") asserting the WG field is absent, the button is enabled without an endpoint, and the deploy call carries no `routerEndpoint`/`wireGuardPort` property.
- **`frontend/tests/e2e/relay-admin.spec.ts`** — dropped `wireGuardEndpoint` from `mockSetupNotDeployed`.

---

## Key Decisions

1. **Frontend-only fix; do not touch the backend.** The backend was already correct post-0447. Changing it would add risk and break a deliberate compat contract for no benefit.
2. **Remove all three dead type members, not just `routerEndpoint`.** Faithful, zero-tech-debt implementation; the frontend now mirrors the backend exactly (single source of truth).
3. **Keep `RelayInstance.wgIP` untouched.** It is a WG-era naming relic but is *live* (backend populates it, `RelayStatusDashboard.tsx:201` renders it). Renaming it is a separate refactor, out of scope for this issue. Flagged here for a future cleanup.
4. **No strict-JSON backend tightening.** See analysis above.

---

## Blockers

None for the code change. The PR-push/merge steps are blocked on GitHub credentials (no `gh` auth / `GH_TOKEN` / git credential helper in this environment) and on asynchronous reviewer feedback — surfaced to the operator before commit.

---

## Tests Run

From `frontend/`:
- `npm run typecheck` (tsc --noEmit) — **PASS** (exit 0)
- `npm run lint` (eslint .) — **PASS** (exit 0)
- `npx vitest run src/api/relay.test.ts src/components/settings/RelaySetupWizard.test.tsx` — **PASS** (23 tests)
- `npx vitest run` (full suite) — **PASS** (1249 tests across 115 files)

Backend unchanged; Go tests not re-run (no backend files modified).

---

## Adversarial self-review (Rule 11) — summary

- **Gap: did we remove every dead reference?** Verified by grep — the only remaining `routerEndpoint`/`wireGuardPort` occurrences in `frontend/` are the intentional regression-test assertions in `RelaySetupWizard.test.tsx`. Real, addressed.
- **Weakness: is the Deploy button ever wrongly enabled/disabled?** The Deploy section renders only when `configuredProviders.length > 0`; `handleDeploy` keeps an empty-providers guard. Button is `disabled={deploying}` only. No dead-end state. False alarm — acceptable.
- **Assumption check: backend truly ignores `routerEndpoint`?** Re-validated against `relay_admin.go:422-485`. Confirmed.
- **Did removing `RelaySetup.wireGuardEndpoint` break any reader?** Grep confirmed no consumer read it. Confirmed.
- **Scope creep risk (strict-JSON / `wgIP` rename)?** Explicitly deferred with rationale. Confirmed not in scope.

Zero real findings remain.

---

## Next Steps

- Push `fix/464-relay-wizard-stale-wireguard-field`, open a PR titled `fix(relay-wizard): remove stale WireGuard endpoint field (#464)`, and link/closes #464.
- Iterate on the automated AI reviewer's findings until APPROVE, then squash-merge.
- (Future, separate scope) Consider renaming `RelayInstance.wgIP` → a neutral name (e.g. `vmIP`) across backend `instanceStatus`, the OpenAPI spec, and the dashboard — it is a naming relic, not a bug.

---

## Files Modified

- `frontend/src/api/relay.ts`
- `frontend/src/api/relay.test.ts`
- `frontend/src/components/settings/RelaySetupWizard.tsx`
- `frontend/src/components/settings/RelaySetupWizard.test.tsx`
- `frontend/tests/e2e/relay-admin.spec.ts`
- `worklogs/0585_2026-06-30_relay-wizard-remove-stale-wireguard-field.md` (this file)
