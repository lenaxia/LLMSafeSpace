# Worklog: Epic 35 — Secretless Credential Injection

**Date:** 2026-06-23
**Session:** Implemented Epic 35: eliminated the `workspace-secrets-<id>` K8s Secret by replacing it with a direct HTTP fetch from the init container using a projected ServiceAccount token.
**Status:** Complete

---

## Objective

Rearchitect secrets injection to not require a K8s Secret, preserving the zero-knowledge model. Every workspace pod boot previously created a `workspace-secrets-<id>` K8s Secret containing plaintext decrypted credentials in etcd for 5–30s. Epic 35 eliminates this entirely.

---

## Work Completed

### Adversarial Design Review (pre-implementation)

Conducted a structured adversarial review (Rule 11) of the Epic 35 design doc before implementation. Found 4 real findings, all validated against source code:

- **F1 [BLOCKER]:** `workspace-config.json` delivery was silently dropped — the proposed init script omitted the copy, and the bootstrap response carried only secrets. **Fix:** bootstrap endpoint reads `default_model` from PostgreSQL (already stored there); response carries both `secrets` + `workspaceConfig`.
- **F2 [BLOCKER]:** RBAC scope-down broke relay credential writes (`oci-credentials`, `gcp-credentials`, `aws-relay-irwa`). **Fix:** three `resourceNames`-scoped Roles retain create/update/patch for each relay cred Secret.
- **F3 [MEDIUM]:** `pushSecretsToAgent` EnsureSecretsManifest caller unhandled. **Fix:** removed the EnsureSecretsManifest call; bind-time delivery is now live HTTP push only.
- **F4 [MEDIUM]:** `ensureWorkspaceServiceAccount` resume-path coverage ambiguous. **Fix:** wired into `handleCreating` (covers both fresh create and resume), mirroring `ensurePasswordSecret`.

### US-35.1: Per-Workspace ServiceAccount (Controller)

- `controller/internal/workspace/constants.go` — `bootstrapSAName()` function
- `controller/internal/workspace/secrets.go` — `ensureWorkspaceServiceAccount()` (idempotent, OwnerRef'd, `AutomountServiceAccountToken: false`)
- Wired into `handlePending` + `handleCreating` (F4 resume coverage)
- `charts/llmsafespaces/templates/rbac.yaml` — added `serviceaccounts` verbs to controller Role
- Tests: `bootstrap_sa_test.go` (6 tests: creates, idempotent, ownerref, automount, preserve, compat)

### US-35.2: Bootstrap Subcommand (agentd)

- `cmd/workspace-agentd/bootstrap.go` — `runBootstrapCommand()` fetches secrets from API via projected SA token, writes `secrets.json` + `workspace-config.json` (F1), degrades gracefully on any failure
- Wired in `main.go` alongside `materialize` subcommand
- Tests: `bootstrap_test.go` (9 tests: success, 404, 500, network error, token missing, file mode, env fallback, missing workspaceID, no-workspaceConfig)

### US-35.3: Pod Bootstrap Endpoint (API)

- `api/internal/handlers/pod_bootstrap.go` — `POST /internal/v1/pod-bootstrap` handler: TokenReview via K8s API, SA-name verification (`workspace-<id>` pattern), returns secrets + workspaceConfig (F1)
- `api/internal/server/router.go` — route registration (no JWT middleware; auth is TokenReview)
- `api/internal/app/app.go` — handler wiring (`NewPodBootstrapHandlerFromClientset`)
- RBAC: ClusterRole for `tokenreviews: create` + ClusterRoleBinding; scoped down API `secrets` verbs (removed create/update/patch); added 3 `resourceNames`-scoped rules for relay creds (F2)
- Tests: `pod_bootstrap_test.go` (12 tests: valid token, missing auth, tokenreview error, SA mismatch, SA not workspace pattern, workspace not found, injector error, empty secrets, no default model, lookup error, missing workspaceID, SA-name parser)

### US-35.4: Init Container Rewire (Controller)

- `controller/internal/workspace/pod_builder.go` — replaced `user-secrets` Secret volume with `bootstrap-token` projected SA-token volume (audience `llmsafespace-api`, 300s TTL); new init script calls `bootstrap` + `materialize`; set `ServiceAccountName` on pod spec to `workspace-<name>`
- `controller/internal/workspace/reconciler.go` — added `APIServiceURL` field
- `controller/internal/controller/controller.go` — wired `apiServiceURL` into reconciler
- Tests: updated `security_test.go` (volume footprint, G17 + ServiceAccountName), `health_test.go` (init script assertions)

### US-35.5: Remove Ephemeral Secret Paths

**Controller removals:**
- `deleteEphemeralSecretsSecret` function + all callers (`phase_creating.go`, `phase_active.go`)
- `workspace-secrets-*` from `cleanupFailedWorkspaceSecrets` list
- `allInitContainersComplete` function (dead code after caller removal)

**API removals:**
- `refreshEphemeralSecrets`, `seedEphemeralSecrets`, `createEphemeralSecretsSecret`, `EnsureSecretsManifest`, `MergeSecretsManifest`, `mergeSecretsByName`, `EnsureWorkspaceConfig` — all removed from `workspace_service.go` (~450 lines deleted)
- `SecretsManifestWriter` interface + `SetSecretsManifestWriter` from `secrets.go` handler
- `SetManifestWriter` from `models_handler.go`
- `SecretInjector` interface + `SetSecretInjector` from `workspace_service.go` (dead code after removal — found during adversarial self-review)
- `pushSecretsToAgent` EnsureSecretsManifest call (F3) — now live HTTP push only
- All callers from `CreateWorkspace`, `RestartWorkspace`, `ActivateWorkspace`
- App.go wiring for `SetSecretsManifestWriter` + `SetManifestWriter`

### US-35.6: Security Regression Tests

- Updated `TestSandboxPod_VolumeFootprint` — asserts `bootstrap-token` projected volume (not `user-secrets` Secret volume); verifies audience, TTL, init-only mount
- Updated `TestG17_SandboxPodDoesNotAutomountSAToken` — asserts `ServiceAccountName` is `workspace-<name>`
- Updated `TestReconcile_Failed_CleansUpSecrets` — `workspace-secrets-*` no longer in cleanup list
- Updated `TestCleanupFailedWorkspaceSecrets_DeletesAll` — 2 Secrets (not 3)

---

## Key Decisions

1. **Static stability trade-off (user decision):** accepted that pods booting during an API outage start with no credentials (degrade gracefully). The running-pod static stability is preserved — Epic 35 only affects boot-path delivery. User chose "Implement Epic 35 as designed" over keeping the K8s Secret as a fallback.

2. **F2 RBAC approach (user decision):** three separate `resourceNames`-scoped rules for relay creds rather than retaining broad create/update/patch on all secrets. Tighter least-privilege.

3. **F1 workspace-config delivery:** the default model is already in PostgreSQL (`workspaces.default_model`, `GetDefaultModel()`). The bootstrap endpoint reads it directly — no K8s Secret needed for workspace-config.json delivery. This eliminated the need for `EnsureWorkspaceConfig` entirely.

4. **ServiceAccountName on pod spec:** the projected SA token volume creates a token for the pod's `spec.serviceAccountName`. Setting this to `workspace-<name>` (with `AutomountServiceAccountToken: false` still suppressing the default mount) ensures the projected token carries the correct identity for TokenReview validation.

---

## Blockers

None.

---

## Tests Run

```
# Controller (all pass, -race clean)
go test -timeout 60s -count=1 ./controller/internal/workspace/
go test -timeout 30s -race -count=1 ./controller/internal/workspace/ -run "TestBootstrapSA|TestEnsureWorkspace|TestG17|TestSandboxPod|TestInitContainer|TestCleanup|TestReconcile_Failed"

# API handlers (all pass)
go test -timeout 120s -count=1 ./api/internal/handlers/
go test -timeout 30s -count=1 ./api/internal/handlers/ -run "TestPodBootstrap|TestParseWorkspaceID"

# API workspace service + app (all pass)
go test -timeout 90s -count=1 ./api/internal/services/workspace/ ./api/internal/app/

# agentd bootstrap (all pass, -race clean)
go test -timeout 15s -race -count=1 ./cmd/workspace-agentd/ -run "TestRunBootstrap"

# Full build + vet (clean)
go build ./api/... ./controller/... ./cmd/workspace-agentd/ ./pkg/...
go vet ./api/... ./controller/... ./cmd/workspace-agentd/
```

**Pre-existing race condition (not introduced by this work):** `managed_process.go:202` has a data race in `managedProcess.supervise` → `os/exec.Cmd.Wait`, reproducible on HEAD (commit `cb7850a6`). Unrelated to Epic 35; flagged for a future fix.

---

## Next Steps

1. **Cluster validation:** deploy to a kind cluster and verify end-to-end: workspace create → init container bootstrap → pod boots with credentials → live reload works.
2. **Pre-existing race fix:** investigate and fix the `managed_process.go` data race (separate worklog).
3. **Metrics:** add a bootstrap-failure counter so operators can alert on pods that booted without credentials due to API unavailability (O1 from the adversarial review).
4. **Update README-LLM.md:** document the secretless injection architecture (replace the `workspace-secrets-<id>` references in the Relay Config Subsystem section).

---

## Files Modified

**New files:**
- `api/internal/handlers/pod_bootstrap.go`
- `api/internal/handlers/pod_bootstrap_test.go`
- `cmd/workspace-agentd/bootstrap.go`
- `cmd/workspace-agentd/bootstrap_test.go`
- `controller/internal/workspace/bootstrap_sa_test.go`

**Modified files:**
- `api/internal/app/app.go`
- `api/internal/handlers/models_handler.go`
- `api/internal/handlers/secrets.go`
- `api/internal/handlers/secrets_integration_test.go`
- `api/internal/server/router.go`
- `api/internal/services/workspace/workspace_service.go`
- `api/internal/services/workspace/workspace_service_test.go`
- `charts/llmsafespaces/templates/rbac.yaml`
- `cmd/workspace-agentd/main.go`
- `controller/internal/controller/controller.go`
- `controller/internal/workspace/constants.go`
- `controller/internal/workspace/controller_test.go`
- `controller/internal/workspace/health_test.go`
- `controller/internal/workspace/phase_active.go`
- `controller/internal/workspace/phase_creating.go`
- `controller/internal/workspace/phase_pending.go`
- `controller/internal/workspace/pod_builder.go`
- `controller/internal/workspace/reconciler.go`
- `controller/internal/workspace/secrets.go`
- `controller/internal/workspace/secrets_test.go`
- `controller/internal/workspace/security_test.go`

**Deleted files:**
- `controller/internal/workspace/init_complete_test.go` (dead code: `allInitContainersComplete` removed)
