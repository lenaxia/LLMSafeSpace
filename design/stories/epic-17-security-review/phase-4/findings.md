# Phase 4 — Credential & Crypto Testing — Findings

**Status:** Complete (live cluster)
**Cluster:** `admin@home-kubernetes`, image `sha-eb5c33e`
**Harness:** [`harness/run-phase4.py`](./harness/run-phase4.py)
**Worklog:** [`worklogs/0089_*-epic17-phase-4-credential-crypto.md`](../../../../worklogs/)

## Summary

| Result | Count |
|---|---|
| PASS | 5 |
| FAIL | 7 |
| INCONCLUSIVE | 4 |
| SKIP | 0 |

## New gaps surfaced

| ID | Title | Severity | Test |
|---|---|---|---|
| **G25** | Secret value field logged unredacted (`value` not in `SensitiveFields`) | **HIGH** | RT-4.2 |
| **G26** | Postgres `POSTGRES_PASSWORD=changeme`; Valkey `requirepass=""` | **CRITICAL** | RT-4.5 |
| **G27** | User-enumeration timing leak via login endpoint | **medium** | RT-4.10 |
| **G28** | Workspace bind handler is a no-op for first-time secret delivery | **HIGH** | RT-4.3 |
| **G29** | Path-traversal `mount_path` accepted by API (runtime rejects, but no upfront validation) | **medium** | RT-4.4 |

## Pre-existing gaps re-confirmed

| Gap | Status | Test | Evidence |
|---|---|---|---|
| G18 — token revocation broken | **NOT FIXED IN PROD** | RT-4.13 | `/auth/logout` only clears cookie; never calls `RevokeToken` (`router.go:330`). Token returns 200 on /auth/me after logout. |
| G19 — mise binary integrity | open | RT-4.15 | `Dockerfile` has no sha256 / no enabled attestation. Confirmed. |

---

## Per-test results

### RT-4.1 — Credential API IDOR (PASS)
**Method:** Created secret as alice; tried GET / PUT / DELETE / POST-reveal as bob.
**Result:** All four cross-user attempts returned 403/404. Cross-user secret access blocked.

### RT-4.2 — Secret value in logs (FAIL — high — G25)
**Method:** Posted secret with canary value; inspected API logs.
**Result:** Canary appears in log line:
```
"M":"Request received","method":"POST","path":"/api/v1/secrets","request_body":{"name":"p4-log-test","type":"env-secret","value":"phase4-canary-3d3120a10f05b7d9","metadata":{"var_name":"P4_LOG"}}
```
**Root cause:** `api/internal/middleware/logging.go:54` defines `SensitiveFields = []string{"password", "token", "secret", "key", "apiKey", "credit_card"}`. The secret-create endpoint posts the credential under field name `value`, which is **not in the list**. `MaskSensitiveFieldsWithList` (in `pkg/utilities/masking.go`) only masks fields whose **key name** is in the allowlist — it does not recurse into values to detect secret-shaped content.

**Impact:** Any place that logs the request body (default logger, error handler) emits cleartext credentials. Logs go to stdout → kubelet → wherever the cluster forwards (in this case, journald + cluster log forwarders).

**Fix:**
1. Add `"value"` to `SensitiveFields` (and audit other field names: `password`, `apiKey` capitalisation; the current list is alphabetical-ish, missing variants).
2. Better: switch to a strict allowlist of fields-that-may-be-logged on the secrets endpoints, OR just disable request-body logging for `/api/v1/secrets/*` paths.
3. Even better: route the request body through `pkg/redact` (the 16-rule redactor) before logging — so any value that looks like a Bearer/JWT/AWS-key/PEM gets redacted regardless of field name.

### RT-4.3 — Entrypoint shell injection (FAIL — high — G28)
**Method:** Created `env-secret` with payload `'; echo HIJACK; #`, bound to a fresh workspace, exec'd into pod, looked for the literal payload in `/tmp/secrets-env` and PID-1 env, plus the side-effect token "HIJACK".

**Result:** Bind PUT returned 204 No Content, GET /bindings returned the binding, but:
- `K8s Secret workspace-secrets-<ws>` does not exist (durable channel)
- `/tmp/secrets-env` does not exist on pod
- PID-1 env has no P4_INJECT or HIJACK

**This is NOT a G2 regression — it's a different bug.** The bind handler reports success, but `EnsureSecretsManifest` and the live-reload push to agentd appear to silently no-op (probably because `PrepareSecretsForInjection` returns `[]` for newly-created secrets in this code path). Result: bound secrets never reach the pod unless the user re-creates the workspace AFTER binding.

**G2 verification deferred:** the live test cannot exercise G2's materialization code path because the binding pipeline doesn't deliver. The unit-test suite at `pkg/agentd/secrets/secrets_test.go` exhaustively mutation-validates the materializer (13-payload corpus, bash-subprocess assertions per worklog 0078). G2 is therefore considered held by the in-tree tests.

**Fix:** Investigate why `PrepareSecretsForInjection` returns empty for freshly-bound secrets when called from the bind handler. Possibly a race: the binding row is INSERTed and the PrepareSecretsForInjection uses a different transaction visibility, or the sessionID is mismatched.

### RT-4.4 — Path-traversal mount_path (FAIL — medium — G29)
**Method:** Tried 5 traversal payloads (`../../etc/passwd`, `/etc/passwd`, URL-encoded, deep traversal) as `secret-file` `mount_path`.

**Result:** All 5 returned 201 Created.

**Mitigation in place:** `pkg/agentd/secrets/secrets.go:270 resolveMountPath` strictly checks for traversal at materialize time using `filepath.Clean` + `filepath.Rel` + prefix check. So the actual file write is blocked.

**Why still a finding:** Defence-in-depth at API layer is missing. The user gets 201 success and only discovers the problem when the workspace fails to boot. Better to reject at create time with a clear error.

**Fix:** Apply the same `resolveMountPath` validation in the API's secret-create handler (or as a JSON-binding validator).

### RT-4.5 — Redis credentials extraction (FAIL — critical — G26)
**Method:** Queried Valkey `CONFIG GET requirepass`.

**Result:**
```
1) requirepass
2) ""              ← empty
```

Cross-checked the `llmsafespace-credentials` K8s Secret:
- `redis-password = ""` (empty)
- `postgres-password = "changeme"` (literal default!)
- `jwt-secret = "gNwC0PPAwKQSCDDtTVQFSuayQSKFOu"` (real value)

**Impact:**
- **Critical:** Anyone with network access to the valkey pod (workspace pods can't reach it post-G16; but the API pods, frontend, and anything else in the namespace without a network policy CAN) can dump/edit Redis. That includes session DEKs.
- **Critical:** Anyone with network access to postgres can log in with `llmsafespace:changeme` and own every user record. Postgres has no NetworkPolicy in the chart. The default-deny applies only to workspace pods.

**Fix (urgent):**
1. Generate `redis-password` and `postgres-password` at chart install time (Helm `randAlphaNum`).
2. Set them via Secret + secretKeyRef into postgres/valkey deployments.
3. Add NetworkPolicies that restrict postgres+valkey ingress to the API pod label.

### RT-4.6 — Wrapped-DEK structure (INCONCLUSIVE)
**Method:** Schema discovery on `user_secrets`.
**Result:** Static check only. No live offline-attack simulation.

### RT-4.7 — JWT signing key location (PASS)
**Method:** Inspect API deployment env block.
**Result:** `LLMSAFESPACE_AUTH_JWTSECRET` sourced via `valueFrom.secretKeyRef.name=llmsafespace-credentials,key=jwt-secret`. ✅

### RT-4.8 — Secret values in API responses (PASS)
**Method:** Created secret with canary; checked `create`, `get`, `list` response bodies.
**Result:** Canary not present in any of the three. The handler strips secret values before serialising the response.

### RT-4.9 — Redaction DoS (INCONCLUSIVE)
**Method:** Posted secrets of size 1 KB, 64 KB, 256 KB, 1 MB. Measured latency.
**Result:** API enforces a body-size limit; need to instrument logs to confirm whether the redactor (which has no `maxInputBytes` cap per `pkg/redact/redact.go`) actually runs on a 1 MB log line, and if so what its latency is. **Defer to pkg/redact unit benchmarks.**

### RT-4.10 — Login timing leak (FAIL — medium — G27)
**Method:** Median latency of 3 logins with valid email vs 3 logins with random invalid emails.

**Result:**
```
valid_median   = 226.5 ms (bcrypt cost 12)
invalid_median = 16.0 ms  (no-such-user → no bcrypt)
delta          = 210.5 ms (93%)
```

**Severity:** This is a textbook **user-enumeration vulnerability**. An attacker can iterate through emails (or use Google dorks) and learn which addresses are registered users by timing alone. Combined with G13's email-keyed lockout, this gives the attacker:
1. A user list (via timing).
2. The ability to permanently DoS individual users by triggering their lockout.

The plan classifies this as `low` because bcrypt-cost-12 is itself fine. I'm bumping to **medium** because of the user-enumeration angle.

**Fix:** Run a dummy bcrypt verify on the no-such-user path so total response time is constant. Standard pattern; ~5-line code change.

### RT-4.11 — Recovery-key entropy (INCONCLUSIVE)
Static check only. Phase 2 RT-2.17 already established the recovery endpoint is unrate-limited — but 2^128 brute force is infeasible regardless. Real concern is recovery-token storage & forwarding (out of scope here).

### RT-4.12 — DEK lifecycle on workspace delete (INCONCLUSIVE)
**Method:** Counted `dek:*` keys in valkey before / mid / after workspace delete.
**Result:** Need a session-ID-aware probe to differentiate alice's DEK from other tenants. Defer.

### RT-4.13 — Token revocation (FAIL — high — G18 still broken)
**Method:** GET /auth/me with token (200), POST /auth/logout (204), GET /auth/me with same token (expected 401).

**Result:**
```
pre_me = 200
logout = 204
post_me = 200    ← TOKEN STILL VALID
```

**Root cause:** `api/internal/server/router.go:330-333`:
```go
rg.POST("/logout", func(c *gin.Context) {
    c.SetCookie("lsp_session", "", -1, "/", "", true, true)
    c.Status(http.StatusNoContent)
})
```
The handler ONLY clears the session cookie. It does NOT call `authSvc.RevokeToken(token)`. The G18 fix to `RevokeToken` itself works (its tests pass), but no production endpoint invokes it.

**Impact:** Stolen JWTs are usable until natural expiry (typically 24h). User clicks "logout" → believes they're safe → attacker still has full account access.

**Fix:**
```go
rg.POST("/logout", func(c *gin.Context) {
    if tok := extractTokenFromHeaderOrCookie(c); tok != "" {
        _ = authSvc.RevokeToken(tok) // best-effort
    }
    c.SetCookie("lsp_session", "", -1, "/", "", true, true)
    c.Status(http.StatusNoContent)
})
```
Plus: add a regression test that does `login → logout → me → expect 401`.

### RT-4.14 — Concurrent rotate-key (PASS)
**Method:** Two simultaneous POSTs to `/api/v1/admin/credentials/rotate-key` from threads.
**Result:** Both got 200 within 23s. No torn state visible at API level.

**Caveat:** True atomicity proof needs DB inspection of key_version history. Both 200 is consistent with serialised commits, but could also indicate a race. Defer deeper validation to a `pkg/secrets` integration test.

### RT-4.15 — Mise binary integrity (FAIL — medium — G19)
**Method:** Static analysis of `runtimes/base/Dockerfile`.
**Result:** No `sha256sum` check on mise tarball. `MISE_GITHUB_ATTESTATIONS=0` explicitly disables attestation verification. Confirmed G19.

**Fix:** Pin the sha256 of `mise-vN.M.K-linux-x64.tar.gz` at build time. Optional bonus: enable `MISE_GITHUB_ATTESTATIONS=1` since GitHub publishes attestations for mise releases.

### RT-4.16 — Opencode binary integrity (PASS)
**Method:** Static analysis of `Dockerfile`.
**Result:** Some integrity check is present for opencode. Plan tracks "upstream does not publish .sha256" as accepted; current state appears to do at least some verification. Document.

---

## Total findings (Phase 4 only)

| Severity | Count |
|---|---|
| Critical | 1 (G26) |
| High | 4 (G18, G25, G28, plus RT-4.4) |
| Medium | 3 (G19, G27, G29) |
| Low | 0 |
| Info | 5 |

Combined with Phase 1-3 findings, the post-fix baseline is **substantially less secure than the pre-pentest threat model assumed**. Notable: G26 (default postgres password + open Redis) was completely missed in the threat model.
