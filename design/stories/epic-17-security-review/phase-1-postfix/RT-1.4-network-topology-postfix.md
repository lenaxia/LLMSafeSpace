# RT-1.4 — Network Topology & NetworkPolicy Mapping (POST-FIX)

**Phase:** 1-postfix (re-test after G16 / G17 / G2 remediation)
**Cluster:** `admin@home-kubernetes` (live, prod variant — same cluster as pre-fix RT-1.4)
**Release:** `llmsafespace` rev **71** in `default` (current `deployed`; rev 70 was a transient `superseded` from a same-second helm upgrade — see Helm history below)
**Control-plane image:** `ghcr.io/lenaxia/llmsafespace/{api,controller,frontend}:sha-eb5c33e`
**Runtime base image:** `ghcr.io/lenaxia/llmsafespace/base:sha-eb5c33e` (G2 bash-shim refactor)
**G16 status:** **POST-fix.** `helm get values` shows `networkPolicy.enabled=true`. Two NetworkPolicy objects rendered and applied.
**G17 status:** **POST-fix.** Sandbox pods spec'd with `automountServiceAccountToken: false`; no SA token volume mounted; no `/var/run/secrets/kubernetes.io/serviceaccount/` directory inside the workspace container.
**G2 status:** Runtime image is `sha-eb5c33e` (post bash-shim refactor). Out of scope for RT-1.4 connectivity probes; noted only for image-tag traceability.

Reference (pre-fix): [`../phase-1/RT-1.4-network-topology.md`](../phase-1/RT-1.4-network-topology.md).

> **Helm release rev note:** the user's task brief specified rev 70. Live `helm history` shows rev 70 was superseded one second later by rev 71 (10:27:06 → 10:28:06 UTC-7) — both apply the same chart at sha-eb5c33e and both contain the same NetworkPolicy / `automountServiceAccountToken` shape. The post-fix tests below were executed against the current `deployed` revision (71). No semantic difference vs rev 70.

---

## 1. Helm-rendered NetworkPolicy state (live)

```
$ kubectl -n default get netpol
NAME                                          POD-SELECTOR                           AGE
llmsafespace-workspace-default-deny-ingress   app=llmsafespace,component=workspace   ~7m
llmsafespace-workspace-egress                 app=llmsafespace,component=workspace   ~7m

$ kubectl get cnp,ccnp -A
No resources found
```

Two NetworkPolicy resources, both gating on the workspace pod label `app=llmsafespace,component=workspace`. No CiliumNetworkPolicy / CiliumClusterwideNetworkPolicy. (Cilium CRDs installed but unused — chart only emits standard k8s NetworkPolicy.)

### 1a. `llmsafespace-workspace-default-deny-ingress` — full spec

```yaml
spec:
  podSelector:
    matchLabels:
      app: llmsafespace
      component: workspace
  policyTypes: [ Ingress ]
  ingress:
  - from:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: default          # pinned to release ns (F1.4.5 unchanged)
      podSelector:
        matchLabels:
          app.kubernetes.io/component: api
          app.kubernetes.io/name: llmsafespace
    ports:
    - { port: 4096, protocol: TCP }   # opencode
    - { port: 4097, protocol: TCP }   # agentd
```

**Effective rule:** Only pods in namespace `default` carrying both labels `app.kubernetes.io/component=api` AND `app.kubernetes.io/name=llmsafespace` may open TCP to a sandbox, and only on 4096/4097. All other ingress (controller, frontend, peer sandboxes, arbitrary cluster pods, kube-system) is dropped.

> Note: kubelet's liveness/readiness probes to 4097 still succeed because k8s NetworkPolicy semantically allows host-network kubelet→pod traffic (verified: `restartCount=0`, pod `Ready=True`).

### 1b. `llmsafespace-workspace-egress` — full spec

```yaml
spec:
  podSelector:
    matchLabels:
      app: llmsafespace
      component: workspace
  policyTypes: [ Egress ]
  egress:
  - to:
    - namespaceSelector: { matchLabels: { kubernetes.io/metadata.name: kube-system } }
      podSelector: { matchLabels: { k8s-app: kube-dns } }
    ports:
    - { port: 53, protocol: UDP }
    - { port: 53, protocol: TCP }
  - to:
    - ipBlock:
        cidr: 0.0.0.0/0
        except:
        - 10.0.0.0/8
        - 172.16.0.0/12
        - 192.168.0.0/16
        - 169.254.0.0/16
```

**Effective rule:** Egress to kube-dns CoreDNS pods on UDP/TCP 53; egress to public internet on **any** port; everything else (RFC1918 in-cluster, link-local cloud metadata) dropped.

CoreDNS pods carry the `k8s-app=kube-dns` label in `kube-system` (verified `kubectl -n kube-system get pods -l k8s-app=kube-dns` returns the two coredns pods), so the DNS allow rule resolves correctly on this cluster.

### 1c. Helm-values cross-check

```
$ helm -n default get values llmsafespace --all | grep -A 12 '^networkPolicy:'
networkPolicy:
  allowedEgressCIDRs: [ 0.0.0.0/0 ]
  apiPodLabelSelector:
    app.kubernetes.io/component: api
    app.kubernetes.io/name: llmsafespace
  blockedEgressCIDRs: [ 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 ]
  dnsNamespace: kube-system
  dnsPodLabelSelector: { k8s-app: kube-dns }
  enabled: true
```

Matches the rendered policy above. G16 is fully deployed.

---

## 2. ServiceAccount-token presence inside sandbox (G17 enforcement)

Probe pod: `default/6d36952e-0cd5-42cb-9630-8f988b3e5f33-cbde9a23` (workspace container).

```
$ kubectl -n default exec <sbx> -- bash -c '...'

ls /var/run/secrets/kubernetes.io/                  → No such file or directory
ls /var/run/secrets/kubernetes.io/serviceaccount/   → No such file or directory
ls /var/run/secrets/kubernetes.io/serviceaccount/token → No such file or directory
mount | grep secret                                 → (no serviceaccount mounts)
ls /var/run/                                        → only `lock/`  (no serviceaccount projection)
```

```
$ kubectl -n default get pod <sbx> -o jsonpath='{.spec.automountServiceAccountToken}'
false

$ kubectl -n default get pod <sbx> -o jsonpath='{.spec.serviceAccountName}'
default

$ kubectl -n default get pod <sbx> -o jsonpath='{.spec.volumes}'
# volumes: workspace (PVC), sandbox-cfg (emptyDir), tmp (emptyDir),
#          sandbox-home (emptyDir), pw-secret.   ← NO `kube-api-access-*` projected volume
```

**Verdict:** G17 is fully enforced. The kubelet does not project a SA token volume, the workspace container has no `/var/run/secrets/kubernetes.io/serviceaccount` directory, and the pod-spec carries `automountServiceAccountToken: false`. Source-of-truth: `controller/internal/workspace/controller.go:697` sets the field directly on every workspace PodSpec.

A jailbreaking agent inside the sandbox can no longer present a ServiceAccount JWT to the kube-apiserver (and post-G16 cannot even reach 10.96.0.1:443 over TCP — see §3).

---

## 3. Connectivity probes from inside a sandbox (G16 egress enforcement)

Source pod: `6d36952e-0cd5-42cb-9630-8f988b3e5f33-cbde9a23` (10.69.6.128).
Peers: `9900e481-...-11f7367c` (10.69.6.69), `c9992183-...-de2a96c3` (10.69.6.107).
Probe technique: same as pre-fix RT-1.4 — `bash -c 'exec 3<>/dev/tcp/<host>/<port>'` for L4 connect, `curl --connect-timeout 5` for L7.

### 3a. Sandbox → cross-tenant peer sandboxes

| Target | Port | TCP result | HTTP result | Pre-fix | Post-fix delta |
|---|---|---|---|---|---|
| peer-A (10.69.6.69) opencode | 4096 | **CONNECT_FAIL (timeout)** | — | CONNECT_OK | **BLOCKED by deny-ingress on peer** |
| peer-A (10.69.6.69) agentd | 4097 | **CONNECT_FAIL (timeout)** | — | CONNECT_OK | **BLOCKED** |
| peer-A (10.69.6.69) `/v1/statusz` | 4097 | n/a | n/a (TCP filtered) | http_code=200 (full sessions list leaked) | **BLOCKED at L4** |
| peer-A (10.69.6.69) `/v1/healthz` | 4097 | n/a | n/a (TCP filtered) | http_code=200 | **BLOCKED at L4** |
| peer-B (10.69.6.107) 4096/4097 | both | **CONNECT_FAIL (timeout)** | — | CONNECT_OK | **BLOCKED** |

Cross-tenant lateral movement at L3/L4 is closed by the deny-ingress NetworkPolicy on the peer sandbox (peer-as-target rejects everything not coming from an API-labeled pod in `default`).

### 3b. Sandbox → in-cluster control-plane / data-plane

| Target | Port | TCP result | HTTP result | Pre-fix | Post-fix delta |
|---|---|---|---|---|---|
| `postgres.default.svc` (10.96.72.15) | 5432 | **CONNECT_FAIL (timeout)** | — | CONNECT_OK | **BLOCKED by egress (10.0.0.0/8 except'd)** |
| `valkey.default.svc` (10.96.185.67) | 6379 | **CONNECT_FAIL (timeout)** | — | CONNECT_OK | **BLOCKED** |
| `llmsafespace-controller-metrics` (10.96.24.198) | 8080 | **CONNECT_FAIL (timeout)** | — | http_code=200 (UNAUTH metrics scrape) | **BLOCKED from sandbox** (but see §5: still open from non-workspace pods) |
| kube-apiserver `10.96.0.1` | 443 | **CONNECT_FAIL (timeout)** | — | CONNECT_OK | **BLOCKED** |
| `kubernetes.default.svc` | 443 | **CONNECT_FAIL (timeout)** | — | CONNECT_OK | **BLOCKED** |

DNS works (kube-dns explicitly allowlisted): `getent hosts postgres.default.svc` resolves to 10.96.72.15, but the subsequent TCP connect is filtered by the egress `0.0.0.0/0 except 10.0.0.0/8` rule. Identical pattern for valkey, controller-metrics, kube-apiserver.

### 3c. Sandbox → cloud / external

| Target | Port | TCP result | HTTP result | Pre-fix | Post-fix delta |
|---|---|---|---|---|---|
| 169.254.169.254 (IMDS) | 80 | **CONNECT_FAIL (timeout)** | — | CONNECT_FAIL (Talos node fw) | **Now blocked at NetworkPolicy layer too** (defence-in-depth, no longer relying on host fw) |
| `api.openai.com` | 443 | CONNECT_OK | http_code=401 (TLS up, auth missing) | http_code=421 | unchanged — public LLM egress permitted |
| `docs.python.org` | 443 | CONNECT_OK | http_code=200 | http_code=200 | unchanged — pip/docs egress permitted |
| `1.1.1.1` | 80, 53, 443, 8080 | CONNECT_OK on all | — | CONNECT_OK | unchanged — **arbitrary public ports still open** (F1.4.4 still open) |
| `1.1.1.1` | 1234 (non-listening) | timeout (target drops) | — | timeout | not a NetPol effect; CF drops |

DNS resolutions verified inside sandbox: `api.openai.com → 162.159.140.245 / 172.66.0.243`; `docs.python.org → fastly IPv6 set`. CoreDNS path is intact.

### 3d. Liveness / readiness probes from kubelet → sandbox

```
$ kubectl -n default get pod <sbx> -o jsonpath='{.status.containerStatuses[0].restartCount}'
0
$ kubectl -n default get pod <sbx> -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
True
```

3 sandbox pods Running, 0 restarts, all Ready. The deny-ingress NetworkPolicy did **not** break kubelet HTTP-GET liveness on `:4097/v1/healthz` or readiness on `:4097/v1/readyz`. (Standard k8s semantics — host-network kubelet traffic is not subject to NetworkPolicy. Verified empirically.)

---

## 4. Connectivity probes from a peer perspective (NetworkPolicy ingress allow-list correctness)

To verify the ingress allow rule resolves correctly, two ephemeral probe pods were planted and torn down (under blast-radius rules — both in `default` ns):

- `pentest-api-impersonator` — labels `app.kubernetes.io/component=api`, `app.kubernetes.io/name=llmsafespace` (matches the NetPol selector)
- `pentest-rando-pod` — no llmsafespace labels

Both used `alpine:3.20` with `automountServiceAccountToken: false`.

### 4a. API-labeled pod → sandbox (should be ALLOWED on 4096/4097, denied on others)

```
> http://10.69.6.128:4096/        → http_code=401   (opencode auth challenge — TCP+HTTP succeeded)
> http://10.69.6.128:4097/v1/statusz → http_code=200
   body excerpt: {"healthy":true,"ready":true,"connected":["opencode"],
                  "providers_configured":1,
                  "sessions":[{"id":"ses_18887778dffe3yMRfw8SX3ln46","title":"Greeting","status":"idle"},
                              {"id":"ses_1863db246ffeSJI7JWRXWc2Kpp","title":"New session - 2026-05-30T16:40:37.561Z","status":"idle"}],
                  ...}
> http://10.69.6.128:4097/v1/healthz → http_code=200
> http://10.69.6.128:9999/        → curl: (28) timeout, http_code=000   (port not in NetPol allow list)
```

Allow rule works: 4096 and 4097 reachable; arbitrary other ports filtered.

### 4b. Unlabeled pod → sandbox (should be DENIED on all ports)

```
> http://10.69.6.128:4097/v1/statusz → curl: (28) timeout, http_code=000
> http://10.69.6.128:4096/          → curl: (28) timeout, http_code=000
```

Default deny works.

### 4c. Unlabeled pod → controller metrics (no NetPol on controller-metrics — F1.4.3 still open)

```
> http://llmsafespace-controller-metrics.default.svc:8080/metrics → http_code=200
   body excerpt:
     # HELP certwatcher_read_certificate_errors_total Total number of certificate read errors
     # TYPE certwatcher_read_certificate_errors_total counter
     certwatcher_read_certificate_errors_total 0
     # HELP certwatcher_read_certificate_total Total number of certificate reads
     # TYPE certwatcher_read_certificate_total counter
     certwatcher_read_certificate_total 46
     # HELP controller_runtime_active_workers Number of...
```

Any non-workspace in-cluster pod can still scrape the unauthenticated controller `/metrics`. The G16 chart adds NetworkPolicies only on workspace pods; controller has no ingress policy.

### 4d. Unlabeled pod → postgres:5432 (no NetPol on postgres)

```
> http://postgres.default.svc:5432/ → curl: (52) Empty reply from server, http_code=000  (TCP open; HTTP-GET to a Postgres server expectedly empty-reply)
```

In-cluster Postgres TCP is open to any in-cluster pod. Auth is the only gate. Same as pre-fix.

Both probe pods deleted post-test.

---

## 5. Per-finding pre-fix → post-fix comparison

| Finding | Pre-fix evidence | Post-fix live evidence | Status |
|---|---|---|---|
| **F1.4.1** (High) — Zero L3/L4 isolation between tenants | `kubectl get netpol -A` returned `No resources found`; cross-sandbox 4096/4097 CONNECT_OK | 2 NetworkPolicies installed (§1); cross-sandbox 4096/4097 → CONNECT_FAIL (timeout); `/v1/statusz` cross-tenant unreachable at L4 (§3a, §4b) | **FIXED for sandbox→sandbox direction.** Sandbox can no longer reach a peer sandbox at L3/L4 in either direction (peer's deny-ingress drops the connect, and the source's egress drops 10.0.0.0/8). Note F1.4.5 caveat below — fix relies on API and workspaces sharing a namespace. |
| **F1.4.2** (High) — agentd `/v1/statusz` and `/v1/healthz` unauthenticated | Cross-sandbox curl returned 200 with sessions list | API-labeled pod still gets 200 from `/v1/statusz` with full sessions array (`ses_18887778dffe3yMRfw8SX3ln46`, titles, status) — see §4a. Cross-tenant probe from a sandbox or unlabeled pod is now blocked at L4. | **PARTIALLY MITIGATED, AUTH GAP STILL OPEN.** L3/L4 reachability narrowed to API-labeled pods, but the API pod itself is the proxy front-end for users — if the API does not enforce per-tenant authz on the `/v1/statusz` proxy path, a regular-tier user can still pivot through API to read another tenant's session list. The *endpoint itself* is still unauthenticated. Promote to RT-3.18 unchanged. |
| **F1.4.3** (Medium) — Controller `/metrics` unauthenticated, unrestricted | Sandbox → controller-metrics:8080 → http_code=200 | Sandbox → controller-metrics:8080 now CONNECT_FAIL (egress to 10.0.0.0/8 dropped). **However**, an unlabeled pod in `default` (or any other in-cluster pod with no workspace labels) → http_code=200 with full metrics body — see §4c. | **PARTIALLY MITIGATED FROM SANDBOX VECTOR, FULLY OPEN FROM ELSEWHERE.** The G16 chart only adds policies to workspace pods; controller has no `default-deny-ingress` and no `kube-rbac-proxy` wrapper. Any sidecar / batch job / other tenant's non-workspace pod still scrapes metrics anonymously. Promote to RT-4.17 unchanged. |
| **F1.4.4** (Medium) — Egress allow-list is wide (0.0.0.0/0 minus RFC1918) | Sandbox → arbitrary public IP/port → OK | Sandbox → 1.1.1.1:80 / :53 / :443 / :8080 all CONNECT_OK; sandbox → api.github.com:443 OK (§3c) | **STILL OPEN.** The chart's egress block is unchanged: `0.0.0.0/0 except RFC1918+169.254/16`. No port restriction, no FQDN restriction. C2-channel risk on arbitrary outbound TCP unchanged. Promote to RT-5.16 unchanged. |
| **F1.4.5** (Low) — Ingress allow rule pins to `.Release.Namespace` | Source: `workspace-network-policy.yaml:33` | Confirmed in live policy: `namespaceSelector.matchLabels."kubernetes.io/metadata.name" = default` (hard-coded; would also need to equal the workspace's namespace if workspaces are split out — see §1a). | **STILL OPEN as latent bug.** Not exploitable on this cluster (api and workspaces both live in `default`), but the constraint is unchanged in the chart. Promote to RT-6.27 unchanged. |
| **F1.4.6** (Low) — Cloud-metadata block enforced by node, not k8s | Sandbox → 169.254.169.254:80 CONNECT_FAIL even with no NetPol | Sandbox → 169.254.169.254:80 CONNECT_FAIL — but now the egress NetPol *also* excepts 169.254.0.0/16 (defence-in-depth). | **MITIGATED at NetPol layer (defence-in-depth).** Host-fw fragility on cloud clusters is now backed by an explicit `except:` entry in the egress policy. RT-7.16 follow-up still warranted to confirm the NetPol holds on EKS/GKE where the host fw doesn't. |
| **F1.4.7** (Low) — MCP service path uncovered by NetPols | `mcp.enabled=false` | Unchanged. `helm get values` confirms `mcp.enabled=false`. NetworkPolicy still scoped only to `app=llmsafespace,component=workspace`. MCP pods (when enabled) carry `component=mcp` and are not selected. | **STILL OPEN as latent bug** for any operator who enables MCP. |
| **F1.4.8** (Info) — All LLMSafeSpace services are ClusterIP; only frontend is Ingress-exposed | `kubectl get svc -n default | grep llmsafespace` showed all ClusterIP, only frontend in Ingress | Unchanged — same set of services. No NodePort, no LoadBalancer added by the post-fix chart. | **No change (info).** |

---

## 6. Net post-fix verdict

### Closed by G16

- **Sandbox-to-sandbox lateral movement** at L3/L4 (the headline pre-fix finding F1.4.1).
- **Sandbox-to-postgres / sandbox-to-valkey** TCP — was a DoS / version-probing / 0-day-exploitation risk pre-fix; now filtered.
- **Sandbox-to-kube-apiserver** TCP — combined with G17, an in-sandbox attacker can no longer present a SA JWT *and* can no longer reach 10.96.0.1:443 anyway.
- **Sandbox-to-controller-webhook** and **sandbox-to-controller-metrics** TCP from a sandbox.
- **Sandbox-to-cloud-metadata** now has defence-in-depth (NetPol + Talos host fw) instead of host-fw-only.

### Closed by G17

- **SA-token theft from sandbox** — the file simply does not exist in the workspace container's filesystem. Even if an attacker fully compromises the sandbox (RCE, container escape that doesn't escape the network namespace), they have no JWT to present to the apiserver.

### Still open after G16/G17

- **F1.4.2** — `/v1/statusz` is reachable from any pod with the API labels in `default`. The API pod is also the user-facing proxy for tenants. If the API's per-tenant session-fetch authz is weak, a regular tenant can read another tenant's sessions list via the API. The agentd endpoint itself is still unauthenticated. The fix is auth, not network.
- **F1.4.3** — controller `/metrics` is open to any non-workspace in-cluster pod (sidecars, batch jobs, non-workspace controller pods, future tenant pods if multi-tenant deployment expands). NetworkPolicy was only added to workspaces.
- **F1.4.4** — egress allowlist is still 0.0.0.0/0 minus RFC1918. C2 / data-exfil channels on any TCP port to any public IP are unchanged.
- **F1.4.5** — `.Release.Namespace` pin is unchanged (latent).
- **F1.4.7** — MCP path unchanged (latent).

---

## 7. New post-fix observations

### 7a. Liveness/readiness probes survive deny-ingress

Verified empirically: `restartCount=0`, `Ready=True` on all 3 workspace pods 7+ minutes after policy install. The k8s NetworkPolicy semantic of allowing host-network kubelet traffic carries through the post-fix chart correctly.

### 7b. CoreDNS allow rule resolves correctly on this cluster

`kube-system/coredns-*` pods carry `k8s-app=kube-dns`, matching the egress allow rule. DNS from sandbox works for both in-cluster and public names. (A misconfiguration here would be silent: if the DNS allow rule didn't match a label on the live coredns pods, sandboxes would fail every DNS lookup. Worth a one-line reminder in the chart values for operators who run a non-default DNS provider — e.g. NodeLocal DNS uses different labels.)

### 7c. Egress allow rule has no port restriction — still permits arbitrary outbound

The egress rule is `to: [ipBlock 0.0.0.0/0 except RFC1918+linklocal]` with **no `ports:` clause**. In Kubernetes NetworkPolicy semantics, an egress rule with `to:` but no `ports:` permits all ports/protocols. This means a jailbreaking agent can dial out on TCP/UDP arbitrary ports to attacker C2. Same as pre-fix F1.4.4 — flagged here as a NEW *visibility* point: the policy *looks* restrictive but in practice doesn't constrain port. An operator should consider adding an explicit `ports: [443/tcp, 53/udp, 53/tcp, 80/tcp]` or migrate to Cilium FQDN policies.

### 7d. NetworkPolicy does NOT cover non-sandbox pods, so anything that can land in `default` ns can still pivot

The two NetworkPolicies are scoped to `app=llmsafespace,component=workspace`. Any pod a user/operator/CI plants in `default` (other charts, shadow services, debug pods) is unrestricted. From an attacker who lands a pod in `default` (e.g. via a compromised release pipeline or a less-secured sibling chart), all the pre-fix attack paths are still open: cross-namespace probing, controller metrics scrape, kube-apiserver dial, postgres/valkey TCP. The G16 fix is **specifically a tenant-isolation control for sandbox-shaped workloads**, not a generic ns hardening.

### 7e. `kubectl get cnp,ccnp -A` still empty

The chart only emits standard `networking.k8s.io/v1.NetworkPolicy`. No CiliumNetworkPolicy / CiliumClusterwideNetworkPolicy. The cluster runs Cilium so these *would* let the operator add L7/HTTP-aware rules and FQDN-based egress, but the chart doesn't ship them. Recommendation: file a Phase-2 hardening item to optionally render `CiliumNetworkPolicy` for FQDN egress (api.openai.com, anthropic.com, docs.python.org, pypi.org, …) instead of the current 0.0.0.0/0 allow.

### 7f. F1.4.5 pin: now visible as concrete-but-currently-inert risk

In the live cluster the API pod and workspace pods both live in `default`, so the `kubernetes.io/metadata.name=default` selector resolves correctly and the ingress allow rule works. If a future operator sets `api.config.kubernetes.namespace` to e.g. `llmsafespace-system` while leaving workspace pods in `default`, the API pod's source-namespace label will not match and **all API→sandbox traffic will be silently dropped** (the API will appear hung when it tries to reach a sandbox). Worth promoting to a chart-template fix: parameterize the namespaceSelector to use `.Values.api.config.kubernetes.namespace` rather than `.Release.Namespace`.

---

## 8. Phase-1-postfix recommendations

1. **Continue with Phase 2** (RT-2.x cross-tenant lateral) to verify F1.4.2 via the API path now that direct-IP path is closed. The threat hasn't moved — it's now an **API-authz** test, not a NetworkPolicy test.
2. **Open a chart-fix issue** for F1.4.3: add a NetworkPolicy on the controller-metrics service (allow only `prometheus`-labeled pods OR wrap in `kube-rbac-proxy`).
3. **Open a chart-fix issue** for F1.4.4: add explicit `ports:` to the egress rule, or expose a Helm value for an operator-configured FQDN allowlist via Cilium.
4. **Open a chart-fix issue** for F1.4.5: parameterize the namespaceSelector by `api.config.kubernetes.namespace`.
5. **Open a chart-fix issue** for F1.4.7: extend the workspace NetworkPolicies to cover MCP pods when `mcp.enabled=true`.
6. **Defer to Phase 7** (cloud-hosted reference cluster): re-test F1.4.6 on EKS/GKE where Talos host fw is absent — the NetPol's `except 169.254.0.0/16` should hold there too, but worth verifying.

---

## Appendix A — Live evidence excerpts (raw)

```
$ kubectl config current-context
admin@home-kubernetes

$ helm list -n default | grep llmsafespace
llmsafespace  default  71  2026-05-30 10:28:06 -0700 PDT  deployed  llmsafespace-0.1.0  0.1.0

$ helm history llmsafespace -n default | tail -5
69  Sat May 30 10:24:00 2026  superseded  llmsafespace-0.1.0  Upgrade complete
70  Sat May 30 10:27:06 2026  superseded  llmsafespace-0.1.0  Upgrade complete
71  Sat May 30 10:28:06 2026  deployed    llmsafespace-0.1.0  Upgrade complete

$ kubectl -n default get netpol
NAME                                          POD-SELECTOR                           AGE
llmsafespace-workspace-default-deny-ingress   app=llmsafespace,component=workspace   ~7m
llmsafespace-workspace-egress                 app=llmsafespace,component=workspace   ~7m

$ kubectl -n default get pods -l app=llmsafespace,component=workspace -o wide
NAME                                            READY   STATUS    RESTARTS   AGE   IP            NODE
6d36952e-...-cbde9a23                            1/1     Running   0          3m    10.69.6.128   worker-01
9900e481-...-11f7367c                            1/1     Running   0          3m    10.69.6.69    worker-01
c9992183-...-de2a96c3                            1/1     Running   0          3m    10.69.6.107   worker-01

$ kubectl -n default get pod 6d36952e-..-cbde9a23 -o jsonpath='{.spec.automountServiceAccountToken}'
false

$ kubectl -n default exec 6d36952e-..-cbde9a23 -- bash -c 'ls /var/run/secrets/kubernetes.io/'
ls: cannot access '/var/run/secrets/kubernetes.io/': No such file or directory
```

## Appendix B — Probe-pod manifests (created and deleted in this run)

```yaml
# pentest-api-impersonator: created to verify ingress allow rule resolves; deleted at end of run
apiVersion: v1
kind: Pod
metadata:
  name: pentest-api-impersonator
  namespace: default
  labels:
    app.kubernetes.io/component: api
    app.kubernetes.io/name: llmsafespace
    app.kubernetes.io/instance: llmsafespace
    pentest: "true"
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  containers:
  - { name: probe, image: alpine:3.20, command: ["sh","-c","sleep 600"] }
---
# pentest-rando-pod: created to verify default-deny-ingress; deleted at end of run
apiVersion: v1
kind: Pod
metadata:
  name: pentest-rando-pod
  namespace: default
  labels: { pentest: "true" }
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  containers:
  - { name: probe, image: alpine:3.20, command: ["sh","-c","sleep 600"] }
```

Both pods created with `automountServiceAccountToken: false`. Both deleted before report close. Blast-radius rules respected: only `default` namespace mutated, only ephemeral pods, only documented test domains used for external probes (api.openai.com, docs.python.org, 1.1.1.1, api.github.com).

— End of post-fix RT-1.4 report.
