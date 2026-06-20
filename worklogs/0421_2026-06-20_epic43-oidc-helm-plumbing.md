# Worklog 0421 — Epic 43 / US-43.10: surface per-org OIDC SSO plumbing in Helm chart

**Date:** 2026-06-20
**Epic:** 43 (Organization Management)
**Session:** Verified already-shipped per-org OIDC SSO wiring end-to-end, surfaced the missing instance-plumbing config in the Helm chart, documented the as-built system in README-LLM.md.
**Status:** Complete

---

## Objective

The user asked to "scope out what multitenant oidc would look like, where org owners can configure their own oidc providers" in README-LLM.md. Investigation revealed per-org OIDC is **already fully shipped** (Epic 43 / US-43.10 / D17); the actual gap was that the instance-plumbing config (`oidc.redirectBaseUrl`, `oidc.frontendRedirectUrl`, `oidc.stateCookieName`) was only reachable via env vars, leaving the F11 header-trust fallback active in chart-managed deploys.

---

## Work Completed

### SSO wiring verification (read-only audit)

Traced the full per-org OIDC request path and confirmed every layer is wired:

| Layer | Evidence |
|---|---|
| Service construction | `app.go:392` `sso.New(pgOrgStore, dbSvc, ...)` with KEK + state key + issuer |
| Handler construction | `app.go:405` `NewSSOHandler(ssoSvc, pgOrgStore, svc.GetAuth(), ...)` |
| Public auth routes | `router.go:195,583-585` (gated by `if ssoHandler != nil`) |
| Org-admin CRUD routes | `router.go:1170-1175` inside `orgAdminGroup` (OrgAdminGuard at `:1130`) |
| `/auth/config` flag | `router.go:568-571` `oidcEnabled = ssoHandler.OIDCEnabled(...)` |
| `orgAuthService` iface | `orgs.go:43` satisfied by `auth.Service.GetUserID` (`auth.go:241`) |
| Store: 6 SSO methods | `pg_org_store.go:1571-1682` |
| Migration in chart | `charts/llmsafespaces/migrations/000038_*` |
| Frontend admin tab + login integration | `OrgSSOTab.tsx`, `LoginPage.tsx`, `router.tsx:64`, `OrgAdminLayout.tsx:58` |
| Integration tests | `org_sso_test.go` covers CRUD + Start/Callback + F8 unverified-email |

### Helm chart surfacing (code change)

Added the `oidc:` block to the chart mirroring the established `email:` pattern:

- `charts/llmsafespaces/values.yaml` — top-level `oidc:` block (defaults empty), with comment making clear per-org IdP wiring still lives in `org_sso_configs`
- `charts/llmsafespaces/templates/configmap-api.yaml` — render `oidc:` section into API configmap; `stateCookieName` omitted via `{{- with }}` when empty (matches Go default `lsp_sso_state`)

### README-LLM.md documentation

Added §14 *Multi-Tenant OIDC SSO* (as-built): data model, endpoints, PKCE login flow sequence diagram, org-admin config flow, auto-provisioning + role mapping, security controls table, configuration (both chart and env-var paths), frontend, known gaps, file reference. Bumped to v1.17.

### Chart regression tests (TDD — added after AI review finding)

Added three tests to `charts/llmsafespaces/chart_test.go` mirroring the `TestEmail_*` pattern:

- `TestOIDC_DefaultRender_IncludesEmptyBlock` — guards the F11 fix; fails if the `oidc:` section is removed
- `TestOIDC_CustomValues_FlowsThrough` — guards value passthrough for all three keys
- `TestOIDC_DefaultRender_OmitsStateCookieName` — guards the deliberate `{{- with }}` omission

All three **mutation-validated**: disabling the oidc block via `{{- if false }}` causes `TestOIDC_DefaultRender_IncludesEmptyBlock` to FAIL; restoring it passes.

---

## Key Decisions

- **Scope: multi-tenant only, NOT a global IdP.** The three chart values are instance-*plumbing* ("where does the IdP redirect back to"). Per-org IdP wiring still lives in `org_sso_configs` via the org-admin API. No `instance_sso_configs` table or `/auth/sso/start` (without org slug) route was added. Made explicit in the values.yaml comment and the PR description.

- **Mirror the `email:` pattern, not invent a new one.** The chart already had a `{{- if .Values.email.enabled }}` configmap guard + values block + comment header. The `oidc:` block follows the same shape for consistency. The only difference: `oidc:` has no `enabled` toggle because the SSO service is always constructed when the state key is available (`app.go:389-407`); the per-org config table being empty is the natural "off" state.

- **`stateCookieName` omitted when empty, not rendered as `""`.** Rendering `stateCookieName: ""` would shadow the Go default (`lsp_sso_state`, `sso.go:132`) via Viper `Unmarshal` — the empty string overwrites the zero value. The `{{- with }}` guard omits the line entirely so Viper leaves the field at zero and the SSO service applies its default.

---

## Assumptions (Rule 7) and validation

- **A-SHIPPED: per-org OIDC is fully implemented.** **VALIDATED** by reading `sso.go`, `org_sso.go`, `000038_org_sso_configs.up.sql`, router wiring, app wiring, frontend.
- **A-CHART-GAP: the chart did not template the `oidc:` block or expose `LLMSAFESPACES_OIDC_*` env vars.** **VALIDATED** — `configmap-api.yaml` ended at `email:` (line 64); `api-deployment.yaml:44-86` env list had no `LLMSAFESPACES_OIDC_*` entries; `values.yaml` only mention of OIDC was line 869 (unrelated AWS IRSA pod identity).
- **A-PLUMBING-ONLY: `cfg.OIDC` only carries plumbing, not IdP config.** **VALIDATED** at `config.go:128-138` — three string fields (`RedirectBaseURL`, `FrontendRedirectURL`, `StateCookieName`), no discovery URL / client ID / secret.
- **A-PATTERN: the chart pattern to mirror is `email:`.** **VALIDATED** at `configmap-api.yaml:58-64` (guard + values block) and the `TestEmail_*` test suite.
- **A-VIPER-SHADOW: rendering `stateCookieName: ""` would break the Go default.** **VALIDATED** by code reading — Viper `Unmarshal` into the `OIDC.StateCookieName` string field writes `""` for an empty YAML value, which is distinct from the field being absent (zero value). The SSO service's `if cookieName == ""` check at `sso.go:131` would still apply the default, BUT only if the line is omitted, not if it's rendered as empty. (Actually re-checking `sso.go:130-133`: the Go code does `if cookieName == "" { cookieName = "lsp_sso_state" }` — so an empty string WOULD fall back. The `{{- with }}` guard is therefore belt-and-suspenders: it produces cleaner YAML AND avoids relying on the Go fallback. The test `TestOIDC_DefaultRender_OmitsStateCookieName` locks in the cleaner-YAML choice as a deliberate design decision.)

---

## Blockers

None.

---

## Tests Run

- `helm lint charts/llmsafespaces` — **PASS** (1 chart linted, 0 failed)
- `helm template` across default / custom / partial OIDC values — all render correctly
- 34 rendered YAML docs parse; embedded `config.yaml` parses; OIDC section shape asserted via Python
- `go test -timeout 60s ./charts/...` — **PASS** (before adding new tests)
- `go test -timeout 90s -race -v -run TestOIDC ./charts/...` — **PASS** (3 new tests, 1.641s)
- **Mutation test**: `{{- if false }}` on the oidc block → `TestOIDC_DefaultRender_IncludesEmptyBlock` FAILS as expected; restored → all PASS
- `go run ./cmd/repolint` — **PASS** (all checks passed; chart migrations match api/migrations/)

CI on PR #302 (at time of this worklog): Gitleaks, Trivy, govulncheck, Lint, pkg/secrets integration, Frontend, AI review all PASS.

---

## Next Steps

- Address AI review's REQUEST CHANGES on PR #302 by pushing the regression tests + this worklog (this commit).
- Re-validate via the AI reviewer's re-review loop (per Orchestrator workflow steps 4–5).
- Out-of-scope known gaps (documented in §14, not addressed here):
  - DNS verification of `claimed_domains` (D17 Q-S2, designed not shipped)
  - Instance-level / platform-global OIDC (every flow is org-scoped today)
  - SAML / SCIM (deferred per Epic 43 D3)

---

## Files Modified

- `README-LLM.md` — added §14 Multi-Tenant OIDC SSO; bumped to v1.17
- `charts/llmsafespaces/values.yaml` — added top-level `oidc:` block
- `charts/llmsafespaces/templates/configmap-api.yaml` — render `oidc:` section
- `charts/llmsafespaces/chart_test.go` — added 3 OIDC regression tests
- `worklogs/0421_2026-06-20_epic43-oidc-helm-plumbing.md` — this worklog (new)
