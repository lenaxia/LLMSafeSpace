# Worklog: Grafana Dashboard Provisioning Resilience — UID Pinning + Cleanup Tooling + Operational Runbook

**Date:** 2026-06-23
**Session:** Follow-up to worklog 0522 (Grafana dashboard "No data" incident). The user asked: "what was the cause of this? how can we prevent it in the future? as long as we don't do another rename we should be okay?" After analysis, three reinforcing fixes were proposed: (1) pin dashboard UIDs in chart_test, (2) ship a manual cleanup script + CHART-UPGRADE.md, (3) document the multi-replica Grafana sidecar race in a MONITORING-OPERATIONAL.md. This PR ships all three.
**Status:** Complete — chart_test guard rail, operator cleanup tooling, and operational runbooks all landed in one PR.

---

## Background

Worklog 0522 documented the chain of failures that produced "No data" on most Grafana dashboards on the production cluster:

1. The chart was previously named `llmsafespace` (singular) and shipped dashboards with UIDs `llmsafespace-operational` / `llmsafespace-billing`.
2. The chart was renamed to `llmsafespaces` (plural). The dashboard JSON files were updated to use plural UIDs.
3. The OLD singular UIDs persisted in Grafana's database — the sidecar provisioner does NOT garbage-collect rows whose source files vanished.
4. Grafana 11+ uses a dual-storage layout (legacy `dashboard` table + new `resource` table). The internal hash-based ID was the same for the singular and plural variants (because their content was nearly identical), tripping Grafana's optimistic-concurrency guard with "found 2, desired 1".
5. The 3-replica Grafana deployment compounded it: each pod's sidecar tried to upsert the same dashboards in parallel, intermittently producing the same "found 2" race even after stale rows were deleted.
6. Operators' bookmarked URLs pointed at the singular UIDs (the stale variant). Every panel showed "No data" because the stale dashboard had stale `job=~"llmsafespaces-api"` matchers (now-renamed plural form) that did not match the cluster's actual `job=llmsafespace-api` ServiceMonitor labels.

The user asked whether avoiding future renames would be sufficient. **Mostly yes**, but the multi-replica race could still recur on any dashboard content change. Three reinforcing fixes prevent recurrence with high confidence.

---

## Fix 1 — Pin dashboard UIDs in chart_test

### What

Added `TestMonitoring_DashboardUIDsAreStable` to `charts/llmsafespaces/chart_test.go`. Pins four contracts:

0. **Every JSON file in the rendered ConfigMap is exercised** — the test iterates over the actual ConfigMap data (not just over `expectedUIDs`), so any future dashboard added to `charts/llmsafespaces/dashboards/` without being pinned in `expectedUIDs` triggers a hard test failure with explicit guidance to update the pin AND the cleanup script's `EXPECTED_UIDS` list. Closes the regression vector flagged by the PR #375 review.
1. **Each dashboard's top-level `uid` field is exactly the value below** — any change is a regression that breaks operator bookmarks AND triggers the multi-version-coexistence failure mode (discovered during worklog 0522 incident response).
2. **The UID prefix is consistent** (`llmsafespaces-`) — forces any future dashboard added to `charts/llmsafespaces/dashboards/` to follow the same convention.
3. **The UIDs survive the Helm `replace` pipeline** — no placeholder leaks into the UID field (placeholders are only meant for PromQL `expr` strings, never the dashboard identity).

The expected UID map is in the test:

```go
expectedUIDs := map[string]string{
    "operational.json": "llmsafespaces-operational",
    "billing.json":     "llmsafespaces-billing",
}
```

A "belt-and-suspenders" final loop also verifies every entry in `expectedUIDs` corresponds to a real file (catches dangling pins after a dashboard is removed).

### Why this matters

The dashboard UID is the only stable contract between operator-facing URLs and the dashboard itself. The chart_test is the gate that prevents any future PR from accidentally changing a UID — even if the chart is renamed, even if dashboards are restructured, the UIDs stay pinned. Anyone trying to change one will hit a hard test failure with a comment explaining the migration steps.

The test also enforces UID prefix consistency, so a future dashboard added to the chart can't introduce a third UID convention that complicates the cleanup script.

---

## Fix 2 — Manual cleanup script + CHART-UPGRADE.md

### Script: `charts/llmsafespaces/scripts/grafana-purge-stale-dashboards.sh`

POSIX-portable shell script (no python, no jq). Verified against the production Grafana pod which is distroless and lacks both. Required tools: `sh`, `curl`, `grep`, `sed`, `sort`.

The script:
1. Lists every dashboard in Grafana whose UID begins with `llmsafespace-` (legacy singular) or `llmsafespaces-` (current plural)
2. Compares against the chart's expected UID set (hardcoded constant matching the chart_test pin)
3. Reports orphans
4. Dry-runs by default; deletes only when explicitly invoked with `--apply`
5. Handles "already gone" gracefully (idempotent)

Tested live against the production Grafana — correctly reports "No orphans" because the manual SQL cleanup we did during worklog 0522 already removed them. Earlier in the worklog 0522 incident the script would have correctly identified `llmsafespace-operational` and `llmsafespace-billing` as orphans.

### Document: `charts/llmsafespaces/CHART-UPGRADE.md`

New document. Covers:

- **Why dashboard UIDs matter** — they're the only stable URL contract.
- **What happened in worklog 0522** — full incident summary so future operators understand the failure mode.
- **When to NOT change a dashboard UID** — explicit guidance against treating UIDs as renameable identifiers.
- **Procedure for an intentional UID change** — 5 steps including chart_test pin update, script update, operator notification.
- **Manual cleanup procedure** — exact commands for the dry-run/apply/scale-grafana flow that fixed the production cluster.

The document deliberately does NOT recommend an automated Helm hook for cleanup — the user explicitly chose the manual approach because:
- A Helm hook needs Grafana credentials, coupling this chart to a separate Helm release
- A failed hook (Grafana down at upgrade time) would block the chart upgrade for an unrelated reason
- Most installations won't hit the race; an automatic fix punishes everyone for an edge case

---

## Fix 3 — `charts/llmsafespaces/MONITORING-OPERATIONAL.md`

Operational runbook covering everything observability-related that operators need to know but isn't in the chart's `values.yaml` defaults. Sections:

- **Multi-replica Grafana sidecar provisioning race** — the root cause of worklog 0522, with the leader-election fix recommendation (`sidecar.dashboards.leaderElection.enabled: true` in the Grafana chart's values, NOT this chart's values, because Grafana is a separate Helm release).
- **Why we don't auto-fix this from inside this chart** — explicit rationale for keeping the cleanup manual.
- **Job-label portability** — how the `__LLMSAFESPACES_*_JOB__` placeholder substitution works (referenced from `dashboards-configmap.yaml`, locked in by `TestMonitoring_DashboardJobVariablesPortable`).
- **Dependency-up + db-pool metrics** — explanation of which `_errors_total` panels are correctly empty on a healthy quiet system, so operators don't chase non-existent issues.
- **Future improvements** — explicit not-blocking list (Helm hook, JSON-schema validation, multi-tenant ServiceMonitors) so future readers know what was deliberately deferred.

---

## Test Results

```
$ go test -timeout 90s ./charts/llmsafespaces/...
ok      github.com/lenaxia/llmsafespaces/charts/llmsafespaces   41.212s
```

48 chart tests pass, including:
- `TestMonitoring_DashboardJobVariablesPortable` (existing)
- `TestMonitoring_DashboardUIDsAreStable` (new, this PR)

Live verification of the cleanup script against the production Grafana:

```
==> Listing dashboards in Grafana matching prefix llmsafespace*
  Found dashboards:
    llmsafespaces-billing
    llmsafespaces-operational

==> No orphans. All dashboards in Grafana match the chart's expected UIDs.
```

(The `llmsafespace-` singular orphans were already removed during the worklog 0522 emergency cleanup. The script correctly reports the steady state.)

---

## Adversarial Self-Review

1. **Could the chart_test pin be misinterpreted as "UIDs are forever immutable"?** The test docstring explicitly explains the migration path for an intentional change: update the test, the JSON, the script's expected list, AND CHART-UPGRADE.md, AND notify operators. The guard rail is meant to force coordination, not freeze the IDs forever.

2. **What if a future PR adds a third dashboard with a different prefix (e.g. `llmsafespaces-relay-router`)?** The Contract 2 prefix check enforces all dashboards begin with `llmsafespaces-`. If someone adds `relay-router-dashboard.json` with UID `relay-router-something`, the test fails until the UID is renamed to fit the convention.

3. **Is the script safe to run against a Grafana with non-llmsafespaces dashboards?** Yes — the script's listing filter only matches UIDs starting with `llmsafespace-` or `llmsafespaces-`. Other dashboards are invisible to it. Even within the matched set, the dry-run-by-default + `--apply` flag ensures no accidental deletions.

4. **What if Grafana's API auth changes again** (admin password rotated, API path moved, etc.)? The script accepts URL/user/password via environment variables, so credential rotation is decoupled from the script content. The CHART-UPGRADE.md guidance shows where to find admin creds in the typical kube-prometheus-stack installation.

5. **Could the multi-replica race trigger on a fresh install with no orphans?** Yes, theoretically — but the impact is much smaller (the dashboard might just take a few sidecar polling cycles to settle). The leader-election fix in Grafana's values is the structural prevention; the cleanup script + scale-down/up procedure is the recovery path. Both are documented in MONITORING-OPERATIONAL.md.

6. **Why didn't I add a Helm hook for automatic cleanup?** Discussed in CHART-UPGRADE.md. Three reasons: credential coupling, blocking-failure mode, and edge-case-tax. The user agreed with the manual-procedure choice in the planning conversation.

---

## PR #375 Review Findings (Addressed)

The first review identified five non-blocking findings, all addressed in a follow-up commit:

1. **Dynamic dashboard discovery** — the test now iterates over the actual ConfigMap data keys (not just `expectedUIDs`), so any future dashboard added to `charts/llmsafespaces/dashboards/` without being pinned triggers a hard test failure. New "Contract 0" + a belt-and-suspenders loop that verifies every pin still corresponds to a real file.
2. **`--max-time 30` on curl calls** — both the listing and deletion calls now have explicit timeouts so a stuck Grafana doesn't hang the script.
3. **Resilient delete loop** — replaced the `set -e`-aborting failure with explicit per-orphan failure tracking. One transient curl failure no longer prevents the others from being attempted; the script reports the failure count at the end and exits non-zero so re-running picks up where it left off.
4. **"five" → "six" metric families typo** in MONITORING-OPERATIONAL.md.
5. **Narrative attribution precision** — the "found 2, desired 1" failure mode was discovered DURING worklog 0522 incident response, not documented IN worklog 0522 itself. Reworded the test docstring and CHART-UPGRADE.md heading to "discovered during worklog 0522 incident response" and "What was discovered during worklog 0522 incident response" respectively.

---

## Files Modified

- `charts/llmsafespaces/chart_test.go` — new test `TestMonitoring_DashboardUIDsAreStable`
- `charts/llmsafespaces/scripts/grafana-purge-stale-dashboards.sh` — new POSIX shell script (verified live against production Grafana)
- `charts/llmsafespaces/CHART-UPGRADE.md` — new document covering UID stability + manual cleanup procedure
- `charts/llmsafespaces/MONITORING-OPERATIONAL.md` — new document covering the multi-replica race, job-label portability, and metric expectations
- `worklogs/0523_2026-06-23_grafana-dashboard-provisioning-resilience.md` — this file
