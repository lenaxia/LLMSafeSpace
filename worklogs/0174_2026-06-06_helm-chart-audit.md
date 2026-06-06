# Worklog 0174 — Helm Chart Audit: 7 Bugs Found and Fixed

**Date:** 2026-06-06
**Agent:** opencode
**PR:** https://github.com/lenaxia/LLMSafeSpace/pull/50

---

## Summary

Full audit of `charts/llmsafespace/` found 7 real bugs — 3 high, 1 medium, 3 low — plus identified 4 false positives. All bugs fixed, regression tests added to `chart_test.go`, and MCP security context moved from hardcoded template values to `values.yaml` to match the API/controller pattern.

The session also diagnosed and fixed a live cluster incident: `mike@kao.family` could not log in at `chat.safespaces.dev`. Root cause was a missing `/api` ingress route for the `additionalHosts` entry — the same issue as F5 below.

---

## Findings

### F1 — High: MCP resources in wrong namespace

**Files:** `mcp-deployment.yaml:6`, `mcp-service.yaml:6`

Both used `{{ .Values.namespace.name }}` — an undefined key that evaluates to `""`. Kubernetes defaults an empty namespace to `default`. On any non-`default` install the MCP Deployment and Service landed in `default` regardless of `--namespace`.

**Fix:** Replaced with `{{ .Release.Namespace }}`.

---

### F2 — High: MCP liveness probe hangs; readiness probe missing

**File:** `mcp-deployment.yaml:43`

The liveness probe used `httpGet: path: /sse` — the SSE stream endpoint. The kubelet's HTTP client opened the connection and waited for a response that never came (SSE is a long-lived push stream), causing the probe to time out and restart the pod repeatedly. There was no readiness probe, so the pod was added to Service endpoints immediately on start.

The `mcp-go` SSEServer only registers `/sse` and `/message` — no health endpoint exists.

**Fix:** Replaced both probes with `tcpSocket` on the MCP port, which confirms the listener is up without initiating an HTTP exchange.

---

### F3 — High: MCP pod rejected by Pod Security Admission

**File:** `mcp-deployment.yaml` (entire spec)

No `podSecurityContext` or `containerSecurityContext` was defined. The chart's own default namespace carries `podSecurityEnforce: restricted`. PSA rejected the pod at admission — it never started.

**Fix:** Added both security contexts matching the API/controller posture: `runAsNonRoot: true`, `runAsUser/Group: 65532`, `seccompProfile: RuntimeDefault`, `readOnlyRootFilesystem: true`, `capabilities.drop: [ALL]`. Added a `/tmp` emptyDir for any runtime temp writes.

As a follow-on (raised in code review), moved the hardcoded values into `values.yaml` under `mcp.podSecurityContext` and `mcp.containerSecurityContext` so operators can override them without forking the template.

---

### F4 — Medium: Frontend container writable root filesystem

**File:** `frontend-deployment.yaml:37`

`readOnlyRootFilesystem: false` was set explicitly — the only container in the chart to opt out. nginx only requires three writable paths: `/var/cache/nginx`, `/var/run`, and `/tmp`.

**Fix:** Added three `emptyDir` volumeMounts for those paths and set `readOnlyRootFilesystem: true`.

---

### F5 — Medium: `additionalHosts` ingress missing `/api` route

**File:** `frontend-ingress.yaml:36-47`

The `additionalHosts` range loop only generated `path: /` → frontend. The `path: /api` → API route present on the primary host was absent. Any additional hostname returned 502 for all API calls because nginx explicitly `return 502` for `/api/` requests that reach it (with the comment "this means ingress routing is misconfigured").

This caused the live incident: `chat.safespaces.dev` (an `additionalHosts` entry in the deployed values) could not reach the login endpoint. The cluster was patched manually, then the chart fixed.

**Fix:** Added the `/api` → `llmsafespace-api:8080` path to the `additionalHosts` loop, making it identical in structure to the primary host rule.

---

### F6 — Low: `additionalHosts` comment stale after F5

**File:** `values.yaml:587`

The comment read: *"additional hostnames for the frontend (frontend-only, no /api path)"* — documenting the bug as intentional behaviour.

**Fix:** Updated comment to accurately describe the fixed behaviour.

---

### F7 — Low: Dead helper with wrong fallback logic

**File:** `_helpers.tpl:129-133`

`llmsafespace.migrations.image` was defined but never called by any template (verified with `git grep`). Its fallback logic was also wrong: it fell back to `api.image.repository` instead of `migrate/migrate`, so if anyone had used it, they would have tried to run the API binary as the migration tool.

**Fix:** Removed the helper entirely.

---

### F8 — Low: Valkey network policy missing migrate Job selector

**File:** `datastore-network-policy.yaml:88-109`

The Postgres ingress policy correctly allowed both the API pod and the migration Job (`app.kubernetes.io/component: migrate`). The Valkey policy only allowed the API pod. Any future migration needing to flush Redis keys (e.g. session invalidation during a schema change) would be silently blocked.

**Fix:** Added the migrate Job `podSelector` to the Valkey ingress rule, symmetric with the Postgres policy.

---

## False Positives

- **migrations-configmap.yaml** path `migrations/*.sql` — Helm resolves relative to chart root; correct.
- **secret.yaml** fresh random on `helm template` — documented intentional behaviour; cluster `lookup` persists across upgrades.
- **webhook-cert.yaml** RSA 2048 — within current security standards; not a finding.
- **validating-webhook.yaml** `failurePolicy: Fail` — intentional, configurable, documented tradeoff.

---

## Tests Added

Seven regression tests added to `chart_test.go`:

- `TestF1_MCPResourcesUseReleaseNamespace` — namespace == test-ns for both Deployment and Service
- `TestF2_MCPProbesAreTCPSocket` — liveness/readiness are tcpSocket, not httpGet
- `TestF3_MCPSecurityContext` — runAsNonRoot, RuntimeDefault seccomp, readOnlyRootFilesystem, drop ALL
- `TestF4_FrontendReadOnlyRootFilesystem` — readOnlyRootFilesystem=true + nginx-cache/nginx-run/tmp emptyDirs
- `TestF5_AdditionalHostsHaveAPIPath` — extra host rule contains both /api and / paths
- `TestF8_ValkeyPolicyAllowsMigrateJob` — migrate component podSelector present in Valkey ingress rules
