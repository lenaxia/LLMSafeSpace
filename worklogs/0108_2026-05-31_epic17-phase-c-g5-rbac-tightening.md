# 0107 — Epic 17 Phase C/G5: RBAC tightening (8 findings)

**Date:** 2026-05-31
**Author:** mikekao + opencode (sonnet)
**Phase:** Epic 17 Phase C, RBAC cluster (G5 + F1.3.1-F1.3.7)
**Status:** Code-level fix complete; awaiting CI build + live cluster re-pentest

---

## Summary

Closes **8 findings** in one PR by rewriting the chart RBAC:

- **G5 (High)** — Controller SA cluster-wide Secret access by default.
- **F1.3.1 (High)** — Controller granted unused services + configmaps + runtimeenvironments/status.
- **F1.3.2 (High)** — `coordination.k8s.io/leases` cluster-wide; only release-ns needed.
- **F1.3.3 (Medium)** — secrets/pods cluster-wide vs `controller.watchNamespaces` intent.
- **F1.3.4 (Medium)** — `runtimeenvironments` full CRUD granted to API SA, unused.
- **F1.3.5 (Medium)** — `pods/log` granted to API SA, unused.
- **F1.3.6 (High)** — `pods/exec` in workspace ns extends to non-sandbox pods.
- **F1.3.7 (Low)** — `storageclasses` grant degrades silently in namespace mode.

Plus duplicates RT-6.2 (Controller SA cluster-bound by default) and
RT-6.16 (Helm default rbac.scope=cluster) close as duplicates of G5.

---

## Stated assumptions (validated)

- **A1** — Operator-supplied `rbac.scope: cluster` is opt-in; default
  changed to `namespace`. (Set in values.yaml.)
- **A2** — Resources actually used by the controller, audited:
  workspaces (CRD), pods, secrets, persistentvolumeclaims,
  networkpolicies, events, leases, storageclasses (read-only).
  **Unused:** services, configmaps, runtimeenvironments/status. (Audit
  via `grep -rE 'corev1\.Service\{|corev1\.ConfigMap\{' controller/`)
- **A3** — API SA actually uses: workspaces (+/status), secrets, pods
  (read), pods/exec, events. **Unused:** runtimeenvironments, pods/log.
  (Audit via grep on api/.)
- **A4** — `storageclasses` is cluster-scoped per k8s — Roles cannot
  reference it; pre-fix grant inside the combined `$controllerRules`
  silently dropped in namespace mode. (Validated: k8s docs +
  `kubectl explain storageclass`.)

---

## Changes

### Chart RBAC

1. `charts/llmsafespace/templates/rbac.yaml` rewritten:
   - **Workspace-namespace Role** (always created): pods, secrets,
     PVCs, NetworkPolicies, events, workspaces CRD. Bound in the
     workspace namespace. Replaces the old combined `$controllerRules`.
   - **Release-namespace Role** for leader-election leases + events.
     Replaces the cluster-wide leases grant (F1.3.2).
   - **StorageClass-reader ClusterRole** (always — storageclasses are
     cluster-scoped). Read-only. Replaces the silently-dropped grant
     (F1.3.7).
   - **Cluster ClusterRole** (opt-in via `rbac.scope: cluster`):
     ONLY workspaces (+ subresources) and runtimeenvironments. NO
     pods/secrets/PVCs at cluster scope (F1.3.3).
   - API Role: removed `runtimeenvironments` (F1.3.4) and `pods/log`
     (F1.3.5).

2. `charts/llmsafespace/values.yaml`: `rbac.scope` default flipped
   from `"cluster"` to `"namespace"` (G5). Comment updated to
   document the change and migration path.

### Application-layer F1.3.6 mitigation

3. `api/internal/handlers/terminal.go`:
   - `bridgeExec` now takes `workspaceID` and verifies the target
     pod's `llmsafespace.dev/workspace` label matches before exec'ing.
   - Standard k8s RBAC cannot constrain pods/exec by name/label;
     this is the application-layer guard.

### Tests

4. `charts/llmsafespace/chart_test.go`: 9 new TestG5_/TestF13x tests:
   - `TestG5_DefaultIsNamespaceScope` — no controller-cluster ClusterRole by default.
   - `TestG5_ClusterScopeOptInRendersClusterRole` — opt-in works AND no pods/secrets in it.
   - `TestF131_ControllerDoesNotGrantUnusedResources` — services + configmaps absent.
   - `TestF132_LeasesAreNamespaceScoped` — leases not in any ClusterRole.
   - `TestF133_ControllerSecretsAreNamespaceScoped` — secrets/pods never cluster-wide.
   - `TestF134_APIDoesNotGrantRuntimeEnvironments`.
   - `TestF135_APIDoesNotGrantPodsLog`.
   - `TestF137_StorageClassesIsAlwaysClusterRole` — for both scope modes.

---

## Migration note for operators

Operators with the previous `rbac.scope: cluster` default will see
narrower permissions after upgrade. Specifically:
- The controller can no longer access secrets/pods OUTSIDE the
  workspace namespace.
- `coordination.k8s.io/leases` is now namespace-scoped — leader
  election still works because it lives in the controller's own
  release namespace.
- `services` and `configmaps` grants are gone — controller code
  audited as not using them.

If the operator relies on multi-namespace deployments (workspaces in
namespace A, controller in namespace B), they must:
1. Set `rbac.scope: cluster` to keep the cluster ClusterRole.
2. Per-namespace bind the workspace Role in each watched namespace
   (the chart's default workspace Role only binds in
   `.Release.Namespace`; multi-namespace deployments require GitOps
   overlays).

This is documented in the rbac.yaml header comment.

---

## Tests

| Command | Result |
|---|---|
| `go test -count=1 -timeout 60s -run 'TestG5_\|TestF13' ./charts/llmsafespace/...` | PASS (9/9) |
| `go test -count=1 -timeout 120s ./charts/llmsafespace/... ./controller/... ./api/...` | PASS |
| `helm template --namespace test-ns --release-name test charts/llmsafespace/` | renders cleanly |

---

## Live re-pentest plan

1. CI builds and ships chart + API image (terminal.go change).
2. `helm upgrade llmsafespace ./charts/llmsafespace -n default --reuse-values`.
3. **G5 verification:**
   `kubectl auth can-i get secrets --as=system:serviceaccount:default:llmsafespace-controller --namespace=kube-system`
   Must return `no` (was `yes` pre-fix).
4. **F1.3.6 verification:**
   - Create a non-workspace pod in the workspace namespace
     (`kubectl run unrelated --image=alpine -- sleep 3600`).
   - Authenticate as a regular user, attempt to open a terminal
     to `unrelated`'s name as if it were a workspace pod. The
     terminal handler must reject with "pod ownership mismatch".
5. **F1.3.7 verification:**
   `kubectl auth can-i get storageclasses --as=system:serviceaccount:default:llmsafespace-controller`
   Must return `yes` regardless of `rbac.scope`.

---

## Files changed

- `charts/llmsafespace/templates/rbac.yaml` (rewrite)
- `charts/llmsafespace/values.yaml` (default flip)
- `charts/llmsafespace/chart_test.go` (9 new tests)
- `api/internal/handlers/terminal.go` (label-check before exec)
- `design/stories/epic-17-security-review/remediation/MASTER-TRACKER.md`

---

## Next finding

Phase C/G6 — F1.1.2 + RT-2.16 (`:sessionId` proxy path traversal).
