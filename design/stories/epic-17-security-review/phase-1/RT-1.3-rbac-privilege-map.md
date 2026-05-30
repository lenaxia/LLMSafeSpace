# RT-1.3 — RBAC Privilege Mapping

**Phase:** 1 (Reconnaissance)
**Sources:** `charts/llmsafespace/templates/rbac.yaml`, `charts/llmsafespace/templates/serviceaccount.yaml`, `charts/llmsafespace/values.yaml`, controller and API code (search hits cited inline).
**Method:** static analysis. Maps both `rbac.scope: cluster` (default) and `rbac.scope: namespace` modes. Cross-references granted permissions against actual code consumers to identify least-privilege violations.

---

## Default chart configuration

| Setting | Value | Location |
|---|---|---|
| `rbac.create` | `true` | `values.yaml:373` |
| `rbac.scope` | `"cluster"` | `values.yaml:382` |
| `serviceAccount.api.create` | `true` | `values.yaml:361` |
| `serviceAccount.controller.create` | `true` | `values.yaml:365` |
| Workspace namespace | release namespace (default; `api.config.kubernetes.namespace` empty) | `_helpers.tpl:99-101` |

The chart toggles the controller's RBAC scope via `$isCluster := eq .Values.rbac.scope "cluster"` (`rbac.yaml:2`). The **API ServiceAccount RBAC is always namespaced** — there is no scope toggle for it.

---

## Controller ServiceAccount — `rbac.scope: cluster` (DEFAULT)

Bound via `ClusterRoleBinding` at `rbac.yaml:55-68`. Controller can read, write, watch every namespace.

| API group | Resource | Verbs | Scope | Used? | Blast radius if SA token leaks |
|---|---|---|---|---|---|
| `llmsafespace.dev` | `workspaces` | full CRUD + watch | cluster-wide | yes | Read/modify/delete every Workspace cluster-wide; tenant cross-takeover |
| `llmsafespace.dev` | `workspaces/status` | full CRUD | cluster-wide | yes | Forge phase to mislead UI / manipulate API gating |
| `llmsafespace.dev` | `workspaces/finalizers` | full CRUD | cluster-wide | yes | Strip finalizer → leak resources; add bogus finalizer → wedge deletion forever |
| `llmsafespace.dev` | `runtimeenvironments` | full CRUD | cluster-wide | yes | Pivot to overwrite runtime image → every newly-created sandbox runs attacker code (supply-chain) |
| `llmsafespace.dev` | `runtimeenvironments/status` | full CRUD | cluster-wide | partial | DoS via status manipulation |
| `""` (core) | `pods` | full CRUD + watch | cluster-wide | yes | Cluster-wide pod takeover. Spawn pods in `kube-system` mounting hostPath/host network → node compromise. Delete control-plane pods → cluster outage |
| `""` | `pods/status` | get,patch,update | cluster-wide | **NO** | Forge `PodIP` cluster-wide → API proxy SSRF; mark crashlooping pods Ready (hide failures) |
| `""` | `secrets` | full CRUD + watch | cluster-wide | yes | **Highest blast radius.** Read every Secret cluster-wide: kube-system, etcd-encryption keys, CNI tokens, cert-manager CAs, customer credentials. → cluster admin equivalent |
| `""` | `persistentvolumeclaims` | full CRUD + watch | cluster-wide | yes | Delete any PVC → data destruction across tenants. Create huge PVCs → storage / cost bomb |
| `""` | `services` | full CRUD + watch | cluster-wide | **NO** | Replace `kube-dns` selector → DNS poisoning. Replace `kubernetes.default` endpoints → cluster API MITM |
| `networking.k8s.io` | `networkpolicies` | full CRUD + watch | cluster-wide | **NO** | Delete every NetworkPolicy → all default-deny posture collapses. Workspace default-deny ingress/egress (G16) destroyed |
| `""` | `events` | create, patch | cluster-wide | **NO** | Spam events; audit log noise |
| `coordination.k8s.io` | `leases` | full CRUD + watch | cluster-wide | yes (release-ns leader election) | Steal/poison **any** Lease cluster-wide: `kube-system/kube-controller-manager`, `kube-scheduler`, `coredns` — disable scheduling, disable DNS leader |
| `""` | `configmaps` | full CRUD + watch | cluster-wide | **NO** | Tamper with `kube-system/coredns` corefile → DNS hijack. Modify `kube-public/cluster-info` → MITM kubeadm joins |
| `storage.k8s.io` | `storageclasses` | get, list, watch | cluster-wide | yes (stale-PVC detection) | Read-only enumeration; low blast radius |

### Subjects bound (cluster mode)

`rbac.yaml:65-68`: single `ServiceAccount` subject. **No Group/User attachment.** Clean baseline.

---

## Controller ServiceAccount — `rbac.scope: namespace`

Same rule list (the `$controllerRules` variable is reused at `rbac.yaml:69-95`); only the scope shrinks to release namespace via `Role` + `RoleBinding`. Cluster takeover collapses to release-namespace takeover.

**Schema bug**: `storageclasses` is a cluster-scoped resource. Granting it via a namespaced `Role` is silently a no-op. The `pvcUsesWaitForFirstConsumer` check at `controller.go:514-529` will return Forbidden in namespace mode. Stale-PVC detection works but is less precise. Indicates the chart was not validated under `rbac.scope: namespace`.

---

## API ServiceAccount — always namespace-scoped

Two `Role` + `RoleBinding` pairs at `rbac.yaml:101-148` (workspace ns) and `rbac.yaml:150-181` (release ns leader-election).

### Role: `<release>-api` in workspace namespace

| API group | Resource | Verbs | Used? | Blast radius |
|---|---|---|---|---|
| `llmsafespace.dev` | `workspaces` | full CRUD + watch | yes | Read/modify/delete every Workspace in workspace ns → cross-user takeover within tenant |
| `llmsafespace.dev` | `workspaces/status` | full CRUD | yes | Forge workspace phase |
| `llmsafespace.dev` | `runtimeenvironments` | full CRUD + watch | **NO** | API SA could swap runtime images → poison every newly-spawned sandbox |
| `""` | `secrets` | full CRUD + watch | yes (session secrets, agentd password) | **High blast radius.** Read every Secret in workspace ns including chart's `<release>-credentials` Secret (postgres-password, redis-password, jwt-secret) → full API impersonation, full database access |
| `""` | `pods` | get, list, watch | yes (proxy targeting) | Enumerate workspace pods, read pod env / annotations / labels |
| `""` | `pods/log` | get, list | **NO** | Stream logs from any pod in workspace ns → leak credentials and PII printed to stdout |
| `""` | `pods/exec` | create | yes (terminal handler) | **Code execution inside any pod in workspace ns**, not just sandbox pods. With default config (workspaceNamespace == releaseNamespace), the API SA can `exec` into the API pod itself, controller pod, migration jobs, MCP pods. Lateral movement. |
| `""` | `events` | create, patch | **NO** | Spam events |

### Role: `<release>-api-leader-election` in release namespace

| API group | Resource | Verbs | Used? | Blast radius |
|---|---|---|---|---|
| `coordination.k8s.io` | `leases` | full CRUD + watch | yes | Steal API leader lease → singleton flapping. Bounded |
| `""` | `events` | create, patch | **NO** | Spam events |

### Subjects bound

All 4 RoleBindings (`rbac.yaml:65-68, 91-94, 145-148, 177-180`) bind exactly one `ServiceAccount` subject. **No Group/User/`system:*` subject.** Clean baseline.

**Cross-namespace anomaly**: API `RoleBinding` lives in `workspaceNamespace` but the subject `ServiceAccount` lives in `.Release.Namespace`. If an operator overrides `api.config.kubernetes.namespace` to a different namespace, the API SA in the release ns gets bound into that other namespace's Role — silently widens blast surface across two namespaces.

---

## Phase-1 derived findings

### F1.3.1 (High) — Controller cluster-scope grants 5 unused permissions

| Granted at | Resource | Evidence of non-use |
|---|---|---|
| `rbac.yaml:17-19` | `pods/status` | No `Status().Update`/`Status().Patch` on Pods anywhere in `controller/internal/` |
| `rbac.yaml:26-28` | `services` | No `corev1.Service` constructors, no `services` resource access |
| `rbac.yaml:29-31` | `networkpolicies` | No `networkingv1` import in `controller/`. NetworkPolicies are static Helm templates |
| `rbac.yaml:32-34` | `events` | No EventRecorder; no `recorder.Event*`; `main.go` does not call `mgr.GetEventRecorderFor` |
| `rbac.yaml:38-40` | `configmaps` | No `ConfigMap{}` constructors, no `configmaps` resource access |

Each is a least-privilege violation. In cluster mode they are full-CRUD cluster-wide. **Fix is one-line removals from `$controllerRules`.**

### F1.3.2 (High) — `coordination.k8s.io/leases` cluster-wide; only release-ns needed

`rbac.yaml:35-37`. Leader election only needs the lock in `.Release.Namespace`. Cluster-wide grant lets a leaked controller token hijack `kube-system/kube-controller-manager`, `kube-scheduler`, `coredns` leader leases → DoS or worse.

### F1.3.3 (Medium) — `secrets` and `pods` cluster-wide vs `controller.watchNamespaces` intent

`controller.watchNamespaces` (`values.yaml:155-165`) gives operators per-namespace cache scoping, but RBAC remains cluster-wide. A leaked token reaches every namespace regardless of cache config.

### F1.3.4 (Medium) — `runtimeenvironments` (full CRUD) granted to API SA but unused

`rbac.yaml:109-114`. Only the typed interface alias is declared (`api/internal/interfaces/interfaces.go:143`). Permission unused.

### F1.3.5 (Medium) — `pods/log` granted to API SA but unused

`rbac.yaml:123-125`. Permits log exfiltration from any pod in workspace ns if SA leaked.

### F1.3.6 (High) — `pods/exec` in workspace ns extends to non-sandbox pods

`rbac.yaml:126-128`. RBAC has no label selector; grants exec on **all pods in workspace ns**. With default config, API SA can exec into API pod itself, controller pod, migration jobs, MCP pods.

### F1.3.7 (Low) — `storageclasses` grant degrades silently in namespace mode

Cosmetic at runtime; indicates the chart was not validated under `rbac.scope: namespace`.

### F1.3.8 (Info) — Subjects bind only ServiceAccounts; no groups/users

Safe baseline. No `system:authenticated` or `system:serviceaccounts` group bindings.

### F1.3.9 (Low) — No grants missing for runtime API calls

Every concrete API call traced has a matching grant. No 403 gap.

---

## Concrete pentest tests (promote to Phase 6 test plan)

Each test exercises a specific finding above. Run with the controller or API SA token (`/var/run/secrets/kubernetes.io/serviceaccount/token`) extracted from a compromised pod or via `kubectl create token`.

**T-1.3.1 (F1.3.1, F1.3.3) — Cluster-wide secret exfiltration via controller SA** → promote to **RT-6.17**.
**T-1.3.2 (F1.3.1) — `pods/status` forge for SSRF** (PodIP injection) → cross-reference to F1.2.2.
**T-1.3.3 (F1.3.1) — NetworkPolicy mass-deletion DoS** → promote to **RT-6.18**.
**T-1.3.4 (F1.3.1) — CoreDNS hijack via configmap edit** → promote to **RT-6.19**.
**T-1.3.5 (F1.3.2) — Steal `kube-controller-manager` leader lease** → promote to **RT-6.20**.
**T-1.3.6 (F1.3.1) — Service hijack of `kubernetes.default`** → promote to **RT-6.21**.
**T-1.3.7 (F1.3.6) — API SA exec into controller pod** → 2-stage escalation (API SA → controller cluster-wide token) → promote to **RT-6.22**.
**T-1.3.8 (F1.3.6) — API SA exec into another API pod, dump JWT secret** → promote to **RT-6.23**.
**T-1.3.9 (F1.3.5) — API SA exfiltrates pod logs** → promote to **RT-6.24**.
**T-1.3.10 (F1.3.3) — API SA reads chart credentials Secret** (postgres/redis/jwt-secret → mint arbitrary tokens) → promote to **RT-6.25**.
**T-1.3.11 (F1.3.7) — Namespace-scope mode: storageclass access denied** → promote to **RT-6.26** under upgrade testing.
**T-1.3.12 (F1.3.8 verify) — Enumerate binding subjects** → cross-cluster sanity check; should always be `kind=ServiceAccount` only.

---

## Recommended remediation prioritization (out of pentest scope)

1. **Drop F1.3.1's five unused rules** from `$controllerRules` (`rbac.yaml:9-44`) — pure win, zero code change.
2. **Move leases (F1.3.2) to a namespaced `Role` in `.Release.Namespace`** — split from `$controllerRules`.
3. **Drop F1.3.4 and F1.3.5 from API Role** (`rbac.yaml:113`, `rbac.yaml:123-125`).
4. **Address F1.3.6** by separating workspace-pod exec from co-located platform-pod exec: deploy workspaces in a dedicated namespace, OR add a Kyverno/admission policy rejecting exec into pods without `component=workspace` label.
5. **Flip default `rbac.scope` to `namespace`** (worklog 0078 G5 deferred). Preconditions: F1.3.7 fix (move storageclasses to a separate ClusterRole) and `controller.watchNamespaces` becomes mandatory.
