# Worklog: Fix Workspace Pod Liveness/Readiness Probes

**Date:** 2026-05-25
**Session:** Diagnose and fix workspace pods stuck in CrashLoopBackOff due to HTTP health probe 401s from opencode 1.2.27.
**Status:** Complete

---

## Objective

All 8 workspace pods were in `CrashLoopBackOff` or `Running` but not Ready, causing "workspace connection failed" errors when users attempted to interact with sessions. Workspaces showed as green (Active) in the UI because the DB phase was correct, but the underlying pods were unreachable.

---

## Assumptions Validated Before Fix

1. **`connection refused` is from the proxy hitting a dead pod** — confirmed via API logs: `dial tcp 10.69.6.18:4096: connect: connection refused` and `dial tcp 10.69.6.16:4096: connect: connection refused`. The proxy had valid cached pod IPs but the pods were not accepting connections.

2. **The pods were crashing before our deployment** — confirmed by pod describe showing `225 restarts` and `Age: 16h` at time of investigation. Our deployment landed at 23:17 UTC; pods had been cycling since 06:31 UTC that morning. Not a regression.

3. **The crash cause is the liveness probe returning 401** — confirmed by:
   - `kubectl exec ... -- curl http://localhost:4096/global/health` → `401 Unauthorized` with `WWW-Authenticate: Basic realm="Secure Area"`
   - Controller describe: `Killing: Container workspace failed liveness probe, will be restarted` (221 events over 16h)
   - opencode 1.2.27 requires Basic auth on all HTTP endpoints including `/global/health`

4. **The controller set HTTP probes** — confirmed at `controller/internal/workspace/controller.go:581-593`: both readiness and liveness probes used `HTTPGet { Path: "/global/health", Port: 4096 }` with no auth headers (HTTP probes cannot carry auth in Kubernetes).

5. **Workspace CRDs in `Failed` phase block controller reconciliation** — confirmed by controller logs: `"Workspace in Failed phase; manual intervention required"` for all 8 workspaces. The controller refused to recreate pods until phase was manually reset.

6. **kubectl patch without status subresource cannot update `.status`** — confirmed; used `kubectl proxy` + direct API call to `/apis/llmsafespace.dev/v1/namespaces/default/workspaces/:id/status` with merge-patch to reset phase to Active.

---

## Work Completed

**Modified: `controller/internal/workspace/controller.go`**
- Replaced both `ReadinessProbe` and `LivenessProbe` `HTTPGet` handlers with `TCPSocket` handlers on port 4096
- TCP probes verify the port is listening without requiring authentication — sufficient to confirm the opencode process is alive and accepting connections
- Retained identical timing parameters: readiness `initialDelay=5s, period=10s, timeout=3s, failureThreshold=3`; liveness `initialDelay=15s, period=30s, timeout=5s, failureThreshold=3`

**Manual cluster remediation:**
- Deployed new controller image `sha-5badcf7` via `helm upgrade` revision 17
- Deleted all 8 crashing workspace pods to force controller reconciliation
- Manually patched all 8 workspace CRD statuses from `Failed` → `Active` via `kubectl proxy` API call (required because `kubectl patch` cannot update status subresource directly in this kubectl version)
- Controller recreated all 8 pods with TCP probes; all reached `1/1 Running` with 0 restarts within 45 seconds

---

## Key Decisions

**TCP probe over HTTP probe with credentials.** The alternative of passing the workspace password as a Basic auth header in the probe was considered but rejected: Kubernetes HTTP probes do not support `Authorization` headers (the `HTTPGetAction` has no credential field). Even if we injected the password via an exec probe, it would add complexity and a secret exposure surface. TCP is the correct tool — it verifies liveness at the transport layer, which is all we need to know that opencode is running and not deadlocked.

**Why this was not caught earlier.** opencode 1.1.x did not require auth on `/global/health`. The change to require auth on all endpoints was introduced in a later version. The runtime image used by these workspace pods (`sha-be91d77`) bakes opencode 1.2.27, which enforces auth universally.

---

## Blockers

None.

---

## Tests Run

- `go test ./controller/...` — all pass (no test changes required; the probe type change is structural, not logic)

---

## Files Modified

- `controller/internal/workspace/controller.go`
- `worklogs/0043_2026-05-25_fix-workspace-pod-probes.md` (this file)
