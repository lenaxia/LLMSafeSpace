# US-6.8: Helm Chart Cleanup

**Epic:** 6 — Collapse Sandbox into Workspace
**Status:** Planning
**Dependencies:** US-6.4

## Objective

Remove all sandbox/sandboxprofile references from Helm chart. Update workspace CRD to include new fields from US-6.1. Update RBAC to reflect that the controller now manages pods/secrets/PVCs directly for workspaces.

## Changes

### RBAC (`charts/llmsafespace/templates/rbac.yaml`)

**Controller ClusterRole — remove:**
- `sandboxes` (all verbs)
- `sandboxes/status`
- `sandboxes/finalizers`
- `sandboxprofiles` (all verbs)
- `sandboxprofiles/status`

**Controller ClusterRole — keep/verify:**
- `workspaces` (get, list, watch, create, update, patch, delete)
- `workspaces/status` (get, update, patch)
- `workspaces/finalizers` (update)
- `pods` (get, list, watch, create, delete)
- `secrets` (get, list, watch, create, update, delete)
- `persistentvolumeclaims` (get, list, watch, create, delete)
- `runtimeenvironments` (get, list, watch)
- `events` (create, patch)

**API ClusterRole — remove:**
- `sandboxes` (get, list, watch, create, delete)
- `sandboxes/status` (get)
- `sandboxprofiles` (get, list)

**API ClusterRole — keep/verify:**
- `workspaces` (get, list, watch, create, update, delete)
- `workspaces/status` (get, update)
- `secrets` (get, create, update, delete)

### CRDs

**Delete:**
- `charts/llmsafespace/crds/sandbox.yaml`
- `charts/llmsafespace/crds/sandboxprofile.yaml`

**Update:**
- `charts/llmsafespace/crds/workspace.yaml` — sync from `pkg/crds/workspace_crd.yaml` (includes new fields from US-6.1: Creating phase, PodSecurityContext, Resources, Timeout, RestartGeneration, PodName, PodIP, Endpoint, etc.)

**Keep:**
- `charts/llmsafespace/crds/runtimeenvironment.yaml`

### Webhooks

**Remove ValidatingWebhookConfiguration entries for:**
- `sandboxes.llmsafespace.dev`
- `sandboxprofiles.llmsafespace.dev`

**Update workspace webhook** for new field validation (Runtime required, Timeout range, MaxRetries range).

### Values (`charts/llmsafespace/values.yaml`)

Remove any sandbox-specific configuration values. Ensure workspace-related values are present:
- `api.security.allowedOrigins` (from US-6.0)
- Controller resource limits (unchanged)

### Deployment templates

- Controller deployment: remove any sandbox-specific env vars or volume mounts
- API deployment: remove sandbox service references; add `LLMSAFESPACE_SECURITY_ALLOWEDORIGINS` env var

## Files Modified

| File | Change |
|------|--------|
| `charts/llmsafespace/templates/rbac.yaml` | Remove sandbox RBAC rules |
| `charts/llmsafespace/crds/sandbox.yaml` | **Delete** |
| `charts/llmsafespace/crds/sandboxprofile.yaml` | **Delete** |
| `charts/llmsafespace/crds/workspace.yaml` | Sync with new fields from US-6.1 |
| `charts/llmsafespace/templates/webhooks.yaml` | Remove sandbox/sandboxprofile webhook entries |
| `charts/llmsafespace/values.yaml` | Remove sandbox config; add CORS config |
| `charts/llmsafespace/templates/api-deployment.yaml` | Add CORS env var |

## Acceptance Criteria

1. `helm lint charts/llmsafespace` passes
2. `helm template charts/llmsafespace` renders without error
3. `helm template charts/llmsafespace | grep -i sandbox` returns zero matches
4. RBAC rules: workspace + pod + secret + PVC + runtimeenvironment only
5. Workspace CRD includes all new fields (Creating phase, PodIP, PodSecurityContext, etc.)
6. No sandbox or sandboxprofile webhook entries
7. Deployed chart: controller starts and reconciles workspaces correctly
