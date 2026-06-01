# Worklog: CRD Schema Drift Fix + upgrade-test Cleanup

**Date:** 2026-06-01
**Session:** Resolve KubeAPIWarningLogger drift warnings; clean up upgrade-test workspaces
**Status:** Complete

---

## Objective

After worklog 0118 restored login and ended the Helm-upgrade cascade,
two follow-ups remained:

1. **CRD drift warnings** flooding the controller log on every reconcile:

       KubeAPIWarningLogger  unknown field "status.diskTotalBytes"
       KubeAPIWarningLogger  unknown field "status.diskUsedBytes"

   Symptom: harmless (k8s drops unknown fields silently) but anything
   reading those fields (frontend disk-usage widgets, controller
   recovery state from Epic 24) was effectively no-op'd.

2. **5 stuck `upgrade-test` workspaces** from the May 29 migration
   testing. Owned by the synthetic `upgrade-test` user; not real
   user data; visible in the workspace list and consuming pods.

---

## Work Completed

### CRD drift diagnosis

Two layers of drift found, not just one:

**Layer 1 — live CRD vs chart CRD.** `kubectl get crd workspaces.llmsafespace.dev`
showed only 20 status fields. `charts/llmsafespace/crds/workspace.yaml`
had 32. Helm 3 install-once semantics for `crds/` had left the cluster
behind across the rev 84-95 upgrade window:

| Field | In Go type | In chart CRD | In live CRD |
|---|---|---|---|
| `diskUsedBytes` | yes | yes | NO |
| `diskTotalBytes` | yes | yes | NO |
| `consecutiveFailures` | yes (Epic 24) | yes | NO |
| `lastFailureClass` | yes (Epic 24) | yes | NO |
| `lastFailureAt` | yes (Epic 24) | yes | NO |
| `nextRetryAt` | yes (Epic 24) | yes | NO |
| `safeMode` | yes (Epic 24) | yes | NO |
| ... 5 more | | | |

Fixed by:

    kubectl apply -f charts/llmsafespace/crds/workspace.yaml
    kubectl apply -f charts/llmsafespace/crds/runtimeenvironment.yaml

After apply: live CRD jumped from 20 to 32 status fields. The
`diskTotalBytes` / `diskUsedBytes` warnings stopped immediately on
the next reconcile.

**Layer 2 — chart CRD vs Go type.** Once the bigger drift was gone, a
new warning surfaced:

    unknown field "status.sessions[N].status"

Investigation showed the chart's `AgentSessionStatus` schema had:

    sessions:
      items:
        properties:
          id, title, lastActivityAt   # date-time

But the Go type at `pkg/apis/llmsafespace/v1/workspace_types.go:51-55`
has:

    type AgentSessionStatus struct {
        ID     string `json:"id"`
        Title  string `json:"title,omitempty"`
        Status string `json:"status"`   // "idle" | "busy"
    }

The Go side renamed `lastActivityAt` → `status` at some point and the
chart CRD never got the corresponding update.

Fix in commit `9612efa`: replace `lastActivityAt` with `status`
(enum `idle|busy`) in the CRD schema. Re-applied to cluster.

After both fixes: zero `KubeAPIWarningLogger` lines in 90s of fresh
controller logs.

### Defensive structural fix — NOTES.txt warning

The README at `charts/llmsafespace/README.md:138-148` already
documented that `helm upgrade` does not update CRDs and that
operators must run `kubectl apply -f charts/llmsafespace/crds/`
on every upgrade. The 0118 incident proved that a README warning
isn't enough — the manual step gets missed.

Added a `CRD UPGRADE` section to `charts/llmsafespace/templates/NOTES.txt`
so the warning prints on every `helm install`/`helm upgrade`. Includes
the verification command:

    kubectl get crd workspaces.llmsafespace.dev \
      -o jsonpath='{.spec.versions[0].schema.openAPIV3Schema.properties.status.properties}' \
      | jq 'keys | length'

so an operator can compare the field count to the Go type and detect
drift after each upgrade.

### Comprehensive Go-vs-chart drift audit

Ran a Python script to compare every JSON tag in the Go
`WorkspaceStatus` and `AgentSessionStatus` structs against the chart
CRD schema. After the sessions fix:

    WorkspaceStatus:
      Go has but chart missing: []
      Chart has but Go missing: []
    AgentSessionStatus:
      Go has but chart missing: []
      Chart has but Go missing: []

No further drift. (Script is one-off; not committed, but the pattern
is documented in this worklog for future use.)

### upgrade-test cleanup

```
$ kubectl get workspaces -n default -o json | python3 -c '... filter owner==upgrade-test ...'
1711f2da-b2cf-4ec9-8715-659eb2518e6e  Suspended
63695e96-581d-46c2-a22d-943276147cb3  Active
9900e481-914e-4e0c-94c1-874e5b673397  Active
b4c8f2be-84f3-4e5f-bfee-4e5ec465fe87  Suspended
dfe7c02a-bec8-43a4-84b2-0681d1d713e7  Active
```

Deleted all 5. Owner-reference cascade cleaned up:
- 5 PVCs (workspace-<uuid>)
- 5 password Secrets (workspace-pw-<uuid>)
- 5 pods (those that had any)

Verification:
- Total workspaces: 20 → 15
- pw secret count: 20 → 15 (matches workspace count)
- 4 Active + 11 Suspended (all owned by real user account)

---

## Key Decisions

- **Skipped the pre-upgrade Helm hook job for CRD apply.** A hook job
  needs cluster-wide CRD write permission, which is itself a security
  concern, and the README + NOTES.txt warning is the same pattern
  used by cert-manager, prometheus-operator, and other production
  charts. The right systemic fix lives outside Helm (e.g. `helm-mapkubeapis`
  or chart-style operator install) — bigger change for another day.

- **Verified BOTH directions of drift.** It would have been easy to
  stop after the first layer (live vs chart) since the visible
  warnings stopped, but a separate `sessions[].status` warning
  emerged once the noise cleared. Doing the full Go-vs-chart audit
  caught it.

---

## Tests Run

- `helm template llmsafespace charts/llmsafespace -f values-cluster.yaml`
  — clean render with the updated CRD.
- Live verification: 90s of fresh controller logs after CRD apply,
  zero `KubeAPIWarningLogger` lines.
- 5 upgrade-test deletions completed without orphan resources.
- `kubectl get workspaces` shows 15/15 owned by the real user account.

---

## Files Modified

- `charts/llmsafespace/crds/workspace.yaml` — replace
  `sessions[].lastActivityAt` (date-time) with `sessions[].status`
  (enum idle|busy) to match Go `AgentSessionStatus`.
- `charts/llmsafespace/templates/NOTES.txt` — new `CRD UPGRADE`
  section warning operators that `helm upgrade` does not update
  CRDs and how to verify drift.

Single commit: `9612efa fix(chart): align workspace CRD with Go AgentSessionStatus type`.

---

## Next Steps

- The Go-vs-CRD drift audit script could be added as a CI check.
  Pre-commit already runs `helm-render`; adding a Python script
  that diffs Go JSON tags against CRD schema would make this kind
  of drift fail at PR time, not at runtime.

- Continue monitoring for the next "ghost field" warning. The
  CRD-management pattern in this repo (write Go types first, manually
  update YAML CRD second) will keep producing these until something
  generates the CRD from the Go types (kubebuilder, controller-gen,
  etc.).

- 1 workspace remains in `Creating` phase (different user, init
  container slow). Not a problem — it'll either become Active or
  hit the new exponential-backoff recovery from US-24.5.
