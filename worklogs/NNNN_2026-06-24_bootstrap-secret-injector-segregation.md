# Worklog: Bootstrap 500 Regression + Secret Injector Interface Segregation

**Date:** 2026-06-24
**Session:** Diagnose and fix the live-cluster outage where workspace
`d95b6751-8796-4ea5-addd-9f5af3053fac` (and any other workspace with bound
user-DEK secrets) reported `ProviderModelNotFoundError custom/glm-5.2` on
every chat message because its org credential never materialised into
`agent-config.json`. Replace the monolithic `PrepareSecretsForInjection`
with two SOLID-segregated interfaces: `SecretInjector` (with session)
and `SessionlessSecretInjector` (no session).

**Status:** Tests green locally. PR pending. Live deploy verification
deferred until CI build + restart of pod `d95b6751-...-546157b5`.

---

## The live failure

User reported chat URL `https://safespace.thekao.cloud/chat/d95b6751-...
/ses_109617e46ffeiQZB1PvPgYXLkj` returning `ProviderModelNotFoundError
custom/glm-5.2`.

Direct inspection of the pod via `kubectl exec`:

```
$ kubectl -n default exec d95b6751-...-546157b5 -- cat /sandbox-runtime/agent-config.json
{
  "$schema": "https://opencode.ai/config.json",
  "disabled_providers": ["opencode"],
  "provider": {
    "opencode-relay": { ... }     # only the relay; no `custom` provider
  }
}

$ kubectl -n default exec d95b6751-...-546157b5 -- cat /sandbox-cfg/secrets.json
[]                                # init container delivered nothing

$ kubectl -n default exec d95b6751-...-546157b5 -- cat /sandbox-cfg/workspace-config.json
cat: ...: No such file or directory   # default-model also undelivered
```

Init container stderr captured the cause:

```
bootstrap: fetch failed: API returned 500
materialize: 0 materialized, 0 skipped, 0 failed
```

API logs for the matching request_id showed the 500 body was
`{"error":"secret preparation failed"}` — but the underlying error was
*never logged*. Diagnosis required reading the source to determine the
swallowed cause.

**Root cause:** `pkg/secrets/injection.go::buildNonLLMSecrets` was called
from the bootstrap handler with `sessionID == ""` (the init container
has no user session). That function unconditionally calls
`keys.GetDEK(ctx, "")` whenever any non-LLM user secret is bound to the
workspace. With empty session, GetDEK returns
`"DEK not available: session expired or not unlocked"`. The error
propagated up through `PrepareSecretsForInjection` → bootstrap handler →
500. The pod's only LLM credential — an org credential
(`provider="custom"`, owner_type="org", server-KEK encrypted, fully
decryptable without a user session) — was collateral damage.

This was a regression introduced by Epic 35 (PR #378, commit `4b48a4e7`,
merged 2026-06-23 13:11 PT — about 31 hours before the incident
surfaced). Pre-Epic-35, `PrepareSecretsForInjection` was always called
from a session-bearing context (`/reload-secrets` or `/v1/...` push
handlers). Epic 35 added `pod_bootstrap.go:159` as a new caller that
hardcodes `sessionID == ""`, exposing the bug. The Epic 35 commit message
correctly documents the design contract — "User-owned creds (DEK-encrypted)
arrive via live `/v1/reload-secrets` push (unchanged — never used the K8s
Secret at boot)" — but `buildNonLLMSecrets` was never updated to honour
that contract.

The companion bug, equally important: the bootstrap handler swallowed
the underlying error at `pod_bootstrap.go:161`, leaving operators with
nothing but a generic 500 message. This is what turned a 1-minute
diagnosis into a 30-minute one.

## Why two SOLID-segregated interfaces, not one fixed function

The original signature mixed two semantically distinct contracts behind
a magic empty-string sentinel:

```go
func (s *SecretService) PrepareSecretsForInjection(ctx, userID, sessionID, workspaceID) ([]byte, error)
```

Three SOLID violations:

- **SRP**: the function did "give me everything decryptable" *and*
  "decide what's decryptable based on whether sessionID is empty" — two
  responsibilities baked into one signature.
- **ISP**: callers without a session (init container, API-key auth) were
  forced to pass `""` to satisfy a parameter they couldn't meaningfully
  populate.
- **Sentinel-as-flag**: empty string carried semantic meaning that wasn't
  enforced by the type system. Wrong code (passing `""` from a
  session-bearing handler) compiled silently.

The fix replaces the single method with two interfaces and two methods:

```go
// pkg/secrets/injection.go

type SecretInjector interface {
    InjectSecrets(ctx context.Context, userID, sessionID, workspaceID string) ([]byte, error)
}

type SessionlessSecretInjector interface {
    InjectSessionlessSecrets(ctx context.Context, userID, workspaceID string) ([]byte, error)
}

// Compile-time assertions.
var (
    _ SecretInjector            = (*SecretService)(nil)
    _ SessionlessSecretInjector = (*SecretService)(nil)
)
```

`*SecretService` satisfies both. Handlers depend on whichever interface
matches their auth context:

- `pod_bootstrap.go` declares `bootstrapInjector = SessionlessSecretInjector`
  — type system prevents a session-bearing call by accident.
- `secrets.go::pushSecretsToAgent` branches: `sessionID != ""` → `InjectSecrets`,
  empty → `InjectSessionlessSecrets`. This unifies the JWT-bind path and
  the API-key-bind path, fixing canary scenario `d-cred-model-flow` as
  a side effect.
- `secrets.go::ReloadSecrets` (JWT-only handler, sessionID always present)
  uses `InjectSecrets`.

The old `PrepareSecretsForInjection` is **deleted**, not deprecated
(Rule 5: zero tech debt).

### Behaviour preservation

`InjectSecrets` is byte-for-byte identical to the old function for
session-bearing callers. The LLM loop and non-LLM loop are unchanged;
audit events on decrypt failures are preserved.

`InjectSessionlessSecrets` differs in two visible ways:

1. User-owned LLM bindings emit `credential_skipped_no_session` audits
   (new action) rather than calling `decryptBinding` and emitting
   `credential_decrypt_failed`. Semantically more accurate — there is
   no decrypt failure, there is no session.
2. User-owned non-LLM secrets emit `secret_skipped_no_session` audits
   (new action) rather than triggering the GetDEK error. Same
   observability story applied uniformly.

Both new audit actions preserve the contract documented on the original
function: "skipped with an audit event".

## Stress-test pass — what we caught, what we missed

Adversarial review of the plan surfaced 9 distinct attack angles. The
two most consequential findings:

### A2.2 — `pushSecretsToAgent` is also a session-less caller

I had originally claimed "only the bootstrap handler passes empty
session". This was wrong. `pushSecretsToAgent` (the bind-time live push)
calls `PrepareSecretsForInjection(_, _, "", _)` for any request
authenticated via API key (no JWT, no jti, no DEK). Canary scenario
`d-cred-model-flow:54-113` documents the failure mode but works around
it by skipping the test. Without addressing this, my fix would have
left the API-key bind path silently broken for any workspace with bound
user-DEK content. The interface segregation makes the fix uniform: the
handler branches on sessionID and picks the right method.

### F6.1 — preserve audit-on-skip semantics

The first cut of the plan would have replaced the GetDEK error with
`return nil, nil` in `buildNonLLMSecrets`. That was correct for the
error-elimination requirement but destroyed the observability contract
documented on `PrepareSecretsForInjection`. The implementation now
emits per-secret skip audits via `auditSkippedUserDEKSecrets`, called
from `InjectSessionlessSecrets`. The LLM loop's existing
`credential_decrypt_failed` audit is matched by a new
`credential_skipped_no_session` audit for the user-binding case.

### Other findings (no plan change)

- **A1.2** Legacy `api-key` user_secrets (sunset 2026-12-19) are also
  user-DEK; the unified fix handles them. Test added to lock this in.
- **A1.3** The live workspace uses an org cred, not admin. Added an
  org-specific test; the original two only covered admin.
- **A4.3** The fix transitively repairs default-model delivery (the
  bootstrap handler only writes `workspaceConfig` to the response when
  the secret prep call succeeds). Test added.
- **A3.1, A4.1** Pre-existing limitations (no auto-retry of push, no
  retry of bootstrap fetch). Documented; not fixed in this PR.
- **A5.1** Cold-boot startup tasks needing SSH keys won't have them
  until live-push runs. Pre-existing since Epic 35; documented.

## Failing tests (TDD evidence)

Eight tests failed against the broken code, all for the exact production
root cause. After the fix, all eight pass:

| File | Test | Layer |
|---|---|---|
| `pkg/secrets/injection_test.go` | `..._BoundNonLLMSecrets` | unit |
| `pkg/secrets/injection_test.go` | `..._PreservesServerKEKCredentials` | unit |
| `pkg/secrets/injection_test.go` | `..._OrgCredential` | unit (live failure case) |
| `pkg/secrets/injection_test.go` | `..._LegacyAPIKey_Skipped` | unit (A1.2) |
| `pkg/secrets/injection_test.go` | `..._AuditsSkippedUserDEKSecrets` | unit (F6.1) |
| `pkg/secrets/injection_test.go` | `..._DeliversWorkspaceConfigDefaultModel` | unit (A4.3) |
| `api/internal/handlers/secrets_push_session_test.go` | `TestHandler_BindPushesOrgCredentialEvenWithAPIKeyAuth_NoSession` | integration (A2.2) |
| `api/internal/handlers/pod_bootstrap_test.go` | `TestPodBootstrap_LogsUnderlyingError_OnInjectorFailure` | unit (PR #1 observability) |

Each failing test was authored *before* its corresponding implementation
change. All eight failed for "DEK not available" or "SetLogger
undefined" — the precise root causes — never for incidental reasons.

## E2E test updates

Three pre-existing E2E tests in `pod_bootstrap_e2e_test.go` failed after
the fix because they asserted user-DEK content materialises at boot —
the *buggy* behaviour, not the documented Epic 35 contract. Updated:

- `TestE2E_BootstrapMaterialize_AllOwnerTypesMaterialized`: now asserts
  user-DEK content does NOT materialise at boot (matching commit
  `4b48a4e7` design).
- `TestE2E_BootstrapMaterialize_PartialFailure_DoesNotBlockGoodProviders`:
  reworked to use admin/org bindings (the fallback contract is among
  server-KEK bindings now; user-DEK bindings are uniformly skipped).
- `TestE2E_PasswordReset_PurgeThenBoot_NoResurrect`: positive
  pre-condition assertion updated; the negative post-condition (no
  resurrection after purge) is preserved.

## Files changed

Production:

- `pkg/secrets/injection.go` — rewrote with two interfaces + audit-on-skip helpers
- `api/internal/handlers/pod_bootstrap.go` — added `SetLogger`, switched to `InjectSessionlessSecrets`
- `api/internal/handlers/secrets.go` — `pushSecretsToAgent` branches on session; `ReloadSecrets` uses `InjectSecrets`

Tests:

- `pkg/secrets/injection_test.go` — 6 new failing-now-passing tests, existing tests renamed
- `pkg/secrets/credential_precedence_test.go` — bulk renamed
- `pkg/secrets/integration_test.go` — bulk renamed
- `pkg/secrets/e2e_test.go` — bulk renamed
- `pkg/secrets/redis_masterkey_e2e_test.go` — bulk renamed
- `pkg/secrets/pg_integration_test.go` — bulk renamed
- `cmd/workspace-agentd/reload_credentials_e2e_test.go` — bulk renamed
- `api/internal/handlers/pod_bootstrap_test.go` — new failing-now-passing observability test, fake renamed
- `api/internal/handlers/pod_bootstrap_e2e_test.go` — 3 E2E tests updated for Epic 35 contract
- `api/internal/handlers/secrets_push_session_test.go` (new) — A2.2 regression guard

## Test results

```
$ go test -timeout 180s ./api/internal/handlers/... ./pkg/secrets/... ./cmd/workspace-agentd/...
ok      github.com/lenaxia/llmsafespaces/api/internal/handlers   86.990s
ok      github.com/lenaxia/llmsafespaces/pkg/secrets             18.067s
ok      github.com/lenaxia/llmsafespaces/cmd/workspace-agentd   103.453s
```

Plus a focused targeted run:

```
$ go test -timeout 60s ./api/internal/handlers/... ./pkg/secrets/... ./cmd/workspace-agentd/...
   ./api/internal/app/... ./api/internal/services/...
[all packages: ok]
```

## Open follow-ups

### Problem B: `provider="custom"` overload (separate epic)

The DB column `provider` does three jobs:

1. Type discriminator (which SDK to load — openai/anthropic/google/etc.)
2. The literal map key written to opencode's `agent-config.json` `provider.{...}`
3. The unique-per-owner identity key (DB constraint)

These coincide for built-in providers but diverge for custom OpenAI-
compatible endpoints (LiteLLM, self-hosted gateways, etc.). The
constraint `UNIQUE (owner_type, owner_id, provider)` means an org can
have at most one credential where `provider="custom"`, and the
human-friendly `name` field (e.g. `thekaocloud`) never reaches opencode
— sessions persist `providerID="custom"` literally.

This is a **schema redesign**, not a hot-fix. To be filed as its own
epic with a design doc covering:

- `slug` (unique-per-owner identity, replaces unique on `provider`)
- `kind` (SDK type enum)
- `display_name` (UX-only)
- Migration plan for the four existing creds in production
- Session-PVC rewrite (or accept that existing sessions need re-pick)
- SDK and frontend updates

The hot-fix in this PR makes the live workspace functional because there
is exactly one `provider="custom"` credential cluster-wide and no name
collisions exist today. Problem B is correctness-and-future-growth, not
availability.

### Live deploy verification

Tests pass against the new code. Live deploy verification requires:

1. Commit on a fix branch (this branch is `feat/free-models-pod-consumption`
   from a different feature; the fix should split out).
2. PR + CI image build.
3. Deploy.
4. `kubectl rollout restart` for the affected workspace pod (which forces
   a fresh bootstrap call against the new API).
5. `kubectl exec d95b6751-...-546157b5 -- cat /sandbox-runtime/agent-config.json`
   confirms `provider.custom` is present with `glm-5.2` in models.
6. New session in the UI sends a chat message; opencode no longer emits
   `ProviderModelNotFoundError`.

## Assumptions

- The `provider="custom"` credential is the only cred bound to this
  workspace via `workspace_credential_bindings`. Confirmed by direct
  PostgreSQL query during diagnosis.
- The user has not had their session DEK evicted from Valkey since the
  fix started (no impact on bootstrap path which doesn't use the DEK,
  but matters for live-push of user-DEK content).
- The new `secret_skipped_no_session` and `credential_skipped_no_session`
  audit actions don't collide with any existing dashboard query. Quick
  grep across `monitoring/`, `observability/`, and dashboards confirms
  no existing references — these are new actions, free to create.

## Adversarial review notes

Two stress-test passes were performed on the plan before any code was
written. Pass 1 caught:

- Plan assumed `buildNonLLMSecrets` was the only failure point; verified
  by reading `decryptBinding` it's actually one of two (LLM user-cred
  loop also fails on `GetDEK("")` but is wrapped in audit+continue at
  the call site). My fix preserves both behaviours.
- Plan assumed only one production caller passes empty sessionID. Wrong
  — `pushSecretsToAgent` does too. Fixed during pass 2.
- Plan assumed audit-volume tolerable. Quick math: ~10 workspaces ×
  ~3 user secrets × ~2 boots/day = ~60 audit rows/day from this path.
  Negligible vs. the existing audit_log volume. Confirmed.

Pass 2 (after writing the failing tests) caught:

- Interface naming — switched from caller-named `BootstrapSecretPreparer`
  to contract-named `SessionlessSecretInjector`.
- Audit on user-DEK *credential* skip (not just user-DEK *secret* skip)
  — symmetry with the LLM loop's existing audit-on-decrypt-failure.

No findings from pass 2 invalidated pass 1's design. No findings from
either pass invalidated the SOLID interface segregation as the right
abstraction level.
