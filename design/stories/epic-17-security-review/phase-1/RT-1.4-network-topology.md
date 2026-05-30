# RT-1.4 — Network Topology & NetworkPolicy Mapping

**Phase:** 1 (Reconnaissance)
**Cluster:** `admin@home-kubernetes`
**Release:** `llmsafespace` rev 68 in `default`
**Control-plane image:** `ghcr.io/lenaxia/llmsafespace/{api,controller}:sha-cdd6305`
**Runtime base image:** `ghcr.io/lenaxia/llmsafespace/base:sha-cdf2ddc`
**G16 status:** PRE-fix. Installed release does NOT include `workspace-network-policy.yaml` (introduced in commit `8ac3953`). User-supplied values omit the `networkPolicy:` block; `helm get manifest` shows zero NetworkPolicy resources rendered.

---

## 1. Static NetworkPolicy inventory

The chart ships exactly **one** NetworkPolicy template: `charts/llmsafespace/templates/workspace-network-policy.yaml`. It renders **two** NetworkPolicy objects, both gated on `.Values.networkPolicy.enabled` (line 1).

### 1a. Render with `networkPolicy.enabled=false` (current cluster)

Zero NetworkPolicy objects. No ingress restriction. No egress restriction. All cluster pod-to-pod traffic permitted by CNI default. **This is the live state.**

### 1b. Render with `networkPolicy.enabled=true` (post-fix)

#### Policy 1: `<release>-workspace-default-deny-ingress` (`workspace-network-policy.yaml:15-41`)

| Field | Value |
|---|---|
| podSelector | `app: llmsafespace`, `component: workspace` |
| policyTypes | Ingress only |
| ingress.from | namespaceSelector `kubernetes.io/metadata.name = <release ns>` AND podSelector `apiPodLabelSelector` (default: `app.kubernetes.io/component=api`) |
| ingress.ports | TCP 4096 (opencode), TCP 4097 (agentd) |

**Effective:** Only the API-server pod, in the same namespace, may open TCP to a sandbox, only on 4096/4097. Cross-sandbox ingress, controller→sandbox, frontend→sandbox, all denied.

#### Policy 2: `<release>-workspace-egress` (`workspace-network-policy.yaml:54-93`)

| Field | Value |
|---|---|
| podSelector | same as Policy 1 |
| policyTypes | Egress only |
| egress rule 1 | DNS UDP/TCP 53 to `kube-system` `k8s-app=kube-dns` |
| egress rule 2 | `0.0.0.0/0` except RFC1918 + `169.254.0.0/16` (cloud metadata) |

**Effective:** Sandbox can reach public internet (any port, any protocol — no port restriction) and DNS through kube-dns. Denied egress to: all RFC1918 (in-cluster), cloud metadata.

**Caveat (NF-5 below):** Policy 1 hard-codes the namespace selector to `.Release.Namespace`. If the API server runs in a different namespace from the workspaces (`api.config.kubernetes.namespace` override), the rule misrenders.

---

## 2. Live cluster current state (PRE-G16)

```
$ kubectl get netpol -A
No resources found

$ kubectl get cnp,ccnp -A
No resources found

$ helm get manifest llmsafespace -n default | grep -c '^kind: NetworkPolicy'
0
```

CRDs for Cilium policies are installed but no instances exist. Confirms the live release does not include the G16 template.

---

## 3. Service exposure map

### 3a. Services in `default`

| Service | Type | Port | Backing | Reachable how |
|---|---|---|---|---|
| `llmsafespace-api` | ClusterIP | 8080 | api Deployment | Cluster-internal + via Ingress `/api` path |
| `llmsafespace-controller-metrics` | ClusterIP | 8080 | controller | **Cluster-internal only, no auth** |
| `llmsafespace-controller-webhook` | ClusterIP | 443→9443 | controller | Cluster-internal, mTLS, consumed by kube-apiserver |
| `llmsafespace-frontend` | ClusterIP | 80 | frontend Deployment | Cluster-internal + Ingress at `safespace.thekao.cloud` |
| `postgres` | ClusterIP | 5432 | external | Cluster-internal only |
| `valkey` (Redis-compat) | ClusterIP | 6379 | external | Cluster-internal only |

**No NodePort, no LoadBalancer.** External exposure is only via Ingress.

### 3b. Ingresses

| Host | Routes |
|---|---|
| `safespace.thekao.cloud/api/*` → `llmsafespace-api:8080` | API |
| `safespace.thekao.cloud/*` → `llmsafespace-frontend:80` | Frontend |

Sandbox pods (4096/4097) are NOT exposed via any Ingress.

---

## 4. Pod-to-pod connectivity (live, observed)

All connectivity below was verified by `kubectl exec` from sandbox pod `51b1c286-...-d3735f57` in `default` using `bash`'s `/dev/tcp` and `curl`. **Pre-fix baseline: nothing is blocked at L3/L4.**

### 4a. Observed evidence

```
=== TCP to postgres:5432 ===                       CONNECT_OK
=== TCP to llmsafespace-api:8080 ===               CONNECT_OK
=== TCP to llmsafespace-controller-webhook:443 === CONNECT_OK
=== TCP to k8s api 10.96.0.1:443 ===               CONNECT_OK
=== TCP to valkey:6379 ===                         CONNECT_OK
=== TCP to cloud metadata 169.254.169.254:80 ===   CONNECT_FAIL  (Talos node fw, NOT k8s)
=== HTTPS to docs.python.org ===                   http_code=200
=== HTTPS to api.openai.com ===                    http_code=421 (TLS reached LB)

# cross-sandbox lateral movement
=== Cross-sandbox TCP to peer agentd:4097 ===      CONNECT_OK
=== Cross-sandbox TCP to peer opencode:4096 ===    CONNECT_OK
=== HTTP GET peer /v1/healthz ===                  http_code=200
=== HTTP GET peer /v1/statusz ===                  http_code=200

# control-plane scrape from sandbox
=== controller /metrics:8080 ===  http_code=200   (UNAUTH metrics)
=== controller /readyz:8081 ===   http_code=200
=== controller webhook :9443 ===  CONNECT_OK
```

### 4b. Auth-gate summary

- **Postgres / Redis:** TCP open from sandbox; auth blocks queries. Pre-fix sandbox has full TCP access for DoS, version probing, 0-day exploitation.
- **kube-apiserver:** Sandbox pod's ServiceAccount is `default` (no LLMSafeSpace-specific SA). Token grants minimal but non-zero permissions.
- **Controller `/metrics`:** **Wide-open Prometheus from any in-cluster pod.** Leaks reconcile counters, leader-election state, queue depth, workspace UIDs in metric labels.
- **agentd `/v1/statusz`, `/v1/healthz`:** Unauthenticated. **Cross-tenant info disclosure** (sessions list, providers, agent version, last error).

---

## 5. Egress paths

### 5a. From sandbox pod (live, pre-G16)

| Destination | Result | Concern |
|---|---|---|
| Public DNS | OK | required |
| Public internet over 443 | OK | expected |
| Public internet over arbitrary ports | OK (no policy) | C2 channel risk |
| Cloud metadata `169.254.169.254` | **FAIL at L3** | Talos node fw — **not** k8s. Won't hold on cloud-hosted clusters |
| In-cluster Postgres / Redis / API / controller / sandbox-peer / kube-apiserver | OK | All blocked post-fix; wide open pre-fix |
| Attacker-controlled domains | OK pre-fix; **OK post-fix** | Post-fix only excludes RFC1918+metadata; exfil channel persists |

### 5b. From API / controller / frontend pods

No NetworkPolicy applies pre- or post-fix; chart only ships workspace-scoped policies.

---

## 6. Phase-1 derived findings

### F1.4.1 (High) — Live cluster has zero L3/L4 isolation between tenants

§2 (no netpol/cnp/ccnp), §4 (cross-sandbox TCP/HTTP succeed), §1.b (G16 fix not deployed). Promote to **RT-2.x**: cross-tenant lateral movement (sandbox A → sandbox B's agentd `/v1/statusz`).

### F1.4.2 (High) — agentd `/v1/statusz` and `/v1/healthz` unauthenticated

`pkg/agentd/types.go:51-63` shows the StatuszResponse schema includes sessions list with IDs, status, titles. Cross-tenant `curl http://<peer-sandbox-IP>:4097/v1/statusz` returned 200 with this body. **Even with G16 ingress NetPol post-fix, this is still reachable from the API pod** — cross-tenant via API proxy may still be possible if path-based authz is missing. Promote to **RT-3.18**: agentd unauthenticated endpoint enumeration.

### F1.4.3 (Medium) — Controller `/metrics` unauthenticated and unrestricted

`controller-deployment.yaml:48-51` exposes the metrics port directly; `controller-service.yaml:9-17` makes a ClusterIP for it. No `kube-rbac-proxy` wrapper. Even post-G16, the workspace egress policy blocks RFC1918 from sandbox→controller-metrics, but **no policy on non-workspace pods** (any other tenant pod, sidecar, batch job in cluster). Promote to **RT-4.17**: controller metrics scrape from arbitrary in-cluster pod.

### F1.4.4 (Medium) — Workspace egress allow-list is wide (0.0.0.0/0 minus RFC1918)

`values.yaml:335-343`, `workspace-network-policy.yaml:82-93`. Fix only excludes private CIDRs; doesn't restrict ports, domains, or destinations. A jailbreaking agent can dial out to attacker C2 on any TCP/UDP port post-fix. Documented as accepted risk in chart. Promote to **RT-5.16**: verify operator can configure a strict allowlist (FQDN-restriction via Cilium FQDN policies).

### F1.4.5 (Low) — Ingress allow rule pins to `.Release.Namespace`, not workspace namespace

`workspace-network-policy.yaml:33`. If operator splits workspaces into a separate namespace, the policy is installed in the workspace ns but selects only the namespace label that matches the release ns. Works only because the API pod IS in the release ns. Promote to **RT-6.27**: NetPol correctness when `api.config.kubernetes.namespace` is overridden.

### F1.4.6 (Low) — Cloud-metadata block enforced by node, not Kubernetes

§5a — TCP to `169.254.169.254:80` fails *with no NetworkPolicy in place*, indicating Talos node-level firewall. On generic EKS/GKE this would succeed pre-fix. Promote to **RT-7.16**: verify cloud-metadata block in cloud-hosted reference clusters; do not rely on host fw.

### F1.4.7 (Low) — MCP service path documented but disabled

`mcp.enabled=false` in user values. Note for completeness: when MCP is enabled, **the workspace NetPols do not cover it** — MCP would have unrestricted ingress from anywhere in-cluster.

### F1.4.8 (Info) — All LLMSafeSpace services are ClusterIP; only frontend is Ingress-exposed

Confirms public attack surface is exactly `safespace.thekao.cloud/{,api/}*`. No NodePort/LoadBalancer leakage.

---

## Appendix: Live evidence excerpts

```
$ kubectl config current-context
admin@home-kubernetes

$ kubectl get netpol -A
No resources found

$ kubectl get cnp,ccnp -A
No resources found

$ kubectl get svc -n default | grep llmsafespace
llmsafespace-api                  ClusterIP   10.96.44.192    <none>   8080/TCP
llmsafespace-controller-metrics   ClusterIP   10.96.24.198    <none>   8080/TCP
llmsafespace-controller-webhook   ClusterIP   10.96.219.236   <none>   443/TCP
llmsafespace-frontend             ClusterIP   10.96.206.15    <none>   80/TCP

$ helm list -A | grep llmsafespace
llmsafespace  default  68  2026-05-29 22:56:17  deployed  llmsafespace-0.1.0  0.1.0
```

All connectivity probes in §4a were performed from inside `default/51b1c286-...-d3735f57` — a default-namespace sandbox pod — only against documented test domains and other in-cluster targets. No exfiltration to attacker domains; blast-radius rules respected.
