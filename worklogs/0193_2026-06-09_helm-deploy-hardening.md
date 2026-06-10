# 0192 — Helm deploy hardening: values.local.yaml, ingress, RBAC

**Date:** 2026-06-09

## Problem

Three distinct failure modes were discovered when deploying PR #72 (React
error #310 fix) to the production cluster:

1. **`helm upgrade` silently drops cluster-specific overrides** — `make
   helm-deploy` does not pass `--reuse-values`, so every upgrade resets
   all values to chart defaults. Two previously-overridden values were
   lost: `redis.host=valkey` (chart default: `redis-master`, doesn't
   exist in this cluster) and `mcp.enabled=false` (chart default: `true`,
   no MCP image published). The API crashed on startup with
   `dial tcp: lookup redis-master ... no such host`.

2. **Frontend ingress not created** — `frontend.enabled` and
   `frontend.ingress.enabled` both default to `false` in the chart. The
   prior working revision (181) had both set to `true` via `--set` flags
   that weren't preserved. The ingress was absent so
   `safespace.thekao.cloud` returned 404 for all routes.

3. **Controller CrashLoopBackOff after upgrade** — `rbac.scope` defaults
   to `namespace` (tightened in Epic 17 G5). But controller-runtime builds
   cluster-wide informer caches regardless of RBAC scope — the reflectors
   call `apiserver.list(secrets, all-namespaces)` and the apiserver denies
   it. The fix is `rbac.scope=cluster`, which adds read-only (get/list/watch)
   for pods/secrets/networkpolicies at cluster scope; CRUD remains
   namespace-scoped via the Role. Additionally the webhook's `failurePolicy`
   was `Fail`, so the crashing controller's webhook blocked subsequent
   `helm upgrade` calls in a chicken-and-egg loop. Resolved by temporarily
   patching `failurePolicy: Ignore` then redeploying.

## Fix

### `charts/llmsafespace/values.local.yaml` (new, gitignored)

Persistent file for cluster-specific overrides. Captures all values that
differ from chart defaults in this deployment:

- `redis.host: valkey`
- `mcp.enabled: false`
- `frontend.enabled: true` + ingress config (traefik, letsencrypt, `safespace.thekao.cloud`)
- `rbac.scope: cluster`

### `Makefile` — `helm-deploy` target

Auto-includes `values.local.yaml` via `-f` flag when the file exists
(`$(wildcard ...)`). The `-f` flag comes before `--set IMAGE_TAG` so image
tags still take precedence. Future deploys require no extra flags:

```
make helm-deploy IMAGE_TAG=<tag> RELEASE_NS=default
```

### `.gitignore`

Added `charts/llmsafespace/values.local.yaml` so cluster credentials
and topology never get committed.

## Outcome

All components healthy post-deploy:

- API (×2): Running, `ts-1781025953`
- Controller: Running, `ts-1781025953`, no RBAC errors
- Frontend: Running, `ts-1781025953`
- Ingress: `safespace.thekao.cloud` → 192.168.5.12, TLS issued by letsencrypt
- `GET /api/v1/auth/config` → 200 ✓

## Assumptions validated

- `rbac.scope=cluster` only adds read-only cluster-wide watches; CRUD on
  pods/secrets remains namespace-scoped — verified by reading rbac.yaml
  ClusterRole rules (get/list/watch only, no create/update/delete).
- `values.local.yaml` is applied before `--set` flags so image tag
  overrides always win — verified via `make -n helm-deploy` dry-run.
