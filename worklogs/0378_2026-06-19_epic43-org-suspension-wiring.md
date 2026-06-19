# Worklog: Epic 43 — Org-Suspension Wiring + Fail-Closed Internal Endpoint (F1/F5)

**Date:** 2026-06-19
**Session:** Fix worklog 0372 findings F1 + F5. Part of a 4-PR remediation split by subsystem; this PR owns the Helm chart wiring + the internal org-status endpoint. Each finding was independently re-verified against source (Rule 11 Phase 2) before fixing.
**Status:** Complete

---

## Objective

- **F1 (CRITICAL)** — D20 org-level workspace suspension was dead code in every Helm deployment: the chart never passed `--api-service-url`, so `OrgStatusClient` was always nil and `applyOrgSuspension` short-circuited.
- **F5 (HIGH)** — the internal org-status endpoint (`GET /api/v1/internal/orgs/:orgID/status`) was fully unauthenticated by default (the shared-secret gate was opt-in and the chart left it unset); the doc comment falsely claimed a NetworkPolicy was the primary boundary.

---

## Work Completed

### F1 — Wire org-suspension in the chart
- `values.yaml`: `controller.apiServiceURL` (default `""` → chart derives the in-cluster URL) + top-level `internalToken` (default `""` → auto-generated).
- `controller-deployment.yaml`: derive `--api-service-url` from release name + namespace + API port when unset; always wire `LLMSAFESPACE_INTERNAL_TOKEN` env.
- `api-deployment.yaml`: wire `LLMSAFESPACE_INTERNAL_TOKEN` env.
- `secret.yaml`: generate + persist an `internal-token` key (same rotation model as jwt/master secrets).

### F5 — Fail-closed internal endpoint
- `internal_org_status.go`: invert the default — **403** when `LLMSAFESPACE_INTERNAL_TOKEN` is unset; `crypto/subtle.ConstantTimeCompare` for the comparison (was `!=`, a timing leak).
- `api-network-policy.yaml` (new): opt-in default-deny ingress for the API pod (gated on `networkPolicy.apiIngressRestricted`, default **false** — a default-on API default-deny would lock users out given deployment-specific ingress selectors; the token gate is the load-bearing control, the NetworkPolicy is defense-in-depth).
- Corrected now-stale comments in `controller/main.go` and `router.go` (2 spots) that described the pre-F5 "reachable when unset" / "NetworkPolicy is co-primary" behaviour.

### F1 + F5 are co-dependent
F5's fail-closed requires F1 to wire the token on BOTH the API (so the endpoint admits) and the controller (so the poll authenticates). One mounted Secret configures both sides.

---

## Key Decisions

1. **Token gate is mandatory fail-closed; NetworkPolicy is opt-in defense-in-depth.** Validated the controller sends `X-Internal-Token` when non-empty (`org_status_client.go:137`) and reads it from `LLMSAFESPACE_INTERNAL_TOKEN` (`controller/main.go:104`); both deployments now mount the same Secret key. A default-on API NetworkPolicy would lock users out (ingress selectors are deployment-specific), so it ships off.
2. **`internalToken` reuses the jwt/master-secret rotation model** — generate on first install, reuse across upgrades (rotation would briefly break org-suspension polling until both pods restart).
3. **`controller.apiServiceURL` defaults to in-cluster derivation** so D20 is functional out-of-the-box; operators can override.

---

## Blockers

None.

---

## Tests Run

- Handler: `TestInternalOrgStatus_TokenUnsetFailsClosed` (was `...AllowsAccess`), `...TokenRequiredWhenSet`, suspended/active/missing-org fail-open.
- Chart (`helm template`): `TestF1_ControllerArgs_PassesApiServiceURL`, `...ApiServiceURL_HonorsOverride`, `...InternalTokenEnv_OnBothDeployments`, `...SecretIncludesInternalToken`, `TestF5_ApiNetworkPolicy_DefaultOff`, `...OptIn`.
- Renders `--api-service-url=http://<release>-api.<ns>.svc:8080`, `LLMSAFESPACE_INTERNAL_TOKEN` on both deployments, `internal-token` in the Secret.

---

## Next Steps

Stale-comment cleanup in `controller/main.go` + `router.go` landed here because they describe the behaviour this PR changes. Companion PRs: SSO (#265), auth (#267); the dependent db/handler PR (F6/F7) follows.

---

## Files Modified

- `api/internal/handlers/internal_org_status.go`, `internal_org_status_test.go` — F5 fail-closed + constant-time compare.
- `charts/llmsafespace/values.yaml`, `templates/{controller,api}-deployment.yaml`, `templates/secret.yaml` — F1 wiring + F5 token.
- `charts/llmsafespace/templates/api-network-policy.yaml` (new) — F5 defense-in-depth.
- `charts/llmsafespace/chart_test.go` — F1/F5 chart tests.
- `controller/main.go`, `api/internal/server/router.go` — stale-comment corrections (Rule 5).
- `pkg/types/auth.go` — pre-existing gofmt drift fix per Rule 5.
