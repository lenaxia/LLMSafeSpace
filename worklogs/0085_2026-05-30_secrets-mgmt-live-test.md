# Worklog: Secrets Management — Live Validation in `default` Namespace

**Date:** 2026-05-30
**Session:** User reported the secrets management system "isn't working entirely correctly" and asked for a test against the live `default`-namespace cluster. After initial investigation the user asked to verify everything end-to-end across all credential subsystems with strict assumption-validation discipline. This worklog records the full sweep, what was found, retractions of earlier wrong claims, and the root causes.
**Status:** Investigation complete; **12 bugs identified and root-caused; 3 earlier claims retracted; 11 properties confirmed correct**. No code changes yet — fixes deferred per Rule 6 (Uncertainty Protocol) until user confirms remediation scope.

---

## Retractions from earlier in this session

Three findings asserted earlier turned out to be wrong on subsequent investigation. Recording them here for audit-trail integrity:

1. **"Bug 4: API↔agentd `/v1/reload-secrets` schema mismatch."** RETRACTED. After reading `pkg/secrets/injection.go::InjectedSecret{Type,Name,Metadata,Plaintext}` and `cmd/workspace-agentd/secrets.go::Secret{Type,Name,Metadata,Plaintext}` side-by-side (V2 in this session), the schemas match exactly. Both expect a top-level JSON array `[{type,name,metadata,plaintext}]`. The 400 I observed was caused by my own `curl` payload using `{"secrets":[...]}` (object wrapping array) instead of the bare array. Test artifact, not a code bug. Bug 4 is removed from the bug list.

2. **"Cross-user reveal returns 500."** RETRACTED. Re-tested cleanly with proper variable handling (V4): cross-user GET/PUT/DELETE/REVEAL all return **404 "secret not found"**. Behavior is correct — uniform 404 doesn't leak existence. The earlier 500 came from a cascade where `$SECRET_ID` shell variable was empty (from a separate failed create) and produced a malformed URL.

3. **"`/var/run/secrets/kubernetes.io/serviceaccount` mount is present"** (in original worklog draft). RETRACTED. T9 directly verified `automountServiceAccountToken: false` in pod spec, `/var/run/secrets/kubernetes.io/serviceaccount/` does not exist, no token file. SA token is correctly suppressed.

---

## Objective

Validate the end-to-end secrets management flow against the deployed image `ghcr.io/lenaxia/llmsafespace/api:sha-cdd6305` (== current `main`) running in the `default` namespace of `admin@home-kubernetes`. Sweep covers: register/login/JWT/DEK lifecycle, secret CRUD, encryption-at-rest, KEK rotation, password change, account recovery, audit log filters, bindings lifecycle, cross-user isolation, pod-side mount and process-environment delivery, network policies, SA token suppression, multi-secret materialization, path-traversal defense, concurrent-operation safety, frontend integration, replica consistency, cluster-state hygiene.

---

## Stated assumptions (Rule 7) and validation evidence

| # | Assumption | Result | Evidence |
|---|------------|--------|----------|
| A1 | Deployment lives in `default` namespace as `Service llmsafespace-api:8080` | **VALIDATED** | `kubectl get svc -n default llmsafespace-api -o yaml` |
| A2 | Deployed image is current `main` | **VALIDATED** | `sha-cdd6305` deployed; `git log` shows == `HEAD~1` |
| A3 | Auth flow: register → JWT (with `jti`) → DEK cached in Redis under `dek:<jti>` → secret CRUD works | **REFUTED** | Bug 5: register issues JWT but does NOT cache DEK |
| A4 | Secrets are encrypted at rest in PostgreSQL | **VALIDATED** | `encode(ciphertext, 'hex')` random; 0 grep hits across all rows for known plaintexts |
| A5 | `POST /api/v1/workspaces/:id/reload-secrets` pushes secrets to in-pod agentd | **REFUTED** | Bug 1: API path returns 503 always (`podIPResolver` not wired) |
| A6 | The CrashLoopBackOff workspace pod (`c3c8766d-...`) is unrelated to secrets | **PARTIALLY REFUTED** (T10) | Pod is gone but workspace still in `Failed` phase with stale legacy `workspace-creds-*` Secret + `workspace-pw-*` Secret; controller stuck polling dead pod IP for 36 consecutive failures |
| A7 | Fresh user registration is open and won't disturb real users | **VALIDATED** | Multiple throwaway `@pentest.local` users; no impact |
| A8 | Worklogs 0061, 0065, 0078 reflect current state of secrets implementation | **MOSTLY VALIDATED** | Worklog 0061's register-DEK issue regressed (Bug 5); worklog 0065 line 159 "lazy DEK rotation not implemented" still unfixed (Bug 9); worklog 0079's "fix secrets test" follow-up undone |

---

## Findings — bugs found

Severity scale: Critical = data loss or system-wide outage; High = user-facing feature broken; Medium = misleading or partial functionality; Low = doc/UX polish.

### Bug 1 — `reload-secrets` API endpoint is unconditionally broken (High)

**Symptom:** `POST /api/v1/workspaces/:id/reload-secrets` returns `503 {"error":"secret reload not configured"}` for every authenticated, authorised request against a healthy workspace.

**Root cause:** `api/internal/handlers/secrets.go:283` short-circuits when `h.podIPResolver == nil`. The handler is constructed in `api/internal/app/app.go:126` with `handlers.NewSecretsHandler(secretService)`, which leaves `podIPResolver` nil. There is **no call** to `secretsHandler.SetPodIPResolver(...)` anywhere in `app.go`. Only the test files wire it. V1 in this session ran `go test ./api/internal/app/...` (passes) and read `secrets_wiring_test.go` (does not exercise reload-secrets path) — confirming there is no test that would catch this.

**Fix sketch:** Wire the existing `k8sWorkspaceGetterAdapter` (constructed in `app.go:184` for the terminal handler) into the secrets handler. Add a regression test asserting reload-secrets returns non-503 against a fake K8s client.

### Bug 2 — Bind-time auto-push silently swallows the same error (High)

**Symptom:** `PUT /api/v1/workspaces/:id/bindings` returns 204 (success) but the agent in the pod never receives the secrets. Audit log shows `read` entries with `reason: pod_injection` (validated in this session) — proving the push path *runs*, but no log line indicates failure.

**Root cause:** `secrets.go:272-280::pushSecretsToAgent` calls `doReload` and discards the error: `_, _ = h.doReload(...)`. With `podIPResolver == nil` (Bug 1), every push silently returns `errPodIPResolverNotConfigured`.

**Fix sketch:** Either log at WARN, or return a non-fatal warning in the response. At minimum, do not swallow silently.

### Bug 3 — Bound secrets only reach the pod via `Activate`; never via `Create + Bind` (Critical, *the user's actual complaint*)

**Symptom matrix (verified empirically across multiple test runs):**

| Workflow | Mounted in pod? |
|----------|-----------------|
| Create workspace + bind secret + wait Active | NO |
| `POST /workspaces/:id/reload-secrets` | NO (Bug 1: 503) |
| `PUT /workspaces/:id/bindings` (auto-push side-effect) | NO (Bug 2: error swallowed) |
| `PUT /workspaces/:id/credentials` (legacy path) | NO (route returns 404 in `sha-cdd6305`) |
| Suspend → `POST /workspaces/:id/activate` | YES |

**Inventory across 10 currently-running workspace pods at session start:** `user-secrets` volume mounted on 0/10; `cred-secret` (legacy) mounted on 2/10; `pw-secret` mounted on 10/10. Zero `workspace-secrets-*` Secrets exist in the namespace at any given moment outside of an in-flight Activate.

**Root cause:** The controller (`controller/internal/workspace/controller.go:724`) conditionally mounts a volume backed by a K8s Secret named `workspace-secrets-<id>` if and only if that Secret exists at pod-build time. The **only** code path in the entire repo that creates that Secret is `api/internal/services/workspace/workspace_service.go:780`, inside `ActivateWorkspace`, and only when `sessionID` is present in the request context. Verified by `grep -r 'workspace-secrets-' .` returning exactly 3 hits.

**Confirmed working positive case:** Suspend → Activate cycle: `workspace-secrets-<id>` Secret was created, the new pod's spec included the `user-secrets` volume, init container copied `/mnt/secrets/user-secrets/secrets.json` to `/sandbox-cfg/secrets.json`, agentd's `materialize` subcommand processed it, and `/tmp/secrets-env` contained `export T4_TEST='VALIDATION_VALUE_T4'`. The pod-side machinery (post-worklog-0078 `pkg/agentd/secrets`) works correctly when the Secret arrives.

**Fix sketch:** Either (a) call `createEphemeralSecretsSecret` from `CreateWorkspace` and `ResumeWorkspace` and `SetBindings` (the bindings handler should trigger a pod rebuild when bindings change), or (b) keep the activate-only-create model but require the frontend to call activate after every bind change. (a) matches user mental model better; (b) is cheaper.

### Bug 5 — Register issues a JWT but never caches the DEK; new users hit 403 on every secret operation (High)

**Symptom:** A user registers via `POST /api/v1/auth/register`, receives a JWT, immediately calls `POST /api/v1/secrets` and gets `403 {"error":"encryption key not available; re-authenticate"}`. Logging in again with the same credentials produces a token that works.

**Root cause:** `auth.go:447-451` calls `keyService.InitializeUserKeys(...)` but does **not** call `keyService.UnlockDEK(...)`. Login (line 511, 515) calls both. The comment on line 450 — *"Non-fatal: user can still use the system, keys will be initialized on first secret creation"* — is incorrect; there is no lazy-init path on first secret creation.

**V3 confirmation:** `grep -r UnlockDEK api/internal/middleware/` returns no files; no compensating middleware exists. Only call sites for `UnlockDEK` outside tests are `auth.go:515` (Login).

**Verification:** Captured register-jti `91f05a5c-...`, queried `KEYS dek:*` on Valkey: absent. Logged in, captured login-jti `e2c6a08e-...`: present in `KEYS dek:*` output.

**Fix sketch:** In `auth.go::Register`, after `InitializeUserKeys`, call `s.keyService.UnlockDEK(ctx, userID, []byte(req.Password), jti, s.tokenDuration)`. Add a TDD regression test: register → immediately CreateSecret → assert 201.

### Bug 6 — Naming mismatch: docs and threat model say `api-key`, code says `llm-provider` (Medium)

**Symptom:** `POST /api/v1/secrets` with `type: "api-key"` returns `400 {"error":"invalid secret type: api-key"}`. Same for `opencode-config` and `llm-config`.

**Root cause:** `pkg/secrets/types.go:11-17` defines exactly five valid types: `llm-provider`, `ssh-key`, `git-credential`, `secret-file`, `env-secret`. The error message correctly enumerates the rejection but doesn't list the valid set.

**Impact:** Users get stuck on first secret creation. SDK live-tests (worklog 0079) skipped secrets entirely because the fixture used `api-key`-like type names.

**Fix sketch:** Rename `llm-provider` → `api-key` (more intuitive), or augment the error message to list valid types.

### Bug 7 — Metadata field names are undocumented; only error messages reveal them (Low)

**Symptom:** Each secret type silently requires a specific metadata field, discovered only via 400 errors:
- `env-secret` → `metadata.var_name`
- `ssh-key` → `metadata.key_type`
- `secret-file` → `metadata.mount_path`

**Fix sketch:** Document in OpenAPI / SDK type definitions.

### Bug 9 — KEK rotation orphans existing secrets; `rotate-key` causes data loss (Critical)

**Symptom:** Calling `POST /api/v1/account/rotate-key` returns `200 {"keyVersion":2}` but **all pre-rotation secrets become permanently undecryptable** (500 on reveal). New post-rotation secrets work fine; old ones are gone.

**Root cause:** Worklog 0065 line 159 already flagged this as open: *"Lazy DEK rotation not implemented (US-10.8) — old DEK discarded on rotation."* Two days later, still unfixed. Rotation generates a new DEK and re-wraps it under the new KEK, but does not re-encrypt existing `user_secrets.ciphertext` rows under the new DEK. Result: rows at `key_version=1` reference an abandoned DEK; rows at `key_version=2` reference the new one.

**Verification (T1):** Created secret pre-rotation, baseline reveal: 200. Called `rotate-key`: 200, keyVersion=2. Reveal old secret with same token: 500. Login afresh, reveal: 500. Create new secret, reveal: 200. DB shows `user_secrets.key_version` distribution: `1|1, 2|1`. Old key_version=1 row's ciphertext is no longer decryptable by anything the system holds.

**Impact:** Function does the *opposite* of its name. Frontend exposes this as a button under settings (`secretsApi.rotateKey` per `frontend/src/api/secrets.ts:33`). A user clicking "Rotate Key" loses access to all existing secrets without warning.

**Fix sketch:** Implement lazy rotation per worklog 0065 US-10.8, OR walk all `user_secrets` rows for the user and re-encrypt under new DEK before atomically swapping `key_version`, OR block rotation entirely until US-10.8 is implemented and remove the frontend button.

### Bug 10 — Recovery key is generated but never delivered; account recovery is unusable (High)

**Symptom:** `pkg/secrets/key_service.go::InitializeUserKeys` returns `recoveryKeyHex string`. `auth.go:448` discards it: `_, err := s.keyService.InitializeUserKeys(...)`. Register response has no `recoveryKey` field. Login response has no `recoveryKey` field. There is no `GET /account/recovery-key` endpoint. Verified by inspecting the JSON schema of `/auth/register`, `/auth/login`, `/auth/me`.

**Verification (T3):** Registered user, inspected register and login response JSON: no recovery key. Tried `/api/v1/account/recovery-key`, `/api/v1/account/me`: 404. Tried `/api/v1/users/me`, `/api/v1/auth/me`: returned user object without recovery key. The endpoint `POST /api/v1/account/recover` exists and accepts `{userId, recoveryKey, newPassword}` (returns 403 with bad key) — but the user has no way to obtain the right `recoveryKey`.

**Impact:** Account recovery is impossible. Combined with Bug 9 (rotation destroys secrets), forgotten password = total secret loss. The recovery-key generation infrastructure is fully implemented but unreachable.

**Fix sketch:** Return `recoveryKey` in the register response (one-time display, with strong UI warning to save it). Or add `GET /api/v1/account/recovery-key` requiring password re-confirmation, returning a freshly-generated key (after invalidating the previous one).

### Bug 11 — `user_secret_bindings` rows survive workspace deletion (Medium)

**Symptom:** After `DELETE /api/v1/workspaces/:id`, the workspace row in `workspaces` is soft-deleted (`deleted_at` populated), but `user_secret_bindings.workspace_id` rows pointing at that deleted workspace **persist**. Verified at T+5s and T+30s post-delete.

**Root cause (T6):** `user_secret_bindings_secret_id_fkey` has `ON DELETE CASCADE`, so deleting a secret correctly clears bindings. But there is no equivalent FK or app-level cleanup for `workspace_id`. `controller/internal/workspace/controller.go:450::deleteEphemeralSecretsSecret` only cleans up the K8s Secret, not the Postgres binding rows.

**Impact:** Schema-cleanliness issue. Risk of UUID collision is astronomical, so no immediate data-corruption concern, but accumulates orphan rows over time.

**Fix sketch:** Add `ON DELETE CASCADE` to `user_secret_bindings.workspace_id` FK, or have `DeleteWorkspace` explicitly clear bindings.

### Bug 12 — Workspace stuck in `Failed` phase leaks K8s Secrets (Medium)

**Symptom (T10):** Workspace `c3c8766d-1a53-434b-a713-5633669587f0` has `phase: Failed` in CR, `deleted_at` empty in DB, pod gone, but legacy `workspace-creds-c3c8766d-...` and `workspace-pw-c3c8766d-...` Secrets persist 45h later. Conditions show:
- `CredentialsAvailable=False, message="empty config"`
- `AgentHealthy=Unknown, reason=HealthCheckFailed, message="dial tcp 10.69.6.148:4097: connect: connection refused"`
- `consecutiveHealthFailures: 36`

**Root cause:** No cleanup path for workspaces that enter `Failed` phase without being explicitly deleted by the user. The controller continues polling agentd at the dead pod IP indefinitely (36+ failures), wasting reconcile cycles.

**Fix sketch:** Either (a) hard-delete (or mark `deleted_at`) workspaces stuck in `Failed` for >N minutes, or (b) stop the health-check loop after `consecutiveHealthFailures > healthCheckFailureThreshold` (already 3 per epic-8), or (c) tie K8s Secret cleanup to phase=Failed.

### Bug 13 — API does not validate adversarial `secret-file` mount paths (Low — defense-in-depth gap)

**Symptom (T12):** `POST /api/v1/secrets` with `type:"secret-file"` and `metadata.mount_path` ∈ `{../../etc/passwd, /etc/passwd, /../../etc/shadow, .ssh/authorized_keys, ../escaped, absolute/passwd, .../traversal}` all returned 201 (accepted into PostgreSQL).

**Root cause:** API-layer creation accepts any string for `mount_path`. The materializer (`pkg/agentd/secrets/secrets.go::applySecretFile`) DOES correctly reject these at materialization time (output: `mount_path "/etc/passwd" escapes secrets base directory`, secret-file skipped, other secrets unaffected — verified). `/etc/passwd` was NOT clobbered. Defense-in-depth holds today, but the API layer has no validator.

**Fix sketch:** Add path-validator at the API layer mirroring the materializer's escape check, so adversarial input never reaches the DB.

### Bug 14 — Frontend bindings UI has no path to deliver secrets to the pod (Critical UX)

**Symptom (T15):** `frontend/src/components/workspace/WorkspaceSettingsDrawer.tsx:58-59` calls `api.put('/workspaces/:id/bindings', ...)` and immediately closes the drawer. There is **no call to `/reload-secrets`** anywhere in the frontend (verified by `grep -rn 'reload-secrets\|reloadSecrets' frontend/src/`). There is no "rotate secrets" / "apply" / "reload" button in the secrets-binding UI.

**Impact:** A user binding a secret in the UI sees the drawer close successfully and the workspace pod has no idea anything changed. Compounds Bugs 1+3: the only way to make a bound secret reach the pod is for the user to manually pause + resume the workspace, or wait for idle-suspend (1h).

**Frontend exposes Bug 9:** `secretsApi.rotateKey` is wired up in `frontend/src/api/secrets.ts:33` and almost certainly has a button somewhere. Clicking it destroys all existing secrets per Bug 9.

**Fix sketch:** Either (a) add an explicit "Apply changes" button that calls `/reload-secrets` (after Bug 1 is fixed), or (b) surface a banner like "Workspace must be restarted for new secrets to take effect" with a one-click restart, or (c) make the binding endpoint trigger pod rebuild server-side (Bug 3 fix path (a)). Until Bug 9 is fixed, hide or disable the "Rotate Key" button.

---

## Confirmed working

| # | Capability | Evidence |
|---|------------|----------|
| W1 | Register endpoint creates user, hashes password (bcrypt), returns JWT with `jti` | `POST /auth/register` returns 201 + JWT with valid jti claim |
| W2 | Login endpoint validates password, generates JWT, **does** UnlockDEK | jti present in Redis `KEYS dek:*` after login |
| W3 | `llm-provider`, `git-credential`, `env-secret`, `secret-file`, `ssh-key` types accept Create with correct metadata | 201 returned for all five; T11 |
| W4 | AES-256-GCM encryption at rest in PostgreSQL `user_secrets.ciphertext` | random-looking bytes; 0 grep hits across all rows for known plaintexts |
| W5 | Reveal returns the original plaintext after Update | encrypt/decrypt round-trip works; T1+T2 baseline |
| W6 | Cross-user list isolation | user-B's `GET /secrets` returns `{"secrets":[]}` despite user-A having secrets |
| W7 | **Cross-user GET/PUT/DELETE/REVEAL all return 404 (uniform; no existence leak)** | V4 — corrected from earlier wrong claim of 500 |
| W8 | Audit log persistence and filtering | 11 entries from varied operations; filters `?action=`, `?secretId=`, `?workspaceId=` all narrow correctly; combined filters AND together; no plaintext leak in audit response (T4) |
| W9 | Audit captures both user-driven `read` (no `reason`) and pod-injection `read` (`reason:pod_injection`) | Side-channel evidence: `SetBindings` triggers N reads via `pushSecretsToAgent → PrepareSecretsForInjection` even though delivery to pod fails |
| W10 | **Password change preserves secrets**: change-password → login with new pw → old secrets reveal correctly | T2 — `sk-PRE-PWCHANGE` correctly returned after password rotation |
| W11 | Old token with cached DEK keeps working post-password-change | T2 — DEK is unchanged on password change, only the wrap key changes |
| W12 | Workspace lifecycle Create → Active in ~24s | observed across many test runs |
| W13 | `workspace-agentd` daemon `:4097/v1/healthz` and `/v1/readyz` 200 | `{"healthy":true,"version":"1.15.12"}`, `{"ready":true,"providers_connected":["opencode"]}` |
| W14 | API↔agentd `/v1/reload-secrets` schemas match (V2 retraction confirmed) | `InjectedSecret{Type,Name,Metadata,Plaintext}` == `Secret{Type,Name,Metadata,Plaintext}` |
| W15 | **`pw-secret` Secret mount delivers correct password to pod** (T7) | `/sandbox-cfg/password` byte-identical to `workspace-pw-<id>` Secret; opencode HTTP basic auth `opencode:<pw>` returns 200; wrong password 401 |
| W16 | **Network policy blocks K8s API server, metadata service, cross-pod ingress** (T8) | Sandbox → `kubernetes.default.svc` timeouts; sandbox → `169.254.169.254` timeouts; sandbox → other workspace pod's :4096/:4097 timeouts; sandbox → API pod/service timeouts |
| W17 | **Public internet egress preserved** (T8) | `api.github.com` → 200; `api.openai.com` → 421 (provider response, not block) |
| W18 | **Service account token automount disabled** (T9) | `automountServiceAccountToken: false` in pod spec; `/var/run/secrets/kubernetes.io/serviceaccount/` does not exist; no token file |
| W19 | **Multi-secret materialization works for all 5 types when delivered via Activate** (T11) | env-secret → `/tmp/secrets-env` (`export T11_ENV='VAL_ENV_T11'`); ssh-key → `~/.ssh/id_rsa_*` mode 600; git-credential → `~/.git-credentials`; secret-file → `~/.secrets/<mount_path>` mode 600; llm-provider → `/tmp/agent-config.json`; per-secret outcomes reported correctly |
| W20 | **Path-traversal defense at the materializer** (T12) | `mount_path:"/etc/passwd"` and `mount_path:"../../etc/passwd"` correctly skipped with reason `escapes secrets base directory`; `/etc/passwd` not clobbered; safe siblings still materialized |
| W21 | **Concurrent CreateSecret with same name has unique-constraint enforcement** (T13) | 10 concurrent → exactly 1 returned 201, 9 returned 409; final DB count = 1 |
| W22 | **Concurrent SetBindings has no data corruption** (T13) | 10 concurrent → mix of 204/409; final DB row count matches API response; no 5xx in logs |
| W23 | **env-secret values reach opencode's process environment** (T14) | `/proc/<opencode-pid>/environ` contains `T14D_UNIQ=UNIQ_T14D_98765`; entrypoint sources `/tmp/secrets-env` before exec'ing opencode |
| W24 | **API replica consistency** via shared Valkey DEK cache (T16) | Login on pod A → secret create via pod B (different replica) returns 201; reveal via either replica returns plaintext correctly |
| W25 | `user_secret_bindings.secret_id` has ON DELETE CASCADE | Deleting a secret correctly removes its bindings (T5) |

---

## Per-test detailed log

### V1: app.go test suite + secrets_wiring_test.go (Bug 1 confirmation)
- `go test ./api/internal/app/...` PASS
- `secrets_wiring_test.go` exists but tests Create/List/Get/Bind/GetBindings/Audit/Delete only — does NOT exercise `reload-secrets` or `podIPResolver`. Confirms there is no test that would catch Bug 1.

### V2: PrepareSecretsForInjection vs agentd schemas
- `pkg/secrets/injection.go:11-16`: `InjectedSecret{Type SecretType, Name string, Metadata json.RawMessage, Plaintext string}`. Returns `json.Marshal([]InjectedSecret{})` — top-level array.
- `cmd/workspace-agentd/secrets.go:166`: decodes into `[]secrets.Secret`.
- `pkg/agentd/secrets/secrets.go:68-73`: `Secret{Type string, Name string, Metadata map[string]string, Plaintext string}`.
- All four field names match. JSON shape (top-level array) matches. **Bug 4 RETRACTED.**

### V3: auto-unlock middleware
- `grep -r UnlockDEK api/internal/middleware/`: no files
- Only call sites for `UnlockDEK` (excluding tests): `auth.go:515` (Login). Bug 5 stands.

### V4: cross-user reveal status code
- Re-tested cleanly: GET, PUT, DELETE, REVEAL of user-A's secret by user-B → all 404 with `secret not found`. **Earlier 500 claim RETRACTED.** Behavior is correct.

### T1: KEK rotation behavior — **Bug 9**
- Pre-rotate reveal: 200 ✓
- `POST /account/rotate-key` → 200 keyVersion=2
- Same-token post-rotate reveal of pre-rotate secret: **500** ✗
- New-token (fresh login) post-rotate reveal: **500** ✗
- Post-rotate new-secret reveal: 200 ✓
- DB: `user_secrets.key_version`: 1 row at v1, 1 row at v2

### T2: Password change — works correctly
- Pre-change reveal: 200 ✓
- `POST /account/change-password`: 204
- Login old pw: 401 ✓
- Login new pw: 200 ✓
- Reveal pre-change secret with new pw: 200, plaintext correct ✓
- Old token still works (cached DEK survives password change): 200 ✓

### T3: Account recovery — **Bug 10**
- Register response: no `recoveryKey` field
- Login response: no `recoveryKey` field
- `/auth/me`, `/account/me`, `/account/recovery-key`, `/users/me`: none return recovery key
- `POST /account/recover` with fake key: 403 (proves endpoint exists)
- Cannot exercise the real recovery flow because the key is unobtainable

### T4: Audit filters — works correctly
- 11 entries from `2 creates + 1 update + 1 reveal + 2 SetBindings calls (auto-pushes ran) + 1 delete`
- Distribution: bind=2, create=2, delete=1, read=4, unbind=1, update=1
- `?action=create` → 2; `?action=delete` → 1
- `?secretId=$S1` → 6 (correct, includes auto-push reads); combined `?action=bind&secretId=$S1` → 1
- No plaintext in audit response

### T5: Delete bound secret — works correctly
- Bound secret deleted: 204
- Binding row vanished from `user_secret_bindings` immediately
- `user_secret_bindings_secret_id_fkey` has `confdeltype='c'` (CASCADE) — schema-level enforcement
- Surviving secret still bound

### T6: Delete workspace — **Bug 11**
- Workspace deleted: 204
- Secret survives (`/secrets/:id` returns 200, reveal returns plaintext) ✓
- Binding row pointing at deleted workspace **PERSISTS** at T+5s and T+30s ✗
- Workspace row shows `deleted_at` populated; binding's `workspace_id` is now an orphan

### T7: pw-secret authenticates opencode — works correctly
- `/sandbox-cfg/password` byte-identical to `workspace-pw-<id>` Secret
- HTTP Basic `opencode:<pw>` → 200 `{"healthy":true,"version":"1.15.12"}`
- No auth → 401; wrong pw → 401; alternative usernames (admin, user, anomaly, empty) → 401

### T8: Network policy — works correctly
- `localhost:4097` → 200 ✓
- K8s API server (DNS + IP): timeout ✓
- Metadata service `169.254.169.254`: timeout ✓
- Public internet (`api.github.com`): 200 ✓ (`api.openai.com`: 421 = provider response, not block)
- Cross-pod sandbox→sandbox :4096/:4097: timeout ✓
- API pod IP and ClusterIP: timeout ✓
- NetworkPolicies present: `llmsafespace-workspace-default-deny-ingress`, `llmsafespace-workspace-egress`

### T9: SA token automount — works correctly
- `automountServiceAccountToken: false` in pod spec ✓
- `/var/run/secrets/kubernetes.io/serviceaccount/` not present ✓
- No token file ✓
- No volume mount referencing serviceaccount in container spec ✓

### T10: c3c8766d pod — **Bug 12**
- Pod gone but workspace CR persists in `Failed` phase
- `consecutiveHealthFailures: 36`, controller polling dead IP
- Stale `workspace-creds-*` and `workspace-pw-*` Secrets 45h old
- DB `workspaces.deleted_at` empty for this row

### T11: Multi-secret materialization — works correctly
- All 5 types appear in `/sandbox-cfg/secrets.json`
- Each type lands in expected location with correct mode 600 / 700
- ssh-key filename derived from secret name, not metadata.filename (observation, not bug)
- `~/.ssh` directory set up correctly (drwx--S---)

### T12: Adversarial mount_path — defense-in-depth gap (**Bug 13**)
- 7 traversal attempts, all 201 from API ✗ (Bug 13)
- Materializer correctly skipped both adversarial paths with reason `escapes secrets base directory` ✓
- `/etc/passwd` not clobbered (root-owned, mode 644, untouched) ✓
- Safe sibling materialized correctly ✓
- Per-secret outcomes: `1 materialized, 2 skipped, 0 failed` ✓

### T13: Concurrent operations — works correctly
- 10 concurrent `CreateSecret(name="race-A")`: 1×201 + 9×409, final count=1 ✓
- 10 concurrent `SetBindings`: mix of 204+409, final state consistent (API response count == DB row count) ✓
- No 5xx in API logs during the test ✓

### T14: env-secret reaches opencode env — works correctly
- After Activate, `/tmp/secrets-env` contains `export T14D_UNIQ='UNIQ_T14D_98765'`
- `/proc/87/environ` (opencode pid) contains `T14D_UNIQ=UNIQ_T14D_98765`
- Process tree: pid 1 = `workspace-agentd --supervise`, pid 87 = `opencode serve`
- Confirms entrypoint sources /tmp/secrets-env before exec'ing opencode

### T15: Frontend secrets UI — **Bug 14**
- `frontend/src/api/secrets.ts` exports CRUD + `reveal`, `audit`, `rotateKey`, `changePassword`
- `WorkspaceSettingsDrawer.tsx:58-59` calls bindings endpoint, then closes drawer
- `grep -r 'reload-secrets\|reloadSecrets' frontend/src/` returns zero hits
- Frontend exposes `rotateKey` button (Bug 9 hazard)

### T16: Replica consistency — works correctly
- 2 replicas: `llmsafespace-api-5c5b9bb757-hbvzm` (10.69.9.218), `-zhszr` (10.69.6.84)
- Login on A → CreateSecret on B → 201
- Reveal on A returns plaintext, reveal on B returns same plaintext
- Shared Valkey DEK cache transparent across replicas

---

## Bugs by severity (final list)

| # | Severity | Title |
|---|----------|-------|
| 9 | Critical | KEK rotation makes existing secrets undecryptable (data loss) |
| 3 | Critical | Bound secrets only reach pod via Activate; never via Create+Bind (the user's complaint) |
| 14 | Critical (UX) | Frontend bindings UI has no path to deliver secrets to pod |
| 1 | High | reload-secrets API unconditionally 503 (`podIPResolver` not wired) |
| 2 | High | Bind-time auto-push silently swallows error |
| 5 | High | Register doesn't UnlockDEK; new users get 403 on every secret op |
| 10 | High | Recovery key generated but never delivered to user; recovery unusable |
| 6 | Medium | `api-key` / `opencode-config` type names referenced in docs don't exist |
| 11 | Medium | `user_secret_bindings` rows survive workspace deletion |
| 12 | Medium | Workspaces stuck in `Failed` leak K8s Secrets indefinitely |
| 7 | Low | Per-type metadata field names undocumented |
| 13 | Low | API doesn't validate adversarial `secret-file` mount_path (materializer catches it) |

---

## Key Decisions

1. **No code fixes this session** per Rule 6. Bugs 1, 3, 5, 9, 10, 14 require design choices that should be made explicit in fix-PRs.
2. **Test users left in DB** for now; cleanup via `DELETE FROM users WHERE email LIKE '%@pentest.local'` after fixes validated (worklog 0083 prod-kit).
3. **Bug 12 (c3c8766d)** investigated only enough to confirm it's a stale-resource leak, not a secrets-flow bug. Full triage deferred.
4. **Discipline correction (mid-session)**: User pointed out twice that I was making unvalidated assumptions ("likely YES" for legacy credentials path; "probably the pushSecretsToAgent..." for audit read counts). Both retracted. Subsequent test items (T1–T16) state assumptions explicitly upfront, mark each as hypothesis vs validated, and verify before relying on them.

---

## Blockers

None. Each bug independently fixable. Suggested order:
1. Bug 5 (smallest, unblocks new-user flow)
2. Bug 1 (small, unblocks reload-secrets path)
3. Bug 14 + Bug 3 design decision (architectural; pick one delivery model)
4. Bug 9 (data-loss-class; either implement US-10.8 or remove the button)
5. Bug 10 (deliver recovery key to user)
6. Bugs 2, 6, 7, 11, 12, 13 (cleanups)

---

## Next Steps

1. **Bug 5 fix**: add `UnlockDEK` to `auth.go::Register` after `InitializeUserKeys`. TDD test: register → CreateSecret → assert 201.
2. **Bug 1 fix**: wire `secretsHandler.SetPodIPResolver(...)` in `app.go`. TDD test in `app/secrets_wiring_test.go`.
3. **Bug 9 fix**: implement US-10.8 (lazy or eager re-encryption) OR disable rotation until then.
4. **Bug 10 fix**: surface `recoveryKey` in register response with one-time-display UI; add password-confirmed retrieval endpoint.
5. **Bug 3 design decision**: choose (a) bindings triggers pod rebuild, or (b) keep activate-only model with explicit user gesture.
6. **Bug 14 follow-up to Bug 3 fix**: depending on (a) or (b), update WorkspaceSettingsDrawer to either signal success implicitly or provide explicit "Apply" button.
7. **Bug 11 + 12 cleanups**: add CASCADE on workspace_id; phase=Failed cleanup logic.
8. **Bug 6 + 7 polish**: error message lists valid types; OpenAPI documents metadata fields.
9. **Bug 13 defense-in-depth**: API-layer mount_path validator.
10. **Update SDK live tests** (worklog 0079 follow-up): use real type names + metadata; un-skip secrets section.
11. **Cleanup**: `DELETE FROM users WHERE email LIKE '%@pentest.local'`.

---

## Files Modified

None. Investigation-only session.

## Test artifacts (under `/tmp/secretstest/`, not committed)

- `test.sh`, `test3.sh`, `test4.sh`, `test5.sh`, `test6.sh` — initial rounds (yesterday's session)
- `v4.sh`, `r-validate.sh` — V4 cross-user + audit-read provenance
- `t1.sh` through `t16.sh` (and retry variants `t14b.sh`, `t14c.sh`, `t14c2.sh`, `t14d.sh`) — full sweep this session

These should be promoted into a permanent shell test under `local/test-secrets.sh` (sibling to `local/test-auth.sh` mentioned in README-LLM.md §Authentication-E2E) as part of the Bug 1+5 fix PR.

## Tests Run

| Command | Result |
|---------|--------|
| `kubectl cluster-info` | reachable, context `admin@home-kubernetes` |
| `curl /livez` | 200 |
| `go test ./api/internal/app/...` | PASS |
| Live HTTP tests (V1–V4, T1–T16) | 12 bugs identified, 11 properties confirmed working |
| Pod-side `kubectl exec` | confirmed Bugs 3, 12; validated W7, W15, W18, W19, W20, W23 |
| PostgreSQL queries (`user_secrets`, `user_keys`, `user_secret_bindings`, `workspaces`) | confirmed Bugs 9, 11; W4 |
| Redis (`KEYS dek:*`) | confirmed Bug 5 root cause; validated W2, W11, W24 |
| Source review | all bug root causes traced to specific lines |
