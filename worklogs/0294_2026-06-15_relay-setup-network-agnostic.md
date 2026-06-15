# Worklog 0292 — Relay Setup Wizard: Make Prerequisite Checklist Network-Stack Agnostic

**Date:** 2026-06-15
**Epic:** 43 (Relay Admin UX)
**Story:** US-43.2 (setup checklist)
**Scope:** Remove the MetalLB-specific prerequisite gate from the relay setup endpoint and wizard.
**Status:** Complete

## Objective

`GET /api/v1/admin/relay/setup` was returning HTTP 500 in production because
`checkMetalLB` listed pods in the `metallb-system` namespace — a cross-namespace
read the API service account (`system:serviceaccount:default:llmsafespace-api`)
has no RBAC for. Because `GetSetup` aborts on the first check error, the entire
setup wizard was unusable.

The prior session mitigated the 500 by switching `checkMetalLB` to discovery of
the `metallb.io/v1beta1` API group. On review, the user correctly identified
that this still bakes a specific load-balancer implementation into the checklist.
The relay does not depend on MetalLB; it depends on the operator-supplied
`routerEndpoint` being reachable from relay VMs. That reachability can come from
MetalLB, kube-vip, a cloud LB, `hostNetwork: true`, or a tunnel — and the Epic-42
design (`A21`) documents the `hostNetwork` fallback explicitly. A MetalLB
prerequisite gate therefore breaks its own documented fallback path.

This session removes the MetalLB gate entirely so the checklist is
network-stack agnostic.

## Assumptions (Rule 7) and validation

1. **No remaining consumers of `metalLBInstalled`** — Validated via repo-wide
   grep (`metalLBInstalled|MetalLBInstalled|checkMetalLB|"MetalLB installed"`):
   all remaining hits are in transient opencode logs, zero in source, SDKs, or
   OpenAPI specs. Removing a JSON field is wire-safe (JSON clients tolerate
   missing fields).
2. **Prerequisites were advisory, not gating** — Validated by reading
   `RelaySetupWizard.handleDeploy`: it only checks `providers.length` and
   `routerEndpoint`. Removing one advisory row changes no deploy behavior.
3. **Reachability is still verified** — Validated: instance `Healthy` in
   `GetStatus` reflects whether the WireGuard tunnel actually came up. The LB
   check was leaky and redundant.
4. **Universal prerequisites are retained** — `checkRouter` (relay-router
   Deployment) and `checkCRD` (InferenceRelay CRD) are LLMSafeSpace-owned, not
   LB-specific. Correct to keep.

## Work Completed

### Backend
- `api/internal/handlers/relay_admin.go`: removed `MetalLBInstalled` field from
  `setupResponse`, removed the `checkMetalLB` function, removed its call from
  `GetSetup`. Added a doc comment on `GetSetup` explaining why the checklist is
  LB-agnostic and where reachability is verified.
- `api/internal/handlers/relay_admin_test.go`: removed the two MetalLB tests
  (`TestRelaySetup_MetalLBCRD_Detected`, `TestRelaySetup_MetalLBNotInstalled_NoError`).

### Frontend
- `frontend/src/api/relay.ts`: removed `metalLBInstalled` from `RelaySetup`.
- `frontend/src/components/settings/RelaySetupWizard.tsx`: removed the
  "MetalLB installed" prerequisite row (router + CRD rows remain).
- `frontend/src/components/settings/RelaySetupWizard.test.tsx`: removed the
  field from the mock and the `getByText("MetalLB installed")` assertion.
- `frontend/tests/e2e/relay-admin.spec.ts`: removed the field from the e2e mock
  and changed the prerequisites assertion to "Relay router deployed".

### Docs
- `design/stories/epic-43-relay-admin-ux/README.md`: updated the `GET /setup`
  response example to drop `metalLBInstalled` (and include `awsConfigured` to
  match the current contract), with a rationale note.

## Key Decision

**Remove rather than generalise.** The MetalLB check is not a "make it detect
any LB" problem — load-balancer implementation is an operator/infra concern, not
an application-level prerequisite. The wizard should verify only the things
LLMSafeSpace owns (router pod, CRD, creds) and let the operator own
reachability. Adding a generic LB-probe would re-introduce the same coupling
under a different name.

The Epic-42 design doc (`epic-42-multi-cloud-inference-relay/README.md`) was
**not** touched: its MetalLB references describe the actual LB choice for this
specific bare-metal Talos cluster (US-42.8), which is legitimate infra design,
not an application-level prerequisite.

## Adversarial Self-Review (Rule 11)

- **SDK/OpenAPI breakage?** None — grep confirms no references; JSON field
  removal is backward-compatible for parsers.
- **Unused imports after deletion?** None — `go build`, `go vet`, `gofmt` all
  pass (Go fails compilation on unused imports).
- **Empty Prerequisites step?** No — 2 items remain (router + CRD).
- **New reachability gap?** Pre-existing — prereqs were advisory only; instance
  health in status dashboard covers reachability; documented in doc comment.

Zero real findings.

## Tests Run

- `go test -run 'Relay|Setup' ./api/internal/handlers/` → `ok` (0.267s)
- `go vet ./api/internal/handlers/` → EXIT 0
- `gofmt -l` on the two Go files → clean
- `npm run typecheck` (frontend) → EXIT 0
- `vitest run` (RelaySetupWizard + relay api) → 16/16 passed
- `eslint` on the 4 changed frontend files → EXIT 0

## Files Modified

- `api/internal/handlers/relay_admin.go`
- `api/internal/handlers/relay_admin_test.go`
- `frontend/src/api/relay.ts`
- `frontend/src/components/settings/RelaySetupWizard.tsx`
- `frontend/src/components/settings/RelaySetupWizard.test.tsx`
- `frontend/tests/e2e/relay-admin.spec.ts`
- `design/stories/epic-43-relay-admin-ux/README.md`
- `worklogs/0292_2026-06-15_relay-setup-network-agnostic.md` (this file)

## Next Steps

- Open/merge the PR for `feat/epic42-relay-reconciler-phase1`. Note: the prior
  discovery-based MetalLB fix and its tests landed in commit `599e4512`; this
  commit supersedes that approach by removing the gate entirely. Reviewers should
  be aware both changes are on this branch.
- Optionally: if a *generic* reachability preflight is ever desired, it belongs
  in the Deploy step (validate `routerEndpoint` resolves / port is open), not in
  a static prerequisite checklist.
