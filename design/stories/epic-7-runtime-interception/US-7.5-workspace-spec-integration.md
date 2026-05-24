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

The controller updates the main container's security context to allow writable rootfs (for pip/npm installs) while keeping non-root:

```go
// Main container (workspace):
SecurityContext: &corev1.SecurityContext{
    RunAsNonRoot:             &trueVal,
    AllowPrivilegeEscalation: &falseVal,
    Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
    SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
    // NOTE: No ReadOnlyRootFilesystem — agent needs to pip/npm install
}
```

Remove: `ReadOnlyRootFilesystem: true` (agent needs writable paths for package installs).
Keep: `RunAsNonRoot: true`, drop all caps, seccomp.

#### Sidecar Container

When sentinel is active (always in K8s), the controller adds the daemon sidecar:

```go
sidecar := corev1.Container{
    Name:    "system-daemon",
    Image:   runtimeImage, // same base image
    Command: []string{"/usr/local/bin/system-daemon"},
    SecurityContext: &corev1.SecurityContext{
        RunAsUser:                ptr(int64(0)),
        AllowPrivilegeEscalation: &falseVal,
        Capabilities: &corev1.Capabilities{
            Drop: []corev1.Capability{"ALL"},
            Add:  []corev1.Capability{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETUID", "SETGID"},
        },
        SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
    },
    VolumeMounts: []corev1.VolumeMount{
        {Name: "daemon-socket", MountPath: "/run/llmsafespace"},
        {Name: "policies", MountPath: "/etc/llmsafespace", ReadOnly: true},
        {Name: "daemon-log", MountPath: "/var/log/llmsafespace"},
    },
}
```

Shared volumes added to pod:
```go
volumes = append(volumes,
    corev1.Volume{Name: "daemon-socket", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
    corev1.Volume{Name: "daemon-log", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
)
```

Main container gets additional volume mount:
```go
{Name: "daemon-socket", MountPath: "/run/llmsafespace"},
```

#### Entrypoint

Main container entrypoint stays as `entrypoint-opencode.sh` (unchanged). The daemon runs in its own sidecar container with its own entrypoint (`/usr/local/bin/system-daemon`).

The sentinel file (`/etc/llmsafespace/mode`) is mounted into the main container via the policies ConfigMap. Wrappers check for it to decide whether to enforce policy and talk to the daemon socket.

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
