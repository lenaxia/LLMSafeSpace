# Bargauge fix: `reduceOptions.values: true` → `false`

## Problem

Reported by user immediately after deploying PR #379 (the new by-User
Noisy Neighbor row): "I only have one active user at the moment, but
that user shows up a bunch of times in the user tables. and the
workspace ones are only showing one workspace repeatedly."

## Root cause

Every `bargauge` panel in both dashboards had `options.reduceOptions.values: true`.

In Grafana 11, the `bargauge` panel type defaults to **range query mode**
(unlike `stat`, which defaults to instant). When `reduceOptions.values` is
`true`, Grafana renders one bar per **(series, datapoint)** tuple — not
one bar per series. For a 5-minute time window with the default 30-second
scrape step, each series produces ~11 datapoints, so the panel renders
~11 bars per workspace/user, all sharing the same legend label
(`{{workspace_id}}` or `{{user_id}}`).

This explains both symptoms:

- **"One workspace repeated"**: 11 unique workspaces × 11 datapoints =
  ~110 bars labeled with workspace_id. With each workspace's 11 bars
  appearing consecutively in the bargauge, it looks like the same
  workspace is being shown over and over.
- **"One user shown a bunch of times"**: with 1 active user_id and
  `sum by (user_id)` producing 1 series with 11 datapoints, the panel
  renders 11 identical bars.

Live verification against the cluster's `kube-prometheus-stack-prometheus`
instance confirmed the diagnosis:

```
query_range: topk(10, llmsafespaces_workspace_memory_used_bytes{...})
  series count: 11
  workspace=10910c88 points=10
  workspace=1d6d8407 points=11
  ...
  total bars Grafana would render with values:true = 110
```

## Fix

Set `options.reduceOptions.values: false` on every bargauge panel. With
`values: false`, Grafana applies the configured reduction calc
(`lastNotNull`, already set on every panel) per series and renders **one
bar per series**, value = the most recent non-null datapoint.

This is the intended behavior for "Top-N X by Y" panels: one bar per
top-N entity, sized by its current value.

## Affected panels (12 total)

`operational.json` (7 panels — the entire Noisy Neighbor section):

| id | title |
|---|---|
| 21 | Top-10 Workspaces by CPU |
| 22 | Top-10 Workspaces by Memory |
| 23 | Top-10 Workspaces by Disk % of PVC |
| 24 | Top-10 Users by Active Compute (parallel workspaces) |
| 39 | Top-10 Users by CPU |
| 40 | Top-10 Users by Memory |
| 41 | Top-10 Users by Disk % of PVC |

`billing.json` (5 panels — every bargauge):

| id | title |
|---|---|
| 9 | Top-10 Users by Token Consumption |
| 10 | Top-10 Users by Compute Hours |
| 11 | Top-10 Workspaces by Compute Hours |
| 12 | Top-10 Users by Daily Quota Utilization % |
| 20 | Top-10 Users by LLM Request Count |

Dashboard versions bumped: operational 3 → 4, billing 2 → 3.

## Why this bug went undetected

- Both dashboards were created with `values: true` from inception (the
  bug pre-dates PR #379, which only inherited the same template).
- The symptom only becomes visible when there are many concurrent
  workspaces. Earlier in the cluster's life there were typically only
  one or two workspaces running, so 1 workspace × 11 datapoints = 11
  bars all looked plausibly like 11 different workspaces (you'd have
  to read the labels carefully).
- Grafana's bargauge documentation is sparse on the difference between
  `values: true` and `values: false`. The actual UI label for the
  toggle is "All values" with helper text "If checked, all values will
  be shown, otherwise only the calculated value will be shown" — clear
  in retrospect, ambiguous on first reading.

## Validation

### Chart tests

- `go test -run TestMonitoring_Dashboard -count=1 ./charts/llmsafespaces/...` — PASS.
- `TestMonitoring_DashboardJobVariablesPortable` — PASS (no placeholder
  changes).
- `TestMonitoring_DashboardUIDsAreStable` — PASS (UIDs unchanged).
- `TestMonitoring_DashboardConfigMap_*` — PASS.

### Diff is minimal

```
charts/llmsafespaces/dashboards/billing.json     | 14 +++++++-------
charts/llmsafespaces/dashboards/operational.json | 16 ++++++++--------
2 files changed, 15 insertions(+), 15 deletions(-)
```

12 lines flipped from `"values": true` to `"values": false`, plus the
two `version` bumps and a trailing-newline fix in `billing.json` that
the dashboard JSON had been missing.

## Adversarial self-review

- **Could `values: false` ever lose data?** No. With `values: false`,
  Grafana applies the configured calc (`lastNotNull` on every panel)
  per series. Since each panel uses an instant-style "current value"
  metric (gauge or rate-of-counter), the last non-null datapoint **is**
  the value the panel is meant to display. Reducing further to a single
  scalar matches the UI affordance these panels are providing
  ("top-10 X **right now**").
- **Could `topk(10, ...)` interact strangely with `values: false`?**
  No. `topk` is evaluated at each instant and selects the 10 highest
  series. With `values: false` Grafana takes the `lastNotNull` of each
  of those series — exactly the top-10 most recent values, one bar per.
- **Will the visual change break operator muscle memory?** Bars now
  show one per workspace/user instead of ~11 per. The total number of
  bars on screen drops from ~110 to ~10 per panel. Operators looking
  at the dashboard will notice; that's intentional and an improvement.
  The legend labels and value units are unchanged.
- **Should I have also reviewed non-bargauge panel types?** No. The
  `reduceOptions.values` field only affects panels that render
  per-series breakdowns (`stat`, `bargauge`, `gauge`). I audited the
  whole dashboard for `bargauge`-type panels and fixed all 12. The
  `stat` panels (e.g., "Active Workspaces") use `values: false`
  already (they show one calculated number, not one per series, so
  the bug doesn't apply to them).

## Files touched

- `charts/llmsafespaces/dashboards/operational.json` — 7 bargauge
  panels: `values: true` → `values: false`. Version 3 → 4.
- `charts/llmsafespaces/dashboards/billing.json` — 5 bargauge panels:
  same fix. Version 2 → 3.
- `worklogs/0528_2026-06-23_dashboard-bargauge-values.md` — this
  worklog.

## Followups

- After deploy: hard-refresh both dashboards in the browser; expect
  one bar per workspace/user instead of one bar per (entity, scrape).
- Consider adding a chart_test that asserts every bargauge panel has
  `reduceOptions.values: false`, so this exact regression cannot
  reoccur. Out of scope for this fix; would be a small follow-up PR.
