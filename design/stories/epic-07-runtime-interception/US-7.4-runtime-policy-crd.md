# US-7.4: RuntimePolicy CRD

**Epic:** 7 — Runtime Interception Layer
**Status:** Closed — Architecture incompatible with current codebase (see issue #40 and epic README)
**Dependencies:** None

## Objective

Define a new cluster-scoped CRD that maps a language to its security policy configuration. This replaces the dead fields on `RuntimeEnvironment` (tags, securityFeatures, preInstalledPackages, etc.) with a purpose-built type.

## Design

### CRD Definition

```go
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=rtp
// +kubebuilder:printcolumn:name="Language",type="string",JSONPath=".spec.language"
// +kubebuilder:printcolumn:name="Enabled",type="boolean",JSONPath=".spec.enabled"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

type RuntimePolicy struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec              RuntimePolicySpec `json:"spec"`
}

type RuntimePolicySpec struct {
    // Language this policy applies to (e.g., "python", "nodejs", "go").
    Language string `json:"language"`

    // Enabled controls whether this policy is active. Disabled policies
    // are ignored by wrappers (pure passthrough).
    // +kubebuilder:default=true
    Enabled bool `json:"enabled"`

    // RestrictedModules is a list of modules/packages that cannot be imported
    // at runtime. Language-specific semantics (Python: import hook; Node: require patch).
    RestrictedModules []string `json:"restrictedModules,omitempty"`

    // AllowedPackageSources restricts where packages can be installed from.
    // Empty = all sources allowed.
    AllowedPackageSources []string `json:"allowedPackageSources,omitempty"`

    // BlockedPackages that cannot be installed via the package manager.
    BlockedPackages []string `json:"blockedPackages,omitempty"`

    // BlockedFlags are package manager CLI flags that are rejected.
    // e.g., ["--trusted-host", "--allow-unauthenticated"]
    BlockedFlags []string `json:"blockedFlags,omitempty"`

    // EnvVars injected when the language runtime is invoked.
    EnvVars map[string]string `json:"envVars,omitempty"`

    // SeccompProfile additions for this language (path to profile JSON).
    SeccompProfile string `json:"seccompProfile,omitempty"`

    // DisableCGO (Go-specific): force CGO_ENABLED=0.
    DisableCGO bool `json:"disableCGO,omitempty"`

    // DisableCodeGen (Node-specific): --disallow-code-generation-from-strings.
    DisableCodeGen bool `json:"disableCodeGen,omitempty"`
}
```

### How It's Consumed

1. **At pod creation**: Controller reads workspace `spec.languages[].policy`, resolves the `RuntimePolicy` CRD, and mounts the policy as a ConfigMap/JSON file at `/etc/llmsafespace/policies/<language>/config.json`.

2. **At runtime**: Language wrappers (US-7.3) read the config file on each invocation. No K8s API calls from inside the pod.

3. **By the daemon** (US-7.1): Package manager policy (allowedPackageSources, blockedPackages, blockedFlags) is loaded into the daemon's policy engine at startup.

### Example Manifests

```yaml
apiVersion: llmsafespace.dev/v1
kind: RuntimePolicy
metadata:
  name: python-hardened
spec:
  language: python
  enabled: true
  restrictedModules:
    - ctypes
    - subprocess
    - os.system
    - importlib
  allowedPackageSources:
    - https://pypi.org/simple/
  blockedPackages:
    - os-sys-calls
  blockedFlags:
    - --trusted-host
    - --index-url  # force default PyPI only
  envVars:
    PYTHONDONTWRITEBYTECODE: "1"
    PYTHONHASHSEED: "random"
---
apiVersion: llmsafespace.dev/v1
kind: RuntimePolicy
metadata:
  name: nodejs-standard
spec:
  language: nodejs
  enabled: true
  restrictedModules:
    - child_process
    - cluster
    - dgram
  disableCodeGen: true
  blockedFlags:
    - --ignore-scripts
---
apiVersion: llmsafespace.dev/v1
kind: RuntimePolicy
metadata:
  name: go-standard
spec:
  language: go
  enabled: true
  disableCGO: true
  allowedPackageSources:
    - https://proxy.golang.org
```

### Webhook Validation

- `spec.language` is required, non-empty
- `spec.language` must be lowercase alphanumeric (no special chars)
- `spec.allowedPackageSources` entries must be valid URLs
- `spec.restrictedModules` entries must be non-empty strings

## Files Created

| File | Purpose |
|------|---------|
| `pkg/apis/llmsafespace/v1/runtimepolicy_types.go` | Type definitions |
| `pkg/apis/llmsafespace/v1/runtimepolicy_deepcopy.go` | Generated deepcopy |
| `pkg/crds/runtimepolicy_crd.yaml` | CRD manifest |
| `charts/llmsafespace/crds/runtimepolicy_crd.yaml` | Helm CRD |
| `charts/llmsafespace/templates/runtimepolicy-defaults.yaml` | Default policies (python-hardened, nodejs-standard, go-standard) |
| `controller/internal/webhooks/runtimepolicy_webhook.go` | Validation webhook |

## Acceptance Criteria

1. `kubectl apply -f runtimepolicy.yaml` succeeds
2. `kubectl get rtp` lists policies with Language and Enabled columns
3. Webhook rejects empty language
4. Webhook rejects invalid URLs in allowedPackageSources
5. `make deepcopy` succeeds
6. `make test` passes
