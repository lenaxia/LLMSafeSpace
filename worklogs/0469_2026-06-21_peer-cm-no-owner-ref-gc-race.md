# Worklog: Peer ConfigMap Owner-Ref Removal — Win the GC vs Kubelet Race

**Date:** 2026-06-21
**Session:** Live testing of PR #334 (CM-clear in handleDeletion) revealed the fix didn't actually propagate to the router. Investigated, discovered the GC-vs-kubelet race, and shipped PR #335 to remove the CM's ownerReference entirely.
**Status:** PR #335 open and reviewed. Live verification pending merge + deploy.

---

## Objective

PR #334 added `syncPeerConfigMap(..., []PeerEntry{})` in `handleDeletion` before removing the finalizer. The intent was that kubelet would propagate the cleared content to the relay-router pod's volume mount before owner-reference GC removed the CM.

This session: verify the fix works in production. If it doesn't, find out why and fix it properly.

---

## Findings

### The fix from PR #334 didn't work in production

Created an InferenceRelay CR, waited for the relay to become healthy, deleted the CR, then checked router metrics for 13+ minutes:

```
relay_router_active_streams{relay="i-081033f73a10171fb"} 0
relay_router_relay_healthy{relay="i-081033f73a10171fb"} 0
relay_router_relay_egress_bytes{relay="i-081033f73a10171fb"} 0
```

The orphan relay was still present in the router's in-memory fleet, even though the CR was deleted, the controller logged successful deletion, the EC2 instance was terminated, and the CM was deleted by GC.

### Root cause: GC vs kubelet race

Tracing the timeline:
1. Controller writes `{"relays":[]}` to CM (T+0)
2. Controller removes finalizer on InferenceRelay (T+0.001)
3. Kubernetes GC observes the InferenceRelay is gone, starts deleting the CM (T+~0.5s)
4. CM is deleted from etcd
5. kubelet eventually re-syncs the volume mount — but the CM is already gone, so the existing file is left in place (kubelet's optional-CM volume semantics)
6. Router's `pollPeerConfig` keeps reading the same stale file forever

The window between CM-update (step 1) and CM-deletion (step 4) is typically too short for kubelet's atomic-writer to propagate the new content. kubelet's `volumeManager.reconcile()` runs every minute by default but is also event-driven on CM updates — but a delete-then-update race in the same second usually loses to the deletion.

### The proper fix: don't use ownerReference

Removing the ownerReference entirely eliminates the GC trigger. The controller manages the CM lifecycle directly:

- Created when first relay provisioned
- Updated as fleet changes
- Set to `{"relays":[]}` when last relay destroyed (PR #334 logic, preserved)
- **Persists across CR deletions with empty content** — no GC, no race
- A subsequent CR creation re-uses the same CM in place

This is strictly simpler than the prior design (which conflated K8s-managed cleanup with explicit controller-managed cleanup).

---

## PR #335

Removed the `owner` parameter from `syncPeerConfigMap` (it was unused after the ownerRef-set call was removed). Removed `Owns(&corev1.ConfigMap{})` from `SetupWithManager` since the watch wouldn't fire without owner-ref anyway. The Update path explicitly strips any pre-existing `OwnerReferences` so clusters that ran an earlier controller version (with PR #334's ownerRef-on-create) self-heal on the first reconcile after upgrade.

Tests:
- `TestSyncPeerConfigMap_NoOwnerRef` pinning the absence of ownerRef on Create.
- `TestSyncPeerConfigMap_StripsExistingOwnerRef` pinning the upgrade self-heal path.
- `TestSyncPeerConfigMap_NoOpWhenIdenticalAndNoOwnerRef` pinning the steady-state perf optimization.
- `TestHandleDeletion_PeerCMHasNoOwnerRef` pinning the end-to-end deletion lifecycle.
- Updated existing test callers to drop the now-removed owner argument.

Reviewer findings addressed across three commits:
1. Stale comment in `handleDeletion` (still mentioned owner-reference deletion) → corrected (commit 2)
2. Dead `owner` parameter — removed entirely from the function signature (commit 2)
3. Stale test comments → updated (commit 2)
4. Missing worklog → this file added (commit 2)
5. Update path didn't strip pre-existing ownerRefs → strip-on-update + 2 new tests (commit 3)

---

## Key Decisions

- **Removed `Owns(&corev1.ConfigMap{})`** in SetupWithManager rather than keeping it as a no-op. Reflects the real lifecycle: the CM is not Kubernetes-owned by the InferenceRelay anymore; the controller manages it via reconcile. If an operator manually edits the CM, the next reconcile cycle will overwrite their edit.
- **Did NOT introduce a watch on the CM directly**. The `pollPeerConfig` loop in the router runs every 5s and the controller's reconcile interval is 30s for healthy fleets — fast enough that direct CM watch isn't worth the extra reconcile traffic.
- **Did not delete the CM when no relays exist** (stays at `{"relays":[]}`). Lifecycle simplicity: CM exists when controller has been responsible for the fleet at any point. If you want to remove it, delete the relay-router-peers ConfigMap manually after deleting the last InferenceRelay CR.

---

## Adversarial Self-Review

For PR #335:
1. **Does removing `Owns(...)` break anything?** Only the watch-on-owned-CMs would fire reconciles. With no owner-ref, the watch wouldn't fire anyway. The reconciler is now solely event-driven on InferenceRelay changes (For). Confirmed via `controllerutil.SetControllerReference` removed from the only path that set it.
2. **Multiple controllers?** The lifecycle assumes a single controller manages the CM. If two controllers were running (split-brain), they'd both try to update the CM. Mitigated by leader election in the controller manager (`enable-leader-election=true` in the chart).
3. **Operator deletes the CM manually?** The next reconcile cycle's `syncPeerConfigMap` would Create it fresh. Recovery is automatic.
4. **CM update fails during reconcile (not just deletion)?** Existing error handling: `syncPeerConfigMap` returns the error, reconciler logs and requeues. Unchanged.

---

## Blockers

None. PR #335 reviewed APPROVE pending the worklog accuracy fix in this entry. After merge: CI build → deploy → live re-verification.

---

## Tests Run

- `go test -timeout 60s ./controller/internal/relay/` — pass
- `go test -timeout 240s -short ./...` — all green
- `make lint` — 0 issues
- Live verification pending next deploy

---

## Next Steps

1. Wait for PR #335 CI to publish images
2. Deploy with new tag, verify the orphan-cleanup test:
   - Apply InferenceRelay CR
   - Wait for relay healthy
   - Delete CR
   - Within ~30s, router metrics should no longer list the relay
3. If verified, mark Tests 8 and the orphan-cleanup observation in worklog 0468 as fully closed.
4. Final cleanup: terminate any orphan EC2 instances, document final cluster state, close the worklog 0464 testing thread.

---

## Files Modified

| File | Change |
|---|---|
| `controller/internal/relay/router_configmap.go` | Removed `owner` parameter + `controllerutil.SetControllerReference` call; Update path now strips pre-existing OwnerReferences and includes ownerRef state in the no-op check |
| `controller/internal/relay/reconciler.go` | Updated `syncPeerConfigMap` callers (no owner arg); removed `Owns(&corev1.ConfigMap{})` from SetupWithManager; updated handleDeletion comment |
| `controller/internal/relay/coverage_test.go` | Updated test callers; renamed `TestSyncPeerConfigMap_WithOwnerRef` → `TestSyncPeerConfigMap_NoOwnerRef`; added `TestSyncPeerConfigMap_StripsExistingOwnerRef` and `TestSyncPeerConfigMap_NoOpWhenIdenticalAndNoOwnerRef` |
| `controller/internal/relay/reconciler_test.go` | Updated test callers (removed nil owner argument) |
| `controller/internal/relay/wgremoval_test.go` | Updated test caller |
| `controller/internal/relay/deletion_clears_peers_test.go` | Updated comments to reflect no-ownerRef lifecycle; added `TestHandleDeletion_PeerCMHasNoOwnerRef` |
| `worklogs/0469_2026-06-21_peer-cm-no-owner-ref-gc-race.md` | This file |
