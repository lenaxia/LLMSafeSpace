# 0106 — Epic 17 Phase C/G4 part 2: F1.2.4 Spec.NetworkAccess enforced

**Date:** 2026-05-31
**Author:** mikekao + opencode (sonnet)
**Phase:** Epic 17 Phase C, F1.2.4
**Status:** Code-level fix complete; awaiting CI build + live cluster re-pentest

---

## Summary

Closes the High-severity **F1.2.4** finding: pre-fix the controller
ignored `Spec.NetworkAccess` entirely. A user could declare
`networkAccess.egress: [{domain: api.openai.com}]` expecting outbound
to be limited; the controller never created a matching NetworkPolicy.

This commit:
1. Generates a per-workspace NetworkPolicy from declared FQDNs at
   reconcile time (DNS-resolved /32 ipBlocks with HTTP/HTTPS allow
   plus DNS to kube-dns).
2. Refreshes the policy on every Active reconcile so spec changes,
   CDN IP rotation, and toggle-off all propagate.
3. **Filters out cluster-internal suffixes and private/internal IPs**
   (RFC1918, 169.254/16, 127/8, multicast, CGNAT) so an attacker
   can't widen the chart-wide `blockedEgressCIDRs` via NetPol union
   semantics.

---

## Skeptical-validator pass found a critical bypass

The first validator pass flagged: the per-workspace NetPol's union
semantics with the chart-wide policy let a user declare
`Domain: kubernetes.default.svc.cluster.local` → resolved to
apiserver ClusterIP → /32 NetPol allow → **defeats the chart's
RFC1918 + 169.254/16 exclusion**. Same pattern works for any
in-cluster service (Prometheus, Harbor, internal LLM gateways) and
for DNS-rebinding to cloud metadata IP.

REWORK applied in this same commit:

1. **Webhook layer** (admission):
   - `validateEgressDomain()` rejects cluster-internal suffixes
     (`.cluster.local`, `.svc`, `.local`, `.internal`).
   - Rejects IP literals (force resolution + filter path).
   - Validates LDH grammar (max 253 total / 63 per label).
2. **Controller layer** (defense-in-depth):
   - `isPrivateOrInternal()` drops any resolved IP in
     RFC1918 / 169.254/16 / 127/8 / 224/4 / 100.64/10.
   - `isInternalDomain()` short-circuits cluster-internal suffixes
     before DNS lookup.
   - `HostResolver` interface allows test injection (was hitting
     real DNS, flaky in hermetic CI — validator finding).
3. **Reconcile-time refresh**:
   - `ensureWorkspaceEgressNetworkPolicy` now also called from
     `handleActive` (not only `handleCreating`'s pod-create branch).
     Without this, spec changes, CDN IP rotation, and toggle-off
     would never take effect.

---

## Stated assumptions (validated)

- **A1** — Multiple NetworkPolicies on the same pod take **union**
  of their rules. (Validated: k8s docs §"Behavior of to and from
  selectors" + workspace pod carries both `app=llmsafespace,
  component=workspace` (chart selector) AND
  `llmsafespace.dev/workspace=<name>` (per-workspace selector).)
- **A2** — `net.DefaultResolver` in the controller pod resolves
  through CoreDNS in-cluster. (Validated: standard k8s DNS-config
  injection on every pod.)
- **A3** — `Spec.NetworkAccess.Egress[].Domain` is the only
  field the user can populate to declare egress allow-list.
  (Validated: `pkg/apis/.../workspace_types.go:30-38`.)
- **A4** — workspace pods carry `LabelWorkspace = ws.Name` so
  per-workspace NetPol can selector-match. (Validated:
  `controller.go:663`.)

---

## Changes

### New file

1. `controller/internal/workspace/network_policy.go`:
   - `HostResolver` interface + `defaultHostResolver` production impl.
   - `privateOrInternalCIDRs` (RFC1918 + 169.254/16 + 127/8 + 224/4 + 100.64/10).
   - `isPrivateOrInternal(ip)` filter.
   - `internalDomainSuffixes` short-circuit.
   - `isInternalDomain(d)` filter.
   - `buildWorkspaceEgressNetworkPolicy(ws)` returns the desired
     NetPol with DNS-resolved /32 ipBlocks + kube-dns DNS rule;
     filters internal suffixes and private IPs.
   - `ensureWorkspaceEgressNetworkPolicy(ws)` create/update/delete
     idempotent helper with owner ref + DeepEqual short-circuit.
   - `resolveDomainIPv4(ctx, resolver, domain, logger)` helper.

### Controller integration

2. `controller/internal/workspace/controller.go`:
   - `WorkspaceReconciler.HostResolver` field for test injection.
   - `handleCreating` calls ensure before pod creation (failure non-fatal).
   - `handleActive` calls ensure on every reconcile to refresh.

### Webhook integration

3. `controller/internal/webhooks/workspace_webhook.go`:
   - `validateEgressDomain(d)` with cluster-internal suffix block,
     IP-literal rejection, LDH grammar.
   - Handle iterates Spec.NetworkAccess.Egress[] and rejects on
     first failure.
   - Added `net` import.

### Tests

4. `controller/internal/workspace/security_test.go`:
   - `stubResolver` for hermetic DNS.
   - `TestG4_F124_GeneratesPerWorkspaceEgressPolicyWhenDeclared`
     — uses stub, asserts /32 CIDRs land verbatim.
   - `TestG4_F124_DropsResolvedPrivateIPs` — feeds 169.254/10
     IPs back, asserts NO ipBlock leaks (validator-found bypass).
   - `TestG4_F124_NilNetworkAccessProducesNoExtraPolicy`
   - `TestG4_F124_EmptyEgressProducesNoExtraPolicy`

5. `controller/internal/webhooks/workspace_webhook_test.go`:
   - `TestG4_F124_RejectsClusterInternalDomain` (6 payloads)
   - `TestG4_F124_RejectsIPLiteralDomain` (5 payloads)
   - `TestG4_F124_AllowsLegitimatePublicDomains` (5 payloads)
   - `TestG4_F124_RejectsMalformedDomains` (7 payloads)

---

## Mutation-validated

- Disable `isPrivateOrInternal` filter → `TestG4_F124_DropsResolvedPrivateIPs` FAIL ✓ ("private IP `10.0.0.5/32` leaked into NetPol allow"). Restored.

---

## Known residual / followups

- **Hardcoded kube-dns selector** (`kubernetes.io/metadata.name=kube-system`, `k8s-app=kube-dns`). The chart values let an operator override `dnsNamespace`/`dnsPodLabelSelector` for the chart-wide policy; the controller's per-workspace policy duplicates the literal. If the operator overrides, the per-workspace DNS rule won't match. Followup: thread the selector via controller flags. Non-blocking — the chart-wide policy still allows DNS.
- **No status condition** for DNS-resolution failure. Currently logged at V(1). Followup: surface as `WorkspaceCondition`.
- **IPv6 dropped** by `To4()`. Documented; out of scope.

---

## Tests

| Command | Result |
|---|---|
| `go test -count=1 -timeout 120s ./controller/...` | PASS |
| `go test -count=1 -timeout 60s ./charts/llmsafespace/...` | PASS |
| `go build ./controller/...` | clean |

---

## Live re-pentest plan

After CI builds the new controller image:

1. `helm upgrade llmsafespace ./charts/llmsafespace -n default --reuse-values`.
2. **Bypass attempt 1 (must FAIL):**
   ```
   kubectl apply -f - <<EOF
   apiVersion: llmsafespace.dev/v1
   kind: Workspace
   metadata: { name: w-bypass, namespace: default }
   spec:
     owner: { userID: u1 }
     runtime: ghcr.io/lenaxia/llmsafespace/base:latest
     storage: { size: 5Gi }
     networkAccess:
       egress:
         - domain: kubernetes.default.svc.cluster.local
   EOF
   ```
   Must respond: `admission webhook denied: spec.networkAccess.egress[0].domain: domain ends in cluster-internal suffix...`
3. **Legitimate use (must succeed):**
   `domain: api.openai.com` → workspace creates → NetPol
   `workspace-egress-w-good` exists → kubectl get np shows /32
   ipBlock for the resolved IP.
4. **Refresh test:** patch `networkAccess.egress` to add a domain →
   wait for reconcile → NetPol Spec contains the new IP.
5. **Toggle-off test:** unset `networkAccess` →
   workspace-egress-* NetPol deleted.

---

## Next finding

Phase C/G5 — F1.3.1 through F1.3.7 + G5 (RBAC tightening,
single-PR-closes-many).
