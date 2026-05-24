# US-6.8: Helm Chart Cleanup

**Epic:** 6 — Collapse Sandbox into Workspace
**Status:** Planning
**Dependencies:** US-6.4

## Objective

Remove all sandbox/sandboxprofile references from Helm chart.

## Changes

### RBAC (`charts/llmsafespace/templates/rbac.yaml`)

Controller rules: remove `sandboxes`, `sandboxes/status`, `sandboxes/finalizers`, `sandboxprofiles`, `sandboxprofiles/status`.
API rules: remove `sandboxes`, `sandboxes/status`, `sandboxprofiles`.

### CRDs

Delete: `charts/llmsafespace/crds/sandbox.yaml`, `charts/llmsafespace/crds/sandboxprofile.yaml`
Update: `charts/llmsafespace/crds/workspace.yaml` — sync from US-6.1

### Webhooks

Remove ValidatingAdmissionWebhook entries for Sandbox and SandboxProfile resources.
Update workspace webhook for new fields.

## Acceptance Criteria

1. `helm lint charts/llmsafespace` passes
2. `helm template` renders without error
3. No sandbox references in rendered templates
4. RBAC rules: workspace + pod + secret + PVC only
