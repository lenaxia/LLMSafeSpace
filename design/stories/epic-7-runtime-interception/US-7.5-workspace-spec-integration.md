# US-7.5: Workspace Spec — Languages + Privileged Packages

**Epic:** 7 — Runtime Interception Layer
**Status:** Planning
**Dependencies:** US-7.4 (RuntimePolicy CRD)

## Objective

Extend the Workspace CRD spec to declare which languages are active (with policy references) and which packages require privileged installation at pod creation time. Wire the controller to resolve policies and mount them as config files.

## Design

### Workspace Spec Additions

```go
type WorkspaceSpec struct {
    // ... existing fields ...

    // Languages declares which language runtimes are active in this workspace
    // and which security policy to apply to each. Optional — if omitted, all
    // runtimes work with no policy enforcement.
    Languages []WorkspaceLanguage `json:"languages,omitempty"`

    // PrivilegedPackages are system packages (apt) installed at pod creation
    // time via init container. Use for packages that require root to install
    // (e.g., python3-dev, build-essential, libssl-dev).
    PrivilegedPackages []string `json:"privilegedPackages,omitempty"`
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

#### Init Container for PrivilegedPackages

When `spec.privilegedPackages` is non-empty, the controller adds an init container to the pod spec:

```go
initContainer := corev1.Container{
    Name:    "install-packages",
    Image:   runtimeImage, // same base image
    Command: []string{"/opt/llmsafespace/.bin/apt"},
    Args:    append([]string{"install", "-y", "--no-install-recommends"}, workspace.Spec.PrivilegedPackages...),
    SecurityContext: &corev1.SecurityContext{
        RunAsUser: ptr(int64(0)), // root
    },
    VolumeMounts: // same mounts as main container (writes to shared filesystem)
}
```

The init container runs as root, installs packages, then exits. The main container starts as the sandbox user with packages already available.

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
    {"language":"python","enabled":true,"restrictedModules":["ctypes","subprocess"],...}
  nodejs.json: |
    {"language":"nodejs","enabled":true,"disableCodeGen":true,...}
```

#### Daemon Policy Mounting

The daemon (US-7.1) also needs policy config. The controller generates a merged daemon policy from all active language policies and mounts it at `/etc/llmsafespace/daemon/policy.json`:

```json
{
  "allowedCommands": ["apt", "pip", "npm", "go"],
  "allowedSubcommands": {"apt": ["install", "update", "list"], "pip": ["install", "list"]},
  "blockedFlags": {"pip": ["--trusted-host"], "apt": ["--allow-unauthenticated"]},
  "blockedPackages": {"pip": ["os-sys-calls"], "npm": []},
  "allowedSources": {"pip": ["https://pypi.org/simple/"], "go": ["https://proxy.golang.org"]},
  "rateLimit": {"maxPerMinute": 10}
}
```

### Webhook Validation

- `spec.languages[].name` must be non-empty, lowercase alphanumeric
- `spec.languages[].policy` if set, must reference an existing RuntimePolicy CRD
- `spec.privilegedPackages` entries must be non-empty strings, no shell metacharacters
- No duplicate language names in the array

### API Changes

`CreateWorkspaceRequest` and `UpdateWorkspaceRequest` gain:

```go
type CreateWorkspaceRequest struct {
    // ... existing ...
    Languages          []WorkspaceLanguage `json:"languages,omitempty"`
    PrivilegedPackages []string            `json:"privilegedPackages,omitempty"`
}
```

Updating `languages` or `privilegedPackages` triggers a pod recreation (same as credential change — bump restart generation).

## Files Modified

| File | Change |
|------|--------|
| `pkg/apis/llmsafespace/v1/workspace_types.go` | Add `Languages`, `PrivilegedPackages`, `WorkspaceLanguage` |
| `pkg/apis/llmsafespace/v1/workspace_deepcopy.go` | Regenerate |
| `pkg/apis/llmsafespace/v1/workspace_webhook.go` | Validate new fields |
| `pkg/crds/workspace_crd.yaml` | Add new spec fields |
| `controller/internal/workspace/controller.go` | Init container logic, policy ConfigMap creation, volume mounts |
| `api/internal/services/workspace/workspace_service.go` | Pass through new fields |
| `pkg/types/types.go` | Add to API request/response types |

## Acceptance Criteria

1. Workspace with `privilegedPackages: [python3, build-essential]` creates pod with init container that installs them
2. Workspace with `languages: [{name: python, policy: python-hardened}]` mounts policy config at `/etc/llmsafespace/policies/python.json`
3. Updating `privilegedPackages` triggers pod recreation
4. Updating `languages` updates ConfigMap (no pod recreation needed — wrappers read on each invocation)
5. Webhook rejects invalid policy references
6. Webhook rejects shell metacharacters in privilegedPackages
7. Workspace with no `languages` field works (no policy mounted, wrappers passthrough)
