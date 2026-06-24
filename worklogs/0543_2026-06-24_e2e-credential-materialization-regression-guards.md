# Worklog: E2E Credential Materialization Regression Guards

**Date:** 2026-06-24
**PR:** #394
**Branch:** `test/e2e-credential-materialization-regression-guards`

## Summary

Added 7 e2e test files (33 tests) wiring real layers end-to-end across the
credential lifecycle. Every audit found the same anti-pattern: each layer is
unit-tested in isolation with the next layer stubbed, so cross-boundary
regressions slip through CI.

## Motivation

Org-level LLM providers stopped materializing in new workspaces on the deployed
cluster. Investigation revealed that every layer of the boot path
(`SecretService` → `PodBootstrapHandler` → `agentd bootstrap` → `materialize`)
was tested in isolation with the next layer mocked. A break at any seam would
ship undetected.

Static analysis confirmed the code on `main` is correct for the happy path —
the deployed bug is operational (KEK mismatch, config, or DB bindings). These
tests guard against the same class of regression recurring from code changes.

## Tests added

| File | Subsystem | Happy | Unhappy |
|------|-----------|-------|---------|
| `api/internal/handlers/pod_bootstrap_e2e_test.go` | Boot-path materialization | 3 | 7 |
| `cmd/workspace-agentd/reload_credentials_e2e_test.go` | Live reload path | 2 | 3 |
| `controller/internal/workspace/pod_spec_consistency_test.go` | Reconciler pod spec | 3 | 1 |
| `pkg/secrets/kek_rotation_e2e_test.go` | Master KEK rotation | 1 | 3 |
| `pkg/secrets/password_reset_crypto_e2e_test.go` | Reset crypto erasure | 1 | 1 |
| `api/internal/services/sso/sso_jwt_e2e_test.go` | SSO JWT issuance | 1 | 2 |
| `cmd/workspace-agentd/model_enricher_e2e_test.go` | Model enrichment chain | 1 | 1 |

## Key gaps closed

- **Boot path**: real `SecretService` (with real per-ownerType `RootKeyProvider`s)
  → real `PodBootstrapHandler` → real `workspace-agentd bootstrap` + `materialize`
  subprocesses, asserting on the materialized `agent-config.json`.
- **Reload path**: the reload-path twin — `PrepareSecretsForInjection` → real
  `reloadSecretsHandler` → `agent-config.json`.
- **Controller pod spec**: cross-validates env↔script, mounts↔paths, ordering
  on a single Reconcile-produced pod.
- **KEK rotation**: the missing "old key FAILS to decrypt post-rotation" assertion.
- **Password reset**: the `database.go:790` "no future materialization" guarantee,
  tested at both the materialization and crypto layers.
- **SSO**: real HS256 JWT issuer, validated as a genuine parseable JWT.
- **Model enricher**: full chain enricher → `FormatOpenCodeConfig` → `agent-config.json`.

## Review feedback addressed

- Renamed/fixed `WrongKEK_SkipsBinding` to test the real decrypt-failure path
  (wrong key B wired, decrypt attempted + fails) via a `wrongOrgKey` config
  field, distinct from the nil-provider skip path.
- Added this worklog.

## Assumptions validated

- `injection.go:176` nil-org skip path verified verbatim.
- `bootstrap.go:78-79` graceful degradation (empty secrets, exit 0) verified.
- `bootstrapAudience` constant agrees across controller + API.
- `InitializeUserKeys` UPSERT overwrites wrapped DEK.
- Controller SA+Pod created in same `handleCreating` pass.

## Follow-ups (not in this PR)

- Production materialization bug requires cluster diagnostics to pinpoint
  (KEK mismatch vs. missing bindings vs. `org_id` NULL).
- Stub duplication across packages could be extracted to a shared test helper.
