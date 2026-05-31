# 0110 — Epic 17 Phase C/G7: F1.4.2 agentd admin port auth

**Date:** 2026-05-31
**Author:** mikekao + opencode (sonnet)
**Phase:** Epic 17 Phase C, F1.4.2
**Status:** Code-level fix complete; awaiting CI build + live cluster re-pentest

---

## Summary

Closes the High-severity **F1.4.2**: agentd's admin port (4098)
served `/v1/healthz`, `/v1/readyz`, and `/v1/statusz` without
authentication. The chart-wide G16 NetPol restricts ingress to the
API/controller pods, but a misconfigured cluster (NetPol disabled,
CNI bug, operator opted out) would let any pod with route to the
workspace pod IP probe the session list and provider config of any
other workspace.

Fix:
- `requireBearerToken(token, handler)` middleware in
  cmd/workspace-agentd/main.go.
- `/v1/statusz` and `/v1/readyz` are wrapped when
  `AGENTD_ADMIN_TOKEN` is set in the env (operator-controlled).
- `/v1/healthz` stays unauthenticated — it only emits process
  liveness `{ok, started_at}`, kubelet's liveness probe targets it,
  and there's no leaked info.
- Per-workspace token = the workspace's existing `password` Secret
  value. Reuses infrastructure rather than introducing a new Secret.
- Controller injects `AGENTD_ADMIN_TOKEN` env var via SecretKeyRef.
- Controller's `/v1/statusz` poll reads the same Secret and sends
  `Authorization: Bearer ...`.
- ReadinessProbe HTTPGet now carries the literal Bearer header
  (kubelet probes can't read env vars; we materialize the token
  into the probe spec at pod-build time).

---

## Stated assumptions (validated)

- **A1** — kubelet probes use the pod IP, not loopback. (k8s docs;
  also validated by the existing pod template binding 0.0.0.0:4098.)
- **A2** — kubelet's `httpHeaders` field accepts static
  `{name, value}` pairs but does NOT resolve env vars or Secrets.
  (k8s API ref; tested locally.)
- **A3** — `ensurePasswordSecret` runs in `handlePending` BEFORE
  `buildPod` is reached. (Validated: `controller.go:184` →
  `controller.go:203` ordering.)
- **A4** — The password Secret is per-workspace and randomly
  generated. Using it as the admin token doesn't introduce
  cross-workspace token reuse.

---

## Changes

### agentd

1. `cmd/workspace-agentd/main.go`:
   - **NEW** `requireBearerToken(token, handler)` middleware.
   - `/v1/readyz` and `/v1/statusz` wrapped when env token is set.
   - `/v1/healthz` stays unauthenticated.
   - Reads `AGENTD_ADMIN_TOKEN` from os.Getenv at startup.

2. `cmd/workspace-agentd/admin_token_test.go` (NEW):
   - 4 tests: rejects unauthenticated, rejects wrong tokens,
     accepts correct, empty-token-is-bypass.

### Controller

3. `controller/internal/workspace/controller.go`:
   - `buildPod` reads the password Secret at pod-build time.
   - Adds `AGENTD_ADMIN_TOKEN` env var to the workspace container
     via SecretKeyRef.
   - ReadinessProbe HTTPGet carries `Authorization: Bearer <token>`
     literal header (since kubelet can't resolve env vars).
   - `enrichAgentStatus` reads the same password Secret on every
     poll and sends `Authorization: Bearer ...` to /v1/statusz.

---

## Tests

| Command | Result |
|---|---|
| `go test -count=1 -timeout 30s -run TestG7_F142 ./cmd/workspace-agentd/...` | PASS (4/4) |
| `go test -count=1 -timeout 120s ./cmd/workspace-agentd/... ./controller/...` | PASS |
| `go build ./...` | clean |

---

## Live re-pentest plan

After CI ships new agentd + controller images:

1. `helm upgrade llmsafespace ./charts/llmsafespace -n default --reuse-values`.
2. Wait for workspace pod recreation.
3. **Bypass attempt:**
   ```
   kubectl run probe --rm -it --image=curlimages/curl -- \
     sh -c 'curl -v http://<workspace-pod-IP>:4098/v1/statusz'
   ```
   Must respond **401 Unauthorized** with `WWW-Authenticate: Bearer realm="agentd"`.
4. **Authenticated probe:**
   ```
   PW=$(kubectl get secret workspace-pw-<ws> -o jsonpath='{.data.password}' | base64 -d)
   curl -H "Authorization: Bearer $PW" http://<pod-IP>:4098/v1/statusz
   ```
   Must respond 200 with the StatuszResponse JSON.
5. **Kubelet probe** still works: `kubectl describe pod <ws>` shows
   `Readiness: Ready` (probe spec carries the Authorization header).

---

## Tracker update

`MASTER-TRACKER.md`:
- F1.4.2 → MINE / live-pending

---

## Next finding

Phase C/G8 — F1.7.5 (JWT signing-key rotation).
