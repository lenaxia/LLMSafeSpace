# Worklog: RestartWorkspace Drops User Secrets — Helper Refactor + Live Recovery

**Date:** 2026-06-01
**Session:** Diagnose lost SSH key after `restartGeneration` bump, ship Option B refactor, recover affected workspaces
**Status:** Complete (API-side fix shipped; controller-side invariant filed as follow-up)

---

## Objective

After committing the runtime base image upgrade (worklog implicit; see
commit a20fb23 — added ssh, build/dev tools, db clients, AWS CLI v2)
and helm-upgrading the cluster, I patched four Active workspaces'
`spec.restartGeneration` to force pod recreation onto the new image.

User then reported: workspace `6d36952e-0cd5-42cb-9630-8f988b3e5f33`
session `ses_18887778dffe3yMRfw8SX3ln46` had a GitHub SSH key
configured, but the in-pod agent reported it was no longer mounted.

Goal: identify why the key disappeared, recover it for the user, and
prevent the same regression from hitting any future restart.

---

## Diagnosis

### What's mounted vs what should be mounted

The pod's `credential-setup` initContainer runs:

    if [ -f /mnt/secrets/user-secrets/secrets.json ]; then
      cp /mnt/secrets/user-secrets/secrets.json /sandbox-cfg/secrets.json
    fi
    cp /mnt/secrets/password/password /sandbox-cfg/password

`/mnt/secrets/user-secrets/secrets.json` resolves to a Secret volume
named `user-secrets`, backed by the K8s Secret `workspace-secrets-<id>`
(per `controller/internal/workspace/pod_builder.go:365-381`):

    userSecretsName := fmt.Sprintf("workspace-secrets-%s", workspace.Name)
    if err := r.Get(ctx, ..., userSecretsSecret); err == nil {
        // mount it
    } else if !errors.IsNotFound(err) {
        return ..., fmt.Errorf("checking user-secrets secret: %w", err)
    }

If the Secret is `NotFound`, the volume is silently omitted. The init
container's `[ -f ... ]` then silently no-ops, and the pod comes up
with no `secrets.json` — exactly the symptom the user reported.

### Why the Secret was missing

For workspace `6d36952e-...`:

| Object | State |
|---|---|
| DB `user_secret_bindings` for this ws | 2 rows — `github` (ssh-key), `sdafafdsfasdfasdf` (secret-file) |
| K8s `workspace-creds-<id>` | exists, holds `provider-config: {}` (legacy, unrelated) |
| K8s `workspace-secrets-<id>` | **missing** |
| K8s `workspace-pw-<id>` | exists |

The bindings are intact in PostgreSQL; only the K8s `workspace-secrets-<id>`
Secret was absent. Cross-checking all 4 restarted workspaces confirmed
the pattern:

    6d36952e: bindings=2, k8s_secret=no  ← user-affected
    a020fc49: bindings=0, k8s_secret=no  (no user impact)
    c3c8766d: bindings=3, k8s_secret=no  ← also affected
    c8660334: bindings=0, k8s_secret=no  (no user impact)

### Why ActivateWorkspace works but RestartWorkspace doesn't

`api/internal/services/workspace/workspace_service.go:840-849`
(pre-fix) — `ActivateWorkspace` re-emits the manifest before resuming:

    if s.secretInjector != nil {
        sessionID, _ := ctx.Value(sessionIDContextKey).(string)
        if sessionID != "" {
            secretsJSON, err := s.secretInjector.PrepareSecretsForInjection(...)
            if err != nil {
                s.logger.Warn(...)
            } else if len(secretsJSON) > 2 {
                s.createEphemeralSecretsSecret(ctx, workspaceID, secretsJSON)
            }
        }
    }

`api/internal/services/workspace/workspace_service.go:473` (pre-fix) —
`RestartWorkspace` had **no equivalent**. It bumped `spec.restartGeneration`
and returned. The controller observed the bump in `handleActive`, deleted
the pod, then `handleCreating` rebuilt it from scratch. The rebuild
read `workspace-secrets-<id>` from the apiserver — which by that point
was missing/empty — so the new pod started with no user secrets.

The Secret almost certainly didn't exist at all for these workspaces
(rather than being deleted by the restart). The `workspace-secrets-<id>`
Secret is created lazily by the API service: only on the activation
path, only when the user has an active session, and only when there
are bindings to inject. Workspaces created via the V1→V2 migration
path or ones that never went through a fresh `ActivateWorkspace` after
their first bind would never have had this Secret materialized — until
the next pod-create read the apiserver, found nothing, and silently
omitted the volume. The runtime image tooling change exposed this
because the restart was the first pod recreation for several of these
workspaces in days.

### Sequence diagram

    User clicks "Bind github SSH key"
        ↓
    POST /workspaces/:id/bindings
        ↓
    SecretService.SetBindings(ctx, ...)        ← writes user_secret_bindings row
        ↓
    SecretsHandler.pushSecretsToAgent          ← (a) EnsureSecretsManifest + (b) live push
        ↓
    workspace-secrets-<id> Secret exists       ← good

vs

    Operator runs:  kubectl patch ws ... --type merge -p '{"spec":{"restartGeneration":N}}'
        ↓
    Controller handleActive observes bump
        ↓
    deletePodByName + status=Creating
        ↓
    handleCreating rebuilds pod
        ↓
    pod_builder.buildCredentialSetupInit looks up workspace-secrets-<id>
        ↓
    NOT FOUND → user-secrets volume omitted
        ↓
    Pod comes up with no secrets.json          ← bad

The same outcome occurs if the user clicks a hypothetical "Restart"
button in the UI that hits `RestartWorkspace`: that endpoint also
skipped the manifest refresh.

---

## Fix shipped (Option B)

Three options were considered:

- **Option A (surgical):** copy the `EnsureSecretsManifest` block from
  `ActivateWorkspace` into `RestartWorkspace`. Smallest diff.
- **Option B (extract helper):** move the secret-refresh logic into a
  new `refreshEphemeralSecrets` method called from both `ActivateWorkspace`
  and `RestartWorkspace`. Eliminates the duplication that allowed the
  bug to exist.
- **Option C (reuse ActivateWorkspace):** make `RestartWorkspace` call
  `ActivateWorkspace`. Rejected: phase mismatch (Activate→Resume requires
  `Suspended`; Restart targets `Active`), and `enforceMaxActiveWorkspaces`
  side-effects.

Shipped **Option B**.

### `refreshEphemeralSecrets` contract

    func (s *Service) refreshEphemeralSecrets(ctx context.Context, userID, workspaceID string)

Single source of truth for "the workspace pod is about to be (re)built;
make sure its `workspace-secrets-<id>` matches the user's current
bindings." Behavior:

| Condition | Behavior |
|---|---|
| `secretInjector` is nil (test default) | no-op, return |
| `sessionID` missing in context | log Warn, return — admin/script restart path |
| `PrepareSecretsForInjection` errors | log Warn, return — preserve existing Secret |
| Returned payload `[]` (no bindings) | return — preserve existing Secret |
| Returned payload non-empty | call `createEphemeralSecretsSecret` (idempotent create-or-update) |

Failure paths are non-fatal by design: a stuck workspace is harder to
recover from than a stale-by-one-restart secret. The user can re-bind to
refresh; they cannot easily un-stick a workspace that refused to restart.

### Call sites

`workspace_service.go:840` — `ActivateWorkspace`. The previous inline
block (lines 840-849) is replaced by a single call.

`workspace_service.go:512` — `RestartWorkspace`. New call **before**
the `RestartGeneration++` bump, so the new Secret is in place before
the controller observes the bump and deletes the pod. The order matters:
if the bump-then-refresh order were used, a fast controller reconcile
could rebuild the pod against the stale Secret in the window before the
refresh writes.

### Why `sessionID` is required

`PrepareSecretsForInjection` (`pkg/secrets/injection.go:43`) calls
`GetDEK(ctx, sessionID)` to retrieve the per-session Data Encryption
Key. Without it, decryption cannot run.

This is intentional — and it means an admin/script-driven `kubectl patch`
on `restartGeneration` cannot refresh the Secret. That's a security
feature: a sandbox compromise or operator script cannot trigger fresh
plaintext materialization of user secrets without a live user session.
The cost is that operator restarts (like the one that caused this
incident) bypass the helper. The follow-up below addresses that.

### Tests added

`api/internal/services/workspace/workspace_service_test.go`:

| Test | Asserts |
|---|---|
| `TestRefreshEphemeralSecrets_NilInjector_NoOp` | nil injector path is safe; no clientset call |
| `TestRefreshEphemeralSecrets_NoSessionID_SkipsAndWarns` | missing sessionID → injector NOT called |
| `TestRefreshEphemeralSecrets_EmptyBindings_NoWrite` | `[]` payload → no Secret write (preserves existing) |
| `TestRefreshEphemeralSecrets_NonEmptyBindings_WritesManifest` | full happy path: Secret named correctly, payload exact, ephemeral label present |
| `TestRefreshEphemeralSecrets_PrepareFails_SkipsWriteCleanly` | Prepare error → no partial Secret left behind |
| `TestRestartWorkspace_RefreshesEphemeralSecrets` | regression: full RestartWorkspace path produces `workspace-secrets-<id>` |
| `TestRestartWorkspace_RefreshFailureDoesNotBlockBump` | refresh failure still allows `RestartGeneration++` to land |

A new `fixtureWithFakeClientset` test helper wraps the existing
`fixture` with `k8sfake.NewSimpleClientset` so tests can observe Secret
writes without a real apiserver.

All seven new tests pass; full `api/...` short suite green.

---

## Live recovery

### Affected workspaces

Two workspaces in the cluster have DB bindings but no K8s
`workspace-secrets-<id>` after the restart:

- `6d36952e-0cd5-42cb-9630-8f988b3e5f33` — 2 bindings (the user-reported case)
- `c3c8766d-1a53-434b-a713-5633669587f0` — 3 bindings

### Why I cannot restore them server-side

The fix prevents this from happening again. To restore the *currently*
broken state, a logged-in user session is required (see "Why sessionID
is required" above). Specifically:

1. Re-bind: user clicks the secret in the UI and re-runs the bind
   action. `SetBindings` re-fetches the secret, calls
   `pushSecretsToAgent` → `EnsureSecretsManifest` writes the K8s Secret,
   and the live `/v1/reload-secrets` push delivers the payload to the
   running agentd without a pod restart.

2. Re-activate: suspend, then resume. `ActivateWorkspace` (now using
   `refreshEphemeralSecrets`) re-emits the Secret. The pod restart
   picks it up.

Option 1 is preferred: no pod restart, immediate effect. Communicated
to the user.

### Verification of the fix in the existing pod

`6d36952e-...` pod was already running on the new image
(`sha-a20fb23`). The init container's `cp` no-op'd and produced no
`secrets.json`. That state persists until the next refresh — once the
user re-binds, the Secret will materialize and the live push will
deliver it.

---

## Follow-up filed: controller-side invariant

The API-side fix closes the most common path (API-mediated lifecycle
transitions), but a `kubectl patch` or operator script that bumps
`restartGeneration` directly bypasses this helper entirely. The
controller-side path that rebuilds pods (`pod_builder.buildCredentialSetupInit`)
silently omits the `user-secrets` volume when the K8s Secret is
NotFound, which is the failure mode that caused this incident.

**Proposed remediation** (not in this commit):

The controller should refuse to start a pod when the bindings table
says secrets should be present but the K8s Secret is empty/absent.
Two viable shapes:

1. **Block in `handleCreating`:** if `len(workspace.Status.HasBindings) > 0`
   (a new status field) and the K8s Secret is absent, transition to a
   `WaitingForSecrets` phase and surface a clear status message. The
   user re-binds (which triggers `EnsureSecretsManifest`) and the
   controller proceeds.

2. **Make the controller call back into the API:** the controller
   issues a server-to-server "refresh secrets for workspace X" call
   to the API, which uses an internal-only authentication path. This
   couples controller to API and adds a new auth surface — probably
   the wrong trade-off.

Option 1 is cleaner. The status field can be populated by the API at
bind time (set `HasBindings: true` whenever `SetBindings` produces a
non-empty set; clear it on empty). The controller uses it as a
required-secrets manifest.

This is filed as a follow-up rather than shipped now to keep this
change focused and reviewable.

---

## Files touched

    api/internal/services/workspace/workspace_service.go         (+78, -8)
    api/internal/services/workspace/workspace_service_test.go    (+183, -1)

No CRD, schema, or migration changes. Backward-compatible.

---

## Verification

- `go test ./internal/services/workspace/ -count=1`: PASS
- `go test ./... -count=1 -short` (full api): PASS
- `gofmt -l`, `goimports -l`, `golangci-lint run`: 0 issues

Live cluster verification will be the next bind/restart by an affected
user, with the fix deployed.

---

## Observations and lessons

1. **Lifecycle invariants need a single owner.** Three paths
   (`ActivateWorkspace`, `RestartWorkspace`, controller-driven
   pod-rebuild) all participate in "make sure the pod has its secrets",
   and each could enforce the invariant independently. Two of them
   silently didn't. Consolidating into `refreshEphemeralSecrets`
   removes the API-side duplication; the controller-side gap remains.

2. **Silent fallthrough is a bug-amplifier.** The init container's
   `if [ -f ... ]; then ... fi` and the controller's `errors.IsNotFound`
   branch both treat "no Secret" as "skip this step, continue happily."
   That's the wrong default for a security-relevant resource. Future
   change: explicit `WaitingForSecrets` phase that blocks pod start
   when the bindings table contradicts the K8s state.

3. **The runtime image tooling work was the trigger but not the cause.**
   The bug existed for any restart of an Active workspace whose
   `workspace-secrets-<id>` had never been materialized. Most workspaces
   in this cluster have lived through enough restarts that their state
   would have surfaced earlier — but several pre-Epic-10 workspaces had
   never been touched by the new path. The runtime image upgrade was
   the first time many of them were rebuilt.

4. **Test fixtures: `k8sfake.Clientset` integration is cheap and
   effective.** The new `fixtureWithFakeClientset` lets us assert on
   real K8s API objects (Secrets, in this case) without standing up a
   real apiserver. Worth using more broadly for any test that exercises
   K8s side-effects.
