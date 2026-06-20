# Epic 52: Comprehensive Test Coverage

**Status:** Planning
**Created:** 2026-06-20
**Priority:** High
**Depends On:** None (self-contained; stories ordered by component then risk)
**Related epics:**
- **Epic 46** (US-46.12 / US-46.13) — initial MISSINGTESTS triage and lint baseline; this epic closes the residual gap.
- **Epic 33** — observability infrastructure this epic's canaries feed.
- **Epic 25** — API robustness; several stories here add the regression tests those fixes lack.

---

## Problem Statement

The codebase has 618 Go source files and 301 test files — a 49% file-level
ratio that looks healthy until you look *where* the tests actually are. Test
density is bimodal: `api/internal/handlers/` (45 source / 65 test) and
`controller/internal/workspace/` (21 source / 25 test) are densely covered,
while entire subsystems — relay drivers, several `pkg/` leaf modules, all
`cmd/` entry points except `workspace-agentd`, the inference-relay worker —
have **no unit coverage at all**. Below the file-count level, the gaps widen:
phase reconcilers (`phase_active.go`, `phase_creating.go`, `phase_pending.go`,
`phase_suspend.go`, `phase_terminating.go`) and `reconciler.go`,
`recovery.go`, `secrets.go`, `pvc.go`, `runtime_resolver.go` in the workspace
controller have no direct tests despite being the production-critical state
machine. The same is true for every relay cloud driver except `aws_driver.go`.

Beyond unit coverage, the **integration layer is thin and inconsistent**:
`api/internal/services/*/` has no shared harness, so every service that
*does* have integration tests reinvents Postgres/Redis fakes, context
plumbing, and migration setup. The nightly kind e2e
(`.github/workflows/e2e-nightly.yml` → `local/test.sh`) is a 9-step shell
script with no per-step isolation, no parallel scenarios, and no coverage
report; if a regression slips past CI, it shows up as one of nine opaque
"Test N failed" messages.

The **canary layer** (Tier 1 shallow, Tier 2 deep in
`sdks/canary/`) is the strongest part of the test pyramid, but it has its
own gaps: no Tier 3 (failure-injection / chaos) scenarios, no
synthetic-traffic runner that exercises long-lived sessions and re-connect
backoff, and no canary coverage for the controller, MCP server, or
inference-relay worker — only for the SDK-facing API.

This epic fills all three layers — unit, integration, e2e — and expands the
canary/synthetic-traffic layer from a request-shape check into a
production-coverage signal.

### Inventory of identified gaps

| Area | Gap | Risk | Story |
|---|---|---|---|
| Controller workspace | `reconciler.go`, 5× `phase_*.go`, `recovery.go`, `secrets.go`, `pvc.go`, `runtime_resolver.go`, `helpers.go`, `network_policy.go`, `constants.go` — no direct unit tests; only integration-via-fake-client coverage exists | State-machine regressions are caught only at e2e; the workspace lifecycle is the product | US-52.1 |
| Controller relay | `cloudinit.go`, `oci_driver.go`, `router_configmap.go`, `rsa.go`, `health.go`, `constants.go` — no tests; `aws_driver.go`/`driver.go`/`wireguard.go`/`reconciler.go` have only happy-path tests | Cloud VM provisioning failures hit production undetected | US-52.2 |
| API services | `kubernetes/` service — 0 tests; `eventbroker/broker.go` — only `user_broker` tested; `ratelimit`, `metering`, `msgqueue`, `policy`, `sessionindex` — 1 test file each, narrow scope | These are cross-cutting services; bugs cascade across every handler | US-52.3 |
| `pkg/` leaf modules | `pkg/types/` (13 files) — 2 tests; `pkg/interfaces/`, `pkg/config/` — 0 tests; `pkg/email/` covered by Epic 49 (#306, 8 tests) | Public/shared types are the contract surface for the whole codebase | US-52.4 |
| `cmd/` binaries | `cmd/mcp`, `cmd/redact`, `cmd/repolint`, `cmd/relay-proxy`, `cmd/relay-router` — 0 or near-0 tests (only `seal-key` and `workspace-agentd` are covered) | CLI entry points are untested; flag parsing and signal handling regress silently | US-52.5 |
| Integration harness | Each service test file builds its own Postgres + miniredis + migration setup; no shared helper | Integration tests drift, repeat setup, and are expensive to author — so they aren't written | US-52.6 |
| Nightly e2e | `local/test.sh` — single linear shell script, 9 opaque steps, no isolation, no coverage | A regression in step 4 hides steps 5–9; coverage is invisible | US-52.7 |
| Frontend | 109 vitest unit tests for 170 components (~64%); only 9 Playwright e2e specs; key flows (org-admin, billing, relay-setup) have no e2e | UI regressions ship to prod; only `chat`/`auth`/`streaming` paths are covered | US-52.8 |
| Inference-relay worker | 1 `index.test.ts`; `secrets rotation`, `rate-limit`, `usage-logging`, `circuit-breaker`, `relay-mode-fallback` modules — 0 tests | The relay is the LLM cost surface; bugs cost real money | US-52.9 |
| Fission canaries | 40 Go scenarios, 39 Python, 39 TypeScript; Tier 3 (chaos) missing entirely; no controller/MCP/worker canaries | We detect request-shape regressions in 1 min but not control-plane or LLM-path regressions | US-52.10 |
| Synthetic traffic | `seed-accounts` exists but no traffic runner; no long-session exerciser; no reconnect-backoff validation | No signal on session-duration drift or SSE-reconnect reliability under load | US-52.11 |

---

## Design Principles

These principles are normative for every story in this epic. A test that
violates them adds noise, not signal.

### P1. Every test must declare intent

Every test (or table row) carries an inline comment — one sentence — that
answers, in order:

1. **What** is under test (function, path, contract).
2. **Value added** — what would break in production if this test were deleted.
3. **Failure mode protected against** — the specific bug class (race,
   nil-deref, wrong-status, leak, authz bypass, etc.).
4. **Expected outcome** — the assertion, in concrete terms (status code,
   metric delta, no error log line, idempotent second call returns same
   result).

A test that cannot answer all four is not added. Reviewers reject tests
missing any of the four with the comment "intent unclear" — exactly as
Rule 11's adversarial review rejects unexplained code.

### P2. Test the contract, not the implementation

Tests assert observable behaviour (HTTP status, CRD status field,
metric increment, log line, K8s object shape), not private method names or
internal struct layout. This is what makes tests valuable across refactors
and what makes "the test still passes" mean "the system still works" rather
than "the system still happens to be shaped the same way internally."

**Anti-pattern this rejects:** tests that call unexported helpers directly
to assert intermediate state. Those tests break on every refactor and catch
no real bugs.

### P3. Three layers, three purposes — no leakage

| Layer | Purpose | Examples in this repo | Failure signal |
|---|---|---|---|
| **Unit** | One function or type, in isolation, fast (<100ms), deterministic, no I/O | `pkg/redact/redact_test.go`, `pkg/secrets/crypto_test.go` | "This function is wrong" |
| **Integration** | Multiple components wired together, with fakes or testcontainers for external deps | `api/internal/handlers/secrets_integration_test.go`, `pkg/secrets/pg_integration_test.go` | "These components don't compose" |
| **E2E** | The full system against real deps (kind cluster, real Postgres, real Redis) | `local/test.sh`, `tests/epic26/relay_e2e_test.go` | "The deployed system is broken" |

A test in the wrong layer is worse than a missing test: a unit test that
spins up Postgres hides the unit-layer gap and runs slow. Each story names
the layer for every test it adds.

### P4. Isolation, reuse, no shared mutable fixtures

- **Unit/integration tests** never share state across `t.Run` subtests. Each
  subtest builds its own fixture or uses `t.Cleanup` to reset. The
  `fixture{}` pattern in `api/internal/services/workspace/workspace_service_test.go:33`
  is the canonical shape — constructor returns a struct, tests call
  `newFixture(t)`, nothing leaks across tests.
- **E2E tests** create resources with deterministic prefixes
  (`e2e-<scenario>-<random>`) and clean them up in `defer`/`t.Cleanup`,
  even on failure. No scenario assumes state from a previous run.
- **Canary scenarios** follow the same rule — TESTPLAN.md §1 already
  mandates this; new scenarios must not regress it.
- **Shared test helpers** live in one of three places:
  `api/internal/testharness/` (new, US-52.6), `tests/helpers/` (new,
  US-52.7), or `sdks/canary/<lang>/` (existing). No duplication across
  these three.

### P5. Fakes over mocks where the contract is non-trivial

`testify/mock` is appropriate when the mock surface is small (≤4 methods)
and the assertions are about call sequencing. For anything with state
(database, cache, K8s client), use the existing fakes:
`go-sqlmock` / `pgxmock`, `miniredis`, `k8s.io/client-go/kubernetes/fake`,
`sigs.k8s.io/controller-runtime/pkg/client/fake`. A mock that reimplements
a fake badly is the most common source of "tests pass, prod breaks" in this
codebase's history.

### P6. Coverage is a side-effect, not a goal

`go test -cover` numbers are reported per story for visibility, but no
story is "done" because coverage hit a threshold. A story is done when the
intent-declared tests pass, the adversarial review (Phase 2 of Rule 11)
finds zero real gaps, and the canary/e2e layer would have caught the same
bug class. Coverage thresholds are tracked in the epic's acceptance criteria
as a *regression* guard, not a completion gate.

---

## Stated Assumptions

| # | Assumption | Validation |
|---|---|---|
| A1 | The `fixture{}` pattern at `api/internal/services/workspace/workspace_service_test.go:33` is the project's idiomatic test-helper shape — every new unit/integration test follows it | Verified: same pattern in `controller/internal/workspace/controller_test.go:36` (`reconcilerFor`) and `controller/internal/relay/reconciler_test.go:36` (`stubDriver`) |
| A2 | The canary two-tier model (TESTPLAN.md §2) is the project's authority on canary design — Tier 3 extends it, doesn't replace it | Verified: `sdks/canary/TESTPLAN.md` §2 defines Shallow/Deep, schedules, and alert policy; `canary-functions.yaml` deploys only Tier 1+2 today |
| A3 | `local/test.sh` is the only existing system-level e2e runner; CI's `e2e-pr.yml` "Full-stack E2E" job is disabled (`if: false`) | Verified: `.github/workflows/e2e-pr.yml:30` and `local/test.sh:1` |
| A4 | `tests/epic26/` and `tests/gharouter/` are the only Go-level e2e test packages; both target specific epics, not general coverage | Verified: `ls tests/` |
| A5 | Envtest (`pkg/apis/llmsafespaces/v1/envtest_defaults_test.go`) is the established pattern for CRD-default and webhook tests at the schema level | Verified: `.github/workflows/envtest.yml` + `pkg/apis/.../envtest_defaults_test.go` |
| A6 | testify + go-sqlmock + miniredis + `client-go/kubernetes/fake` + `controller-runtime/client/fake` are the only sanctioned test doubles | Verified: `README-LLM.md` Testing Requirements §Mock conventions |
| A7 | Frontend tests use vitest (unit) and Playwright (e2e); the `src/test/setup.ts` file is the global setup entry point | Verified: `frontend/vitest.config.ts`, `frontend/src/test/setup.ts`, `frontend/tests/e2e/*.spec.ts` |
| A8 | The inference-relay worker is structured for testability: `handleRequest(req, env)` is a pure function extracted from the CF entrypoint | Verified: `workers/inference-relay/src/index.test.ts:6-8` documents this contract |
| A9 | Fission is the deployed canary runtime; `canary-functions.yaml` defines `Package`+`Function`+`TimeTrigger` per scenario | Verified: `sdks/canary/fission/canary-functions.yaml` |
| A10 | Synthetic traffic differs from canaries: canaries assert contract conformance on discrete endpoints; synth traffic exercises long-lived user journeys with reconnects, mid-session failures, and session-duration measurement | Validated by absence — no synthetic-traffic runner exists today; `seed-accounts` only provisions identities |

---

## Story List

| Story | Layer | Effort | Depends on |
|---|---|---|---|
| [US-52.1](US-52.1-controller-phase-reconciler-tests.md) | Unit + envtest | L | — |
| [US-52.2](US-52.2-relay-driver-cloudconfig-tests.md) | Unit + integration | M | — |
| [US-52.3](US-52.3-api-services-coverage-gaps.md) | Unit | M | US-52.6 (uses harness) |
| [US-52.4](US-52.4-pkg-leaf-module-tests.md) | Unit | M | — |
| [US-52.5](US-52.5-cmd-binaries-tests.md) | Unit + integration | M | — |
| [US-52.6](US-52.6-integration-harness-standardisation.md) | Infra | M | — |
| [US-52.7](US-52.7-nightly-e2e-kind-expansion.md) | E2E | L | US-52.6 |
| [US-52.8](US-52.8-frontend-coverage-gaps.md) | Unit + e2e | M | — |
| [US-52.9](US-52.9-inference-relay-worker-tests.md) | Unit + integration | M | — |
| [US-52.10](US-52.10-fission-canary-tier3-expansion.md) | Canary | L | — |
| [US-52.11](US-52.11-synthetic-traffic-runner.md) | Synth | L | US-52.10 |
| [US-52.12](US-52.12-second-pass-validation.md) | Process | S | All above |

All stories cite concrete files, concrete failure modes, and concrete
assertions. US-52.12 closes the epic with the second-pass review described
in the user's request — every test added by US-52.1–52.11 is re-audited
there for meaningfulness.

---

## Acceptance Criteria (epic-level)

The epic is **done** when:

- [ ] Every story's intent-declared tests are merged and pass under
      `go test -timeout 60s -race -count=1 ./...` and the frontend vitest
      suite.
- [ ] The adversarial second-pass (US-52.12) records zero real findings.
- [ ] `api/internal/middleware/MISSINGTESTS.md` is deleted (its remaining
      items — auth-token-expiry, distributed rate limit — are implemented by
      US-52.3).
- [ ] Coverage of every package listed in the gap inventory exceeds 60%
      line coverage, measured by `go test -cover ./...` and recorded in
      `worklogs/NNNN_*.md` for the closing worklog.
- [ ] Nightly e2e publishes a JUnit XML + HTML report as a CI artifact and
      fails CI on regression (US-52.7).
- [ ] Tier 3 canaries (US-52.10) and the synthetic-traffic runner
      (US-52.11) are deployed to the staging cluster and visible in the
      canary dashboard.
- [ ] The integration harness (US-52.6) is the single source of Postgres +
      Redis + migration setup; no service test reinvents it after merge.
- [ ] **Cross-cutting (second-pass audit X1):** every story's PR includes
      a worklog entry showing at least one test failing when its
      protection is removed — the only proof the test adds value.
- [ ] **Cross-cutting (X3):** the closing worklog lists all per-story
      coverage deltas in one table for review.

---

## Non-goals

- **Performance / load benchmarking.** Owned by `hack/benchmark-*.sh` and
  Epic 33's metering work. This epic only adds *correctness* tests.
- **Security penetration testing.** Out of scope per TESTPLAN.md §11; owned
  by dedicated security tooling (Trivy, Gitleaks, security-scan workflow).
- **Rewriting existing tests.** Stories add tests; they do not refactor
  passing tests for style. Style drift is owned by lint.
- **Test-driven rewrites of working code.** If a story discovers a bug
  while writing tests (per Rule 11), it fixes the bug + adds the regression
  test, but it does not use the test as a pretext for unrelated rewrites.
- **Coverage chasing.** Reaching an arbitrary percentage is not a goal.
  The goal is closing the specific named gaps in the inventory.

---

## Test Infrastructure Standards (normative for this epic)

These standards are written once here and cited by every story. Stories do
not restate them.

### Go unit tests

- Package: `<pkg>` or `<pkg>/internal_test` (white-box only when the public
  API forces it; prefer black-box `package <pkg>_test`).
- Naming: `Test<Subject>_<Condition>` (e.g. `TestReconcile_PVCNotFound`).
  Table-driven subtests: `t.Run("PVC not found", ...)`.
- Helpers: a `fixture` struct + `newFixture(t *testing.T) *fixture`
  constructor; never module-level `var setup = ...`.
- Timeouts: every test file sets `//go:generate` is not needed; tests run
  under the global `-timeout 60s`. Individual tests that need shorter
  deadlines use `context.WithTimeout` and `t.Deadline()`.
- Race: all tests pass under `-race`.

### Go integration tests

- File suffix: `*_integration_test.go` (matches existing convention in
  `pkg/secrets/`, `api/internal/handlers/`).
- Build tag: `//go:build integration` — so `go test -short ./...` skips
  them. CI runs them in a separate job.
- Harness: US-52.6's `api/internal/testharness/` — single import for
  Postgres testcontainer, miniredis, migration runner.
- Each test calls `harness.New(t)` in the test body (not `TestMain`), so
  `t.Parallel()` is safe and cleanup is automatic.

### Go e2e tests

- Location: `tests/<area>/` (e.g. `tests/lifecycle/`, `tests/proxy/`).
  Not under `api/` or `controller/` — e2e tests span both.
- Build tag: `//go:build e2e`.
- Runner: US-52.7's `tests/runner/` — wraps `local/test.sh`'s port-forward
  logic in a Go test harness with `t.Cleanup`, structured logging, and
  JUnit output.
- Each scenario is independent; no shared global state.

### Frontend tests

- Unit: vitest, colocated `*.test.tsx`, follows
  `frontend/src/test/setup.ts`.
- E2E: Playwright, `frontend/tests/e2e/<flow>.spec.ts`, each spec uses a
  fresh browser context per test (Playwright default).

### Canaries

- Three SDK implementations (Go, Python, TypeScript) for every scenario —
  the existing rule. New scenarios add all three.
- Each scenario: `Handler(w, r)` for Fission + `main()` for CLI, returning
  `Result{}` JSON. Identical contract across all three SDKs.
- Schedule declared in `TESTPLAN.md` and `canary-functions.yaml` together;
  no schedule exists in only one of the two files.

### Synthetic traffic

- New `sdks/canary/synth/` directory, Go-only (the synth runner is a state
  machine, not a contract check — three implementations add no value).
- Config via env vars matching the canary convention
  (`LLMSAFESPACES_SYNTH_*`).
- Output: Prometheus metrics + structured JSON log; no `Result{}` (synth
  runs are continuous, not discrete pass/fail).
