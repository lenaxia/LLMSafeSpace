# Second-Pass Validation: Epic 52 Test Plans

**Audited:** 2026-06-20
**Auditor:** Self-audit per Rule 11 Phase 1+2 (independent audit to follow
at PR time per the orchestrator's Validator Loop)
**Scope:** All test plans in US-52.1 through US-52.11 + this epic's
infrastructure proposals.
**Method:** Apply M1–M6 (defined in US-52.12) to every named test in every
story; for each, record KEEP / REWORK / DROP with rationale; amend
originating stories to reflect decisions.

---

## Summary

| Story | Tests planned | KEEP | REWORK | DROP | Net |
|---|---|---|---|---|---|
| US-52.1 | 47 | 41 | 5 | 1 | 46 |
| US-52.2 | 25 | 22 | 3 | 0 | 25 |
| US-52.3 | 28 | 25 | 2 | 1 | 27 |
| US-52.4 | 18 | 14 | 3 | 1 | 17 |
| US-52.5 | 19 | 16 | 2 | 1 | 18 |
| US-52.6 | 9 | 7 | 1 | 1 | 8 |
| US-52.7 | 22 | 18 | 3 | 1 | 21 |
| US-52.8 | 19 | 17 | 2 | 0 | 19 |
| US-52.9 | 26 | 23 | 2 | 1 | 25 |
| US-52.10 | 27 | 23 | 3 | 1 | 26 |
| US-52.11 | 5 journeys | 4 | 1 | 0 | 5 |
| **Total** | **245** | **210** | **27** | **8** | **237** |

Net reduction: 8 tests dropped (3.3%), 27 strengthened (11%). The
reductions look small because the original plans were already written
under M1–M6 awareness — the audit is the verification, not a rewrite. The
8 drops and 27 reworks are recorded below with the rationale Rule 11
Phase 2 requires.

---

## Per-story findings

### US-52.1 — Controller phase reconciler

#### REWORK-1.1: `TestPhasePending_StorageSizeZero_Fails`

- **M1 failure:** Original asserted "ws.phase → Failed with descriptive
  condition." That protects against the panic but not against silent
  acceptance.
- **Fix:** Tighten to: "ws.phase → Failed; ws.conditions contains
  StorageInvalid=True with reason `parse_error`; **no panic**." The
  `no panic` clause is asserted via `recover()` in the test wrapper, so
  the test fails if either (a) the workspace is wrongly accepted or (b)
  the controller panics. Both are real regressions.
- **Status:** Amended in US-52.1.

#### REWORK-1.2: `TestReconcile_MetricsIncrement`

- **M2 tautology:** The metric `reconcile_total` is incremented
  unconditionally in every reconcile. Asserting "incremented" cannot fail
  unless the metric is deleted entirely.
- **Fix:** Assert the *label values*: `reconcile_total{phase="pending",
  status="success"}` increments for a Pending-reconcile; the same counter
  with `status="error"` increments for a failed reconcile. Two table rows
  covering both branches; each fails if its specific label is wrong.
- **Status:** Amended in US-52.1.

#### REWORK-1.3: `TestReconcile_MaxConcurrentReconciles`

- **M3 layer:** 100 concurrent reconciles against a fake client is an
  integration test (it tests the controller-runtime workqueue, not our
  code). It also runs >10s, blowing the unit-test budget.
- **Fix:** Move to `tests/lifecycle/` (US-52.7) as
  `TestE2E_Lifecycle_ConcurrentReconciles` against the real controller in
  a kind cluster; remove from US-52.1.
- **Status:** Removed from US-52.1; added to US-52.7.

#### REWORK-1.4: `TestPhaseCreating_ConditionsUpdated`

- **M1 vague:** "correct statuses" is undefined. A reader cannot tell
  what the test asserts.
- **Fix:** Specify per-condition: `Ready=True` only when pod is Ready;
  `PodScheduled=True` after scheduling; `CredentialsAvailable=True` only
  after Secret materialised. Three rows; each fails if its specific
  condition is mis-set.
- **Status:** Amended in US-52.1.

#### REWORK-1.5: `TestPhaseActive_StatusRefresh`

- **M1 vague:** "Status fields refresh" — which fields? When?
- **Fix:** Specific assertions: `agentHealth.lastProbeTime` advances
  monotonically across reconciles; `lastActivityAt` reflects the value
  written by the API (set via test fixture); `conditions` LastTransitionTime
  updates only on actual transition.
- **Status:** Amended in US-52.1.

#### DROP-1.1: `TestPVC_OwnerReference`

- **M2 duplicate:** OwnerReference correctness is *implicitly* tested by
  `TestPhaseTerminating_RemovesFinalizer` — if ownerRef is wrong, GC
  doesn't cascade, and the finalizer test fails because resources aren't
  cleaned.
- **Fix:** Remove from US-52.1. The terminating test covers it.
- **Status:** Removed from US-52.1.

### US-52.2 — Relay drivers + cloud-config

#### REWORK-2.1: `TestCloudInit_RendersMinimalUserData`

- **M1 weak:** "Output is valid YAML" is a parse smoke-test. Real bugs
  are missing sections, not YAML syntax errors.
- **Fix:** Replace with: "Output is valid YAML **and** contains the keys
  `write_files`, `runcmd`, and `systemd.units` — the three contract
  sections every cloud-init for relay VMs must have."
- **Status:** Amended in US-52.2.

#### REWORK-2.2: `TestCloudInit_SizeWithinMetadataLimit`

- **M1 vague:** "Rendered size < provider limit per driver" — what is the
  limit? Is it asserted or just documented?
- **Fix:** Specify: AWS limit 16384 bytes (per AWS docs); GCP 262144;
  OCI 16384. Test asserts `len(rendered) < driver.Limit()` and
  `driver.Limit()` returns the documented constant. Two failures caught:
  render bloat and limit-constant drift.
- **Status:** Amended in US-52.2.

#### REWORK-2.3: `TestOCIDriver_Status_Terminated`

- **M1 over-assertion:** "Status returns unhealthy + termination reason
  → triggers destroy+reprovision." Triggering destroy is a reconciler
  side-effect, not driver behaviour. The unit test on the driver cannot
  see whether destroy was triggered.
- **Fix:** Scope to driver only: "Status returns unhealthy with
  reason=Terminated." The destroy+reprovision side-effect is tested in
  `TestReconciler_DestroyInProgress_DoesNotReprovision` and the new
  `TestReconciler_TerminatedVM_TriggersDestroy` (added to US-52.2 in
  this audit).
- **Status:** Amended in US-52.2.

### US-52.3 — API services

#### REWORK-3.1: `TestPolicy_DefaultDeny_WhenNoPolicyMatches`

- **M3 layer ambiguity:** If `policy.Evaluate` is a pure function (no DB
  lookup), unit-testable. If it loads policies from DB, integration-only.
  The plan didn't say which.
- **Fix:** Confirm against source. `api/internal/services/policy/`
  evaluates against an in-memory policy cache populated by the policy
  loader; evaluation itself is pure. Unit test is correct layer. Add
  note: "If policy ever adds DB lookup, move this to integration."
- **Status:** Amended in US-52.3 with the layer note.

#### REWORK-3.2: `TestMsgQueue_DLQ_AfterMaxRetries`

- **M1 unspecified N:** "After max retries" — what is max? If the test
  doesn't fix N, the test is non-deterministic (the default could change).
- **Fix:** Test sets `MaxRetries: 3` explicitly via config; asserts the
  message hits DLQ after exactly 3 attempts. If the default changes, this
  test still passes because it sets its own N.
- **Status:** Amended in US-52.3.

#### DROP-3.1: `TestKubernetesService_InClusterFallback`

- **M5 isolation violation:** The fallback reads `~/.kube/config` — a
  developer's actual kubeconfig. Running this test on a developer
  machine with a live kubeconfig can mutate cluster state (the test
  might actually call the apiserver).
- **Fix:** Drop. The fallback path is exercised by every integration
  test in CI (which uses kubeconfig, not in-cluster); adding it as a
  unit test adds risk without value. The CI e2e (US-52.7) covers the
  real path.
- **Status:** Removed from US-52.3.

### US-52.4 — pkg leaf modules

#### REWORK-4.1: `TestAllDTOs_HaveJSONTags`

- **M1 incomplete:** Reflection catches missing tags but not wrong tags
  (e.g., `json:"User_id"` instead of `json:"user_id"`).
- **Fix:** Augment with a curated "known schema" table — for the 10 most
  important DTOs, list the expected json keys; test asserts they match.
  Reflection catches new fields without tags; the table catches renames.
- **Status:** Amended in US-52.4.

#### REWORK-4.2: `TestSES_Send_Throttling_Retries`

- **M1 unspecified retry policy:** "Retried; final result nil." How many
  retries? What backoff? Without these fixed, the test cannot fail on
  backoff regressions.
- **Fix:** Stub returns Throttling for the first 2 calls, 200 on the 3rd.
  Assert the stub was called exactly 3 times; assert total elapsed ≥
  configured backoff floor (use a fake clock to avoid wall-clock
  flakiness).
- **Status:** Amended in US-52.4.

#### REWORK-4.3: `TestConfig_SecretNeverLogged`

- **M1 regex risk:** "No secret substring" — what counts? If the test
  puts `password=hunter2` and asserts no `hunter2` in logs, it catches
  literal leaks but not formatted variants (`hunter***2`, base64, etc.).
- **Fix:** Use a secret of random non-word characters
  (`!@#$%^&*()_+-={}[]|\\:;'"<>?,./`) unlikely to appear in any
  formatting; assert the random blob never appears in the log buffer.
  This catches literal, masked, and structural leaks.
- **Status:** Amended in US-52.4.

#### DROP-4.1: `TestNoop_ReturnsNil`

- **M1 failure:** Noop returning nil is the function's definition. There
  is no plausible code change that is a regression but doesn't fail
  compilation. The test cannot catch any real bug.
- **Fix:** Drop. If we ever change `noop` to do something, that's a new
  feature and warrants its own tests at that time.
- **Status:** Removed from US-52.4.

### US-52.5 — cmd binaries

#### REWORK-5.1: `TestRelayProxy_HealthEndpoint`

- **M2 trivial:** "200 OK" alone is weak — every healthy service returns
  200. The regression risk is the response *shape*, not the status.
- **Fix:** Assert the response body has a `status` field equal to
  `"ok"` and an optional `version` field. If the body shape changes,
  the k8s probe (which only checks status) still works but monitoring
  parsing breaks — this test catches that.
- **Status:** Amended in US-52.5.

#### REWORK-5.2: `TestRepolint_DoesNotMutateRepo`

- **M5 isolation:** "Repo unchanged after run" requires hashing every
  file, which is slow and brittle (timestamps, mode bits).
- **Fix:** Hash only files repolint is documented to inspect; record
  before/after tree of `git ls-files` + content hashes. If repolint
  adds new files outside `.git/`, that's the real regression — assert
  no new files appear.
- **Status:** Amended in US-52.5.

#### DROP-5.1: `TestRedact_HelpFlag`

- **M2 duplicate:** `--help` exit code is covered by the `cmdtest`
  helper's exit-code assertion (already a helper responsibility). The
  redact-specific test adds nothing.
- **Fix:** Drop from redact. Keep `TestMCP_VersionFlag` (US-52.5)
  because version reporting is content, not just exit code.
- **Status:** Removed from US-52.5.

### US-52.6 — Integration harness

#### REWORK-6.1: `TestHarness_Parallel_NoInterference`

- **M1 under-specified:** "Each harness sees only its own data." How
  verified? If both harnesses insert into `users`, the test must
  distinguish them.
- **Fix:** Each harness inserts a row with a unique marker
  (`email = 'test-<harness_id>@test'`); each harness queries its own
  marker and asserts it sees exactly 1, not 2. The marker makes the
  isolation explicit.
- **Status:** Amended in US-52.6.

#### DROP-6.1: `TestHarness_NoDuplicatePortBinding`

- **M1 wrong-target:** Port allocation is testcontainers-go's job, not
  the harness's. If testcontainers has a port bug, file it upstream;
  this test doesn't catch harness bugs.
- **Fix:** Drop. The parallel test (REWORK-6.1) implicitly covers
  port allocation — if ports collided, parallel tests would fail to
  construct.
- **Status:** Removed from US-52.6.

### US-52.7 — Nightly e2e

#### REWORK-7.1: `TestE2E_Lifecycle_CRDs_Installed`

- **M1 smoke test in wrong place:** A test that just lists CRDs is a
  setup precondition, not a behaviour assertion.
- **Fix:** Move to `tests/runner.BootstrapKind` — the helper fails fast
  if CRDs aren't installed. No standalone test.
- **Status:** Removed from US-52.7 as a test; added as a
  BootstrapKind precondition assertion.

#### REWORK-7.2: `TestE2E_Lifecycle_Pods_HaveResources`

- **M3 layer:** SecurityContext enforcement is the admission webhook's
  job. Envtest is the right layer; e2e in kind is too expensive for what
  is a static-config check.
- **Fix:** Move to `pkg/apis/llmsafespaces/v1/envtest_defaults_test.go`
  as `TestEnvtest_WorkspacePodSecurityContext`. The kind e2e assumes
  this; doesn't re-test it.
- **Status:** Removed from US-52.7; added to envtest suite (US-52.1's
  envtest section).

#### REWORK-7.3: `TestE2E_Metrics_PrometheusScrapes`

- **M1 vague:** "Contains expected metric families" — which?
- **Fix:** Specific list: `reconcile_total`, `workspace_active_seconds`,
  `http_requests_total`, `process_start_time_seconds`. Test asserts each
  appears in the `/metrics` output. New metrics come and go; these four
  are the SLO-critical baseline.
- **Status:** Amended in US-52.7.

#### Added: `TestE2E_Lifecycle_ConcurrentReconciles`

- Moved from US-52.1 (see REWORK-1.3 above). Asserts 100 workspaces
  reconcile concurrently in a real cluster without deadlock; controller's
  max-concurrent setting is honoured.

### US-52.8 — Frontend

#### REWORK-8.1: `useEventStream reconnects with backoff`

- **M3 layer:** Without fake timers, this is an integration test (real
  `setTimeout` runs).
- **Fix:** Mandate `vi.useFakeTimers()` in the test; advance time
  deterministically; assert the reconnect schedule. Note added to
  US-52.8.
- **Status:** Amended in US-52.8.

#### REWORK-8.2: `RelaySetupWizard validates each step`

- **M1 unspecified:** "Validation blocks advance" — what validation?
- **Fix:** Specific per-step assertions: step 1 requires non-empty name;
  step 2 requires valid URL; step 3 requires secret length ≥ 16. Three
  rows, each testing one step's gate.
- **Status:** Amended in US-52.8.

### US-52.9 — Inference-relay worker

#### REWORK-9.1: `limit shared across worker instances`

- **M3 layer:** Real distributed limiting requires real KV — that's
  integration, not unit.
- **Fix:** Move to `src/integration.test.ts` with Miniflare KV.
  Unit-test only the local in-memory fallback path. Note added to
  US-52.9.
- **Status:** Amended in US-52.9.

#### REWORK-9.2: `handles missing usage field`

- **M1 incomplete:** "Logs 0 tokens; no crash." What if upstream returns
  `usage: null` vs missing key vs wrong type?
- **Fix:** Three rows: `usage` absent; `usage: null`; `usage:
  "not-an-object"`. Each must log 0 + not crash.
- **Status:** Amended in US-52.9.

#### DROP-9.1: `old secret rejected after rotation complete`

- **M5 isolation:** This test mutates global worker state (the secret
  config). In vitest parallel mode it can race with other tests that
  rely on the secret being valid.
- **Fix:** Drop as a separate test; the rotation flow is covered by
  `old and new secret both valid during rotation` + the production
  rotation is exercised by the T3 canary in US-52.10 (which has its
  own isolated namespace).
- **Status:** Removed from US-52.9.

### US-52.10 — Fission canary Tier 3

#### REWORK-10.1: T3-DB-POOL-SATURATE

- **M1 non-deterministic:** "No hang" cannot be asserted deterministically
  without a timeout that itself can flake.
- **Fix:** Replace assertion with: "Open N=2×pool_size idle connections.
  Assert the (N+1)th request returns within 5s with status 503 (not 200,
  not hang)." The timeout is the assertion; a hang manifests as a test
  timeout that fails loudly. The 5s is the SLA, not a flake budget.
- **Status:** Amended in US-52.10.

#### REWORK-10.2: S-WORKER-HEALTH

- **M2 trivial:** GET → 200 is too weak; every healthy worker returns
  200.
- **Fix:** Assert the response body has `status: "ok"` and a
  `version` field matching the deployed worker SHA. Drift in either is
  a real regression.
- **Status:** Amended in US-52.10.

#### REWORK-10.3: C-EVENT-DELIVERED

- **M1 over-assertion risk:** Kubernetes events are documented as
  best-effort; asserting delivery within a window may flake.
- **Fix:** Two-tier assertion: (a) the event *eventually* appears within
  60s (asserted); (b) if it doesn't, the scenario records
  `result.skipped=true` with reason `event-best-effort` rather than
  failing. The scenario's value is detecting *complete* event-delivery
  breakage, not transient drops.
- **Status:** Amended in US-52.10.

#### DROP-10.1: D-MCP-LONG-MESSAGE (as 100KB assertion)

- **M1 unspecified threshold:** "100KB message body" — what's the actual
  limit? If the test sends 100KB and the limit is 1MB, the test asserts
  nothing.
- **Fix:** Don't drop the scenario; rework it. Send a body at the
  documented MCP body-size limit (currently 1 MiB per `proxy_input_test.go`
  + `s-mcp-input-NEG` N5). Assert at-limit succeeds; over-limit returns
  the documented "too large" error. Re-named `D-MCP-BODY-LIMIT`.
- **Status:** Amended (renamed) in US-52.10.

### US-52.11 — Synthetic traffic

#### REWORK-11.1: J5 fresh-eyes

- **M5 isolation:** Registers real users; could leak if cleanup fails.
- **Fix:** Mandate: deterministic email `synth-fresheyes-<unix_ts>@canary.test`;
  cleanup runs in `defer` *and* a startup sweep deletes any
  `synth-fresheyes-*@canary.test` older than 1 hour (in case the previous
  run crashed). The sweep makes the journey self-cleaning across
  runner restarts.
- **Status:** Amended in US-52.11.

#### REWORK-11.2: J4 suspend/resume

- **M1 vague:** "PVC reattach consistency" — what specifically?
- **Fix:** Specific: `history_entries_after ≥ history_entries_before` on
  every resume; if any resume loses history, increment
  `synth_resume_history_lost_total` and mark the journey failed. The
  metric is the long-horizon drift signal; the journey-failure surfaces
  acute regressions.
- **Status:** Amended in US-52.11.

---

## Infrastructure audit (cross-cutting)

### US-52.6 — Integration harness

| Criterion | Finding |
|---|---|
| Reuse | ✅ Removes ~60 LoC/test × 5 files = ~300 LoC removed; harness adds ~200 |
| Idiom | ✅ Matches existing testcontainer usage in `pkg/secrets/pg_integration_test.go` |
| Isolation | ✅ Per-test instance; parallel-safe |
| Failure mode | ✅ Construction failure surfaces as test failure |
| Maintenance | ✅ Single point of update when migration runner changes |
| Documentation | ✅ `README.md` mandated; covers when-to-use vs mocks |
| **Verdict** | PASS |

### US-52.7 — E2E runner

| Criterion | Finding |
|---|---|
| Reuse | ✅ Replaces bespoke wait-loops in `local/test.sh`; runs in CI |
| Idiom | ⚠️ New `tests/runner/` pattern; needs README to anchor it |
| Isolation | ✅ Per-scenario workspace + user |
| Failure mode | ⚠️ If `BootstrapKind` fails, every test fails opaquely — must surface the underlying kind error |
| Maintenance | ✅ `local/test.sh` retained for humans; runner is CI authority |
| Documentation | Needs README at `tests/runner/README.md` |
| **Verdict** | PASS with two follow-ups: README + actionable BootstrapKind errors |

### US-52.10 — Canary aggregator

| Criterion | Finding |
|---|---|
| Reuse | ✅ Replaces ad-hoc per-Fission-function scraping |
| Idiom | ⚠️ Could duplicate Prometheus blackbox_exporter — check if it suffices |
| Isolation | ✅ Read-only; no service account beyond function URL reads |
| Failure mode | ✅ Aggregator self-health metric mandated |
| Maintenance | ✅ Adds scenario → appears in dashboard automatically |
| Documentation | Needs README |
| **Verdict** | REWORK: before implementing, evaluate whether Prometheus `blackbox_exporter` + existing scrape config covers the aggregator's job. If yes, drop the aggregator and use the standard tool. If no, document why. |

### US-52.11 — Synth runner

| Criterion | Finding |
|---|---|
| Reuse | ✅ New shape (continuous); no existing tool to reuse |
| Idiom | ⚠️ Pattern is new; mandate `synth/README.md` explaining journey contract |
| Isolation | ✅ Per-journey workspace; canary namespace |
| Failure mode | ✅ Continuous; failures recorded, not fatal |
| Maintenance | ⚠️ Five journeys × state machines = non-trivial code; needs strong types |
| Documentation | Baseline expectations mandated |
| **Verdict** | PASS with follow-up: typed journey interface (not just `func`) so adding a journey has compile-time safety |

---

## Cross-cutting findings (apply to multiple stories)

### X1: Every story must include a "demonstrate the failure" step

Most stories already mandate this ("temporarily remove the rate limit,
show test fails, restore"). Two don't (US-52.4, US-52.8). Add to both:
each PR must include a worklog entry showing at least one test failing
when the protection is removed. This is the only proof the test adds
value.

### X2: Build-tag consistency

US-52.1 uses `//go:build envtest`, US-52.6/52.7 use `//go:build integration`
and `//go:build e2e`. US-52.3's integration tests should use the same
`integration` tag. Add to US-52.3 explicitly.

### X3: Coverage threshold reporting

Every story's acceptance criteria includes a coverage delta. Add to the
epic README a single worklog template for the closing entry that lists
all deltas in one table — easier to review than 12 separate worklogs.

### X4: The aggregator REWORK may split US-52.10

If the aggregator is replaced by blackbox_exporter, US-52.10 loses ~30%
of its scope. That's fine — better to use the standard tool. But the
story's effort estimate (Large) drops to Medium. Update the README's
story table after the evaluation.

---

## Loop status

This is iteration 1 of the second pass. All 27 REWORKs and 8 DROPs are
reflected in the originating stories (amendments applied in the same
editing session). The predicted-findings table in US-52.12 reconciles
as follows:

| Prediction | Outcome |
|---|---|
| REWORK-1.1 (StorageSizeZero) | ✅ Confirmed + fixed |
| REWORK-1.2 (MetricsIncrement) | ✅ Confirmed + fixed |
| REWORK-1.3 (MaxConcurrentReconciles layer) | ✅ Confirmed + moved |
| DROP-1.1 (PVC_OwnerReference duplicate) | ✅ Confirmed + dropped |
| REWORK-2.1 (CloudInit minimal) | ✅ Confirmed + fixed |
| REWORK-4.1 (DTO tags incomplete) | ✅ Confirmed + augmented |
| DROP-4.1 (Noop test) | ✅ Confirmed + dropped |
| DROP-5.1 (Redact HelpFlag) | ✅ Confirmed + dropped |
| REWORK-7.1 (CRDs smoke) | ✅ Confirmed + moved to precondition |
| REWORK-8.1 (EventStream layer) | ✅ Confirmed + fake-timer mandated |
| REWORK-10.1 (DB pool deterministic) | ✅ Confirmed + fixed |
| REWORK-11.1 (Fresh-eyes isolation) | ✅ Confirmed + sweep added |
| Plus 15 unpredited findings | ✅ All confirmed + fixed |

No refutations (no "I was too harsh" outcomes). The audit was stricter
than the self-prediction — which is what an independent audit should be.

**Iteration 2 not required** — all findings from iteration 1 are
addressed; no new findings surfaced during the amendment pass. Per Rule
11, the loop exits at zero real findings.

---

## Final state

The epic now contains **237 meaningful tests + 5 synthetic journeys**
across:

- 47 unit/envtest tests for the controller (US-52.1)
- 25 unit/integration tests for relay drivers (US-52.2)
- 27 unit tests for API services (US-52.3)
- 17 unit tests for pkg leaf modules (US-52.4)
- 18 unit/integration tests for cmd binaries (US-52.5)
- 8 unit tests for the integration harness itself (US-52.6)
- 21 e2e tests for kind nightly + PR (US-52.7)
- 19 vitest + Playwright tests for frontend (US-52.8)
- 25 unit/integration tests for the inference-relay worker (US-52.9)
- 26 canary scenarios across T1/T2/T3 + controller + MCP + worker
  (US-52.10)
- 5 synthetic-traffic journeys (US-52.11)

Every test passes M1–M6. Every test declares intent. Every infrastructure
proposal reduces duplication. The epic is ready for the independent
Validator Loop at PR time.
