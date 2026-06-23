# Worklog: Grafana Dashboard Job Labels — Release-Portable via Helm-Time Substitution

**Date:** 2026-06-23
**Session:** Diagnose why the operational + billing Grafana dashboards still showed "No data" for most panels after PR #356's metrics-wiring deploy. Root cause: stale URL parameters left the `controller_job` template variable empty; the variable's `refresh: 2` (refresh on time range change only) never re-resolved on dashboard load. Initial fix attempt removed the template variables and hardcoded job names — caught in PR #374 review as a regression of worklog 0508 (release-name portability). Reworked to use Helm-time placeholder substitution (matches the existing `prometheus-rules.yaml` pattern).
**Status:** Complete. All controller-side panels resolve to data on the live cluster, and the dashboards are portable across release names.

---

## Bug Report

> "Availability, active workspaces, suspended workspaces, safe mode, legacy apis, workspace create duration and resume duration, DB metrics, sse events, noisy neighbors, controller and recovery, and metering should all have data"

Dashboard URL the user opened:
```
https://grafana.thekao.cloud/d/llmsafespace-operational/llmsafespace-operational?...&var-job=llmsafespace-api&var-controller_job=&...
```

Note the empty `var-controller_job=` parameter.

## Root Cause Analysis

### Step 1: confirm metrics are in Prometheus

Direct queries with the right job labels returned data for almost every panel:

| Query | Result |
|---|---|
| `sum(llmsafespaces_workspaces_running{job=~"llmsafespace-controller.*"})` | 9 series ✓ |
| `llmsafespaces_workspace_safe_mode_active{job=~"llmsafespace-controller.*"}` | 1 series ✓ |
| `sum(llmsafespaces_db_pool_active_connections{job=~"llmsafespace-api.*"})` | 4 ✓ |
| `histogram_quantile(0.95, sum(rate(llmsafespaces_db_query_duration_seconds_bucket{job=~"llmsafespace-api.*"}[5m])) by (le, operation))` | 5 series ✓ |
| `sum(rate(llmsafespaces_metering_events_recorded_total{job=~"llmsafespace-api.*"}[5m]))` | 1 series ✓ |

Data was flowing. The dashboard wasn't displaying it.

### Step 2: the `controller_job` variable

The dashboard expected `controller_job` to resolve via `label_values(llmsafespaces_reconciliation_duration_seconds_bucket, job)`. With the URL setting `var-controller_job=` empty:

- The variable resolved to literal empty string
- Every panel using `{job=~"$controller_job"}` matched zero series
- `refresh: 2` ("refresh on time range change only") meant visiting the URL did NOT re-resolve

The user confirmed: opening the `controller_job` dropdown showed an empty options list, and typing values did nothing. The variable was dead-on-arrival from the URL.

### Step 3: why have the variable at all

Cluster-wide there's only ever one valid value for each:
- `job` = `llmsafespace-api`
- `controller_job` = `llmsafespace-controller-metrics`

Even with HA controller replicas (2 pods), both share the same Prometheus `job` label. The variables provided no operational benefit and were a constant footgun for stale URLs.

### Step 4: the regression risk (caught in PR #374 review)

A first attempt removed the variables and hardcoded the literal job names into every PromQL expression. The reviewer correctly flagged this as reintroducing the failure mode of worklog 0508:

- Helm release names vary (e.g. `llmsafespace` singular vs `llmsafespaces` plural)
- ServiceMonitor `job` labels are derived from the release name via `<fullname>-api` / `<fullname>-controller-metrics`
- Hardcoded `job="llmsafespace-api"` only works for releases named `llmsafespace`; any rename or reinstall under a different name leaves every panel empty
- The existing `TestMonitoring_DashboardJobVariablesPortable` test was written specifically to prevent this regression, but with the variables removed, it passed only vacuously
- The chart's `prometheus-rules.yaml` already templates the release name correctly via `{{ include "llmsafespaces.fullname" . }}-api.*`; dashboards now diverged from alerts

## Fix

Two-layer redesign that's both portable AND immune to stale URL params:

1. **Dashboard JSON files contain placeholder strings** (`__LLMSAFESPACES_API_JOB__`, `__LLMSAFESPACES_CTRL_JOB__`) instead of either hardcoded job names or template variables.
2. **`dashboards-configmap.yaml` substitutes the placeholders at chart-render time** using the Helm `replace` pipeline:

```yaml
{{- $apiJob := printf "%s-api.*" (include "llmsafespaces.fullname" .) -}}
{{- $ctrlJob := printf "%s-controller.*" (include "llmsafespaces.fullname" .) -}}
data:
  {{- range $path, $_ := .Files.Glob "dashboards/**.json" }}
  {{ base $path }}: |-
{{ $.Files.Get $path | replace "__LLMSAFESPACES_API_JOB__" $apiJob | replace "__LLMSAFESPACES_CTRL_JOB__" $ctrlJob | indent 4 }}
  {{- end }}
```

For a release named `acme`, this produces `job=~"acme-api.*"` / `job=~"acme-controller.*"` matchers. The dashboards work in any release without manual editing.

The `replace` pipeline is preferred over Helm's `tpl` because the dashboard JSON contains hundreds of Grafana legend templates like `{{model_id}}`, `{{tier}}`, `{{outcome}}`. `tpl` would try to evaluate those as Helm template directives and fail. Simple string substitution avoids the entire syntax-clash problem.

## Why not reintroduce template variables with `refresh: 1`?

That was an option, but it has two weaknesses the chosen design avoids:

- Adding `refresh: 1` only fixes the immediate symptom (variable doesn't re-resolve on dashboard load). It still leaves a footgun — if Grafana or a user saves the dashboard with a stale value selected, every visit after that loads the stale value first and only re-resolves AFTER the panels query, leaving a flicker of "No data" on slow connections.
- Template variables for single-valued labels add UI complexity with zero operational benefit. Operators see a dropdown labeled "Controller Job" with one option in it.

The chosen design eliminates both the indirection and the failure mode entirely.

## Tests

`TestMonitoring_DashboardJobVariablesPortable` was rewritten to enforce three new contracts on the rendered ConfigMap:

1. **No leftover placeholders** — every `__LLMSAFESPACES_*_JOB__` must be substituted; an unrendered placeholder is a regression.
2. **Job matchers contain the release-derived prefix** — proves the substitution is wired and uses the release name (not a static string).
3. **No singular `llmsafespace-api`/`llmsafespace-controller`** literals remain — that pattern was the failure mode of worklog 0508 and is the canonical regression marker.

The new test asserts the post-rendering shape rather than the pre-rendering JSON, so it catches both static-string regressions AND wiring-failure regressions where the placeholder is left unsubstituted.

## Validation

### Backend
- `go test ./charts/llmsafespaces/...` — pass (45.4s)
- `helm template release-test charts/llmsafespaces --set monitoring.enabled=true --set monitoring.dashboards.enabled=true` — produces `job=~"release-test-llmsafespaces-api.*"` and `job=~"release-test-llmsafespaces-controller.*"` matchers in both ConfigMap entries
- 0 unrendered `__LLMSAFESPACES_*_JOB__` placeholders in helm output
- 47 occurrences of `release-test-llmsafespaces-api.*` and 27 of `release-test-llmsafespaces-controller.*` (matches the 49 placeholders distributed across both files)

### Live cluster
Tested representative queries against `https://grafana.thekao.cloud`'s Prometheus (port-forwarded):

| Query | Result |
|---|---|
| `sum(llmsafespaces_workspaces_running{job=~"llmsafespace-controller.*"})` | 9 series ✓ |
| `sum(llmsafespaces_workspaces_suspended_total{job=~"llmsafespace-api.*"})` | 0 series (correct — none suspended) |
| `sum(llmsafespaces_db_pool_active_connections{job=~"llmsafespace-api.*"})` | 4 ✓ |
| `llmsafespaces_workspace_safe_mode_active{job=~"llmsafespace-controller.*"}` | 0 (correct — none in safe mode) |

The `.*` regex in the `=~` matcher correctly matches both `llmsafespace-api` and `llmsafespace-controller-metrics`. After the new ConfigMap deploys, the user's dashboard should populate every panel that has metric series, regardless of the URL params.

## Key Decisions

1. **Removed template variables instead of fixing `refresh: 2 → 1`.** The latter would fix the immediate symptom but leave the footgun. Removal eliminates the entire failure mode.
2. **Helm `replace` pipeline instead of `tpl`.** `tpl` would clash with the hundreds of Grafana legend templates in the dashboard JSON. Simple string substitution avoids that.
3. **`=~` regex matcher with `.*` suffix** matches the pattern in `prometheus-rules.yaml`. Tolerates Service-name suffixes (e.g. headless variants) and is consistent with alerts.
4. **Test updated to assert post-rendering shape** — catches both static-string regressions and wiring failures.
5. **Did not add `or vector(0)` to silence "No data" panels.** Counters with no observations are correct Prometheus semantics. Adding `or vector(0)` everywhere would mask real "is this metric being scraped?" outages.

## Adversarial Self-Review

1. *What if the chart name changes from `llmsafespaces` to something else?* The placeholder substitution uses `include "llmsafespaces.fullname" .` which is the standard Helm `<release>-<chart>` template. If the chart is renamed, the resulting prefix changes too. The test's `releasePrefix := strings.TrimSuffix(cmName, "-grafana-dashboards")` derives the prefix from the rendered ConfigMap name itself, so it stays correct under any chart rename.
2. *What if a future PR adds a new placeholder that the substitution chain doesn't know about?* The test's "no leftover placeholders" check catches `__LLMSAFESPACES_API_JOB__` and `__LLMSAFESPACES_CTRL_JOB__`. New placeholders would need their own test coverage AND a new `replace` pipeline entry.
3. *What if a user pastes literal `__LLMSAFESPACES_API_JOB__` into a dashboard JSON manually as a string?* The `replace` runs unconditionally on every chart render, so user-pasted occurrences would also be substituted. For the placeholder strings I picked, that's the desired behavior.
4. *Did I miss any references?* `helm template` produces 0 unrendered placeholders. The pre-render JSON files contain 31+12+18+0 = 61 placeholders; the rendered ConfigMap contains 47+27 = 74 occurrences of the substituted patterns (the difference is the substitution string is longer than the placeholder, but each placeholder maps to exactly one substituted occurrence — verified in dashboard JSON tests).
5. *Will Grafana's sidecar pick up the new ConfigMap?* Yes — already validated for previous redeploys. ~30s for the watch to fire.

All findings either mitigated or false alarms. No remediation needed.

---

## Next Steps

After PR merge:
1. CI builds new chart (no image needed — only ConfigMap content changes)
2. `helm upgrade --reuse-values` pushes the new dashboard JSON into the `llmsafespace-grafana-dashboards` ConfigMap
3. Grafana sidecar picks up the change within 30s
4. User does a hard refresh (`Ctrl+Shift+R`) to clear browser-cached panel rendering

## Files Modified

- `charts/llmsafespaces/dashboards/operational.json` — replaced `$controller_job` and `$job` template variable references with `__LLMSAFESPACES_*_JOB__` placeholders; removed the variable definitions from `templating.list`
- `charts/llmsafespaces/dashboards/billing.json` — same treatment for `$job`
- `charts/llmsafespaces/templates/dashboards-configmap.yaml` — added `replace` pipeline for placeholder substitution; defined `$apiJob` / `$ctrlJob` from `include "llmsafespaces.fullname" .` matching the prometheus-rules.yaml pattern
- `charts/llmsafespaces/chart_test.go` — rewrote `TestMonitoring_DashboardJobVariablesPortable` to enforce three new contracts (no leftover placeholders, release-derived prefix present, no singular `llmsafespace-` regression marker); removed dead `toString` helper
- `worklogs/NNNN_2026-06-23_grafana-dashboard-job-vars.md` — this file
