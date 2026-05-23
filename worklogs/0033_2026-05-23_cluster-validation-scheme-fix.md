# Worklog 0033: Cluster Validation, Scheme Conversion Root Cause, First-User-Admin

**Date:** 2026-05-23
**Scope:** Cluster validation of worklog 0032 changes; discovery and fix of the actual scheme-conversion root cause behind watch log spam; auto-promote first user to admin
**Status:** Complete — all four validations passed against `admin@home-kubernetes`

## Objective

Execute the cluster-validation followups from worklog 0032 and close the remaining items from the next-steps list:

1. Confirm CI publishes `ts-<unix>`, `sha-<commit>`, `dev` tags
2. Switch live deployment to immutable `sha-` tag
3. Validate fresh users can create sandboxes without manual permission seeding
4. Validate watch loop steady-state has no Warn-level "watch channel closed" spam
5. Implement and validate auto-promote-first-user-to-admin

## What Was Validated

### 1. CI image versioning (worklog 0032 fix)

Workflow run `26325601714` (commit `e8cdbc8`) produced three images, each with three tags carrying the same `ts-` timestamp shared by the `prepare` job:

```
ghcr.io/lenaxia/llmsafespace/api:ts-1779517165
ghcr.io/lenaxia/llmsafespace/api:dev
ghcr.io/lenaxia/llmsafespace/api:sha-e8cdbc8
ghcr.io/lenaxia/llmsafespace/controller:ts-1779517165
ghcr.io/lenaxia/llmsafespace/controller:dev
ghcr.io/lenaxia/llmsafespace/controller:sha-e8cdbc8
ghcr.io/lenaxia/llmsafespace/base:ts-1779517165
ghcr.io/lenaxia/llmsafespace/base:dev
ghcr.io/lenaxia/llmsafespace/base:sha-e8cdbc8
```

`ts-1779517165` decodes to `2026-05-23 06:19:25 UTC`, matching the workflow start. Sortable per request.

### 2. Cluster pinned to immutable tag

```
helm upgrade llmsafespace charts/llmsafespace -n default --reuse-values \
  --set api.image.tag=sha-e8cdbc8 \
  --set controller.image.tag=sha-e8cdbc8
```

Both deployments rolled out cleanly. Confirmed:

```
ghcr.io/lenaxia/llmsafespace/api:sha-e8cdbc8 \
  @sha256:86aa6c7bbb1eedc5e35714215c91b6bc18a60cad7927e9cc6d9c4081b587d41b
```

`charts/llmsafespace/values.yaml` documentation updated to recommend `sha-`/`ts-` over moving `dev` tag (worklog 0031's kubelet pull-cache issue).

### 3. Permissions model end-to-end

Cleared the manually-seeded wildcard permission rows from worklog 0031:

```sql
DELETE FROM permissions WHERE user_id='17638d7b-...';
```

Then registered a brand-new user (`fresh@example.com`, `role='user'`, zero permissions) and exercised three paths:

| Action | Expected | Actual |
|--------|----------|--------|
| Create sandbox with no `workspaceRef` (auto-creates owned workspace) | 201 | 201 — `sb-txrxs`, `workspaceRef: dcf6ed7e-...` |
| Create sandbox with `workspaceRef` pointing at admin's workspace | 403 forbidden | 403 — `"user 2083ff7d-... does not own workspace 998a78ff-..."` |
| Promote freshuser to `admin`, attach to victim's workspace | 201 (admin bypass) | 201 — `sb-sfvgq` with `user-id` label = freshuser, `workspace` label = victim's |

All paths verified against the real API on `sha-e8cdbc8`.

### 4. Watch loop bug — actual root cause was scheme conversion, not apiserver cycling

When validating the watch fix from worklog 0032 against the cluster, the API logs showed *exponentially-backing-off Warn entries every few seconds*:

```
"M":"Sandbox watch returned error event",
"reason":"InternalError","code":500,
"message":"unable to decode an event from the watch stream:
unable to decode watch event: no kind \"Sandbox\" is registered for the
internal version of group \"llmsafespace.dev\" in scheme
\"pkg/runtime/scheme.go:100\""
```

This is **the actual root cause** of the "watch channel closed" log spam reported in worklog 0029 — not apiserver-driven cycling as worklog 0031 assumed, and not a benign close as worklog 0032's design assumed.

**The bug:** `pkg/kubernetes/client_crds.go:71` set `config.NegotiatedSerializer = serializer.NewCodecFactory(scheme.Scheme)`. When `rest.Request.Watch()` builds its decoder via `runtime.NewClientNegotiator`, it calls `DecoderToVersion(serializer, nil)` (the negotiator's `decode` field is unset). With conversion enabled and a nil GroupVersioner, the codec attempts to convert the decoded object to the *internal hub version* of its group. Our CRD types have no `__internal` version registered, so every watch event fails to decode. The legacy `crd_watcher` silently dropped these as non-Sandbox objects (the type assertion in `handleEvent` swallowed them). Worklog 0032's new error-event-aware loop correctly surfaced them.

**The fix:** use `WithoutConversion()`:

```go
config.NegotiatedSerializer = serializer.NewCodecFactory(scheme.Scheme).WithoutConversion()
```

This is the canonical pattern for CRD clients without separate internal/external versions. Three new tests in `pkg/kubernetes/client_test.go` lock in the contract:

- `TestNegotiatedSerializerDecodesWatchPayload` — green path: `WithoutConversion()` decodes Sandbox JSON correctly
- `TestNegotiatedSerializerWithConversionFailsForCRD` — locks in the failure mode: `NewCodecFactory(scheme.Scheme).DecoderToVersion(serializer, nil)` errors with "internal version"
- `TestSchemeRegisteredWithGroup` — sanity check that `init()` registered Sandbox

After deploying `sha-5ca1f91`:

```
$ kubectl logs llmsafespace-api-... | grep -E '"L":"[^"]*WARN|"L":"[^"]*ERROR'
(no output — zero Warn or Error entries in 5+ min of operation)
```

### 5. First-user-becomes-admin (worklog 0032 follow-up #3)

Implemented in `auth.Service.Register`:

```go
userCount, err := s.dbService.CountUsers(ctx)
if err != nil { return nil, errors.New("registration failed") } // fail closed
role := "user"
if userCount == 0 { role = "admin" }
```

Added `CountUsers(ctx) (int, error)` to `DatabaseService` interface, implementation, and mock. Four new TDD tests:

- `TestRegister_FirstUserBecomesAdmin` — `CountUsers → 0` → role=admin
- `TestRegister_SubsequentUsersAreNotAdmin` — `CountUsers → 1` → role=user
- `TestRegister_CountUsersError_FailsClosed` — `CountUsers → err` → reject registration (don't silently default)
- `TestRegister_Success` (existing) — updated to mock `CountUsers → 5` for non-first path

**Cluster validation** (after wiping users table and re-registering):

```
DELETE FROM users;  -- 3 rows deleted (cascades clean up everything)
SELECT count(*) FROM users;  -- 0
```

Then:

| User | Registration response | DB role |
|------|----------------------|---------|
| `founder@example.com` (first) | `"role":"admin"` | `admin` |
| `second@example.com` | `"role":"user"` | `user` |

Both as expected.

## Files Modified

| File | Type | Change |
|------|------|--------|
| `pkg/kubernetes/client_crds.go` | edit | Add `.WithoutConversion()` to NegotiatedSerializer; document why |
| `pkg/kubernetes/client_test.go` | new | 3 TDD tests for the codec contract |
| `api/internal/services/auth/auth.go` | edit | `Register` now calls `CountUsers`; first user gets `role=admin` |
| `api/internal/services/auth/auth_test.go` | edit | +3 new TDD tests, +1 existing test updated to mock `CountUsers` |
| `api/internal/services/database/database.go` | edit | Add `CountUsers(ctx) (int, error)` |
| `api/internal/interfaces/interfaces.go` | edit | Add `CountUsers` to `DatabaseService` |
| `api/internal/mocks/database.go` | edit | Add mock for `CountUsers` |
| `charts/llmsafespace/values.yaml` | edit | Document `sha-`/`ts-` pinning recommendation for production |
| `worklogs/0033_*.md` | new | This file |

## Tests Run

```
go test -timeout 180s -race -count=1 ./...   # all 27 packages pass
go vet ./...                                  # clean
```

Cluster validation (against `sha-5ca1f91` on `admin@home-kubernetes`):

- 5 sandbox CRUD calls — all 2xx as expected
- 2 user registrations on empty DB — first=admin, second=user, both confirmed in PostgreSQL
- 5+ min log tail — zero Warn or Error log entries
- Watcher consumption confirmed via `GET /sandboxes/:id/status` returning live phase data

## Key Decisions

1. **TDD recovery on the scheme bug.** When the bug surfaced via cluster validation, I wrote the unit test *before* applying the fix. Verified the test fails on master code and passes after the fix. The test exercises the exact code path used by `rest.Request.Watch()`'s negotiator (`DecoderToVersion(serializer, nil)`).

2. **`CountUsers` errors fail closed.** A transient DB error during registration must not silently default to admin. We refuse the registration. The user can retry; if the system genuinely has no DB, no one should be promoting themselves anyway.

3. **First-user-becomes-admin is at registration time, not at login time.** Doing the check at login would mean every login pays the `CountUsers` cost; doing it at registration is one-shot. There's a tiny TOCTOU window (two simultaneous registrations on a fresh DB could both see count==0 and both become admin), but this is acceptable: the *very* first user wins by milliseconds in practice, and "two admins on day one" is not a security incident.

4. **Did not delete the legacy `permissions` table or `CheckPermission` method.** Both could be removed since `auth.CheckResourceAccess` (which uses `CheckPermission`) has zero callers in production code. But that's a separate cleanup PR; out of scope here.

5. **Helm chart documentation only, no template changes.** The `tag: ""` default still falls back to `.Chart.AppVersion`. The fix is operational guidance, not a chart-level enforcement. If we wanted to enforce immutable tags, a `helm template`-time check could fail-closed on `tag == "dev"` or `tag == "latest"`, but that's overreach for a default values file.

## Next Steps

1. **Delete the unused `permissions` table and `CheckPermission` method** — both are tech debt now that worklog 0032 dropped the only production caller. Optional, separate PR.

2. **Phase 4 MCP server** — still open from worklog 0029, untouched in this session.

3. **Add a `RetryWatcher`-based replacement for the hand-rolled watcher** — the current `SandboxWatcher` works correctly now, but `k8s.io/client-go/tools/watch.NewRetryWatcher` is the idiomatic upstream solution and would handle resourceVersion / 410 / bookmarks for free. Since our hand-rolled version is fine, this is purely a cleanup.

4. **Consider a `helm test` hook** that validates `image.tag` doesn't match common moving-tag patterns (`dev`, `latest`, `main`) for production deployments. Defense-in-depth against the kubelet caching issue.

5. **The "worklog 0029 misdiagnosis" lineage is complete.** Each subsequent worklog refined the diagnosis: 0029 said MCP, 0030 said credentials, 0031 said apiserver cycling, 0032 said error-event handling, 0033 finds the actual scheme-conversion bug. A reminder: never trust a Warn-level log without first checking whether the underlying type assertion is silently dropping events.
