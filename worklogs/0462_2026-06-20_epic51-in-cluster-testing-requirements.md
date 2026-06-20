# Worklog: Epic 51 — In-Cluster Testing Requirements

**Date:** 2026-06-20
**Session:** Cataloguing what remains to be validated on real infrastructure after the code, unit tests, Helm template tests, and envtest integration tests for Epic 51 (S51.1–S51.4) have all shipped and merged.
**Status:** Action item — these tests must be run before Epic 51 is declared production-ready.

---

## Context

Epic 51 (Tenant Isolation — gVisor + Resource Quotas) shipped across four PRs:

| PR | Stories | What was tested |
|---|---|---|
| #304 | Design doc | N/A |
| #310 | S51.1 (gVisor), S51.3 (tenant label), S51.4 (hardening) | Unit tests (buildPod, label resolution, hardening assertions), API tests (label spoof prevention) |
| #317 | S51.2 (quota webhook) | Unit tests (12: allow/deny/boundary/isolation/terminal/fail-open) |
| #320 | Integration tests | Helm template tests (10: rendering correctness), envtest webhook integration (2: real API server admission chain) |

What passed: Go handler logic, Helm manifest rendering, and envtest-level webhook admission (real kube-apiserver routing admission requests to the handler and enforcing denial).

What was NOT tested: anything requiring real compute nodes (gVisor runtime), real networking (CNI NetworkPolicy enforcement), or real storage (EFS). These are documented below as explicit action items.

---

## In-Cluster Testing Checklist

### 1. gVisor RuntimeClass — pods actually run under runsc (AC8 prerequisite)

**Why it can't be tested outside a cluster:** envtest runs kube-apiserver + etcd but no kubelet, no container runtime, no nodes. A pod created via envtest never actually starts — it's an API object only. gVisor isolation requires the `runsc` binary on a real node, a configured container runtime (containerd/CRI-O), and a pod that actually gets scheduled and started.

**Setup:**
- Provision at least one node with gVisor installed:
  - EKS + Karpenter: add `runsc` install to `EC2NodeClass` userData (Epic 18 S18.9 plans for this — coordinate).
  - Manual cluster: follow https://gvisor.dev/docs/user_guide/install/.
  - Kind/minikube: not supported (gVisor requires a real kernel; nested virtualization in CI runners is unreliable).
- Deploy the chart with `gvisor.enabled: true`.
- Verify the RuntimeClass exists: `kubectl get runtimeclass gvisor`.
- Verify the controller flag is set: `kubectl -n <ns> get deploy <release>-controller -o jsonpath='{.spec.template.spec.containers[0].args}' | tr ',' '\n' | grep runtime-class`.

**Test:**
```bash
# Create a workspace and verify it schedules under gVisor.
# Confirm the RuntimeClass is set on the pod:
kubectl get pod -n <ns> <workspace-pod> -o jsonpath='{.spec.runtimeClassName}'
# Expected: gvisor
# To verify runsc is actually the active runtime (not just the
# RuntimeClass assignment), check node-level signals:
# - Pod events may show "runsc" in sandbox creation messages
# - gVisor's /proc is a synthetic filesystem; compare /proc/version
#   inside the pod vs the host kernel — they will differ under gVisor
# - The definitive check: run `dmesg` on the node and look for
#   runsc log lines (gVisor logs to the node's dmesg by default)
```

**Pass criteria:**
- Workspace pod reaches `Running` phase (not stuck in `Pending`/`ContainerCreating` with a runtime error).
- `spec.runtimeClassName` is `gvisor`.
- Pod's `/proc` or kernel version string confirms runsc is the active runtime.

**Fail modes to watch for:**
- `RuntimeClass "gvisor" not found` — RuntimeClass not deployed or wrong name.
- `Failed to create pod sandbox` with runsc error — gVisor not installed on the node, or containerd not configured with the runsc handler.
- Pod stays `Pending` — node selector or taint prevents scheduling on gVisor-capable nodes.

---

### 2. gVisor compatibility with runtime toolchains (AC2 prerequisite)

**Why it matters:** gVisor doesn't implement all Linux syscalls. Most dev tooling works, but ptrace-based debuggers, certain seccomp filters, and some networking tools may break. The per-workspace `spec.runtimeClass: "runc"` opt-out exists for this, but needs validation.

**Test matrix:**

| Runtime | Test command | Expected |
|---|---|---|
| Python | `pip install numpy && python -c "import numpy; print(numpy.__version__)"` | Works |
| Node | `npm install express && node -e "require('express')"` | Works |
| Go | `go build ./...` | Works |
| Rust | `cargo new test && cd test && cargo build` | Works |
| Java | `javac Hello.java && java Hello` | Works (monitor JIT performance) |
| mise | `mise install python@3.12 node@20` | Works |
| opencode | Full agent session: prompt → response → tool use → file write | Works |

**Test procedure per runtime:**
```bash
# Create a workspace with the runtime, under gVisor:
# (ensure gvisor.enabled=true in Helm values)
llmspaces create --runtime python:3.12 --name gvisor-compat-test
# Wait for Active, then exec into the pod and run the test command.
```

**Pass criteria:**
- All runtimes in the matrix complete without errors.
- opencode full session works (prompt → LLM response → tool execution → file write verified).

**Fail modes:**
- `ENOENT` or `EPERM` from syscall gVisor doesn't implement → document in compatibility matrix, add to opt-out guidance.
- Java JIT significantly slower → measure startup time; if >2x slower, document.

---

### 3. gVisor performance overhead (AC8)

**Why it matters:** Acceptance criterion 8 requires measuring overhead and accepting/rejecting gVisor based on a <30% overhead target. This cannot be done without real nodes.

**Test:**
```bash
# Create two identical workspaces — one under gVisor, one under runc:
# gVisor:
llmspaces create --runtime python:3.12 --name perf-gvisor
# runc (opt-out):
llmspaces create --runtime python:3.12 --name perf-runc
#   (set spec.runtimeClass: "runc" via CRD patch or admin API)

# In each workspace, run a representative I/O + compute benchmark:
# - File I/O: fio 4K random read/write
# - Build: pip install + python startup time
# - Network: curl to an external LLM API endpoint (the dominant workload)

# Measure:
# 1. Workspace cold-start time (Pending → Active): compare gVisor vs runc
# 2. File I/O throughput: fio results
# 3. LLM API round-trip latency: time curl
```

**Pass criteria:**
- Cold-start overhead <30% (e.g., runc 10s → gVisor <13s).
- File I/O overhead <30%.
- Network latency overhead <10% (gVisor networking is typically near-native).
- If any metric exceeds target, document and evaluate whether to keep gVisor default or make it opt-in.

**What to document:**
- Results table (runc vs gVisor for each metric).
- Decision: gVisor as default (production) vs opt-in.
- If Java JIT >2x slower, add a note to the compatibility matrix.

---

### 4. Per-tenant quota webhook — real cluster enforcement (AC5, AC9)

**Why envtest isn't sufficient:** envtest proves the API server routes admission requests to the webhook and enforces denial. It does NOT prove:
- The `objectSelector` in the rendered ValidatingWebhookConfiguration actually filters correctly in production (envtest uses a programmatically-constructed VWC, not the Helm-rendered one).
- The webhook works with real controller-runtime client (envtest uses a bare client without informer cache).
- Quota counting works across multiple namespaces (envtest uses a single `default` namespace).

**Test:**
```bash
# Deploy with quota enabled:
# helm install ... --set webhooks.tenantQuota.maxWorkspacesPerTenant=3

# Create 3 workspaces for the same user/org:
llmspaces create --runtime python:3.12  # workspace 1 — should succeed
llmspaces create --runtime python:3.12  # workspace 2 — should succeed
llmspaces create --runtime python:3.12  # workspace 3 — should succeed (at limit)
llmspaces create --runtime python:3.12  # workspace 4 — should be REJECTED

# Verify the rejection message references the quota:
# Expected: "tenant \"<tenant-id>\" workspace count 4 would exceed limit 3"

# Create a workspace for a DIFFERENT user/org — should succeed:
# (proves tenant isolation, not just global count)
```

**Pass criteria:**
- Pod 4 (over limit) is rejected by the webhook, not by the API service.
- The rejection message contains the tenant ID and the count/limit.
- A different tenant's workspace is not affected.
- Workspaces created before enabling the quota continue to run (no retroactive eviction).

**Fail modes:**
- All creates succeed → webhook not registered, objectSelector filtering out workspace pods, or controller flag not set.
- All creates fail → webhook handler erroring (check controller logs), or wrong limit value.
- Wrong tenant affected → label mismatch between pod and CRD.

---

### 5. Network isolation between tenants (AC3)

**Why it can't be envtest-tested:** envtest doesn't run a CNI or kube-proxy. NetworkPolicy enforcement requires a real CNI (Calico, Cilium, AWS VPC CNI with NetworkPolicy support). The chart-level default-deny ingress + RFC1918-filtered egress are rendered and verified by Helm template tests, but policy enforcement is CNI-dependent.

**Test:**
```bash
# Create two workspaces for two different users/orgs:
llmspaces create --runtime python:3.12 --name tenant-a-ws   # user-a
llmspaces create --runtime python:3.12 --name tenant-b-ws   # user-b

# Get the pod IPs:
POD_A=$(kubectl -n <ns> get pod -l llmsafespaces.dev/workspace=tenant-a-ws -o jsonpath='{.items[0].status.podIP}')
POD_B=$(kubectl -n <ns> get pod -l llmsafespaces.dev/workspace=tenant-b-ws -o jsonpath='{.items[0].status.podIP}')

# From tenant-a's pod, try to reach tenant-b's pod:
kubectl -n <ns> exec tenant-a-ws-pod -- curl -sS --connect-timeout 5 http://$POD_B:4096/healthz
# Expected: connection timeout (NetworkPolicy blocks it)

# Verify tenant-a CAN reach the API service (legitimate path):
kubectl -n <ns> exec tenant-a-ws-pod -- curl -sS --connect-timeout 5 http://<api-service>:8080/healthz
# Expected: 200 OK
```

**Pass criteria:**
- Pod-to-pod traffic between different tenants is blocked (curl times out).
- Pod-to-API traffic works (curl returns 200).
- Pod-to-external-internet works (curl to a public LLM API endpoint).

**Fail modes:**
- Tenant A can reach tenant B → CNI not enforcing NetworkPolicy, or chart policies misconfigured.
- Tenant A can't reach API → egress policy too restrictive, or API service selector mismatch.
- Depends on CNI: AWS VPC CNI requires the NetworkPolicy engine (Amazon's VPC CNI NetworkPolicy or Calico).

---

### 6. gVisor opt-out via spec.runtimeClass (AC2)

**Test:**
```bash
# With gvisor.enabled=true (default RuntimeClass is gvisor):
# Create a workspace that opts out:
llmspaces create --runtime python:3.12 --name optout-test
# Patch the workspace CRD to set spec.runtimeClass: "runc":
kubectl patch workspace optout-test --type=merge -p '{"spec":{"runtimeClass":"runc"}}'

# Verify the pod runs under runc, not gVisor:
kubectl get pod -n <ns> -l llmsafespaces.dev/workspace=optout-test -o jsonpath='{.items[0].spec.runtimeClassName}'
# Expected: runc (the controller sets RuntimeClassName to the explicit
# value "runc" from spec.runtimeClass — not cleared/empty)

# Verify the workspace still functions:
kubectl exec -n <ns> <optout-pod> -- python -c "print('hello from runc')"
```

**Pass criteria:**
- Opt-out workspace's pod has `runtimeClassName: runc` (explicitly set by the controller, not cleared).
- Workspace functions normally under runc.
- Non-opted-out workspaces still run under gVisor.

---

### 7. Quota webhook fail-open behavior

**Note:** This test validates Kubernetes-level `failurePolicy` behavior (what happens when the webhook endpoint is unreachable). This is distinct from the handler-level fail-open in `PodTenantQuotaValidator.Handle()` (which returns `Allowed` when the client.List call errors — tested in unit tests, not reproducible here). Scaling the controller tests whether the API server respects `failurePolicy: Fail` (the chart default) when the webhook server is down.

**Test:**
```bash
# Deploy with quota enabled.
# Scale the controller to 0 replicas (makes the webhook endpoint unreachable):
kubectl scale deploy -n <ns> <release>-controller --replicas=0

# Attempt to create a workspace:
# Expected: creation FAILS — the chart defaults to failurePolicy: Fail,
# so the API server rejects the pod create when the webhook endpoint
# is unreachable (connection refused / timeout). This is the secure
# default: no workspaces created while the controller is down.

# Scale controller back up:
kubectl scale deploy -n <ns> <release>-controller --replicas=1

# Verify workspace creation works again after recovery.
```

**Pass criteria:**
- With `failurePolicy: Fail` (default): workspace creation fails while the controller is down. This is the secure default — no workspaces created while the webhook is unavailable.
- After controller recovery, workspace creation works again.

**Note:** The chart currently uses a single `failurePolicy` value (`webhooks.failurePolicy`, default `Fail`) for ALL three webhooks (runtimeenv, workspace, quota). Changing it to `Ignore` would also weaken the workspace validation webhook (registry allow-list, status forge protection), which is not recommended. If per-webhook failurePolicy is needed in the future (e.g., quota webhook `Ignore` while workspace validation stays `Fail`), the chart's `validating-webhook.yaml` must be refactored to template per-webhook failurePolicy values rather than the shared `webhooks.failurePolicy`. This is a future enhancement, not a current option.

---

## Priority Order

1. **gVisor runtime test (#1)** — blocks everything else; if gVisor doesn't work, the entire isolation thesis is invalid.
2. **gVisor compatibility (#2)** — if common runtimes break under gVisor, we need the opt-out path validated before production.
3. **Network isolation (#5)** — the other half of the tenant isolation story; works independently of gVisor.
4. **gVisor performance (#3)** — determines whether gVisor is viable as a default or must be opt-in.
5. **Quota webhook enforcement (#4)** — proven at envtest level; cluster test confirms Helm rendering + real controller client.
6. **gVisor opt-out (#6)** — quick validation once #1 and #2 pass.
7. **Quota fail-open (#7)** — operational validation; low risk but important to document.

---

## Infrastructure Requirements

| Test | Needs | How to provision |
|---|---|---|
| gVisor runtime (#1) | Node with runsc installed | EKS + Karpenter EC2NodeClass userData, or manual AMI |
| gVisor compatibility (#2) | Same as #1 | Same as #1 |
| gVisor performance (#3) | Same as #1, plus a baseline runc node | Two node pools or one node with both runtimes |
| Quota webhook (#4) | Any cluster with the chart deployed | Standard deployment |
| Network isolation (#5) | CNI with NetworkPolicy enforcement | Calico, Cilium, or AWS VPC CNI with NP |
| gVisor opt-out (#6) | Same as #1 | Same as #1 |
| Quota fail-open (#7) | Any cluster | Standard deployment |

**Estimated effort:** 1–2 days on a provisioned cluster, assuming gVisor nodes are available. The bulk of the time is gVisor node provisioning (AMI/userData) and the compatibility matrix.
