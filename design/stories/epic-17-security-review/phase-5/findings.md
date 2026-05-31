# Phase 5 — Proxy & Network Egress — Findings

**Status:** Complete (live cluster, 5/14 PASS, 3 FAIL, 6 INCONCLUSIVE)
**Cluster:** `admin@home-kubernetes`, image `sha-eb5c33e`
**Harness:** [`harness/run-phase5.py`](./harness/run-phase5.py)
**Worklog:** [`worklogs/0090_*-epic17-phase-5-proxy-network.md`](../../../../worklogs/)

## Summary

| ID | Result | Severity | Title |
|---|---|---|---|
| RT-5.1 | PASS | info | Proxy target not influenceable by headers/path |
| RT-5.2 | PASS | info | Proxy port hardcoded (static confirm) |
| RT-5.3 | PASS | info | CL-TE conflict handled correctly (pipelining, not smuggling) |
| RT-5.4 | INCONCLUSIVE | info | SSE injection (deferred) |
| RT-5.5 | INCONCLUSIVE | info | Connection limit not observed in 15-burst test |
| RT-5.6 | INCONCLUSIVE | info | Stale pod IP exploitation (defer to integration test) |
| RT-5.7 | FAIL | **medium** | **G30** — Sandbox can resolve via 8.8.8.8 (NetPol bypass) |
| RT-5.8 | PASS | info | kube-apiserver unreachable (G16 holds) |
| RT-5.9 | INCONCLUSIVE | info | MCP transport injection (deferred) |
| RT-5.10 | PASS | info | Non-WS endpoint did not upgrade |
| RT-5.11 | FAIL | low | **G4** — Proxy uses plain HTTP to in-cluster opencode |
| RT-5.12 | INCONCLUSIVE | info | stripPatchParts DoS (cannot trigger black-box) |
| RT-5.13 | FAIL | low | **G12** — ResponseHeaderTimeout=300s |
| RT-5.14 | INCONCLUSIVE | info | stripPatchParts unexpected-shape handling (deferred) |

## New gap

### G30 — Egress NetPol allows external DNS resolvers

**Reproduction (from inside sandbox pod):**
```
$ timeout 3 bash -c '</dev/tcp/8.8.8.8/53'    # TCP=0 (success)
$ perl -e '... UDP DNS query to 8.8.8.8:53'   # 28 bytes (success)
```

**Root cause:** The egress NetworkPolicy at `charts/llmsafespace/templates/workspace-network-policy.yaml`:
```yaml
- to:
  - ipBlock:
      cidr: 0.0.0.0/0
      except: [10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16]
```
The "DNS only to kube-dns" rule is a SEPARATE rule (port 53 to kube-dns), but the second rule allows ALL ports to ALL public IPs — so port 53 to 8.8.8.8 is allowed by the second rule, fully circumventing the first.

**Impact:**
- DNS exfiltration via attacker-controlled resolver (compromised LLM ↔ home server's DNS).
- DNS tunnelling (encode arbitrary data in TXT record queries).
- Bypasses any logging/auditing on the cluster's CoreDNS.

**Fix options:**
- (A) Add explicit deny on port 53 to non-kube-dns: requires Cilium L7 policies or Calico GlobalNetworkPolicy (standard k8s NetworkPolicy can't do "allow X except port 53 to Y").
- (B) Restrict the public-IP allow-list to specific ports (e.g., only 443/80) — breaks legitimate package install / API access.
- (C) Use Cilium FQDN policies to allowlist specific external hostnames (api.openai.com, api.anthropic.com, etc.).

Recommendation: (C) for production; (A) as a quick fix.

## Pre-existing findings re-confirmed

### G4 — Plain-HTTP proxy (low)
`api/internal/handlers/proxy.go:405`:
```go
targetURL := fmt.Sprintf("http://%s:%d%s", podIP, opencodePort, targetPath)
```
Proxy speaks HTTP (not HTTPS) to opencode. Mitigated by NetworkPolicy + Basic Auth password. Documented residual risk.

### G12 — ResponseHeaderTimeout 300s (low)
`api/internal/handlers/proxy.go:95`:
```go
ResponseHeaderTimeout: 300 * time.Second,
```
Slow-headers DoS surface. Caps at 10 connections × N workspaces × 300s ≈ thousands of stuck goroutines under attack.

## Methodology notes

- **RT-5.3 false positive caught**: initial run flagged "smuggling appears successful" because two HTTP/1.1 status lines appeared in the response. Investigation showed two distinct `X-Request-Id` headers, meaning Go's `net/http` correctly resolved CL-TE conflict (TE wins per RFC 7230) and pipelined the trailing bytes as a new request. **Real smuggling needs an intermediate proxy disagreeing with origin** — direct pentest against `net/http` is not exploitable. PASS, with note to retest if a CDN/proxy is added in front.
- **RT-5.5 inconclusive**: 15 simultaneous /events GETs all returned 2xx in <2s. The connection limit is enforced on long-lived streaming, not short bursts. Defer to a load test with sustained SSE connections.
- **RT-5.12 / 5.14 cannot be black-box tested**: `stripPatchParts` runs on opencode RESPONSES, not user requests. To trigger, we'd need to control opencode's outputs. These need unit-level fuzz tests in `pkg/proxy` (or wherever `stripPatchParts` lives).

## Cleanup

`phase5-alice@pentest.local` user + workspace deleted; no residue.
