# US-7.5: Workspace Spec — Languages + RuntimeClass

**Epic:** 7 — Runtime Interception Layer
**Status:** Planning
**Dependencies:** US-7.4 (RuntimePolicy CRD)

## Objective

Extend the Workspace CRD spec to declare which languages have active security policies and which Kubernetes RuntimeClass to use for isolation. Wire the controller to resolve policies, mount them as config files, and set the pod's runtimeClassName.

## Design

### Workspace Spec Additions

```go
type WorkspaceSpec struct {
    // ... existing fields ...

    // Languages declares which language runtimes have active security policies.
    // Optional — if omitted, all runtimes work with no policy enforcement.
    Languages []WorkspaceLanguage `json:"languages,omitempty"`

    // RuntimeClass sets the pod's runtimeClassName for isolation level.
    // Empty = cluster default (runc). Set to "gvisor" or "kata" for
    // multi-tenant deployments.
    RuntimeClass string `json:"runtimeClass,omitempty"`
}

type WorkspaceLanguage struct {
    // Name of the language (e.g., "python", "nodejs", "go").
    Name string `json:"name"`

    // Policy references a RuntimePolicy CRD by name. If empty or the
    // referenced policy has enabled=false, no restrictions are applied.
    Policy string `json:"policy,omitempty"`
}
```

### Controller Behavior

#### Policy Resolution and Mounting

For each entry in `spec.languages`:
1. Controller resolves the `RuntimePolicy` CRD by name
2. Serializes the policy spec to JSON
3. Creates/updates a ConfigMap `{workspace}-policies` with one key per language
4. Mounts the ConfigMap at `/etc/llmsafespace/policies/` in the pod

```yaml
# Generated ConfigMap
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-workspace-policies
data:
  python.json: |
    {"language":"python","enabled":true,"restrictedModules":["ctypes"],...}
  nodejs.json: |
    {"language":"nodejs","enabled":true,"disableCodeGen":true,...}
```

Also generates daemon policy from language policies and mounts at `/etc/llmsafespace/daemon/policy.json`.

#### RuntimeClass

```go
pod.Spec.RuntimeClassName = &workspace.Spec.RuntimeClass  // nil if empty
```

One line. The runtime class must exist in the cluster (not validated by the controller — Kubernetes rejects the pod if it doesn't exist).

#### Security Context Changes

The controller must update the pod security context to support the daemon model:

```go
// Before (current):
SecurityContext: &corev1.SecurityContext{
    ReadOnlyRootFilesystem:   &trueVal,
    RunAsNonRoot:             &trueVal,
    AllowPrivilegeEscalation: &falseVal,
    Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
}

// After:
SecurityContext: &corev1.SecurityContext{
    AllowPrivilegeEscalation: &falseVal,
    Capabilities: &corev1.Capabilities{
        Drop: []corev1.Capability{"ALL"},
        Add:  []corev1.Capability{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETUID", "SETGID"},
    },
    SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
}
// Remove: ReadOnlyRootFilesystem, RunAsNonRoot
// Remove: pod-level RunAsUser/RunAsGroup (daemon is root, drops to 1000 internally)
```

#### Entrypoint Change

```go
// Before:
Command: []string{"/usr/local/bin/entrypoint-opencode.sh"},

// After:
Command: []string{"/usr/local/bin/system-daemon"},
```

The daemon handles starting opencode internally.

### Webhook Validation

- `spec.languages[].name` must be non-empty, lowercase alphanumeric
- `spec.languages[].policy` if set, must reference an existing RuntimePolicy CRD
- `spec.runtimeClass` if set, must be a valid DNS subdomain name
- No duplicate language names

### API Changes

```go
type CreateWorkspaceRequest struct {
    // ... existing ...
    Languages    []WorkspaceLanguage `json:"languages,omitempty"`
    RuntimeClass string              `json:"runtimeClass,omitempty"`
}
```

Updating `languages` updates the ConfigMap (no pod recreation — wrappers read on each invocation).
Updating `runtimeClass` triggers pod recreation (different runtime = new pod).

## Files Modified

| File | Change |
|------|--------|
| `pkg/apis/llmsafespace/v1/workspace_types.go` | Add `Languages`, `RuntimeClass`, `WorkspaceLanguage` |
| `pkg/apis/llmsafespace/v1/workspace_deepcopy.go` | Regenerate |
| `pkg/apis/llmsafespace/v1/workspace_webhook.go` | Validate new fields |
| `pkg/crds/workspace_crd.yaml` | Add new spec fields |
| `controller/internal/workspace/controller.go` | Security context changes, entrypoint change, policy ConfigMap, runtimeClassName |
| `api/internal/services/workspace/workspace_service.go` | Pass through new fields |
| `pkg/types/types.go` | Add to API request/response types |

## Acceptance Criteria

1. Workspace with `languages: [{name: python, policy: python-hardened}]` mounts policy at `/etc/llmsafespace/policies/python.json`
2. Workspace with `runtimeClass: gvisor` creates pod with `runtimeClassName: gvisor`
3. Pod starts with `system-daemon` as entrypoint (not entrypoint-opencode.sh directly)
4. Pod security context has correct capabilities (no ReadOnlyRootFilesystem, no RunAsNonRoot)
5. Updating `languages` updates ConfigMap without pod recreation
6. Updating `runtimeClass` triggers pod recreation
7. Webhook rejects invalid policy references
8. Workspace with no `languages` field works (no policy mounted, wrappers passthrough)
