# 0090 — Epic 17 Phase 5 — Proxy & Network Egress

**Date:** 2026-05-30
**Author:** mikekao + opencode (sonnet)
**Phase:** 5 — Proxy & Network Egress

## Summary

14 RT-5.x tests. **5 PASS, 3 FAIL, 6 INCONCLUSIVE, 0 SKIP.**

New gap surfaced:
- **G30** medium — Egress NetPol allows external DNS resolvers (e.g., 8.8.8.8:53). The "DNS only to kube-dns" rule is overridden by the second egress rule that allows ANY port to ANY public IP.

Pre-existing gaps re-confirmed:
- **G4** low — Proxy uses plain HTTP to in-cluster opencode.
- **G12** low — `ResponseHeaderTimeout: 300s` is excessive.

## Assumptions stated and validated

| # | Assumption | Validation | Result |
|---|---|---|---|
| A1 | Proxy target IP from `Status.PodIP` (not user-controlled) | `proxy.go:367,373` | ✅ |
| A2 | Proxy port hardcoded | `proxy.go:29 const opencodePort = agentd.AgentPort` | ✅ |
| A3 | maxConnectionsPerWorkspace = 10 | `proxy.go:28` | ✅ |
| A4 | Proxy uses plain HTTP to opencode | `proxy.go:405 fmt.Sprintf("http://...")` | ✅ confirms G4 |
| A5 | ResponseHeaderTimeout = 300s | `proxy.go:95` | ✅ confirms G12 |
| A6 | stripPatchParts uses encoding/json (no depth limit) | code-grep `proxy.go:519` | ✅ |
| A7 | Sandbox can hit external IPs (per Phase 1) | re-tested | ✅ + new finding (G30): also DNS port 53 |

## Methodology corrections during the run

### RT-5.3 false-positive caught and corrected

Initial run flagged "smuggling appears successful (multiple HTTP/1.1 status lines)" with severity **critical**. I then inspected the raw response and found two distinct `X-Request-Id` headers. That means Go's `net/http`:

1. Read `Transfer-Encoding: chunked` (TE)
2. Saw the conflicting `Content-Length: 5` (CL)
3. Per RFC 7230 §3.3.3, when both are present and TE is chunked, **TE wins**.
4. Read the chunked body (`0\r\n\r\n` = empty body, end-of-message).
5. The trailing bytes `GPOST /admin HTTP/1.1...` are then interpreted as the **next pipelined request** on the same TCP connection.
6. `GPOST` is an unknown method, gin returns 404 with a fresh request_id.

This is **HTTP pipelining working correctly**, not smuggling. True CL-TE smuggling requires an intermediate proxy (CDN, load balancer) that disagrees with the origin server on which header to honour. The deployed API has no such intermediate, so the attack is not exploitable today.

I updated the test to:
- PASS if 2 status lines AND 2 distinct request IDs (= correct pipelining).
- FAIL only if 2 status lines but only 1 request ID (= true smuggling).

This is the **mutation-validation discipline** applied to my own harness logic: every interpretation must be supported by observable evidence, not assumed.

### RT-5.5 inconclusive (connection limit)

15 simultaneous `GET /events` requests all returned 2xx in <2s. The `maxConnectionsPerWorkspace=10` cap probably applies only to **long-lived streaming** connections, not short bursts. The harness can't easily hold 11 long-lived SSE streams from one Python process. Defer to a load test using a tool like `vegeta` or `k6`.

### RT-5.7 — direct DNS bypass discovered

Initial RT-5.7 used `getent hosts` (which goes through CoreDNS). I then realised the more interesting probe is "can we bypass kube-dns entirely?" — used `bash /dev/tcp/8.8.8.8/53` (TCP) and a perl raw UDP probe to 8.8.8.8:53. Both succeeded → confirmed G30.

## Findings detail

### G30 — DNS bypass via egress NetPol gap

**Why this is interesting:** the chart explicitly narrows the DNS allow-rule to kube-dns:
```yaml
- ports:
  - port: 53
    protocol: UDP
  - port: 53
    protocol: TCP
  to:
  - namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: kube-system
    podSelector:
      matchLabels:
        k8s-app: kube-dns
```
But the SECOND egress rule allows ANY port to ANY public IP:
```yaml
- to:
  - ipBlock:
      cidr: 0.0.0.0/0
      except: [10.0.0.0/8, ...]
```

NetworkPolicy rules are **OR-ed** — if any rule permits the traffic, it's allowed. So port 53 to 8.8.8.8 is allowed by rule 2, fully bypassing rule 1's intent.

**Fix:** Standard k8s NetworkPolicy can't express "allow X except port Y to Z". Need either:
- Cilium FQDN policies (allow-list specific external hostnames)
- Calico GlobalNetworkPolicy with negative selectors
- Application-layer DNS proxy (e.g., dnsdist or a logging Unbound)

## Cleanup

`phase5-alice@pentest.local` user + workspace + DEK deleted. No residue.

## Files

- `design/stories/epic-17-security-review/phase-5/harness/run-phase5.py` (~750 lines)
- `design/stories/epic-17-security-review/phase-5/findings.md`
- `design/stories/epic-17-security-review/phase-5/evidence/RT-5.{1..14}.json`
- `worklogs/0090_2026-05-30_epic17-phase-5-proxy-network.md` (this file)

## Next: Phase 6 — K8s & Infrastructure
