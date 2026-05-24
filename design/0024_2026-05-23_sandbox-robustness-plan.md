# Sandbox Lifecycle Robustness — Plan

**Status:** Pre-implementation. No code yet.
**Author:** This session, before any robustness work.

This document plans 7 fixes to the sandbox lifecycle fragilities surfaced during cluster validation of worklog 0035 (CRD type consolidation). Every assumption I rely on is stated and validated up front. No fix is committed before plan review.

**Constraints (per user, post-plan-V1):**
- Backward compatibility is **NOT a constraint** — pre-launch project. Schema additions, renames, phase enum changes are all on the table.
- Tests must include unhappy paths and e2e/integration tests, not just unit tests with fakes.
- Commit + push after every logical unit of work.
- Follow the orchestrator workflow in `README-LLM.md` §"Multi-Agent Workflow": implement → skeptical validator → fixes → re-validate; loop until zero findings.

---

## 1. Validation methodology

Before each proposed fix, I list the assumptions it depends on. I validate each by reading code, reading the deployed YAML, or running a kubectl/curl probe against the live cluster. **Any assumption I cannot validate from sources I have access to becomes an explicit open question that blocks the corresponding fix until answered.**

I do not include cosmetic / "feels like it should be true" assumptions. If something is obvious from a single glance at code I cite the file:line.

---

## 2. The seven fixes (ranked by ROI)

These are the seven fixes I proposed verbally, repeated here so the plan is self-contained:

| # | Fix | One-line summary |
|---|-----|------------------|
| 1 | First-class restart API | `POST /sandboxes/:id/restart` increments a CRD spec field; controller does graceful pod recycle |
| 2 | Distinguish transient from terminal pod absence | "Pod went missing while Running" → first attempt = revert to Pending, only `Failed` after K retries |
| 3 | Sandbox watches its own credential Secret | Credential Secret update triggers sandbox reconcile → automatic pod recycle |
| 4 | Reject auto-workspace-creation when runtime requires creds | New `RuntimeEnvironment.spec.requiresCredentials`; sandbox creation 409s if creds missing |
| 5 | `POST /sandboxes/:id/retry` to recover Failed | Resets Failed → Pending; bounded by `spec.maxRetries` |
| 6 | Document the contract | `design/SANDBOX-LIFECYCLE.md` — phase state machine + recovery semantics |
| 7 | `local/test.sh` uses production-shape flow | Explicit workspace → set creds → sandbox; not auto-create |

---

## 3. Assumptions and validation evidence

### A1 — Sandbox phase enum is closed; adding values requires Go + YAML changes

**Source:**
- `pkg/apis/llmsafespace/v1/sandbox_types.go:151` declares the enum: `Pending;Creating;Running;Suspending;Suspended;Resuming;Terminating;Terminated;Failed`
- `pkg/crds/sandbox_crd.yaml` declares the same enum

**Validated:** ✓. Adding `Restarting` (fix #1) and using `Pending` for transient retry (fix #2) means: #2 requires no schema change (`Pending` is already in the enum); #1 requires both Go + YAML enum updates.

### A2 — Sandbox CRD has no Status.ObservedGeneration field today

**Source:**
- `grep -rn ObservedGeneration controller/internal/sandbox pkg/apis/llmsafespace/v1` returns hits **only** in `workspace_types.go`. Sandbox has none.
- `pkg/crds/sandbox_crd.yaml` status block confirmed not to declare `observedGeneration`.

**Validated:** ✓. Implication for fix #1: if we add a `spec.restartCounter`, the controller has no canonical "did I observe this revision yet?" field. Two options:
- (a) Add `Status.ObservedGeneration` — requires Go + YAML schema change but is the canonical Kubernetes pattern.
- (b) Use an annotation `llmsafespace.dev/restart-revision` and compare against `Status.LastRestartRevision`.

Recommendation: (a). Aligned with how Workspace already does it (`workspace_types.go:185`); zero novelty.

### A3 — Credentials are read once at pod init, not at runtime

**Source:** `controller/internal/sandbox/controller.go:649-653`:

```sh
if [ -f /mnt/secrets/credentials/provider-config ]; then
  cp /mnt/secrets/credentials/provider-config /sandbox-cfg/credentials
else
  echo '{}' > /sandbox-cfg/credentials
fi
```

The init container copies the Secret file to `/sandbox-cfg/credentials` once. Opencode reads that local file at process start. Subsequent updates to the K8s Secret (kubelet syncs ~60s) reach the cred-secret volume mount, but `/sandbox-cfg/credentials` is a static copy — not a symlink, not a bind mount.

**Validated:** ✓. Implication: fix #3 (sandbox watches its credential secret) **requires** triggering pod recycle on Secret change. Just letting kubelet sync the mounted Secret won't help — opencode never re-reads its config.

### A4 — Credentials Secret is owner-referenced to Workspace, not Sandbox

**Source:** `api/internal/services/workspace/workspace_service.go:396-401`:

```go
ownerRef := metav1.OwnerReference{
    APIVersion: "llmsafespace.dev/v1",
    Kind:       "Workspace",
    Name:       crd.Name,
    UID:        crd.UID,
}
```

**Validated:** ✓. Implication for fix #3: `Owns(&corev1.Secret{})` on the Sandbox controller (which exists at `controller.go:783`) doesn't fire for credential Secrets — those secrets aren't owned by Sandboxes. To trigger reconciliation on credential change we need `Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(mapSecretToSandboxes))` with a custom mapper that finds all Sandboxes with `spec.workspaceRef == secretWorkspaceID`.

### A5 — Sandbox controller already manages its own owned Secret (the password secret)

**Source:** `controller/internal/sandbox/controller.go:457-467, 783`. The `sandbox-pw-{name}` Secret is created and owner-referenced by the sandbox controller; `Owns(&corev1.Secret{})` causes reconciles when it changes.

**Validated:** ✓. Implication: fix #3's added `Watches(&corev1.Secret{}, ...)` is purely additive — the existing Owns() relationship is unchanged.

### A6 — Failed phase is currently terminal in code

**Source:** `controller/internal/sandbox/controller.go:121` — the phase dispatch:

```go
case common.SandboxPhaseTerminated, common.SandboxPhaseFailed, common.SandboxPhaseSuspended:
    // ... no further reconcile
```

`Suspended` has a recovery path (`POST /workspaces/:id/resume` → workspace controller resumes children). `Failed` has no exit. `Terminated` is correctly terminal (resource is being deleted).

**Validated:** ✓. Implication for fix #5: making Failed recoverable requires adding a transition out — either via API call (the proposed `POST /sandboxes/:id/retry`) or automatic backoff.

### A7 — `Pod not found` while phase is Running goes straight to Failed

**Source:** `controller/internal/sandbox/controller.go:191-216`:

```go
err := r.Get(ctx, ..., &corev1.Pod{})
if errors.IsNotFound(err) {
    if r.parentWorkspaceIsSuspending(ctx, sandbox) {
        sandbox.Status.Phase = common.SandboxPhaseSuspended
        ...
    }
    logger.Info("Pod not found, marking sandbox as failed")
    sandbox.Status.Phase = common.SandboxPhaseFailed
```

**Validated:** ✓ (and reproduced live: `sb-lw482` cycle in this session, pod force-deleted, sandbox went to `Failed`). Implication for fix #2: this is the exact branch to soften.

### A8 — `Pending` phase triggers pod creation; `Creating` waits for it to come up

**Source:** `controller/internal/sandbox/controller.go:142-178`. `handlePendingSandbox` builds and creates the pod. `handleCreatingSandbox` (called from line 313) waits for it to reach Running.

**Validated:** ✓. Implication for fix #2: reverting `Running` → `Pending` correctly triggers pod recreation via the existing pending handler. No new state-machine logic needed.

### A9 — There's no place to count retries in current Sandbox status

**Source:** `pkg/apis/llmsafespace/v1/sandbox_types.go` — SandboxStatus has phase, conditions, podName, podNamespace, startTime, endpoint, resources, podIP, lastActivityAt. No retry counter.

**Validated:** ✓. Implication for fix #2 and #5: we need a new status field `RestartCount int32` (or similar) added to SandboxStatus. Schema change in both Go + YAML.

### A10 — RuntimeEnvironment has no `requiresCredentials` field today

**Source:** `pkg/apis/llmsafespace/v1/runtimeenvironment_types.go` — fields are Image, Language, Version, Tags, PreInstalledPackages, PackageManager, SecurityFeatures, ResourceRequirements. No requiresCredentials.

**Validated:** ✓. Implication for fix #4: this is a new field requiring Go + YAML schema change.

### A11 — Workspace creation accepts only `runtime` from `CreateWorkspaceRequest`, not credentials

**Source:** `pkg/types/types.go::CreateWorkspaceRequest` has Name, Runtime, StorageSize, StorageClass, Labels. No credentials.

**Validated:** ✓. Implication: there is no atomic "create workspace with credentials in one call." Set credentials always lives in a separate `PUT /workspaces/:id/credentials`. This is good for separation of concerns but means fix #4's "reject sandbox-create when runtime needs creds and workspace has none" still needs the explicit `PUT /workspaces/:id/credentials` step.

### A12 — Force-deleting a pod produces no graceful Terminating window

**Source:** Live cluster reproduction in this session. `kubectl delete pod ... --force --grace-period=0` deletes immediately; the sandbox reconcile after the deletion has no transient pod to observe.

**Validated:** ✓. Implication: any fix that depends on "graceful delete" must explicitly **not** use `--force --grace-period=0`. Operator documentation must call this out.

### A13 — controller-runtime supports cross-resource Watches with custom EventHandler

**Source:** Standard controller-runtime pattern, used throughout K8s ecosystem. `sigs.k8s.io/controller-runtime/pkg/handler.EnqueueRequestsFromMapFunc` exists.

**Validated:** ✓ (well-known framework feature; verified by grep `import.*controller-runtime/pkg/handler` in repo and grep usage patterns in `controller-runtime` docs). Implication for fix #3: standard pattern, low risk.

### A14 — `local/test.sh` Test 6 already does explicit-workspace flow when credentials are present

**Source:** `local/test.sh:131-183`. Workspace `e2e-workspace` is created via raw `kubectl apply` (not the API), credentials set, then sandbox created with `workspaceRef: ${WORKSPACE_NAME}`.

**Validated:** ✓ — but uses kubectl apply for the workspace, not the API. Implication for fix #7: flip to API-only flow (`POST /workspaces`, then `PUT /credentials`, then `POST /sandboxes`) so the script exercises the production path that any real client would take.

### A15 — opencode does not currently support config reload at runtime

**Source:** Upstream `anomalyco/opencode` — third-party binary pulled into the runtime image. No SIGHUP / inotify / config reload logic in our code (none in `runtimes/`). Adding it requires upstream contribution + version bump.

**Validated:** ✓. Implication: we cannot do "live credential reload without pod recycle" via #3a (SIGHUP). All credential changes require pod recycle. Fix #3 must trigger a pod recycle in the controller, not "reload signals."

---

## 4. Open questions (blockers if not answered before implementation)

### Q1 — How do existing in-flight prompts behave when their pod is being recycled?

**Why it matters:** Fix #1 (restart API) and fix #3 (auto-recycle on credentials change) both delete and recreate the sandbox pod. Any HTTP request from the proxy to opencode that's in flight at delete-time will fail. SSE streams will close.

**What I cannot validate from code:**
- Whether the proxy retries failed requests transparently
- Whether session state (which lives in `/workspace` PVC) survives across pod restarts cleanly — this is **probably yes** because opencode persists sessions to disk and the PVC is workspace-scoped, but I haven't observed it.

**Probe to validate:** Restart-during-prompt test. Send a prompt, immediately call `POST /sandboxes/:id/restart` (after the API exists). Expect: prompt fails with a clear error, **and** a fresh prompt to the same session ID afterward sees the prior conversation in history.

**Recommendation:** Add this probe to `local/test.sh` Test 6 as part of fix #7. Implementing fixes #1 and #3 without this probe means we'd be hand-waving over the in-flight-request question.

### Q2 — Should restart be cluster-controller-driven or API-server-driven?

Two options for fix #1:
- **(a)** API service deletes the pod directly via clientset; controller observes and recreates (current pattern, but leaks pod knowledge into the API service).
- **(b)** API service patches `Sandbox.spec.restartCounter`; controller deletes and recreates the pod when it observes the change.

**Recommendation:** (b). Cleaner separation, declarative, idempotent (PUT-with-same-counter is a no-op), works the same way as `kubectl rollout restart deployment` (which patches `spec.template.metadata.annotations[kubectl.kubernetes.io/restartedAt]`).

### Q3 — What does `RestartCount` count?

Two definitions:
- **(a)** Total number of pod restarts triggered by user-initiated restart API + auto-recycles (#3) over the lifetime of the sandbox
- **(b)** Just the auto-recovery counter for fix #2 (incremented when transient-pod-loss recovery happens; resets when sandbox stays Running for N minutes)

These are different things. (a) is a metric, (b) is a circuit breaker.

**Recommendation:** Track them as separate fields:
- `Status.RestartCount int32` — cumulative, never resets, useful for debugging/metrics
- `Status.TransientFailureCount int32` — resets on stable Running > 5 min, used for fix #2's "K retries before declaring Failed" logic, default K=3

### Q4 — Is `Restarting` phase needed at all, or does `Pending → Creating → Running` suffice?

Fix #1 triggers a graceful pod delete + recreate. The phases would be: `Running → ??? → Pending → Creating → Running`.

**Two options:**
- **(a)** Reuse `Pending` for the restart-pending state. UI distinguishes via condition `Reason=Restarted`.
- **(b)** Add `Restarting` to the phase enum. Phase becomes `Running → Restarting → Creating → Running`. UI gets a clear signal without inspecting conditions.

**Re-evaluation now that backward-compat is not a constraint (per user direction post-V1):** schema change is free. The trade-off becomes purely about clarity vs surface area.

- (a) keeps the enum at 9 values; reasons live in conditions where they belong; precedent set by Kubernetes Pod (`Pending` covers many sub-states distinguished by conditions).
- (b) adds a clear top-level signal, slightly bigger enum, slightly more API surface.

**Recommendation:** **(a)** — even with backward-compat off the table, the conditions-carry-reasons pattern is more idiomatic to controller-runtime / Kubernetes. Adding `Restarting` doesn't add value the conditions can't carry; it just adds another value clients have to handle. Stay with (a).

This drops fix #1's "phase enum update" from the changeset.

---

## 5. Implementation plan

### Phase ordering and atomicity

Fixes have dependencies. Order:

```
6 (docs)            ← independent, low effort, write first to lock contract
2 (transient → Pending)   ← depends on A7, A8, A9; needs Status.RestartCount
1 (restart API)     ← depends on A2 (ObservedGeneration), shares Status.RestartCount with #2
3 (watch credential secret) ← depends on A4 (mapper), A13; uses #1's restart machinery
5 (retry Failed)    ← depends on Q3 (RestartCount semantic); reuses #1's restart machinery
4 (reject create when creds missing) ← depends on A10 (requiresCredentials); independent of #1-3
7 (test.sh refactor) ← validates #1, #2, #3, #4, #5 end-to-end
```

This produces 6 logical commits (two for fix #2 if Status.RestartCount is added separately, but I'd combine).

### Per-fix implementation sketch

#### Fix #6 — `design/SANDBOX-LIFECYCLE.md`

**Work:** Write a markdown doc with:
- Phase state machine diagram (current 9 phases + transitions)
- What triggers each transition (controller branch citations)
- Recovery semantics: which phases are recoverable, by what API call
- The "do not force-delete pods" rule with the failure mode
- Credential-rotation flow (PUT credentials → restart sandbox → opencode picks up)

**Schema impact:** None.
**Tests:** None (doc).
**Risk:** Low — doc-only.

#### Fix #2 — Transient pod absence reverts to Pending; only Failed after K retries

**Work:**
1. Add `Status.RestartCount int32` and `Status.TransientFailureCount int32` to `pkg/apis/llmsafespace/v1/sandbox_types.go`. Update `pkg/crds/sandbox_crd.yaml` to match.
2. In `controller/internal/sandbox/controller.go:191-216`, before marking Failed, check `Status.TransientFailureCount`. If `< maxTransientFailures` (constant = 3), revert to Pending and increment counter. Log structured event. After 5 min stable Running, reset counter.
3. Add an annotation `llmsafespace.dev/transient-retry-after: <RFC3339>` for backoff scheduling, or use `ctrl.Result{RequeueAfter: backoff}`.

**Tests:**
- Unit: pod-not-found while Running, parent workspace not suspending, count=0 → phase becomes Pending, counter=1
- Unit: pod-not-found while Running, count=2 (would be third strike) → phase becomes Failed, counter=3
- Unit: pod stable Running for 5 min → counter resets

**Schema impact:** Two new int32 fields in Sandbox status. Backward compatible (zero values are sensible defaults).
**Risk:** Medium — touches the most sensitive reconcile branch.

#### Fix #1 — `POST /sandboxes/:id/restart` API + controller observation

**Work:**
1. Add `Spec.RestartRevision int64` to Sandbox CRD (Go + YAML).
2. Add `Status.ObservedGeneration int64` to Sandbox CRD (matches Workspace; explicit per Q2).
3. New API route `POST /api/v1/sandboxes/:id/restart` in `api/internal/server/router.go` that calls a new `SandboxService.RestartSandbox(ctx, userID, id)`.
4. `RestartSandbox` patches `Spec.RestartRevision = max(current, time.Now().UnixNano())` (monotonically increasing).
5. Controller: when reconciling, if `Spec.RestartRevision > Status.ObservedRestartRevision`, gracefully delete the pod and update status. Pod recreation goes through the existing Pending → Creating → Running path.
6. Set conditions: `RestartRequested = True (reason: UserInitiated, message: ...)` until restart completes.

**Tests:**
- API: POST /restart with valid token → 202 Accepted; sandbox spec.restartRevision updated
- API: POST /restart with foreign user → 403/404 (existing authorization)
- Unit (controller): RestartRevision increased → pod gracefully deleted (DeleteOptions.GracePeriodSeconds = pod's terminationGracePeriodSeconds, default 30s)
- Unit: ObservedRestartRevision updated only after pod recreated and Running

**Schema impact:** Two new fields (one spec, one status).
**Risk:** Medium — new API surface, controller-side state machine addition. Shares `Status.RestartCount` with fix #2 (increment on every observed restart, whether user-initiated or auto).

#### Fix #3 — Sandbox controller watches credential Secret; triggers restart on change

**Work:**
1. In `SetupWithManager`, add `Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(mapCredSecretToSandboxes))`.
2. The mapper: given a Secret, extract workspace ID from name (`workspace-creds-{ID}`), list all Sandboxes with `spec.workspaceRef == ID`, return their NamespacedNames.
3. In reconcile, detect "the credential secret data hash changed since last observation" via a `Status.CredentialSecretHash string` field. If changed and pod is currently Running: increment Spec.RestartRevision (re-uses fix #1's machinery).

**Tests:**
- Unit: Secret update → controller called for each attached sandbox
- Unit: Hash unchanged → no restart triggered
- Unit: Hash changed → RestartRevision incremented, pod gracefully deleted

**Schema impact:** One new status field (CredentialSecretHash, string).
**Risk:** Medium — new cross-resource Watch; care needed to avoid reconcile storms (only react to data changes, not metadata).

#### Fix #5 — `POST /sandboxes/:id/retry` to recover Failed

**Work:**
1. Add `Spec.MaxRetries int32` (default 3) to Sandbox CRD.
2. New API route `POST /api/v1/sandboxes/:id/retry`. Service method checks current phase == Failed; if so, clears Status.Phase (reset to Pending), clears failure conditions, increments `Status.RestartCount`. Bounded by `Spec.MaxRetries`.
3. Controller: existing Pending → Creating → Running path takes over.

**Tests:**
- Unit: Failed sandbox + retry → Pending
- Unit: Running sandbox + retry → 409 Conflict
- Unit: After MaxRetries reached → 409 with explanation

**Schema impact:** One new spec field.
**Risk:** Low — orthogonal to other lifecycle logic.

#### Fix #4 — Reject auto-workspace-creation when runtime requires creds

**Work:**
1. Add `Spec.RequiresCredentials bool` (default false) to RuntimeEnvironment CRD (Go + YAML).
2. In `api/internal/services/sandbox/sandbox_service.go:CreateSandbox`, after looking up the runtime: if `runtime.Spec.RequiresCredentials == true` and `req.WorkspaceRef == ""`: return 409 "runtime X requires credentials; create workspace explicitly, set credentials, then create sandbox with workspaceRef".
3. Same check when `WorkspaceRef != ""` but the workspace has no `workspace-creds-{id}` Secret: 409 "set credentials on workspace before attaching sandbox."
4. Update existing seeded RuntimeEnvironment(s) that need credentials (e.g. any LLM-using runtime) to set `requiresCredentials: true` in helm values.

**Tests:**
- Unit: runtime.requiresCredentials=true, workspaceRef="", → 409
- Unit: runtime.requiresCredentials=true, workspaceRef set, secret exists → 201
- Unit: runtime.requiresCredentials=true, workspaceRef set, no secret → 409
- Unit: runtime.requiresCredentials=false → 201 (existing behavior)

**Schema impact:** One new spec field on RuntimeEnvironment.
**Risk:** Low-medium. The user-facing behavior change is the main risk: existing clients that auto-create workspaces would start getting 409 if their runtime is marked `requiresCredentials`. Mitigation: only mark runtimes that genuinely require creds (i.e., not `base`).

#### Fix #7 — `local/test.sh` and CI use production-shape flow

**Work:**
1. Replace the kubectl-apply workspace creation with `POST /api/v1/workspaces`.
2. Insert `PUT /api/v1/workspaces/:id/credentials` between workspace creation and sandbox creation.
3. Add a "restart sandbox + verify session continuity" probe (validates fix #1 + Q1 + #3).
4. Add a "delete pod via kubectl, verify recovery, not Failed" probe (validates fix #2).
5. Add a "set credentials after pod is up, verify auto-recycle and creds applied" probe (validates fix #3).

**Tests:** The script *is* the test. All probes gate on LLM creds (existing pattern from worklog 0031).

**Schema impact:** None.
**Risk:** Low.

---

## 6. Test strategy summary

Per `README-LLM.md` Rule 0 + Testing Requirements, every fix delivers all three test layers:

- **Unit tests:** happy + unhappy path table-driven, in `_test.go` next to the code.
- **Integration tests:** for controller-side changes, use `sigs.k8s.io/controller-runtime/pkg/envtest` to spin up a real apiserver + etcd + the controller under test. For API-side, use the existing `httptest` + fake k8s client patterns with realistic wiring.
- **E2E tests:** new probes added to `local/test.sh` that hit the live cluster (or the kind cluster in CI). Gate on `LLM_BASE_URL/LLM_API_KEY/LLM_MODEL` for prompt-dependent probes; non-LLM probes always run.

| Fix | Unit tests (happy + unhappy) | Integration tests (controller + API) | E2E probes (test.sh) |
|-----|------------------------------|--------------------------------------|-----------------------|
| 6 (docs) | n/a | n/a | n/a |
| 2 (transient → Pending) | 3 happy + 4 unhappy: pod found, pod missing first time, pod missing 3rd time, counter reset, status update conflict, parent workspace suspending wins, force-delete vs graceful-delete | envtest: graceful pod delete, controller observes, sandbox stays in Pending then back to Running | `kubectl delete pod` (graceful, no `--force`) → assert sandbox returns to Running, never enters Failed |
| 1 (restart API) | 4 happy + 5 unhappy: API auth, foreign user 404, idempotent same-revision no-op, controller observation latency, restart on terminated sandbox 409, restart on terminating sandbox 409, ObservedGeneration race, status update conflict | envtest: PATCH spec.restartRevision → controller deletes pod → recreates → sets Status.ObservedRestartRevision; e2e via httptest: POST /restart auth + 202 + foreign 404 | restart-during-prompt: prompt + immediate POST /restart → assert prompt fails OR completes; new prompt sees prior history; sessionID stays the same |
| 3 (watch cred secret) | 4 happy + 4 unhappy: hash unchanged, hash changed, secret deleted, multiple sandboxes per workspace fan-out, mapper handles non-cred secrets, mapper handles missing workspace, reconcile burst suppression | envtest: PUT credentials → secret update → mapper enqueues sandboxes → restart triggered for each | set-creds-after-create probe: create sandbox → set creds → assert pod recycles automatically → prompt succeeds |
| 4 (requiresCredentials) | 4 happy + 4 unhappy: requires=true + workspaceRef + secret → 201, requires=true + auto-create → 409, requires=true + workspaceRef + no secret → 409, requires=false + auto-create → 201, requires=true + foreign workspace → 404 (not 409, don't leak existence), requires=true + secret deleted between check and create → handled, runtime not found → 400 | httptest e2e: full POST /sandboxes flow exercising all 4 paths against fake k8s | mark `python` runtime requiresCredentials=true, attempt create without creds → 409; with creds → 201 |
| 5 (retry Failed) | 3 happy + 4 unhappy: Failed → Pending, Running 409, Terminating 409, MaxRetries reached 409, retry on terminated 409, foreign user 404, optimistic concurrency conflict | httptest e2e: POST /retry against a controller-failed sandbox; verify state machine | force-failure (intentionally bad runtime image) → assert Failed → POST /retry → controller picks up → eventually Running after fixing image |
| 7 (test.sh) | (script changes; assertions ARE the test) | n/a | full flow + all 5 probes above + restart-during-prompt + force-delete-recovery + auto-recycle-on-creds + retry-from-failed |

**Coverage targets per fix:**
- Unit: ≥7 cases, ≥3 unhappy
- Integration: ≥1 envtest scenario for controller-side fixes, ≥1 httptest scenario for API-side
- E2E: ≥1 probe in test.sh exercising the live cluster wiring

Total across all fixes: ~30 unit tests, ~5 envtest scenarios, ~3 httptest scenarios, 5 e2e probes.

---

## 7. Per-fix workflow (orchestrator pattern)

Per `README-LLM.md` §"Multi-Agent Workflow", every fix follows the same flow:

1. **Plan + assumptions** — already done globally in §3 of this doc; per-fix specifics in §5.
2. **Write tests first (TDD)** — unit, integration (envtest/httptest), e2e (test.sh probe). Tests must fail.
3. **Implement** — minimal code to pass.
4. **Skeptical validator pass** — re-read the change with the questions from `README-LLM.md` §Multi-Agent Workflow Step 3:
   - Are stated assumptions actually true?
   - Is the code wired into the live request path?
   - Test coverage: happy + unhappy + e2e present?
   - Engineering principles (Rule 4)?
   - Spirit AND letter of the ask?
   - Tech debt (TODOs, hacks, dead code)?
5. **Triage findings** — for each, verify it's real (re-read code, re-run test). False alarms documented.
6. **Remediate real findings** — each fix gets a regression test.
7. **Re-validate** — back to step 4. Loop until zero findings.
8. **Build + test + lint:** `go build ./...`, `go vet ./...`, `go test -race -count=1 -timeout 180s ./...`, `golangci-lint run`. All clean.
9. **Commit + push** — descriptive message referencing the fix number.
10. **Cluster validation** — deploy the new sha tag to `admin@home-kubernetes` and run the e2e probe added in this fix.

The full validation harness (#7 — `local/test.sh` updates) lands last because it depends on every prior fix's behaviour.

---

## 8. What I'm explicitly *not* doing

- **Not** patching upstream opencode for SIGHUP/config-reload (the cleaner fix #3a). Out of scope; requires upstream contribution and a version bump.
- **Not** introducing `Restarting` as a new phase value (per Q4). Conditions carry the signal.
- **Not** unifying `RestartCount`, `TransientFailureCount` into one counter. They mean different things (per Q3).
- **Not** adding automatic-recovery-on-Failed (background reconciler retries Failed without API call). User-driven retry only — keeps the policy clear.
- **Not** changing the Workspace credential model (Secret per workspace, owner-referenced to workspace). It works.

---

## 9. Decision asks before implementation

I cannot validate Q1 without code that doesn't exist yet (the restart API). I'd like an answer to all four open questions in §4 before coding. My recommendations are:

| Q | Recommendation | Defaults if no answer |
|---|----------------|----------------------|
| Q1 | Add the restart-during-prompt probe to test.sh in fix #7; document the in-flight semantics in fix #6 | Probe added; doc says "restart fails any in-flight request; session state in PVC survives" |
| Q2 | (b) — patch spec.restartRevision, controller observes | Will use (b) |
| Q3 | Two separate fields: cumulative `RestartCount` and resetting `TransientFailureCount` | Will use this |
| Q4 | No `Restarting` phase; conditions only | Will skip the phase enum change |

I will proceed with these recommendations as defaults if the user does not push back.

---

## 10. Estimated commit count and order

1. `Fix #6: design/SANDBOX-LIFECYCLE.md (doc)` — independent, sets contract
2. `Fix #2: Sandbox.Status.{RestartCount,TransientFailureCount}; transient pod absence reverts to Pending` — schema + controller logic
3. `Fix #1: POST /sandboxes/:id/restart; Sandbox.Spec.RestartRevision; Sandbox.Status.ObservedGeneration` — API + controller observation
4. `Fix #3: Sandbox controller Watches credential Secret; auto-restart on change` — uses #1's restart machinery
5. `Fix #5: POST /sandboxes/:id/retry; Sandbox.Spec.MaxRetries` — Failed → Pending recovery
6. `Fix #4: RuntimeEnvironment.Spec.RequiresCredentials; sandbox-create 409s when missing` — independent
7. `Fix #7: local/test.sh production-shape flow + 3 robustness probes` — validation harness

Each commit:
- All tests pass (`go test -race -count=1 ./...`, `go vet`, `golangci-lint`)
- Self-contained: schema changes, code changes, and tests in the same commit
- Cluster-validated against `admin@home-kubernetes` for fixes #2, #3, #4, #5 (deploy + run the probe added in fix #7)

Total session work: 7 commits, ~17 new unit tests, 6 new integration probes, 4 schema additions to Sandbox, 1 to RuntimeEnvironment, 1 design doc.
