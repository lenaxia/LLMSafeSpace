# Epic 35: Secretless Credential Injection

**Status:** Ready to Implement
**Depends On:** Epic 10 (secret encryption infrastructure — confirmed in place), Epic 17 (G17 SA token isolation — preserved by design)
**Estimated Effort:** ~16 hours

---

## Problem Being Solved

Every workspace pod boot creates a `workspace-secrets-<id>` K8s Secret containing plaintext decrypted user credentials. This Secret lives in etcd for 5–30 seconds (until init containers complete), is synced to the kubelet's node-local secret store, and is then deleted. The exposure window is brief but real:

- Credentials are stored in etcd — accessible via `kubectl get secret`, etcd snapshots, and audit logs during the window
- The Secret is synced to the node's kubelet before the pod starts — it exists in node memory even after deletion from the API server
- Every pod resume/restart re-creates the window from scratch

The goal is to eliminate this K8s Secret entirely. No Vault, no External Secrets Operator, no new infrastructure — only what already exists in the codebase.

---

## Validated Assumptions

Every claim is verified against live code.

| # | Assumption | Verified At | Result |
|---|---|---|---|
| A1 | `workspace-secrets-<id>` is created by the API server in `EnsureSecretsManifest` | `workspace_service.go:1161` | Confirmed |
| A2 | It is mounted as a Secret volume on the `credential-setup` init container only | `pod_builder.go:370–383` | Confirmed — `Optional: true`, init container only |
| A3 | The init container copies `secrets.json` → `/sandbox-cfg/secrets.json` via shell script | `pod_builder.go:346–350` | Confirmed |
| A4 | The Secret is deleted by the controller after init containers complete | `phase_creating.go:110–111` | Confirmed |
| A5 | The live `/v1/reload-secrets` push path does NOT use the K8s Secret | `secrets.go:293–431` | Confirmed — HTTP push to agentd only |
| A6 | `runMaterializeCommand` treats a missing `secrets.json` as a no-op (exit 0) | `secrets.go:139–145` | Confirmed — `os.ErrNotExist` returns 0 |
| A7 | The workspace pod egress → API service network policy already exists (Epic 26 relay) | `workspace-network-policy.yaml:109–118` | Confirmed — no network policy changes needed |
| A8 | `AutomountServiceAccountToken: false` is set at the pod spec level | `pod_builder.go:196` | Confirmed — G17 hardening |
| A9 | Projected volumes are explicit per-container mounts — they are NOT suppressed by pod-level `AutomountServiceAccountToken: false` | K8s spec (projected volume is a named volume, not the default SA token injection) | Confirmed — pod-level field only suppresses the auto-injected default SA token, not explicit projected volume mounts |
| A10 | `PrepareSecretsForInjection` with `sessionID=""` decrypts admin platform credentials only (server-side KEK) | `workspace_service.go:1126` | Confirmed — called in `seedEphemeralSecrets` with empty sessionID |
| A11 | `database.GetWorkspace(workspaceID)` returns `UserID` without requiring the caller to already know it | `database.go:339–360` | Confirmed — `ws.UserID` populated from `user_id` column |
| A12 | The API server's K8s client (`kubernetes.Interface`) supports TokenReview via `clientset.AuthenticationV1().TokenReviews().Create()` | `k8s.io/client-go` v0.32.3 | Confirmed — standard client-go API |
| A13 | The API server Role does NOT currently have `tokenreviews: create` | `rbac.yaml:241–269` | Confirmed — not present; must be added as a ClusterRole (TokenReview is cluster-scoped) |
| A14 | The controller Role does NOT currently manage ServiceAccounts | `rbac.yaml:49–227` | Confirmed — `serviceaccounts` not present; must be added |
| A15 | `entrypoint-common.sh` calls `workspace-agentd materialize` unconditionally | `entrypoint-common.sh:33` | Confirmed — unchanged; bootstrap writes `secrets.json` before materialize runs |
| A16 | The API service Kubernetes Service name is `{{ include "llmsafespace.fullname" . }}-api` in the release namespace | `api-service.yaml:5–6` | Confirmed |
| A17 | Default API service port is 8080 | `values.yaml:39–40` | Confirmed |
| A18 | K8s projected ServiceAccount tokens with a custom audience do NOT appear as the default SA token — they require an explicit volume mount | K8s projected volume spec | Confirmed — the init container mounts the token; the main container does not |
| A19 | `workspace-agentd` subcommand dispatch exists in `main.go` alongside the `materialize` subcommand | `main.go` | Confirmed — new `bootstrap` subcommand wires in identically |
| A20 | opencode reads `auth.json` from disk on every instance bootstrap (i.e. after `POST /instance/dispose`) — if credentials are in `secrets.json` at pod start, no dispose is needed | `auth/index.ts:57–65`; `bootstrap.ts` | Confirmed — `Auth.all()` re-reads `auth.json` on each load; `FlushProviders` writes `agent-config.json` at boot via `runMaterializeCommand` |
| A21 | `seedEphemeralSecrets`, `refreshEphemeralSecrets`, and `createEphemeralSecretsSecret` are only called from paths that feed into pod boot — no caller outside workspace lifecycle | grep of `workspace_service.go` | Confirmed — all three callers are `CreateWorkspace`, `ActivateWorkspace`, `RestartWorkspace` |
| A22 | The `SecretInjector` interface is on `workspace.Service`, not on a global service | `workspace_service.go:128–135` | Confirmed — `SetSecretInjector` sets the injector; the bootstrap handler needs the same injector |

---

## Solution Overview

Replace the ephemeral `workspace-secrets-<id>` K8s Secret with a direct HTTP fetch from the init container using a short-lived projected ServiceAccount token.

```
Before:
  API server creates workspace-secrets-<id> in etcd
    → controller mounts it into init container via Secret volume
    → init container copies secrets.json → /sandbox-cfg/secrets.json
    → controller deletes the K8s Secret after init completes

After:
  Controller creates workspace-<id> ServiceAccount (with OwnerRef)
    → init container presents projected SA token to API /internal/v1/pod-bootstrap
    → API validates token via TokenReview, verifies SA name matches workspaceID
    → API calls PrepareSecretsForInjection, returns decrypted secrets JSON
    → init container writes /sandbox-cfg/secrets.json
    → no K8s Secret ever created for user credentials
```

**Network path:** Already exists. The workspace pod egress → API service rule was added in Epic 26 (`workspace-network-policy.yaml:109–118`). No network policy changes needed.

**G17 preserved:** `AutomountServiceAccountToken: false` remains on the pod spec. The projected token volume is an explicit named volume mounted only on the init container. The main container (`workspace`) never sees the SA token.

**opencode boot:** Since `secrets.json` is written by the init container before agentd starts, opencode boots with credentials on first start. No `POST /instance/dispose` round-trip needed.

**Graceful degradation on failure:** If the bootstrap HTTP call fails (API unavailable, 5xx, network hiccup), the init container writes an empty `secrets.json` and exits 0. The pod boots without credentials. The existing `POST /v1/reload-secrets` live-push path handles credential delivery on the user's first request — identical to the current behaviour when a workspace has no bindings.

---

## Stories

### US-35.1: Per-Workspace ServiceAccount (Controller)

**Goal:** Controller creates a `workspace-<workspaceName>` ServiceAccount in `handlePending`, with an OwnerReference to the Workspace CRD.

**Files:**
- `controller/internal/workspace/constants.go` — add `bootstrapSAName(workspaceName string) string`
- `controller/internal/workspace/secrets.go` — add `ensureWorkspaceServiceAccount(ctx, workspace)`
- `controller/internal/workspace/phase_pending.go` — call `ensureWorkspaceServiceAccount` after `ensurePasswordSecret`
- `controller/internal/workspace/phase_creating.go` — call `ensureWorkspaceServiceAccount` defensively (same pattern as `ensurePasswordSecret` at line 66)
- `charts/llmsafespace/templates/rbac.yaml` — add `serviceaccounts: get, create, delete` to the controller Role

**SA spec:**
```go
&corev1.ServiceAccount{
    ObjectMeta: metav1.ObjectMeta{
        Name:      bootstrapSAName(workspace.Name),   // "workspace-<name>"
        Namespace: workspace.Namespace,
    },
    AutomountServiceAccountToken: &falseVal,
}
```
OwnerReference set via `controllerutil.SetControllerReference`.

**Idempotent:** if the SA already exists, return nil. Same pattern as `ensurePasswordSecret`.

**Acceptance criteria:**
- SA named `workspace-<workspaceName>` exists after `handlePending`
- SA has OwnerReference pointing to the Workspace with `controller=true`
- SA has `automountServiceAccountToken: false`
- SA is deleted automatically when the Workspace CRD is deleted (via OwnerRef GC)
- Second call with SA already present returns nil (idempotent)

**Tests (TDD):**
- `TestEnsureWorkspaceServiceAccount_Creates` — SA does not exist → created with correct name, namespace, OwnerRef
- `TestEnsureWorkspaceServiceAccount_Idempotent` — SA already exists → returns nil, no error
- `TestEnsureWorkspaceServiceAccount_OwnerRefSet` — OwnerRef controller=true, blockOwnerDeletion=true, correct GVK
- `TestEnsureWorkspaceServiceAccount_AutomountFalse` — `AutomountServiceAccountToken` is `false`
- `TestBootstrapSAName` — `bootstrapSAName("abc123")` returns `"workspace-abc123"`

---

### US-35.2: Bootstrap Subcommand (agentd)

**Goal:** New `workspace-agentd bootstrap` subcommand that fetches decrypted secrets from the API server using a projected SA token and writes them to `/sandbox-cfg/secrets.json`.

**File:** `cmd/workspace-agentd/bootstrap.go` (new)

**Flags:**
- `--workspace-id` string (required)
- `--api-url` string (required; default read from `LLMSAFESPACE_API_URL` env var)
- `--token-file` string (default `/var/run/bootstrap/token`)
- `--out` string (default `/sandbox-cfg/secrets.json`)

**Logic:**
```
1. Read token from --token-file
   → if missing or unreadable: write empty [] to --out, exit 0 (degrade gracefully)
2. POST <api-url>/internal/v1/pod-bootstrap
     Authorization: Bearer <token>
     Content-Type: application/json
     Body: {"workspaceID": "<workspace-id>"}
   HTTP client timeout: 10s; no retries (degrade gracefully on failure)
3. On 200: write response body to --out with mode 0600
4. On 404 or empty body: write [] to --out, exit 0
5. On any error (non-200, network failure, timeout): write [] to --out, exit 0
   Log reason to stderr (operator visibility via kubectl logs)
```

**Exit codes:** 0 always. The bootstrap subcommand never blocks pod boot.

**Wired in `main.go`** alongside the `materialize` subcommand dispatch.

**Acceptance criteria:**
- On 200 response: `secrets.json` written with correct content, mode 0600
- On 404 or empty: `secrets.json` written as `[]`, exit 0
- On 5xx: `secrets.json` written as `[]`, exit 0, reason logged to stderr
- On token file missing: `secrets.json` written as `[]`, exit 0
- On network timeout: `secrets.json` written as `[]`, exit 0
- `--api-url` defaults to `LLMSAFESPACE_API_URL` env var

**Tests (TDD — `cmd/workspace-agentd/bootstrap_test.go`):**
- `TestRunBootstrapCommand_Success` — mock server returns 200 + JSON → file written with correct content
- `TestRunBootstrapCommand_404_WritesEmpty` — mock 404 → `[]` written, exit 0
- `TestRunBootstrapCommand_500_Degrades` — mock 500 → `[]` written, exit 0
- `TestRunBootstrapCommand_NetworkError_Degrades` — mock server closed → `[]` written, exit 0
- `TestRunBootstrapCommand_TokenFileMissing_Degrades` — no token file → `[]` written, exit 0
- `TestRunBootstrapCommand_FileMode` — written file has mode 0600
- `TestRunBootstrapCommand_EnvFallback` — `--api-url` absent, `LLMSAFESPACE_API_URL` set → uses env var
- `TestRunBootstrapCommand_MissingWorkspaceID_Errors` — missing `--workspace-id` → exit 2

---

### US-35.3: Pod Bootstrap Endpoint (API Server)

**Goal:** New `POST /internal/v1/pod-bootstrap` endpoint on the API server. Authenticates via K8s TokenReview, verifies the calling pod's SA identity matches the claimed workspaceID, returns decrypted secrets JSON.

**File:** `api/internal/handlers/pod_bootstrap.go` (new)

**Handler logic:**
```
1. Extract Bearer token from Authorization header
   → missing: 401
2. TokenReview: clientset.AuthenticationV1().TokenReviews().Create(ctx,
     &authv1.TokenReview{
         Spec: authv1.TokenReviewSpec{
             Token:     token,
             Audiences: []string{"llmsafespace-api"},
         },
     }, metav1.CreateOptions{})
   → Authenticated=false: 401
   → K8s API error: 500
3. Extract username from status.user.username
   → Expected format: "system:serviceaccount:<namespace>:workspace-<workspaceID>"
   → Parse workspaceID from SA name
   → workspaceID != claimed workspaceID from request body: 403
   → SA name does not match "workspace-<workspaceID>" pattern: 403
4. Look up workspace in DB: database.GetWorkspace(ctx, workspaceID)
   → not found: 404
5. PrepareSecretsForInjection(ctx, ws.UserID, sessionID="", workspaceID)
   → error: 500
   → empty (len <= 2): return 200 with body []
6. Return 200 with secrets JSON body
```

**Route registration** in `router.go`:
```go
// Internal pod bootstrap — no JWT auth middleware.
// Auth is via K8s TokenReview (projected SA token, audience "llmsafespace-api").
if cfg.PodBootstrapHandler != nil {
    router.POST("/internal/v1/pod-bootstrap", cfg.PodBootstrapHandler.Bootstrap)
}
```

**RouterConfig** addition:
```go
PodBootstrapHandler *handlers.PodBootstrapHandler
```

**Wired in `app.go`** alongside other optional handlers.

**RBAC addition** — `charts/llmsafespace/templates/rbac.yaml`:
```yaml
# ClusterRole (TokenReview is cluster-scoped, not namespace-scoped)
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: {{ include "llmsafespace.fullname" . }}-api-tokenreview
rules:
  - apiGroups: ["authentication.k8s.io"]
    resources: ["tokenreviews"]
    verbs: ["create"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: {{ include "llmsafespace.fullname" . }}-api-tokenreview
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: {{ include "llmsafespace.fullname" . }}-api-tokenreview
subjects:
  - kind: ServiceAccount
    name: {{ include "llmsafespace.api.serviceAccountName" . }}
    namespace: {{ .Release.Namespace }}
```

**API server Role change** — scope `secrets` verbs down now that it no longer creates `workspace-secrets-*`:
```yaml
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch", "delete"]  # removed: create, update, patch
```

**Acceptance criteria:**
- Valid SA token for `workspace-abc` + `workspaceID: "abc"` → 200 + secrets JSON
- Valid SA token for `workspace-abc` + `workspaceID: "xyz"` → 403 (SA/workspace mismatch)
- Invalid token → 401
- TokenReview Authenticated=false → 401
- Unknown workspaceID (not in DB) → 404
- `PrepareSecretsForInjection` error → 500
- No user JWT middleware on this route
- Rate limiting still applies (router-level middleware)

**Tests (TDD — `api/internal/handlers/pod_bootstrap_test.go`):**
- `TestPodBootstrap_ValidToken_ReturnsSecrets` — mock TokenReview authenticated=true, correct SA name → 200
- `TestPodBootstrap_InvalidToken_Returns401` — mock TokenReview authenticated=false → 401
- `TestPodBootstrap_MissingAuthHeader_Returns401` — no Authorization header → 401
- `TestPodBootstrap_SANameMismatch_Returns403` — SA is `workspace-abc`, claims `workspaceID: xyz` → 403
- `TestPodBootstrap_SANotWorkspacePattern_Returns403` — SA is `some-other-sa` → 403
- `TestPodBootstrap_WorkspaceNotFound_Returns404` — workspace not in DB → 404
- `TestPodBootstrap_EmptySecrets_Returns200Empty` — `PrepareSecretsForInjection` returns `[]` → 200 with `[]`
- `TestPodBootstrap_TokenReviewError_Returns500` — K8s API error → 500
- `TestPodBootstrap_InjectorError_Returns500` — `PrepareSecretsForInjection` error → 500
- Integration: `TestPodBootstrapRoute_Registered` — route is wired; `POST /internal/v1/pod-bootstrap` without token returns 401 not 404

---

### US-35.4: Init Container Rewire (Controller)

**Goal:** Replace the `workspace-secrets-<id>` Secret volume on the init container with a projected SA token volume. Change the init script to call `workspace-agentd bootstrap` followed by `workspace-agentd materialize`.

**Files:**
- `controller/internal/workspace/pod_builder.go`
- `controller/internal/workspace/reconciler.go` — add `APIServiceURL string` field
- `controller/main.go` — add `--api-service-url` flag
- `charts/llmsafespace/templates/controller-deployment.yaml` — pass flag with helm-constructed URL

**`buildCredentialSetupInit` changes:**

Remove:
- `userSecretsVol` (the `workspace-secrets-<id>` Secret volume)
- `user-secrets` VolumeMount
- `if [ -f /mnt/secrets/user-secrets/secrets.json ]` copy line

Add:
- `bootstrap-token` projected volume (init container only):
```go
bootstrapTokenVolume := corev1.Volume{
    Name: "bootstrap-token",
    VolumeSource: corev1.VolumeSource{
        Projected: &corev1.ProjectedVolumeSource{
            Sources: []corev1.VolumeProjection{{
                ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
                    Path:              "token",
                    ExpirationSeconds: int64Ptr(300), // 5 minutes
                    Audience:          "llmsafespace-api",
                },
            }},
        },
    },
}
```
- Mount `bootstrap-token` at `/var/run/bootstrap` on init container (ReadOnly). **Not on main container.**
- `LLMSAFESPACE_API_URL` env var on the init container (from `r.APIServiceURL`)

**Init script change:**
```sh
workspace-agentd bootstrap --workspace-id "$WORKSPACE_ID" --api-url "$LLMSAFESPACE_API_URL"
workspace-agentd materialize
cp /mnt/secrets/password/password /sandbox-cfg/password
```

`WORKSPACE_ID` is already set as an env var on the pod spec (line 70 of pod_builder.go) and is inherited by the init container. `LLMSAFESPACE_API_URL` is set as an additional env var on the init container only.

**`buildCredentialSetupInit` signature change:**
```go
// Before: returns (corev1.Container, corev1.Volume, corev1.Volume, error)
// After:  returns (corev1.Container, corev1.Volume, corev1.Volume, error)
//         second Volume is now bootstrapTokenVolume instead of userSecretsVol
```

**`buildPod` change:** passes `r.APIServiceURL` to `buildCredentialSetupInit`. Identical wiring to `r.InferenceRelayURL` (already on the reconciler struct).

**Controller flag (`controller/main.go`):**
```go
flag.StringVar(&wsReconciler.APIServiceURL, "api-service-url",
    "http://llmsafespace-api.llmsafespace.svc.cluster.local:8080",
    "URL of the LLMSafeSpace API service for pod bootstrap credential fetch")
```

**Helm controller deployment** (`charts/llmsafespace/templates/controller-deployment.yaml`):
```yaml
- --api-service-url=http://{{ include "llmsafespace.fullname" . }}-api.{{ .Release.Namespace }}.svc.cluster.local:{{ .Values.api.service.port }}
```

**Acceptance criteria:**
- Pod spec has no `workspace-secrets-*` volume
- `bootstrap-token` projected volume present on pod spec, mounted on init container only
- Main container `workspace` has no `bootstrap-token` mount
- Init container script calls `workspace-agentd bootstrap` before `workspace-agentd materialize`
- `LLMSAFESPACE_API_URL` set as env var on init container
- `AutomountServiceAccountToken: false` still set on pod spec
- `APIServiceURL` field wired from `--api-service-url` controller flag

**Tests (TDD):**
- `TestBuildCredentialSetupInit_NoUserSecretsVolume` — pod spec has no `workspace-secrets-*` volume
- `TestBuildCredentialSetupInit_BootstrapTokenVolumePresent` — `bootstrap-token` projected volume present
- `TestBuildCredentialSetupInit_BootstrapTokenAudience` — projected token audience is `"llmsafespace-api"`
- `TestBuildCredentialSetupInit_BootstrapTokenExpiry` — `expirationSeconds == 300`
- `TestBuildCredentialSetupInit_BootstrapTokenOnInitOnly` — main container has no `bootstrap-token` mount
- `TestBuildCredentialSetupInit_InitScriptCallsBootstrap` — script contains `workspace-agentd bootstrap`
- `TestBuildCredentialSetupInit_InitScriptCallsMaterialize` — script contains `workspace-agentd materialize`
- `TestBuildCredentialSetupInit_APIURLEnvVar` — `LLMSAFESPACE_API_URL` set on init container
- `TestBuildPod_AutomountSATokenStillFalse` — existing G17 test must still pass (no regression)
- Update `TestBuildPod_CredentialSetupVolumes` to assert bootstrap-token volume, not user-secrets volume

---

### US-35.5: Remove Ephemeral Secret Paths (API Server + Controller)

**Goal:** Remove all code paths that create, update, or delete the `workspace-secrets-<id>` K8s Secret.

**Controller — `controller/internal/workspace/secrets.go`:**
- Remove `deleteEphemeralSecretsSecret`
- Remove `workspace-secrets-*` from `cleanupFailedWorkspaceSecrets`

**Controller — `controller/internal/workspace/phase_creating.go`:**
- Remove the `if allInitContainersComplete { r.deleteEphemeralSecretsSecret(ctx, workspace) }` block (lines 109–112)

**Controller — `controller/internal/workspace/phase_active.go`:**
- Remove the safety-net `deleteEphemeralSecretsSecret` call

**API server — `api/internal/services/workspace/workspace_service.go`:**
- Remove `seedEphemeralSecrets`
- Remove `refreshEphemeralSecrets`
- Remove `createEphemeralSecretsSecret`
- Remove `EnsureSecretsManifest` (verify no callers remain before deleting)
- Remove calls to the above from `CreateWorkspace`, `ActivateWorkspace`, `RestartWorkspace`

**Verify before deleting:** grep for all callers of `EnsureSecretsManifest`, `createEphemeralSecretsSecret`, `seedEphemeralSecrets`, `refreshEphemeralSecrets` — confirm zero callers remain after US-35.3 and US-35.4 are wired.

**Acceptance criteria:**
- No code in the codebase calls `EnsureSecretsManifest`, `createEphemeralSecretsSecret`, `seedEphemeralSecrets`, or `refreshEphemeralSecrets`
- No K8s Secret with name matching `workspace-secrets-*` is ever created by any path
- `deleteEphemeralSecretsSecret` does not exist
- `workspace-secrets-*` is not referenced in `cleanupFailedWorkspaceSecrets`
- All tests pass after removal

**Tests (TDD):**
- `TestCreateWorkspace_NoSecretsManifestCreated` — mock K8s client asserts no `workspace-secrets-*` Secret is created
- `TestActivateWorkspace_NoSecretsManifestCreated` — same assertion on activate path
- `TestRestartWorkspace_NoSecretsManifestCreated` — same assertion on restart path

---

### US-35.6: Security Regression Tests

**Goal:** Lock in the security properties of the new design with explicit regression tests.

**File:** `controller/internal/workspace/security_test.go` (extend existing)

**New assertions:**
- `TestPodSpec_NoUserSecretsVolume` — pod spec has no volume named `workspace-secrets-*`
- `TestPodSpec_BootstrapTokenProjectedOnInitOnly` — `bootstrap-token` in init container mounts; NOT in main container mounts
- `TestPodSpec_AutomountSATokenFalse` — existing test; must still pass (G17 preserved)
- `TestPodSpec_SANameForWorkspace` — SA name `workspace-<workspaceName>` created and associated with pod spec's `ServiceAccountName` on the init context (note: pod `ServiceAccountName` field is NOT set; the projected token references the SA by the volume definition)

**File:** `api/internal/handlers/pod_bootstrap_test.go` (integration)
- `TestPodBootstrap_SANameEnforcesWorkspaceIsolation` — SA `workspace-abc` cannot retrieve secrets for `workspace-xyz` even with a valid, authenticated token

---

## Implementation Order

```
1. Write all tests first — must fail before implementation (README-LLM.md §0)

2. US-35.1 — Per-workspace ServiceAccount (controller)
   controller/internal/workspace/constants.go   (bootstrapSAName)
   controller/internal/workspace/secrets.go      (ensureWorkspaceServiceAccount)
   controller/internal/workspace/phase_pending.go (call after ensurePasswordSecret)
   controller/internal/workspace/phase_creating.go (defensive call)
   charts/llmsafespace/templates/rbac.yaml       (serviceaccounts verbs)

3. US-35.2 — Bootstrap subcommand (agentd)
   cmd/workspace-agentd/bootstrap.go             (new)
   cmd/workspace-agentd/main.go                  (wire subcommand)

4. US-35.3 — Pod bootstrap endpoint (API server)
   api/internal/handlers/pod_bootstrap.go        (new)
   api/internal/server/router.go                 (PodBootstrapHandler field + route)
   api/internal/app/app.go                       (wire handler)
   charts/llmsafespace/templates/rbac.yaml       (ClusterRole + ClusterRoleBinding for tokenreviews)
                                                  (scope down secrets verbs)

5. US-35.4 — Init container rewire (controller)
   controller/internal/workspace/reconciler.go   (APIServiceURL field)
   controller/internal/workspace/pod_builder.go  (projected token volume, new init script)
   controller/main.go                            (--api-service-url flag)
   charts/llmsafespace/templates/controller-deployment.yaml (pass flag)

6. US-35.5 — Remove ephemeral secret paths
   controller/internal/workspace/secrets.go      (remove deleteEphemeralSecretsSecret)
   controller/internal/workspace/phase_creating.go (remove deleteEphemeralSecretsSecret call)
   controller/internal/workspace/phase_active.go  (remove deleteEphemeralSecretsSecret call)
   api/internal/services/workspace/workspace_service.go (remove 4 functions + callers)

7. US-35.6 — Security regression tests
   controller/internal/workspace/security_test.go (extend)
   api/internal/handlers/pod_bootstrap_test.go    (isolation test)

8. Run all tests:
   cd controller && go test ./... -timeout 120s -race
   cd api && go test ./... -timeout 120s -race
   cd cmd/workspace-agentd && go test ./... -timeout 120s -race

9. Adversarial self-review (README-LLM.md §11)
```

---

## Non-Requirements (Explicitly Out of Scope)

| Item | Rationale |
|---|---|
| `workspace-pw-<id>` password Secret | Different risk profile — only grants agentd admin port access; long-lived but OwnerRef GC handles cleanup; removing it requires a different mechanism for readiness probe token embedding |
| Remove `AGENTD_ADMIN_TOKEN` env var from pod spec | Lower priority; `secretKeyRef` values are not shown in `kubectl describe pod`; separate epic if needed |
| Vault / External Secrets Operator | No new infrastructure required; this design eliminates the problem with existing primitives |
| User-credential injection (session DEK) at pod boot | Bootstrap uses `sessionID=""` (admin creds only) — same as `seedEphemeralSecrets` today. User credentials arrive via the existing `POST /v1/reload-secrets` live-push on first activation |
| Suspend/delete the password Secret on Suspend | Separate improvement; not part of this epic |
| Retry logic in bootstrap subcommand | Graceful degradation is the correct behaviour; retries add complexity and delay pod boot |

---

## Adversarial Assessment

### Does the SA identity check actually prove pod identity?

Yes. The TokenReview validates the projected token cryptographically (signed by the K8s API server's key). The token carries `sub: system:serviceaccount:<namespace>:workspace-<workspaceID>`. The API server's SA name check (`status.user.username == "system:serviceaccount:<ns>:workspace-<workspaceID>"`) is the authoritative proof: only the pod running under that SA can present a valid token for it. A compromised workspace pod for workspace A can only retrieve credentials for workspace A — it cannot forge a token for workspace B's SA.

### What if an attacker guesses the workspaceID?

The projected token must be presented; guessing the workspaceID is not enough. The token is only available inside the init container, and only for 5 minutes. After the init container exits, the token is gone — it is not mounted on the main container and the projected token volume is not accessible from outside the pod.

### What if the API service is temporarily unavailable?

The bootstrap subcommand degrades gracefully (writes `[]`, exits 0). The pod boots without credentials. The `POST /v1/reload-secrets` push handles delivery on first user activation. This is identical to the current behaviour when a workspace has no credential bindings. No pod boot is blocked by API unavailability.

### What about the projected token after init completes?

The projected token volume (`bootstrap-token`) is mounted only on the init container. The main container (`workspace`) does not have it in its `volumeMounts`. Even if an attacker running inside the workspace pod reads `/proc/1/mounts` or explores the pod's volumes, the projected token is not accessible from the main container's filesystem namespace (K8s does not inject it unless explicitly mounted). The 5-minute TTL means the token is expired before any meaningful exploitation window.

### Does removing `workspace-secrets-<id>` break any existing live-reload path?

No. The live-reload path (`POST /v1/reload-secrets` → `POST /api/v1/workspaces/:id/reload-secrets`) never uses the K8s Secret. It pushes credentials directly to agentd via HTTP. That path is unchanged.
