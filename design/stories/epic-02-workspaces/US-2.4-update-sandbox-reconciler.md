# US-2.4: Update Sandbox Reconciler for Workspaces

**Epic:** 2 - Workspaces
**Priority:** Critical
**Depends on:** US-2.1, US-2.2, US-1.3

## User Story

As a controller developer, I want the Sandbox reconciler to mount workspace PVCs and inject init containers, so that sandboxes have persistent storage and correct credential setup.

## Acceptance Criteria

- [ ] Sandbox pod mounts workspace PVC at /workspace
- [ ] Init containers injected: workspace-setup, credential-setup
- [ ] Server password auto-generated and stored as K8s Secret
- [ ] Password injected via projected volume in credential-setup → /sandbox-cfg/password
- [ ] Credentials resolved from workspace-level Secret only (V2.1 adds session-level override)
- [ ] Sandbox CRD status updated with podIP
- [ ] Suspending/Resuming phases handled
- [ ] workspaceRef field on Sandbox CRD

## Technical Details

**Edit:** `controller/internal/sandbox/controller.go`

**Changes to buildSandboxPod:**

1. If `spec.workspaceRef` is set:
   - Mount workspace PVC at `/workspace`
   - Add `workspace-setup` init container (if spec.packages or spec.initScript set)
   - Add `credential-setup` init container (always present — handles both credentials and password)

2. Generate random password:
   - Create K8s Secret `sandbox-pw-{name}` with `password` key
   - Owner-reference to Sandbox CRD
   - Mount as projected volume in credential-setup init container

3. Resolve credentials:
   - Check if `workspace-creds-{workspace_name}` Secret exists
   - If yes, mount as projected volume in credential-setup
   - credential-setup copies both password and credentials to /sandbox-cfg/

4. Update pod spec:
   - Main container: entrypoint-opencode.sh, port 4096
   - Security context: readOnlyRootFilesystem, runAsNonRoot, drop ALL capabilities
   - EmptyDir mounts: /tmp, /sandbox-cfg, /home/sandbox

5. Update Sandbox CRD status:
   - `status.podIP` from pod status
   - Remove warm pod assignment logic (already removed in US-1.3)

**New phases:**
- `Suspending`: delete pod, set phase=Suspended
- `Resuming`: create new pod with same workspace PVC, set phase=Running

**V2.1:** `mode-gate` init container will be added for high-security mode. Controller currently ignores `spec.securityLevel: high`.

## Design Reference

Section 7.2 (Pod Architecture), 7.6 (Pod Spec), 9.1 (Credential Isolation), 10.2 (Sandbox CRD Changes)

## Effort

Large (6-8 hours)
