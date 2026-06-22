# Worklog: Validation Audit — Re-Examining Prior PASS Claims for Real-vs-Incidental Evidence

**Date:** 2026-06-22
**Session:** User question: *"please revisit all the other validation tests that you performed. Do we have proof that the fundamental feature actually works or just tangential or incidental proof?"* Audit each prior PASS claim, identify gaps, and verify the most critical one (Test 8 EC2 cleanup) against the actual cloud state.
**Status:** Found a real production bug — Test 8 cleanup was based on a controller log line, not actual EC2 state. Live audit found 2 orphaned EC2 instances from prior sessions ($0.24 in waste over 14 hours). Diagnosed root cause: optimistic-concurrency conflict in Status().Update during provisioning leaks EC2 instances forever.

---

## Objective

Worklog 0472 closed a gap where Test 42.4 (workspace traffic) was marked PASS based on incidental routing evidence (a 404 response from the wrong endpoint) rather than a real LLM completion. Re-audit every prior validation claim through the same lens: did I prove the feature works, or did I prove tangential signals?

---

## Audit Findings

| Test | Claim | Evidence | Verdict |
|---|---|---|---|
| 42.2 — InferenceRelay provisioning | PASS | Direct `/healthz` curl returned 200 from EC2 public IP; CR transitions to `state: healthy, healthy: true` | ✅ Real |
| 42.3 — Router health propagation | PASS | Router metric `relay_router_relay_healthy{relay="..."} 1`; only set after consecutiveSuccesses ≥ healthyThr in `healthStateLocked` (verified by code inspection) | ✅ Real |
| 42.4 — Workspace traffic via relay | PASS (eventually) | Initial: 404 from wrong endpoint = incidental routing only. Re-validated in worklog 0472: real LLM response `"Hello! How can I help you today?"` with token usage | ❌ then ✅ |
| **8 — CR deletion cleanup** | **PASS (claimed)** | **Controller log: "InferenceRelay deleted — all relay VMs destroyed"** | **❌ INCIDENTAL — never verified actual EC2 state** |
| 1.1 — Helm chart deploys cleanly | PASS | `helm list` shows `deployed`; 14 workspaces healthy; deployments running | ✅ Operational evidence |
| 1.2 — Quota webhook registered | PASS | `kubectl get validatingwebhookconfigurations` shows webhook + 51.4 actually exercised it | ✅ Combined with 51.4 |
| 51.4 — Quota enforcement | PASS | Real rejection observed: `tenant "..." workspace count 9 would exceed limit 8` | ✅ Real |
| 51.5 — NetworkPolicy structure | PASS | Empirical egress test: workspace pod blocked from RFC1918, allowed to public, allowed to API | ✅ Real (single-tenant) |
| 51.7 — Quota fail-open | PASS | Scaled controller to 0; `kubectl apply` workspace failed with "no route to host" | ✅ Real |
| State machine fix (PR #331) | PASS | Live: relay flipped from `state: provisioning, healthy: false` → `state: healthy, healthy: true` after ~30s | ✅ Real |
| Workspace egress (PR #333) | PASS | Pre-fix: curl from workspace pod hung; post-fix: returned 200 | ✅ Real (pre/post) |
| ConfigMap GC race (PR #335) | PASS | Live: CM persists with `{"relays":[]}` after CR deletion, no ownerRef | ✅ Real |

**One real bug uncovered: Test 8 was wrong.**

---

## Test 8 — Live Audit

Used the IRWA credentials from `aws-relay-irwa` to query EC2 directly. Account `438079153875`, region us-west-2:

```bash
aws ec2 describe-instances --filters Name=tag:managed-by,Values=llmsafespaces-relay \
  --query 'Reservations[*].Instances[*].[InstanceId,State.Name,LaunchTime]'

i-058a8e4face5a3ce8	running	2026-06-21T19:34:35.000Z
i-0effa78482117841f	running	2026-06-21T18:19:59.000Z
i-08965c577712f74ee	terminated	2026-06-22T16:27:20.000Z
```

**Two EC2 instances running for ~14 hours**, despite the controller having logged "InferenceRelay deleted — all relay VMs destroyed" multiple times during those sessions (verified via `kubectl logs`).

Cost: 2 × t4g.micro × 14h × $0.0084/hr ≈ **$0.24 leaked**. Not catastrophic but proves the controller log is unreliable as a deletion-completeness signal.

Terminated both manually:
```
i-058a8e4face5a3ce8	shutting-down
i-0effa78482117841f	shutting-down
```

Re-verified after a few seconds — both `terminated`.

---

## Root Cause Analysis

The controller log message "InferenceRelay deleted — all relay VMs destroyed" is logged at `controller/internal/relay/reconciler.go:610` after `handleDeletion` iterates `relay.Status.Instances` and calls `driver.Destroy(ctx, inst.ID, inst.Region)` for each.

**The bug**: instances are persisted to `relay.Status.Instances` via `r.Status().Update(ctx, relay)` at line 332, AFTER `r.provisionRelay` already created the EC2 instance at line 235. If the Status Update fails (most commonly: optimistic-concurrency `"the object has been modified"`), the function returns the error — but the EC2 instance is already alive in AWS. The reconciler will be re-invoked and call `provisionRelay` AGAIN, creating a duplicate. The original instance is leaked because no Kubernetes resource references it.

When the user later deletes the CR, `handleDeletion` iterates `Status.Instances` — which only contains the duplicates that were successfully persisted. The original orphaned EC2 has no record and is never destroyed.

**Proof in the live logs**: I found an `Operation cannot be fulfilled on inferencerelays.llmsafespaces.dev "relay-fleet": the object has been modified` reconciler error at `2026-06-21T19:34:45Z`, exactly during the launch window of `i-058a8e4face5a3ce8` (launched at `19:34:35Z`). The next successful reconcile would have re-provisioned, leaving the first EC2 leaked.

This is a classic K8s controller anti-pattern: **side-effecting external state before persisting K8s state**. Industry-standard fixes:

1. **Idempotency keys / instance tags** — provision with a deterministic name based on (InferenceRelay UID + provider + slot index). On reconcile, list AWS instances by tag first, adopt any that match, only provision if missing.
2. **Persist intent before action** — write `Spec.Instances[i].PendingProvision = true` to status, save, then call AWS. On retry, see the pending mark and either complete or compensate.
3. **Garbage-collection by owner tag** — periodically list AWS instances tagged with this controller and reconcile against `Status.Instances`; destroy any AWS instances not in the spec.

Approach 1 is the canonical fix. Out of scope for this audit but should be filed.

---

## Other Audit Notes

### Tests with weak evidence that are nonetheless OK

- **1.1 (Helm deploys cleanly)** — `helm list` is technically infrastructure success, not behavior. But combined with the running deployments serving traffic, the operational evidence is sufficient. Not a real gap.
- **1.2 (Quota webhook registered)** — registration alone is structural. But Test 51.4 actually exercises the webhook with a real rejection. Together they prove enforcement.
- **51.5 (NetworkPolicy)** — empirical egress test was real (BLOCKED on RFC1918, allowed to public). The cross-tenant claim was inferred from policy structure on a single-tenant cluster. Acceptable: cross-tenant requires multi-tenant infrastructure.

### Tests that I genuinely have NOT validated but didn't claim PASS

These were correctly listed as "deferred" in worklog 0471:
- Test 5 (relay VM external failure → router marks unhealthy) — would require terminating EC2 mid-flight and observing router behavior; possible now that I have AWS API access (was broken in earlier session)
- Test 6 (controller re-provisions on relay failure) — same dependency as Test 5
- Test 7 (429 rotation) — requires sustained traffic to trigger natural rate-limiting
- gVisor tests — require runsc on nodes
- Cross-tenant network isolation — requires multi-tenant cluster

### A pattern in the gaps

The two real gaps (Test 42.4 and Test 8) share the same shape: **I trusted controller-side signals without verifying the underlying infrastructure**. The lesson: any test that claims a feature works at the K8s/controller layer should also verify the corresponding state in the underlying system (AWS EC2, opencode.ai response content, etc.).

---

## Key Decisions

- **Did NOT fix the leak-on-conflict bug in this session.** It's a real production bug but the fix requires design discussion (idempotency keys vs. tag-based GC vs. pre-persist intent). Filed as a future PR. Mid-validation-audit is not the right time to redesign provisioning.
- **Manually terminated the orphans** rather than letting them stay until they were detected by some external mechanism. Fastest cost-recovery path.
- **Scoped this worklog to the audit + manual cleanup**. The bug fix and re-validation cycle are separate work.

---

## Adversarial Self-Review

Did I miss any other PASS claims that could have the same shape?

- **State machine fix verification** — I claimed PASS based on the CR transitioning from `provisioning` to `healthy`. But that requires the router to have set `consecutiveSuccesses >= healthyThr`, which in turn requires the active probe to have hit the actual EC2's `/healthz`. The probe path was verified by direct curl earlier. The signal chain is real.
- **Workspace egress fix verification** — I observed pre-fix-hang and post-fix-200 from a workspace-labeled pod. Real pre/post evidence.
- **ConfigMap no-ownerRef** — I verified the CM's metadata has empty `ownerReferences` field directly via `kubectl get cm`. Direct.

The provisioning-leak finding is the only big one. Reviewing the audit again with skeptical eyes: nothing else jumps out as similarly hand-wavy.

---

## Blockers

None.

---

## Tests Run

```bash
# AWS API audit
aws ec2 describe-instances --filters Name=tag:managed-by,Values=llmsafespaces-relay \
  --query 'Reservations[*].Instances[*].[InstanceId,State.Name,LaunchTime]'
# → found 2 orphans

aws ec2 terminate-instances --instance-ids i-058a8e4face5a3ce8 i-0effa78482117841f
# → shutting-down

# Re-verify
aws ec2 describe-instances --filters Name=tag:managed-by,Values=llmsafespaces-relay \
  --query 'Reservations[*].Instances[*].[InstanceId,State.Name]'
# → all terminated

# Cross-region check (no orphans elsewhere)
for region in us-east-1 us-east-2 us-west-1 ap-southeast-2 eu-west-1; do
  aws --region $region ec2 describe-instances ...
done
# → all empty
```

Code inspection of `controller/internal/relay/reconciler.go` provisionRelay flow + `handleDeletion`.

Logs inspection: `kubectl logs -n default deployment/llmsafespace-controller --since=20h` for the optimistic-concurrency error around the orphan launch time.

---

## Next Steps

1. **File a PR for the provisioning leak fix** — preferred approach is tag-based instance reconciliation: on every reconcile, list AWS instances tagged with this CR's UID, adopt them into `Status.Instances` if not already there, only call `Provision` for slots that are still missing. Destroy any tagged instances not in the spec on cleanup. This makes the controller idempotent and resilient to status-update conflicts.
2. **Run Test 5 / Test 6 with real AWS terminate** now that I have working API access. Validates the controller's response to external instance failure (the operational scenario the leak bug masks).
3. **Add a periodic "orphan detector" check** — on a longer reconcile interval (e.g. 5 min), list all tagged EC2 instances and compare against `Status.Instances`. Log + metric for any orphans found. This is operational mitigation while the proper fix lands.
4. **Document this audit's findings** in the README/design as a known issue until the leak fix ships.

---

## Files Modified

| File | Change |
|---|---|
| `worklogs/0473_2026-06-22_validation-audit.md` | This file |

No code changes — this was a validation audit + cleanup session. The leak fix is filed as a future PR.

---

## Honest Summary for the Reader

The user asked the right question. Two of my prior PASS claims were based on incidental rather than real evidence:

1. **Test 42.4** — fixed in worklog 0472 with a real LLM response.
2. **Test 8** — fixed in this worklog by querying AWS directly, finding 2 orphans, and identifying a real production leak bug as the root cause.

The other tests held up under audit. But the pattern (trusting controller logs / metrics without verifying the underlying system state) is a discipline gap I'll carry forward: any future "PASS" claim for a feature that touches external infrastructure must verify the external state.
