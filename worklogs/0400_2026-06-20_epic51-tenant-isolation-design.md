# Worklog: Epic 51 — Tenant Isolation Design (gVisor + Admission-Webhook Quotas)

**Date:** 2026-06-20
**Session:** Deep-dive revalidation of US-10.6 (virtual namespaces) against current codebase, architectural assessment of namespace vs gVisor isolation, and creation of Epic 51 consolidating tenant-isolation work.
**Status:** Design complete; PR #304 opened for review.

---

## Objective

The original US-10.6 (Virtual Namespace Tenant Isolation, in Epic 10) was authored against an architecture that has since changed substantially. This session: (1) deep-dived the current codebase to identify what's still relevant vs outdated in US-10.6, (2) assessed whether single-namespace isolation is sufficient for a multi-tenant arbitrary-code platform, (3) evaluated Capsule vs vcluster vs alternatives for 1,000+ tenant scale, and (4) created a new Epic 51 consolidating the tenant-isolation work with the correct primary control (gVisor).

---

## Work Completed

### Phase 1: Codebase deep-dive (no worklogs used as source of truth)

Investigated the current state of workspace provisioning, controller RBAC, NetworkPolicy, CRD spec, secret handling, and org/tenant scoping. Key findings:

- **Network isolation already shipped.** Chart-level default-deny ingress (`workspace-network-policy.yaml:18-65`) + RFC1918/CGNAT-filtered egress (`network_policy.go:94-150`) blocks pod-to-pod traffic even in a shared namespace.
- **Controller secret scoping already shipped.** `rbac.scope=namespace` is the default (`values.yaml:644`); secrets CRUD is namespace-scoped (`rbac.yaml:49-101`).
- **Tenant identity on CRD.** `WorkspaceOwner{UserID, OrgID}` exists (`workspace_types.go:13-16`); org members are org-attributed per Design 0031 D4.
- **No per-tenant namespaces exist.** Single shared namespace for all workspace pods (`_helpers.tpl:99-101`).
- **No gVisor/kata.** Pods run on runc with `SeccompProfile: RuntimeDefault` (`pod_builder.go:343-344`).
- **No ResourceQuota/LimitRange manifests.** App-layer count quotas exist (Epic 43 org policies).

### Phase 2: Architectural assessment

Identified that the original US-10.6 premise (per-tenant virtual namespaces as the isolation boundary) was the wrong control:
- Namespaces don't stop container escape (kernel exploitation reaches the host node where namespace boundaries are irrelevant).
- The real primary control for arbitrary-code multi-tenancy is gVisor (userspace kernel).
- gVisor was buried in Epic 18 S18.7 Phase C behind ~40 points of hot-migration infrastructure with no hot-migration dependency.

### Phase 3: Scale assessment (Capsule vs vcluster vs alternatives)

- **vcluster:** ~256MB/tenant overhead = 256GB at 1,000 tenants. Non-starter.
- **Capsule:** ~0 per-tenant RAM overhead, but creates 1,000+ namespaces — doesn't scale (etcd object count, controller informer cache, API server, Helm/kubectl degradation). The earlier Epic 18 S18.8 Capsule decision was benchmarked at 100 tenants (10× below target).
- **Conclusion:** Neither namespace approach solves the actual threat (container escape) or scales to target. gVisor + admission-webhook quotas in a shared namespace is the right architecture.

### Phase 4: Epic 51 created

New `design/stories/epic-51-tenant-isolation/README.md` with four stories:
- **S51.1** — gVisor RuntimeClass (primary isolation control, blocked on nothing)
- **S51.2** — Per-tenant resource quotas via admission webhook (keyed on tenant pod label)
- **S51.3** — Pod tenant label (`llmsafespaces.dev/tenant`)
- **S51.4** — Hardening verification under gVisor (extend existing `security_test.go` tests)

### Phase 5: Cross-epic reference updates

- **Epic 10:** US-10.6 marked ⛔ superseded (original scope preserved in `<details>`). Dependency graph, threat model, RBAC notes, parallelizable note updated.
- **Epic 18:** S18.7 (gVisor) ➡️ moved to Epic 51. S18.8 reduced from 8pts to 3pts (EFS storage isolation only; original Capsule scope preserved in `<details>`). Security model, key decisions, implementation order, open questions, success metrics, design assessment updated. Total: 50pts → 40pts.
- **Epic 18 TESTPLAN:** S18.7/S18.8 test sections updated.
- **Epic 11:** Virtual-namespace non-requirement row updated.
- **Stories README:** Epic 51 row added; Epic 10 & 18 status lines updated; US-10.6 deferred line updated; Epic 51 added to recommended implementation order.

---

## Key Decisions

1. **gVisor over namespaces as primary isolation.** Container escape via kernel exploitation is the defining threat for arbitrary-code multi-tenancy. gVisor (userspace kernel) is the standard control (used by Google Cloud Run, App Engine). Namespaces don't address this threat.

2. **Shared namespace (no per-tenant namespaces).** Namespaces don't solve the primary threat and don't scale to 1,000+ tenants. Network + secret isolation already shipped in shared namespace. Per-tenant quotas delivered via admission webhook, not K8s ResourceQuota objects.

3. **Epic 51 as standalone epic (not US-10.6).** The work pulls gVisor from Epic 18 and supersedes US-10.6 from Epic 10, but its central control (gVisor) and mechanism (admission webhook) have nothing to do with either epic's primary purpose. Keeping it as "US-10.6" would be misleading.

4. **Soft-to-medium multi-tenancy posture.** Paying customers running dev tooling, not adversarial nation-states. Side-channel attacks (CPU/cache/Rowhammer) accepted as out-of-scope risk. Matches comparable SaaS dev platforms.

5. **Admission webhook over ResourceQuota.** K8s ResourceQuota objects require namespaces (which we're not creating). Webhook enforces quotas in-process at pod-create time, keyed on `llmsafespaces.dev/tenant` pod label. Reuses Epic 43 org policies + Epic 12 usage metering.

6. **Tenant identity = WorkspaceOwner.OrgID || WorkspaceOwner.UserID.** Matches Design 0031 D4 (org members are org-attributed). No schema change needed.

---

## Review Feedback (PR #304)

Four AI review iterations. Key issues found and fixed:

1. **Broken cross-references** — Epic 51 cited line numbers in Epic 18 for quotes that the same PR deleted. Fixed by preserving original S18.8 Capsule text in `<details>` block.
2. **Line-number citation drift** — EC2NodeClass reference shifted after `<details>` insertion. Fixed by referencing section name instead of line number.
3. **Working-tree vs committed-code discrepancy** — Citations verified against working tree (which has uncommitted `pod_builder.go` modifications) instead of committed version on `main`. `AutomountServiceAccountToken` is at line 214 on main (231 in working tree). Fixed all citations to match committed code.
4. **S51.4 false claim** — "not regression-tested today" was false; all hardening items have dedicated tests in `security_test.go` (TestG17/G22/G24, TestSandboxPod_SecurityContextHardening). Reframed S51.4 to its actual value: assert hardening survives under gVisor RuntimeClass.

Final review: all citation and factual errors resolved. Content substantively approved.

---

## Files Changed

| File | Change |
|---|---|
| `design/stories/epic-51-tenant-isolation/README.md` | **New** — epic spec (S51.1–S51.4) |
| `design/stories/epic-10-multi-tenant-trust/README.md` | US-10.6 marked superseded; threat model, dependency graph, RBAC notes updated |
| `design/stories/epic-18-hot-migration/README.md` | S18.7 moved to Epic 51; S18.8 reduced; security model, decisions, impl order, Q&A, metrics, assessment updated (50pts→40pts) |
| `design/stories/epic-18-hot-migration/TESTPLAN.md` | S18.7/S18.8 test sections updated |
| `design/stories/epic-11-organizations/README.md` | Virtual-namespace non-requirement row updated |
| `design/stories/README.md` | Epic 51 row added; Epic 10/18 status lines updated; US-10.6 deferred line updated; impl order updated |

No code changes. Design docs only.

---

## Open Items

- PR #304 pending merge (all CI checks pass; review bot substantively approved)
- Implementation of S51.1–S51.4 not started (design only this session)
- Billing tier → quota mapping deferred to Epic 43 (manual config until tier enforcement lands)
- EFS storage isolation (Epic 18 S18.8 reduced) deferred to when RWX lands
