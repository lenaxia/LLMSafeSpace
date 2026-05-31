# 0089 — Epic 17 Phase 4 — Credential & Crypto Testing

**Date:** 2026-05-30
**Author:** mikekao + opencode (sonnet)
**Epic:** 17
**Phase:** 4 — Credential & Crypto Testing

## Summary

16 RT-4.x tests against live cluster. **5 PASS, 7 FAIL, 4 INCONCLUSIVE, 0 SKIP.**

Surfaced 5 new gaps (G25-G29):
- **G25** HIGH — Secret value field logged unredacted in API request bodies
- **G26** CRITICAL — Postgres `POSTGRES_PASSWORD=changeme`, Valkey `requirepass=""`
- **G27** medium — User-enumeration via login response timing (210 ms delta)
- **G28** HIGH — Workspace bind handler is a no-op for first-time secret delivery
- **G29** medium — Path-traversal `mount_path` accepted by API (runtime rejects)

Re-confirmed **G18 still broken in production** despite the fix landing: `/auth/logout` doesn't call `RevokeToken`. The fix is dormant.

## Assumptions stated and validated

| # | Assumption | Validation | Result |
|---|---|---|---|
| A1 | API in-cluster postgres reachable for cleanup | `kubectl exec deploy/postgres psql` | ✅ |
| A2 | redact uses 16 patterns | code-grep `pkg/redact/redact.go:28-45` | ✅ |
| A3 | bcrypt cost 12 | `auth.go:398 const bcryptCost = 12` | ✅ |
| A4 | DEK wrapped with HKDF KEK | `pkg/secrets/key_service.go:58 InitializeUserKeys` | ✅ |
| A5 | JWT key from K8s Secret only | `kubectl get deploy llmsafespace-api -o yaml` shows `secretKeyRef` | ✅ |
| A6 | Admin routes behind AdminGuard | `router.go:646 admin.Use(middleware.AdminGuard())` | ✅ |
| A7 | secrets.value field is what holds the credential | tested against API directly | ✅ |
| A8 | register API call returns 200 with token | live test | ✅, but DEK not cached → forced separate login step |
| A9 | logout endpoint exists at /api/v1/auth/logout | `router.go:330` | ✅ but only clears cookie |

A8 caused initial RT-4.1 failures: `register` doesn't `CacheDEK`; only `login` does. Fixed by always doing register-then-login in the harness.

A9 escalated into a finding: G18 fix never wired up to /logout endpoint.

## Test methodology corrections (mid-run)

Three significant corrections applied during the live run:

1. **Secret type names**: plan said "env" / "file"; actual API requires "env-secret" / "secret-file" (per `pkg/secrets/types.go:15-19`). Updated all RT-4.x test bodies.
2. **Bindings semantics**: plan implied `POST /api/v1/secrets/<id>/bindings`; actual API is `PUT /api/v1/workspaces/<id>/bindings` with `{"secretIds":[...]}`. Updated RT-4.3.
3. **Tool inventory**: harness lacked `rg`. Replaced static-grep call with `grep -rn`.

Each correction was made AFTER live validation that the assumption was wrong, not before. No assumptions were "fixed" silently.

## Per-test summary

See [`phase-4/findings.md`](../design/stories/epic-17-security-review/phase-4/findings.md) for full details. Highlights:

| ID | Result | Severity | Note |
|---|---|---|---|
| RT-4.1 | PASS | info | IDOR blocked across users |
| RT-4.2 | FAIL | high | **G25** — secret values in API logs |
| RT-4.3 | FAIL | high | **G28** — bind no-op; G2 verification deferred |
| RT-4.4 | FAIL | medium | **G29** — path traversal accepted by API |
| RT-4.5 | FAIL | critical | **G26** — Redis no auth, Postgres default password |
| RT-4.7 | PASS | info | JWT key from secretKeyRef ✅ |
| RT-4.8 | PASS | info | Secret values redacted from responses |
| RT-4.10 | FAIL | medium | **G27** — login timing reveals registered emails |
| RT-4.13 | FAIL | high | **G18 still broken** — logout doesn't revoke |
| RT-4.14 | PASS | info | Concurrent rotate-key both 200 |
| RT-4.15 | FAIL | medium | **G19** confirmed — mise no integrity check |

## Forensic deep-dives

### G18: why the fix is dormant
`api/internal/server/router.go:330-333`:
```go
rg.POST("/logout", func(c *gin.Context) {
    c.SetCookie("lsp_session", "", -1, "/", "", true, true)
    c.Status(http.StatusNoContent)
})
```
Three lines. Never calls `authSvc.RevokeToken()`. The `RevokeToken` function works correctly (proven by its tests + the original G18 fix's mutation tests in worklog 0078). The bug is purely a wiring gap. No regression test caught it because the existing tests only check the function in isolation.

### G25: why request-body masking misses `value`
`api/internal/middleware/logging.go:54`:
```go
SensitiveFields: []string{"password", "token", "secret", "key", "apiKey", "credit_card"}
```
`MaskSensitiveFieldsWithList` (in `pkg/utilities/masking.go`) iterates the list and looks up each name as a map key. The secret-create endpoint posts under `value`, missing the list. The function does NOT scan values for secret-shaped content (Bearer/JWT/AWS-key/PEM patterns).

`pkg/redact` exists with 16 robust patterns and IS available — but middleware doesn't use it. Fix: route the request body through `pkg/redact.Redact()` after JSON-marshalling, or expand the field list to include `value` plus all known credential field names.

### G26: how I noticed the credentials gap
RT-4.5's harness called `valkey-cli config get requirepass` (no `-a` flag). Got `""` back. Cross-checked the K8s Secret `llmsafespace-credentials`:
```
jwt-secret = gNwC0PPAwKQSCDDtTVQFSuayQSKFOu     (real)
postgres-password = changeme                    (default!)
redis-password =                                (empty)
```

Postgres is wide open inside the namespace; valkey is wide open inside the namespace; only the workspace pods are NetPol-blocked. The API and frontend pods have no NetPol — anything compromising those gets the entire datastore.

### G27: timing leak details
3 measurements per arm:
```
valid_ms   = [231, 211, 226]   median 226.5 ms
invalid_ms = [16,  16,  16 ]   median  16.0 ms
delta      = 210.5 ms (93%)
```
This is a reliable, single-request signal. No statistical tricks needed. Standard mitigation: dummy `bcrypt.CompareHashAndPassword` on the no-such-user branch.

### G28: bind no-op
RT-4.3's bind PUT returned 204 in 5-16 ms. K8s Secret `workspace-secrets-<ws>` does not exist after bind. PID-1 env has no payload. `/tmp/secrets-env` does not exist on pod. Yet `GET /workspaces/<ws>/bindings` returns the binding. So the database insert worked but `pushSecretsToAgent` (which runs `EnsureSecretsManifest` and `doReload`) silently no-oped. Likely cause: `PrepareSecretsForInjection` returned an empty array because the DEK lookup or the manifest preparation hit a missed-cache or transaction-isolation bug. Needs deeper investigation in a follow-up worklog.

## Cleanup

| Item | Action |
|---|---|
| `phase4-{alice,bob,admin}@pentest.local` | DELETE FROM users (CASCADE wiped 3 rows) |
| Their workspaces | already deleted by the harness; verified zero residue |
| Valkey `phase4-*` keys | `FLUSHDB` (we own the cluster, no other tenant) |
| Port-forward 19090 | left running |

## Files

- `design/stories/epic-17-security-review/phase-4/harness/run-phase4.py` (~1100 lines)
- `design/stories/epic-17-security-review/phase-4/findings.md`
- `design/stories/epic-17-security-review/phase-4/evidence/RT-4.{1..16}.json`
- `worklogs/0089_2026-05-30_epic17-phase-4-credential-crypto.md` (this file)

## Next: Phase 5 — Proxy & Network Egress
