# US-7.6: RuntimeEnvironment Cleanup

**Epic:** 7 — Runtime Interception Layer
**Status:** Planning
**Dependencies:** US-7.4 (RuntimePolicy CRD exists to absorb responsibilities)

## Objective

Strip `RuntimeEnvironment` CRD down to its only used fields (`image`, `requiresCredentials`). Deduplicate the `resolveRuntimeImage()` function. Remove dead status fields.

## Changes

### Trim CRD Spec

Before (current):
```go
type RuntimeEnvironmentSpec struct {
    Image                string                       `json:"image"`
    Language             string                       `json:"language"`
    Version              string                       `json:"version,omitempty"`
    Tags                 []string                     `json:"tags,omitempty"`
    PreInstalledPackages []string                     `json:"preInstalledPackages,omitempty"`
    PackageManager       string                       `json:"packageManager,omitempty"`
    SecurityFeatures     []string                     `json:"securityFeatures,omitempty"`
    ResourceRequirements *RuntimeResourceRequirements `json:"resourceRequirements,omitempty"`
    RequiresCredentials  bool                         `json:"requiresCredentials,omitempty"`
}
```

After:
```go
type RuntimeEnvironmentSpec struct {
    // Image is the container image for this runtime.
    Image string `json:"image"`

    // Language is used for resolveRuntimeImage lookup (language:version matching).
    Language string `json:"language,omitempty"`

    // Version is used for resolveRuntimeImage lookup.
    Version string `json:"version,omitempty"`

    // RequiresCredentials indicates the runtime needs LLM provider credentials.
    RequiresCredentials bool `json:"requiresCredentials,omitempty"`
}
```

Deleted: `Tags`, `PreInstalledPackages`, `PackageManager`, `SecurityFeatures`, `ResourceRequirements`, `RuntimeResourceRequirements` type.

### Trim CRD Status

Before:
```go
type RuntimeEnvironmentStatus struct {
    Available     bool         `json:"available,omitempty"`
    LastValidated *metav1.Time `json:"lastValidated,omitempty"`
}
```

After: Delete entirely. No reconciler writes to it. Remove `+kubebuilder:subresource:status`.

### Deduplicate resolveRuntimeImage()

Currently exists as identical copies in:
- `controller/internal/sandbox/runtime_resolver.go`
- `controller/internal/workspace/runtime_resolver.go`

After Epic 6 (sandbox deleted), only the workspace copy remains. But the API service also has its own `lookupRuntimeEnvironment()` in `api/internal/services/sandbox/sandbox_service.go`.

Move to a shared location:
```
pkg/runtime/resolver.go
```

Both the controller and API import from there. Single implementation.

### Update CRD YAML

Strip removed fields from:
- `pkg/crds/runtimeenvironment_crd.yaml`
- `charts/llmsafespace/crds/runtimeenvironment_crd.yaml`

### Update Webhook

Simplify `RuntimeEnvironmentValidator` — only validate `image` is non-empty. Remove `language` required check (now optional, only needed for language:version lookup).

### Update Helm Template

`charts/llmsafespace/templates/runtimeenvironment-base.yaml` — remove any references to deleted fields.

## Files Modified

| File | Change |
|------|--------|
| `pkg/apis/llmsafespace/v1/runtimeenvironment_types.go` | Strip to image + language + version + requiresCredentials |
| `pkg/apis/llmsafespace/v1/runtimeenvironment_deepcopy.go` | Regenerate (simpler) |
| `pkg/crds/runtimeenvironment_crd.yaml` | Remove deleted fields |
| `charts/llmsafespace/crds/runtimeenvironment_crd.yaml` | Sync |
| `controller/internal/webhooks/runtimeenvironment_webhook.go` | Simplify validation |
| `controller/internal/workspace/runtime_resolver.go` | Move to `pkg/runtime/resolver.go` |
| `controller/internal/sandbox/runtime_resolver.go` | Delete (if sandbox still exists; otherwise already gone from Epic 6) |
| `api/internal/services/sandbox/sandbox_service.go` | Import from `pkg/runtime/` |
| `pkg/runtime/resolver.go` | New shared location |

## Acceptance Criteria

1. `RuntimeEnvironment` CRD only has `image`, `language`, `version`, `requiresCredentials` in spec
2. No `status` subresource on `RuntimeEnvironment`
3. Single `resolveRuntimeImage()` in `pkg/runtime/resolver.go`
4. No duplicate resolver code
5. `make test` passes
6. Existing `RuntimeEnvironment` manifests with only `image` field still work
