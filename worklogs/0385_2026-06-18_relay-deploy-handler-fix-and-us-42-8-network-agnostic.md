# Worklog 0385 — Relay Deploy Handler Fix + US-42.8 Network-Agnostic Redesign

**Date:** 2026-06-18
**Session:** Triage operator-reported relay deploy failure (`failed to deploy relay fleet: inferencerelays.llmsafespace.dev "relay-fleet" not found`), audit the entire Epic 42/48 relay subsystem to identify what was actually built vs designed, fix the immediate handler bug, and redesign US-42.8 to be network-agnostic.
**Status:** Complete (handler fix shipped; US-42.8 implementation deferred — design redocumented only)

---

## Objective

1. Diagnose why clicking "Deploy Relay Fleet" in the admin UI returned `inferencerelays.llmsafespace.dev "relay-fleet" not found` even though no `relay-fleet` CR existed yet (it should have been *created*, not failed to update).
2. Audit Epic 42 (multi-cloud inference relay) and Epic 48 (relay admin UX) to enumerate **everything missing** for an end-to-end working relay fleet, since the deploy failure was suspected to be the tip of a larger iceberg.
3. Fix the immediate handler bug under TDD.
4. Redesign US-42.8 (the only Epic 42 story still NOT DONE) so the chart is **network-agnostic** — the operator's choice of LB / NodePort / hostNetwork / DIY-DNAT must not be hardcoded to MetalLB.
5. Document everything, commit, push.

---

## Triage findings

### Why the Deploy button failed

`api/internal/handlers/relay_admin.go:519` chose between `Create` and `Update` like this:

```go
existing, err := h.llmClient.InferenceRelays().Get(ctx, "relay-fleet", metav1.GetOptions{})
if err != nil && !apierrors.IsNotFound(err) {
    c.JSON(http.StatusInternalServerError, ...)
    return
}
if existing != nil {
    relay.ResourceVersion = existing.ResourceVersion
    _, err = h.llmClient.InferenceRelays().Update(ctx, relay)
} else {
    _, err = h.llmClient.InferenceRelays().Create(ctx, relay)
}
```

Looks fine, but the assumption "`existing == nil` on NotFound" is **wrong for the typed CRD client this repo ships**. `pkg/kubernetes/client_crds.go:306-315` allocates `result := &v1.InferenceRelay{}` *before* calling `Do().Into(result)` and returns `result` to the caller regardless of error. Standard `client-go` typed-client convention. So on NotFound, `existing != nil` is always true; the handler always took the Update branch; `Update` against a non-existent CR returns NotFound; the user saw `"relay-fleet" not found`.

The matching unit test (`TestRelayDeploy_Create_Success`, `relay_admin_test.go:518`) used `relayMock.On("Get", ...).Return(nil, notFoundError())` — the mock returned `nil` on NotFound, not the empty struct the real client returns. That mock divergence let the bug ship.

### What's missing for the relay fleet to actually function

Three categories:

**A. Bugs in shipped code**
1. `relay_admin.go:519` Get/Create-or-Update gated on `existing != nil` instead of `apierrors.IsNotFound(err)` (this worklog fixes).
2. `MockInferenceRelayInterface.Get` (`mocks/kubernetes/mocks.go:256-262`) diverges from real-client semantics (returns `nil` instead of empty struct on NotFound). The mock should be left as-is for now — fixing it would break ~12 existing tests that depend on the nil contract — but new tests must explicitly reproduce real-client behaviour when exercising NotFound paths. The new regression test does this.

**B. US-42.8 not implemented (the structural gap)**
Worklog `0299_2026-06-15_add-relay-router-to-helm-chart.md` ships the relay-router Deployment + Service but explicitly defers WireGuard:

> **No WireGuard sidecar yet.** The initial Deployment has only the HTTP proxy container. WireGuard (NET_ADMIN, privileged-ish, key management) belongs in US-42.8 when the controller's WG key infrastructure is built. The Service type is `ClusterIP` with only TCP 8080; operators can change to LoadBalancer when WG is added.
>
> **Next Steps:** US-42.8 (WireGuard sidecar): add a WG container + key volume mount to this Deployment, switch Service type to LoadBalancer, add WG UDP port 51820.

No subsequent worklog implements US-42.8. The relay-router pod runs as a plain HTTP proxy with no `wg0` interface, no UDP listener, and no public-internet endpoint. Even if a `relay-fleet` CR is created and the controller provisions cloud VMs, the VMs cannot tunnel back into the cluster, the router cannot dial them on `10.42.42.x:8080`, and the fleet stays at 0/N healthy forever.

This was originally planned with a hard MetalLB dependency, but worklog `0294_2026-06-15_relay-setup-network-agnostic.md` removed the MetalLB checklist gate from the setup endpoint after concluding that MetalLB-or-not is "an infra detail to leave to the operator." The chart-template implementation never followed; the design doc still assumed MetalLB. This worklog's redesign closes that gap (see `## US-42.8 redesign` below).

**C. Operator-side prerequisites**
3. `controller.inferenceRelay.enabled=false` in `charts/llmsafespace/values.yaml:198` — the InferenceRelay reconciler doesn't even register until this flips.
4. `oci-credentials` / `gcp-credentials` Secrets missing on the live cluster (one provider per Secret; the controller refuses to provision without a matching credentialsRef).
5. `aws-relay-irwa` Secret keys not validated for IRWA (IAM Roles for Workload Authentication) content; presence-only checks aren't enough for the AWS driver.

**Categories A and B are code gaps; C is operator config.** This worklog addresses A in code and B in design only — implementing US-42.8 is a follow-up story-sized commit.

### Recent epic timeline (for posterity)

- **Epic 26** (Mar–Jun 2026) — Cloudflare Worker relay, broken by opencode.ai IP-blocking CF egress ranges.
- **Epic 42** (Jun 14–17) — multi-cloud WG relay design + most of the implementation. Stories US-42.1, .3, .4, .5, .6, .7, .9–.13 are Done per worklogs 0261, 0262, 0287, 0289, 0293, 0296, 0299, 0300. **US-42.2 (day-one validation against opencode.ai/zen) and US-42.8 (WG sidecar + ingress + NetworkPolicy) are NOT DONE.** US-42.2 is a manual operator step, not code; US-42.8 is the gap that prevents end-to-end function.
- **Epic 48** (Jun 14–16) — relay admin UI. Done per worklogs 0286, 0307. Introduced the Get/Create handler bug fixed here.
- **Worklog 0344** (Jun 17) — added the API-side ClusterRole for the (cluster-scoped) InferenceRelay CRD. Without that, the handler couldn't even reach the K8s API server. With it, the handler reaches K8s but fails on the bug fixed here.

---

## Work Completed

### Handler fix (TDD)

1. **Red phase.** Added `TestRelayDeploy_Create_RealClientNotFoundSemantics` (`api/internal/handlers/relay_admin_test.go:531-562`). It mimics real-client semantics by returning `(&v1.InferenceRelay{}, NotFoundError())` from `Get` and asserts the handler invokes `Create` (not `Update`). Confirmed failing against the unfixed handler with the exact error string operators reported in the UI: `failed to deploy relay fleet: inferencerelays.llmsafespace.dev "relay-fleet" not found`.

2. **Green phase.** `api/internal/handlers/relay_admin.go:514-525` now gates on `apierrors.IsNotFound(err)` for the Create branch, and falls into the Update branch for `nil` and other-error cases. Comment cites this worklog so future readers don't undo the fix:

```go
// Gate on apierrors.IsNotFound(err), not `existing != nil`. The typed
// client at pkg/kubernetes/client_crds.go pre-allocates an empty struct
// and returns it alongside the NotFound error, so a nil-pointer check is
// always false and we would always fall into the Update branch — which
// then fails with NotFound on a fresh cluster (worklog 0385).
```

3. **Other Get/Create-or-Update sites audited.** `Rotate` (`relay_admin.go:546`), `Pause` (570), `Resume` (594) all use `Get` followed by an `Update` after explicit annotation mutation. Those are correct **only** when the CR already exists, which is the actual contract of those endpoints (you can't rotate what doesn't exist). Their `apierrors.IsNotFound` handling is correct (returns 404 when the CR is missing). No fix needed for those three.

### US-42.8 redesign (network-agnostic)

`design/stories/epic-42-multi-cloud-inference-relay/README.md` updated:

- **Layer 2 (WireGuard Mesh)**: replaced the "MetalLB LoadBalancer (not NodePort)" subsection with a network-agnostic four-mode design.
- **A21**: re-stated as "cluster can expose a UDP endpoint via at least one of: cloud LB, MetalLB, kube-vip, NodePort, hostNetwork, operator-supplied DNAT." Verified-multi-mode.
- **DQ2**: rewritten to describe the four-mode design and the rationale for not coupling the chart to any one LB controller.
- **OQ4**: updated to point at the four-mode resolution.
- **Story table US-42.8**: scope rewritten to "router WireGuard sidecar + network-agnostic ingress (4 modes) + NetworkPolicy"; **MetalLB install removed**.
- Stale references in the architecture diagram caption, Phase 2 plan, Stated Assumptions example, and security summary updated to reflect operator-selectable ingress.

The four ingress modes:

| Mode | Behaviour | When to use |
|------|-----------|-------------|
| `external` (default) | Chart creates **no** ingress resources. Operator wires UDP 51820 themselves. | Universal — any cluster, including ones the chart's authors didn't anticipate |
| `loadBalancer` | Chart creates `Service{type: LoadBalancer}` on UDP 51820. Works with any LB controller (cloud, MetalLB, kube-vip, Cilium L2). | Cloud K8s; bare-metal with an existing LB |
| `nodePort` | Chart creates `Service{type: NodePort}` on a pinned UDP port. | Bare-metal without an LB controller |
| `hostNetwork` | Chart pins the router to a labelled node with `hostNetwork: true`. | Bare-metal where NodePort is undesirable |

**Critically**: the chart **never installs MetalLB or any other LB controller**. That stays an operator responsibility, exactly like Postgres and Redis (`charts/llmsafespace/values.yaml:288`). The default `external` mode means `helm install` succeeds on any K8s distribution — the operator is responsible for declaring `spec.wireGuard.routerEndpoint` and routing UDP 51820 to the router pod.

Implementation of US-42.8 (the chart-template work) is **not** in this commit. This worklog redocuments the design only.

---

## Key Decisions

1. **Handler fix is the sole code change in this commit.** US-42.8 implementation is a separate, larger story-sized change. Mixing them would balloon the PR scope and delay the immediate fix the operator needs. The redesign is documented now so the next implementer doesn't repeat the MetalLB mistake.

2. **Did not "fix" the mock to match real-client semantics.** ~12 existing tests pass `nil` to `Get`'s mock-returns and would all need updating. The risk of silently breaking those tests outweighs the value of mock-fidelity. Instead, the new regression test exercises real-client semantics explicitly. Future test authors writing NotFound paths against `InferenceRelays().Get` should follow the same pattern.

3. **Default mode is `external`, not `loadBalancer`.** A `loadBalancer` default would leave the Service stuck `<pending>` on bare-metal without an LB controller, and the failure mode would be a confused operator wondering why nothing works. `external` produces zero ingress resources — the operator declares `routerEndpoint` and wires it however they want. If they want the chart to render the Service for them, they explicitly pick `loadBalancer` / `nodePort` / `hostNetwork`. This matches the chart's existing posture for Postgres/Redis (operator-supplied infra, chart never installs cluster-scoped dependencies).

4. **Did not add a setup-wizard "ingress mode" picker to the UI.** The four modes are values.yaml-time decisions, not runtime ones. Operators set them at install/upgrade. The wizard's `routerEndpoint` field already covers the operator's runtime declaration; the wizard does not need to know which mode renders the underlying Service.

5. **Did not delete MetalLB references from the design doc wholesale.** MetalLB is still a perfectly valid choice — the redesign explicitly mentions it as one of several LB controllers that satisfy the `loadBalancer` mode. Deleting MetalLB references would falsely imply MetalLB is unsupported. The redesign reframes MetalLB from "required dependency" to "one of many supported LB controllers."

---

## Adversarial Self-Review (Rule 11)

- **Does the fix accidentally break the Update path?** No — `TestRelayDeploy_Update_Existing` (`relay_admin_test.go:543`) covers the case where the CR exists; `Get` returns the existing CR with `nil` error; the handler falls into `else` → `Update`. The fix only redirects the NotFound branch.
- **Is `existing.ResourceVersion` still meaningful in the Update branch after the fix?** Yes — the Update branch is reached only when `err == nil`, in which case `existing` is a populated CR with a real `ResourceVersion`. The fix removes the dead-code path where `existing` was the empty struct + NotFound.
- **Does any other handler have the same bug?** Audited `Rotate`, `Pause`, `Resume` — all three read-then-mutate-then-update, but they correctly return 404 when the CR is missing (rotation/pause/resume of a non-existent fleet is a 404, not a Create). No fix needed.
- **Does the redesign break any existing test?** The redesign is documentation-only; no chart templates exist for US-42.8 yet. Chart tests (`charts/llmsafespace/chart_test.go`) cover what's currently rendered (no WG sidecar, ClusterIP-only Service); they're untouched.
- **Does the redesign violate any ADR?** No ADR mandates MetalLB; the original Epic 42 README named MetalLB as the implementation choice for the bare-metal Talos cluster the project owner runs, but never made it a project-wide constraint. Worklog 0294 already acknowledged this when it removed the MetalLB checklist gate from the setup wizard.
- **Will the operator's existing relay-router Deployment break on chart upgrade?** No — this commit contains zero chart changes. The operator continues running the existing ClusterIP-only Deployment; the chart upgrade path is unchanged. US-42.8 implementation will need to add the WG sidecar + ingress in a backwards-compatible way (default `mode: external` produces no new resources).

Zero real findings.

---

## Tests Run

- `go test -timeout 90s -run TestRelayDeploy_Create_RealClientNotFoundSemantics -v ./api/internal/handlers/ -count=1`
  - Before fix: **FAIL** (`expected 200, actual 500`, error message: `inferencerelays.llmsafespace.dev "relay-fleet" not found` — exactly matches operator-reported UI error)
  - After fix: **PASS** (Create called as expected; `deployed=true` returned)
- `go test -timeout 90s -run "TestRelayDeploy" -v ./api/internal/handlers/ -count=1`
  - All 9 deploy-related tests PASS (Create_Success, Create_RealClientNotFoundSemantics [new], Update_Existing, AcceptsAWS_Success, MissingFields_400 [3 subtests], Defaults_UpstreamURL, OCIOnly_Success, GCPOnly_Success, NetworkError_500)
- `go test -timeout 120s ./api/internal/handlers/ -count=1` — full handlers package PASS in 63.2s, no regressions.

---

## Files Modified

- `api/internal/handlers/relay_admin.go` — gate Create-vs-Update branch on `apierrors.IsNotFound(err)` instead of `existing != nil`.
- `api/internal/handlers/relay_admin_test.go` — added `TestRelayDeploy_Create_RealClientNotFoundSemantics` regression test exercising real-client semantics.
- `design/stories/epic-42-multi-cloud-inference-relay/README.md` — Layer 2 redesign (network-agnostic 4-mode ingress); A21, DQ2, OQ4 updated; story-table US-42.8 row rewritten; stale MetalLB references in diagram caption, Phase 2 plan, example values, and security summary updated.
- `worklogs/0385_2026-06-18_relay-deploy-handler-fix-and-us-42-8-network-agnostic.md` — this worklog.

---

## Next Steps

1. **Operator (immediate):** redeploy the API image with this fix, retry "Deploy Relay Fleet" — the CR will create successfully. The fleet will still be 0/N healthy until US-42.8 is implemented because no WG tunnel terminates in-cluster, but the deploy step itself unblocks.
2. **Operator (manual prereqs):** flip `controller.inferenceRelay.enabled=true` in values.yaml, install `oci-credentials` / `gcp-credentials` / `aws-relay-irwa` Secrets, declare `spec.wireGuard.routerEndpoint` (some externally-reachable `host:51820`).
3. **Next coder (US-42.8 implementation):** add the WG sidecar to the relay-router Deployment + the four ingress-mode templates per the redesigned Layer 2. Will also need: a router WG-key Secret mount, a NetworkPolicy restricting router ingress to workspace pods, and chart tests covering each ingress mode. US-42.2 (day-one validation against opencode.ai/zen IPs) is a prerequisite for the operator before US-42.8 implementation is worth shipping.
