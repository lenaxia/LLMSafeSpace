# Worklog: Epic 38 Story Review — Incomplete, Stopped Mid-Execution

**Date:** 2026-06-12
**Session:** Meta-critical adversarial review of epic 38 stories — started, not finished
**Status:** Blocked

---

## Objective

Perform a third-pass adversarial review of all 12 story files in `design/stories/epic-38-architectural-remediation/`. For each story:
1. Identify every reason the proposed solution will NOT work
2. Assess the validity of each criticism with concrete code evidence
3. Report weaknesses, gaps, failure modes, and mitigations
4. Produce a consolidated findigs worklog

---

## What Was Completed

Three of twelve stories were fully reviewed:

### US-38.1 — Fix Rate Limiter (COMPLETE)

12 criticisms raised and assessed:

| # | Criticism | Verdict |
|---|-----------|---------|
| 1 | `RedisRateLimitBackend` is unnecessary indirection | PARTIALLY VALID — justified by testability and ISP |
| 2 | Sliding window ZADD/ZRANGEBYSCORE is incorrect | PARTIALLY VALID — rejected-request inflation exists but is conservative and self-healing |
| 3 | Token bucket eviction is insufficient | PARTIALLY VALID — wiring risk real; O(N) practically irrelevant |
| 4 | `Client()` accessor breaks encapsulation | INVALID — story explicitly rejects this option |
| 5 | Two code paths diverge | PARTIALLY VALID — temporary, intentional, tested |
| 6 | Middleware has unfixed bugs | PARTIALLY VALID — `X-RateLimit-Remaining` wrong but out of scope |
| 7 | Any working impl is an improvement — bar too low | INVALID — story is self-critical and thoroughly mitigated |
| 8 | Should use a proven library | PARTIALLY VALID — valid concern; library swap is larger refactor |
| 9 | `NewWithClient` panics on nil logger | VALID — real bug in pseudocode; trivially fixable |
| 10 | INCRBY+EXPIRE race creates permanent DoS | PARTIALLY VALID — real race; should fail-closed not log-only |
| 11 | Shared client shutdown hazard | INVALID — handled by standard graceful shutdown |
| 12 | EXPIRE refresh on every request is overhead | INVALID — O(1), necessary for cleanup |

**Actionable changes to US-38.1:**
- `NewWithClient` must accept a `*logger.Logger` parameter to avoid nil panic
- `Increment` should return error when `EXPIRE` fails on `val == 1` (fail-closed, not log-only)
- Set a concrete removal deadline on the deprecated `NewWithCache` shim
- Consider `Start()` enforcement as a constructor validation or documented invariant

### US-38.2 — Decompose ProxyHandler (COMPLETE)

9 criticisms raised and assessed:

| # | Criticism | Verdict |
|---|-----------|---------|
| 1 | Too large to land safely | PARTIALLY VALID — SSETracker callback chain creates hidden merge cliff |
| 2 | Just moves complexity | PARTIALLY VALID — total code increases; cyclomatic complexity per file decreases |
| 3 | EventRouter loses functionality | PARTIALLY VALID — different buffer sizes (16 vs 128) and ownership tracking hand-waved |
| 4 | SessionManager becomes god object | PARTIALLY VALID — `wsConfig` and `priorPhase` don't belong in a session service |
| 5 | Handler can't be "thin" | VALID — SSE framing alone is ~240 lines; 400-line target is unachievable; real floor ~600-700 |
| 6 | Constructor explosion is worse than setters | PARTIALLY VALID — fixes real bugs but doubles Services interface |
| 7 | Test migration underestimated | PARTIALLY VALID — 95 references not 52+; API signature changes mean more than find-replace |
| 8 | Defer until after other stories | INVALID — god object blocks progress on everything else |
| 9 | "Extract verbatim" preserves bugs | PARTIALLY VALID — story contradicts itself; should clearly separate verbatim vs fix steps |

**Actionable changes to US-38.2:**
- Revise 400-line target to ~600-700 lines (SSE framing alone is ~240 lines that must stay)
- Explicitly account for `stream_user_events.go` (296 lines missing from current story)
- Correct test reference count to 95 (not 52+)
- Separate "extract verbatim" steps from "extract and fix" steps — different commit strategies
- Extract `wsConfig` and `priorPhase` from SessionManager into PhaseChangeHandler or a small `WorkspaceConfigCache`
- Address different buffer sizes (16 vs 128) in EventRouter design

### US-38.3 — Replace HKDF with Argon2id (COMPLETE)

9 criticisms raised and assessed:

| # | Criticism | Verdict |
|---|-----------|---------|
| 1 | Split-function is wrong — replace everywhere | INVALID — HKDF correct for high-entropy; Argon2id on 10 sites wastes 50-100ms and destroys `info` domain separation |
| 2 | Login-time upgrade is a race condition | INVALID — conditional `WHERE kdf_version = 0` is atomic; standard optimistic concurrency |
| 3 | Argon2id is wrong — should be scrypt/bcrypt | INVALID — bcrypt is not a KDF; Argon2id is OWASP-recommended PHC winner |
| 4 | Dormant users never migrate | PARTIALLY VALID — Phase 3 should have a hard deadline and monitoring |
| 5 | 100ms performance is unacceptable | PARTIALLY VALID — acceptable for login; story should address bcrypt+Argon2id overlap |
| 6 | Sealed root key format change unnecessary | PARTIALLY VALID — operator passphrases likely high-entropy; heuristic destroys 0.78% of files |
| 7 | HKDF is fine with random salt | INVALID — salt prevents precomputation, not brute force; 2B SHA-256/sec on GPU |
| 8 | Should force-reset all passwords | PARTIALLY VALID — simpler code; nuclear option at scale; recovery-key path still needs HKDF once |
| 9 | Keeping V0 function is permanent tech debt | PARTIALLY VALID — Phase 3 has no owner/deadline; actual maintenance cost is only 7 lines |

**Actionable changes to US-38.3:**
- Fix line 147 classification: `key_service.go:147` is a recovery key path → `DeriveKEKFromKey`, not `DeriveKEKFromPassword`
- Fix sealed root key backward-compat: use length-based detection (old=92 bytes, new=93 bytes), not version-byte heuristic
- Add hard Phase 3 removal deadline (e.g., 180 days post-deployment) and monitoring alert
- Treat sealed root key passphrase as high-entropy input → skip file format change entirely

---

## What Was NOT Completed

The review stopped after US-38.3. Stories US-38.4 through US-38.12 were not reviewed in this pass.

The task that was launched for US-38.4 through US-38.6 was malformed — the tool call was missing the required `subagent_type` parameter. The tool returned a schema error:

> `SchemaError(Missing key at ["subagent_type"])`

This is a tool invocation error, not a code or story problem. The parallel agent for US-38.4 to US-38.6 never ran. The agents for US-38.1, US-38.2, and US-38.3 had already been dispatched and completed successfully before the error occurred.

The remaining nine stories were not reviewed:
- US-38.4 — Hash API keys in Redis
- US-38.5 — Fix nohtml validator
- US-38.6 — Fix controller gauge drift
- US-38.7 — Remove dead code
- US-38.8 — Consolidate dual patterns
- US-38.9 — Move services out of handlers
- US-38.10 — Add PushCredentials retry
- US-38.11 — Fix K8s client wrapper
- US-38.12 — Add agentd graceful shutdown

---

## Files Modified

None. This session was analysis only.

---

## Next Steps

Re-run the meta-critical review for the remaining 9 stories (US-38.4 through US-38.12). Each review must:

1. Read the current story file (already updated in the second review pass)
2. Read all source files the story references
3. Raise every reason the solution will NOT work
4. Assess the validity of each criticism with concrete code evidence
5. Identify weaknesses, gaps, failure modes, and mitigations
6. Apply the same structure used for US-38.1/38.2/38.3 above

The three completed reviews above can be used as quality reference for the remaining nine.
