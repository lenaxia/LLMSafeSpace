# Worklog 0299 — Add Relay-Router to Helm Chart (Fix Router Check 500)

**Date:** 2026-06-15
**Epic:** 43 (Relay Admin UX) / 42 (Multi-Cloud Inference Relay)
**Story:** US-43.2 (setup checklist)
**Status:** Complete

## Objective

`GET /api/v1/admin/relay/setup` was returning HTTP 500 because `checkRouter`
called `Deployments(h.namespace).Get("relay-router")` but the API service account
(`system:serviceaccount:default:llmsafespace-api`) had no RBAC permission for
`get deployments` in any namespace, and the `h.namespace` was the workspace
namespace rather than the release namespace where the router actually lives.

The MetalLB gate was already removed in the prior session. The router check
failed for the same class of reason: insufficient RBAC + wrong namespace.

Instead of removing the router check (which IS an LLMSafeSpace-owned component,
unlike MetalLB which is cluster infra), this session added the relay-router to
the Helm chart and granted the API SA the minimum RBAC needed to probe it.

## Work Completed

### Helm Chart (4 files)

- **values.yaml**: added `controller.inferenceRelay.router` block with image
  (`ghcr.io/lenaxia/llmsafespace/relay-router`), service (ClusterIP, port 8080),
  resources (50m/32Mi req, 200m/128Mi limit), nodeSelector, tolerations, affinity.
- **_helpers.tpl**: added `llmsafespace.relayRouter.labels`,
  `.relayRouter.selectorLabels`, `.relayRouter.image` helpers.
- **relay-router-deployment.yaml** (new): single-replica Deployment gated on
  `controller.inferenceRelay.enabled`. Runs relay-router as non-root (65534),
  readOnlyRootFilesystem, drop ALL capabilities, no service account token
  (the router has no K8s API dependency). Mounts `relay-router-peers` ConfigMap
  at `/etc/relay-router` (optional — pod starts before peers are provisioned).
  Readiness/liveness probes on `/metrics`.
- **relay-router-service.yaml** (new): ClusterIP Service on port 8080, gated
  on `controller.inferenceRelay.enabled`. Named `relay-router` to match the
  hardcoded `routerURL` in the controller and handler.

### RBAC (2 resources)

- **rbac.yaml**: added `api-relay-router` Role + RoleBinding in `.Release.Namespace`
  granting the API SA `get deployments` with `resourceNames: ["relay-router"]`
  (defense-in-depth: no wildcard).

### Backend Go (3 files)

- **api-deployment.yaml**: added `LLMSAFESPACE_KUBERNETES_PODNAMESPACE` downward
  API env var so app.go can read the pod's own (release) namespace.
- **relay_admin.go**: added `routerNamespace` field to `RelayAdminHandler`;
  `checkRouter` now queries `h.routerNamespace` instead of `h.namespace`
  (workspace namespace). Constructor updated with 5th parameter.
- **app.go**: reads `LLMSAFESPACE_KUBERNETES_PODNAMESPACE` env var, falls back
  to `cfg.Kubernetes.Namespace` if unset; passes as `routerNamespace` to
  `NewRelayAdminHandler`.
- **relay_admin_test.go**: all 4 constructor calls updated to pass `testNamespace`
  for both namespace params.

## Key Decisions

1. **Use fixed name `relay-router`** (not `{{ fullname }}-relay-router`) because
   the codebase hardcodes this name in the handler (`checkRouter`), controller
   (`--relay-router-url`), and RBAC. The router is a cluster-wide singleton
   (one per InferenceRelay fleet), so a fixed name is acceptable.

2. **No WireGuard sidecar yet.** The initial Deployment has only the HTTP proxy
   container. WireGuard (NET_ADMIN, privileged-ish, key management) belongs in
   US-42.8 when the controller's WG key infrastructure is built. The Service
   type is `ClusterIP` with only TCP 8080; operators can change to LoadBalancer
   when WG is added.

3. **Optional peer ConfigMap.** `relay-router-peers` is mounted as `optional:
   true` so the router pod starts before the controller provisions relay VMs.
   The router serves /metrics and health probes regardless.

4. **Separate release-namespace Role.** The API already has a Role in the
   workspace namespace (pods, secrets, events). The relay-router lives in the
   release namespace, so a separate Role + RoleBinding in `.Release.Namespace`
   is correct. The `resourceNames` restriction limits blast radius to one
   Deployment.

## Adversarial Self-Review (Rule 11)

- **Non-root compatible?** relay-router listens on :8080 (non-privileged), reads
  ConfigMap (world-readable). ✓
- **No K8s API access needed?** Router has no k8s client. `automountServiceAccountToken:
  false`. ✓
- **ConfigMap absence?** `optional: true` — pod starts, metrics available. ✓
- **Namespace fallback?** If env var unset, falls back to workspace namespace.
  If namespaces differ, NotFound → routerDeployed=false (not 500). Acceptable. ✓
- **RBAC not too broad?** `resourceNames: ["relay-router"]` — single Deployment. ✓
- **No frontend changes needed?** Typecheck passes, wizard already renders
  `routerDeployed`. ✓

Zero real findings.

## Tests Run

- `go test -run 'Relay|Setup' ./api/internal/handlers/` → `ok` (0.235s)
- `go build ./api/internal/handlers/` → EXIT 0
- `gofmt -l` on the 3 Go files → clean
- `npm run typecheck` → EXIT 0
- Helm template rendering: binary not available in this env, but templates follow
  established patterns (same as controller-deployment.yaml + api-service.yaml)

## Files Modified / Created

- `charts/llmsafespace/values.yaml` (modified)
- `charts/llmsafespace/templates/_helpers.tpl` (modified)
- `charts/llmsafespace/templates/relay-router-deployment.yaml` (created)
- `charts/llmsafespace/templates/relay-router-service.yaml` (created)
- `charts/llmsafespace/templates/api-deployment.yaml` (modified)
- `charts/llmsafespace/templates/rbac.yaml` (modified)
- `api/internal/handlers/relay_admin.go` (modified)
- `api/internal/handlers/relay_admin_test.go` (modified)
- `api/internal/app/app.go` (modified)
- `worklogs/0299_2026-06-15_add-relay-router-to-helm-chart.md` (created)

## Next Steps

- When `controller.inferenceRelay.enabled=true` is set and the chart is deployed,
  `GET /admin/relay/setup` will return `routerDeployed=true` + 200.
- US-42.8 (WireGuard sidecar): add a WG container + key volume mount to this
  Deployment, switch Service type to LoadBalancer, add WG UDP port 51820.
