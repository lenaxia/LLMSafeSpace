# Operational dashboard: Noisy Neighbor Detection — by User row

## What changed

Split the operational dashboard's "Noisy Neighbor Detection" section into
two collapsible top-level rows:

- **Noisy Neighbor Detection — by Workspace** (existing row, renamed).
  Three panels (CPU, Memory, Disk %) widened from `w=6` to `w=8` to fill
  the row symmetrically now that the user-active-compute panel has moved
  out.
- **Noisy Neighbor Detection — by User** (new row, id 38). Four panels:
  - **Top-10 Users by CPU** (id 39): `topk(10, sum by (user_id) (rate(workspace_cpu_milliseconds_total[5m])) / 1000)`. Red threshold at 16 cores.
  - **Top-10 Users by Memory** (id 40): `topk(10, sum by (user_id) (workspace_memory_used_bytes))`. Red threshold at 8 GiB.
  - **Top-10 Users by Disk % of PVC** (id 41): `topk(10, sum by (user_id) (workspace_disk_used_bytes) / clamp_min(sum by (user_id) (workspace_storage_bytes), 1) * 100)`. Red threshold at 95%.
  - **Top-10 Users by Active Compute (parallel workspaces)** (id 24): the existing per-user panel, **moved** from the workspace row to its natural home in the user row.

Dashboard `version` bumped from 2 to 3.

## Why this design

### Why aggregate per-workspace gauges instead of using the dedicated user counters

The metrics package already exposes per-user counters
(`llmsafespaces_user_cpu_milliseconds_total`,
`llmsafespaces_user_disk_bytes_seconds_total`,
`llmsafespaces_user_memory_bytes_seconds_total`). They were not used
because:

1. **Noisy-neighbor detection is an instantaneous-state question, not a
   billing question.** The panel descriptions make this explicit ("at
   the current instant"). The user counters are cumulative seconds-products
   designed for billing aggregation; converting them to instantaneous
   resource pressure adds analytical noise.
2. **Symmetry with the by-Workspace row.** The new by-User panels use the
   same underlying metrics as their by-Workspace counterparts, just
   `sum by (user_id)` aggregated. A user appearing hot in by-User and a
   workspace appearing hot in by-Workspace are guaranteed consistent — no
   "user X is hot but no individual workspace is" anomalies.
3. **Disk % requires both used and allocated**, and the user counters do
   not expose allocated bytes. We must aggregate both
   `workspace_disk_used_bytes` and `workspace_storage_bytes` per user_id
   to compute the ratio.

### Why thresholds 16 cores / 8 GiB / 95%

The per-workspace thresholds in the existing panels are:
- CPU red at 4 cores (4× 500m request burst limit)
- Memory red at 2 GiB (4× 512Mi request burst limit)
- Disk % red at 95%

Per-user thresholds scale linearly to "4 workspaces continuously bursting":
- 4 × 4 cores = **16 cores** for CPU
- 4 × 2 GiB = **8 GiB** for memory
- 95% disk-% is unchanged because it is already a ratio.

A user above any per-user red is consuming more aggregate platform resource
than four maxed-out average tenants.

### Why this is two top-level rows, not one row with two sub-row sections

Grafana row-collapse semantics fold every panel between this row and the
next row at the same level. Adding sub-rows would mean clicking the parent
"Noisy Neighbor Detection" row only collapses up to the first sub-row
header — confusing UX.

Two sibling top-level rows (each with the section name in the title plus
"by Workspace" / "by User") collapse independently and group the panels
correctly. This matches how every other section of the operational
dashboard is structured.

### Why panel 24 was moved (not left in the workspace row)

The existing "Top-10 Users by Active Compute (parallel workspaces)" panel
(id 24) was logically misplaced in a row titled "by Workspace". Now that
there is a dedicated by-User row, the panel naturally belongs there.
The panel itself is unchanged — only its `gridPos.x` and `gridPos.y`
were updated to fit the new row layout (x=18, y=50).

### Why workspace panels widened from w=6 to w=8

With one panel removed (the moved id 24), the workspace row needs to fill
24 grid units across 3 panels. `w=8` × 3 = 24, x positions 0/8/16. Matches
the convention used by the other 3-panel rows on this dashboard
(Workspace Lifecycle, Resource Saturation, Controller & Recovery, and
Security & Auth all use this same `w=8 × 3 @ x=0/8/16` shape).

## Validation

### Live PromQL validation against cluster Prometheus

All three new queries were validated against the live deployment's
`kube-prometheus-stack-prometheus` instance via port-forward, using the
**actual** scrape pattern (`llmsafespace-controller.*`) that the chart
substitutes into the dashboard at render time:

| Query | result count | top value | second value |
|---|---|---|---|
| Top-10 Users by CPU | 2 | 1.10 cores | 0.08 cores |
| Top-10 Users by Memory | 2 | 6.08 GB | 530 MB |
| Top-10 Users by Disk % | 2 | 39% | 1% |

All return Prometheus `status: success`, all produce sensible
aggregations across the 2 active users currently in the cluster. The
queries are syntactically valid and semantically correct.

### Chart test suite

- `TestMonitoring_DashboardJobVariablesPortable` — PASS (new panels use
  `__LLMSAFESPACES_CTRL_JOB__` placeholder, correctly substituted).
- `TestMonitoring_DashboardUIDsAreStable` — PASS (top-level UID
  unchanged).
- `TestMonitoring_DashboardConfigMap_*` — PASS (new panels round-trip
  through ConfigMap rendering).
- Full `chart_test.go` suite (`go test ./charts/llmsafespaces/...`) —
  PASS (69.3s).

### Layout verification

After transformation, `gridPos` audit:

```
id= 20 y= 40  Noisy Neighbor Detection — by Workspace        (row, w=24)
id= 21 y= 41  Top-10 Workspaces by CPU                       (w=8, x=0)
id= 22 y= 41  Top-10 Workspaces by Memory                    (w=8, x=8)
id= 23 y= 41  Top-10 Workspaces by Disk % of PVC             (w=8, x=16)
id= 38 y= 49  Noisy Neighbor Detection — by User             (row, w=24, NEW)
id= 39 y= 50  Top-10 Users by CPU                            (w=6, x=0,  NEW)
id= 40 y= 50  Top-10 Users by Memory                         (w=6, x=6,  NEW)
id= 41 y= 50  Top-10 Users by Disk % of PVC                  (w=6, x=12, NEW)
id= 24 y= 50  Top-10 Users by Active Compute                 (w=6, x=18, MOVED)
id= 25 y= 58  Controller & Recovery                          (was y=49)
... [everything below shifted by +9]
```

Panel ids are unique. No panel data was lost — the transformation only
added rows/panels and adjusted gridPos values.

## Adversarial self-review

- **Could `sum by (user_id)` on `workspace_storage_bytes` understate the
  denominator if a workspace's PVC isn't yet observed?** Yes, transiently.
  Mitigation: `clamp_min(..., 1)` prevents division-by-zero and ensures
  the panel never returns `+Inf` or `NaN` even during the first scrape
  cycle of a new workspace. A user with an unobserved-PVC workspace will
  briefly show a slightly inflated disk % until the PVC scrape catches up,
  which is acceptable for a noisy-neighbor signal.
- **Does `topk(10, ...)` work correctly with `sum by (...)`?** Yes —
  `topk` is a final scalar/instant-vector reducer; placing it outside
  `sum by` returns the top-10 user_id values from the aggregated set,
  which is the intended behavior.
- **Could panel 24's move corrupt its existing query?** No — only the
  panel's `gridPos.x` and `gridPos.y` were changed. The Prometheus query
  (`llmsafespaces_user_active_seconds_total`), legend format, threshold,
  unit, and description are byte-identical.
- **Could the y-shift miss any panel?** The transformation uses
  `if p["gridPos"]["y"] >= 49: p["gridPos"]["y"] += 9` which is
  exclusive-of-panel-24 (panel 24 is moved separately). Sanity check: the
  Metering row was at y=67, post-shift y=76 — confirmed via assertion in
  the transform script. Controller row was at y=49, post-shift y=58 —
  confirmed. Both line up with the new user-row span (y=49 header, y=50
  panels h=8, end y=58).
- **Does Grafana 11 still accept `unit: "decbytes"` for memory?** Yes —
  same unit code used by the existing memory panel (id 22). No change in
  semantics.
- **Could the new PromQL ever produce empty panels for legitimately
  empty cluster state?** Yes — if no workspaces are running for any user,
  the queries return zero series and the bar gauges show "No data". This
  is the correct behavior; it matches the by-Workspace panels' behavior
  in the same scenario.

## Files touched

- `charts/llmsafespaces/dashboards/operational.json` — the three new
  bar-gauge panels, the new row header, the moved panel, the widened
  workspace panels, and the y-shift on Controller & Recovery and below.
  Dashboard `version` 2 → 3.
- `worklogs/NNNN_2026-06-23_dashboard-noisy-neighbor-by-user.md` — this
  worklog.

## Followups

- Once this lands and is deployed via `helm upgrade --reuse-values`,
  hard-refresh `https://grafana.thekao.cloud/d/llmsafespaces-operational/`
  in the browser. Grafana caches dashboard JSON aggressively; a hard
  refresh (Ctrl-Shift-R) is required to see the new row.
- Consider extending the Prometheus alerts in
  `charts/llmsafespaces/templates/prometheus-rules.yaml` to fire when a
  user crosses the per-user thresholds (16 cores, 8 GiB, 95% disk). Today
  the alerts are per-workspace only. Out of scope for this PR.
