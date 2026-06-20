# Worklog: Epic 33 — Observability, Metering, and Billing Infrastructure

**Date:** 2026-06-19
**Session:** Implement all 12 user stories of Epic 33 (billing pipeline + optional observability stack) following the Orchestrator Workflow (context → TDD delegation → skeptical validation loop → remediation → verification).
**Status:** Complete

---

## Objective

Implement Epic 33 (`design/stories/epic-33-observability-metrics-billing-events/README.md`): a second-granularity, agentd-sourced billing pipeline (agentd → WAL → events-gateway → Postgres `usage_events`) with an optional VictoriaMetrics/Grafana observability fan-out. The pipeline has zero external dependencies beyond the Postgres the platform already requires (Decision 7).

---

## Work Completed

### Phase 1 — billing pipeline foundation (parallel)

- **US-33.4 `pkg/events/`** — typed event constants (19 event types, Tier 1/2 split via `IsTier1`), `WorkspaceEvent`/`ResourceSample`/`InferenceEvent`/`IngestRequest`/`WALEntry` types, `Writer`/`NoopWriter`/`RecordingWriter`, synchronous `GatewayClient`. `go test ./pkg/events/...` green. Importable by agentd, controller, API, gateway.
- **US-33.6 migrations** — `000039_workspace_events.{up,down}.sql` (`workspace_events` + `workspace_events_dlq`, severity CHECK, 3 indexes) and `000040_usage_events_source_agentd.{up,down}.sql` (extends `usage_events.source` CHECK to add `'agentd'`; constraint recreated by name `usage_events_source_check` for idempotency). Byte-identical in `api/migrations/` and `charts/llmsafespaces/migrations/` (repolint rule).

### Phase 2 — events-gateway

- **US-33.5 `cmd/events-gateway/`** — Gin `POST /ingest`. Tier-1 state events written to Postgres (`workspace_events`) synchronously before 202; returns **503 on Postgres failure** so agentd retries from WAL. `store.WriteComputeSeconds` derives a single `compute_seconds` `usage_events` row from `pod_terminated`/`pod_suspended` events' `detail.started_at` (event-boundary billing precision, Decision 2). Inference → `llm_tokens` rows. Resource samples → optional Prometheus remote_write TSDB (`tsdb.go` + local `prompb.go` + `golang/snappy`); nil-safe noop when `EVENTS_TSDB_URL` unset. Self-metrics, async writer + DLQ, Helm templates (deployment/service/network-policy). 46 tests, sqlmock + httptest.

### Phase 3 — producers (parallel)

- **US-33.7 `cmd/workspace-agentd/events/`** — file-per-entry WAL at `/tmp/agentd-wal/` (PVC `tmp` subPath — survives pod crashes/OOM/eviction/suspend; NOT a new emptyDir). `GatewayClient` satisfies `events.Writer`: WAL-protects Tier 1 (Append→POST→Confirm; on 503 keeps the entry, returns nil so producers continue; on WAL-full falls back to direct push), async-batches Tier 2 + resource samples + inference (1s flush), replays unconfirmed entries on `Start()`. 26 tests incl. outage→accumulate→replay→drain.
- **US-33.10 `controller/internal/workspace/{events,gap_detection}.go`** — reconciler gains `events.Writer` (NoopWriter default; nil-safe `emitEvent`). Emits Tier 1 (`workspace_failed`, `workspace_recovery_exhausted`, `workspace_safe_mode_entered`, `workspace_oom_killed`) and Tier 2 lifecycle events at transition points. `reconcileStaleComputePeriods` (60s, leader-gated): verifies the pod is **actually gone** (NotFound/non-Running) before closing, in-memory StartTime-keyed dedup, `started_at` from `ws.Status.StartTime`. 22 tests.
- **US-33.11 `api/internal/services/auth/events.go`** — `auth_failure`/`account_locked`/`api_key_created`/`api_key_revoked` (Tier 2). IP masked to /24 (v4) / /64 (v6); API-key events carry only an 8-char prefix; no passwords/emails/JWTs/full keys (asserted in tests). `SetEventWriter` setter wired in `services.go` (NoopWriter default).

### Phase 4 — agentd producers (parallel)

- **US-33.8 `cmd/workspace-agentd/{pod_state,resource_sampler}.go`** — `PodStateTracker` fires idempotent `pod_ready` (billing start, with `started_at`) at the first healthy readyz snapshot and `pod_terminated` (with `started_at`) on graceful shutdown; `ResourceSampler` pushes 1s cgroup samples. Wired into `main.go` with the critical shutdown ordering **ResourceSampler.Stop → EmitTerminated → eventWriter.Stop**.
- **US-33.9 `cmd/workspace-agentd/session_tracker.go`** — parses opencode `session.updated` cumulative tokens → per-delta `InferenceEvent`; `session_completed` (Tier 1) on idle; `session_interrupted` (Tier 1) on SSE disconnect while busy. Delta semantics validated against the wire format.

### Skeptical validation loop (MANDATORY) — 2 real billing bugs found & fixed

A separate validator sub-agent (independent of all implementers) traced the full billing path and found two defects invisible to the single-event + sqlmock test suites:

- **B1 (under-billing):** inference token deltas shared one idempotency key (`llmtokens:<session>:<StartedAt.UnixNano()>`) because `InferenceEvent.StartedAt` was the constant `firstSeenAt` → only the first delta of a session was kept, the rest dropped by `ON CONFLICT DO NOTHING`. **Fix:** `session_tracker.go:513` sets `StartedAt: time.Now()` per delta (stable across the client's internal retries, unique per delta). Regression test `TestInferenceEvent_DeltasHaveDistinctStartedAt` proven to fail without the fix.
- **B2 (lost gap-closure):** controller gap-close events carry `source=controller_gap_close`, which is **not** in the `usage_events.source` CHECK → the closing `compute_seconds` INSERT violated the constraint → 503 → agentd-crash gap periods were never closed. **Fix:** `store.go` `usageEventsSource()` maps `controller_gap_close → "reconciliation"` (a CHECK-legal value; matches the US-33.10 DoD). Regression test `TestWriteComputeSeconds_ControllerGapClose_MapsSourceToReconciliation`.

### Phase 5/6 — optional observability (gated on `monitoring.tsdb.deploy`, default off)

- **US-33.1** — removed `user_id` from `api_active_connections` (O(users) cardinality); deleted `workspaces_created_total` (duplicate of controller's) and `workspace_resource_usage` (never populated). Interface/mocks/5 call sites + tests updated. (Sub-worklog 0398.) `RecordRelayInjector` confirmed already absent.
- **US-33.2** — `victoria-metrics.yaml` (StatefulSet + Service + NetworkPolicy + PVC, retention from values).
- **US-33.3** — `vmagent.yaml` (ServiceAccount/RBAC + scrape ConfigMap for cAdvisor/api/controller + Deployment). Controller metrics addr overridden to `0.0.0.0:8080` when tsdb on (8081 is the probe addr — kept 8080 to avoid conflict). `EVENTS_TSDB_URL` auto-wired to the in-cluster VM.
- **US-33.12** — 5 Epic 33 alerts (`GatewayDropping`, `GatewayBufferPressure`, `WALSizeHigh`, `ComputePeriodStale`, `ObservabilityBlind` [gated on tsdb.deploy]) with values-configurable thresholds; 7 gateway-health panels in `operational.json` + pod-state panel in `billing.json`.
- **Wiring gap closed by orchestrator:** the events-gateway `/metrics` was emitted but never scraped → added a gateway **ServiceMonitor** (`servicemonitor.yaml`, gated on `eventsGateway.enabled`) and a **vmagent** scrape target so the new gateway dashboards/alerts receive data.

---

## Key Decisions

1. **agentd emits `pod_ready`/`pod_terminated` only** — it cannot distinguish suspend from terminate at SIGTERM (both delete the pod). The controller owns suspend/resume classification (Decision 1). Billing is exact either way; both close the period. Documented assumption, validated: no in-pod suspend signal exists.
2. **Gateway is stateless; `started_at` travels in the event detail.** agentd stamps `started_at` into both `pod_ready` and `pod_terminated`; the gateway computes `duration = occurred_at − started_at`. The controller's gap-close carries `started_at = ws.Status.StartTime`. This resolves the "how does a stateless gateway know the period start?" ambiguity in the original design.
3. **No new billing tables.** Per the 2026-06-19 design revalidation, `compute_periods`/`inference_events` were dropped; the gateway writes the existing Epic 12 `usage_events` (`source='agentd'`, migration 000040). Stripe exporter + `/api/v1/usage` read `usage_events` unchanged.
4. **Deferred design flaws (D1–D3), each with rationale:**
   - **D1 (scoped `events_gateway` DB role)** — deferred: the `migrate/migrate` runner cannot safely provision a LOGIN password (no psql variable support → would hardcode a secret); the project has zero precedent for scoped roles (design revalidation A3); the gateway is cluster-internal + parameterized-only queries. Remediation path: operator-bootstrapped role + secret, documented.
   - **D2 (narrow agentd-vs-controller `started_at` double-billing race)** — deferred: requires stateful gateway dedup; probability is very low (needs gateway-down + pod-gone + phase-still-Active + >90s stale simultaneously, and the pod-gone guard usually prevents it). Documented as a known limitation.
   - **D3 (duplicate `workspace_events` rows on Tier-1 WAL replay)** — deferred: the billing `compute_seconds` row is correctly idempotent; an aggressive unique constraint to dedup the operational-log rows would risk dropping a legitimate billing-boundary event (worse than duplicate operational noise). Accepted as noise.
5. **Snappy/protobuf for remote_write** — promoted `golang/snappy` + `google.golang.org/protobuf` to direct deps rather than pulling in the heavyweight `prometheus/prometheus` (local `prompb.go`). Keeps the gateway minimal (Rule 4).

---

## Blockers

None.

---

## Tests Run

- `go build ./...` → OK (whole module compiles).
- `go test -race -count=1 ./pkg/events/...` → ok (1.07s)
- `go test -race -count=1 ./cmd/events-gateway/...` → ok (1.09s)
- `go test -race -count=1 ./cmd/workspace-agentd/events/...` → ok (4.78s)
- `go test -race -count=1 ./cmd/workspace-agentd/...` → ok (46s; full pkg incl. US-33.8/33.9)
- `go test -race -count=1 ./controller/...` → ok (all subpkgs)
- `go test -race -count=1 ./api/internal/services/metrics/...` → ok
- `go test -race -count=1 -timeout 300s ./api/internal/services/auth/...` → ok (219s; bcrypt-cost-12-bound, pre-existing — needs >180s)
- Regression tests for B1/B2: proven to fail without the fix, pass with it.
- `helm lint charts/llmsafespaces` → 1 chart linted, 0 failed.
- `helm template` (defaults) → 0 VM/vmagent resources, no `EVENTS_TSDB_URL` (billing path standalone). `--set monitoring.tsdb.deploy=true` → VM StatefulSet/Service/NP + vmagent + `EVENTS_TSDB_URL=http://<release>-victoria-metrics:8428/api/v1/write` + 3 vmagent scrape jobs (cAdvisor/api/controller/**events-gateway**) + `ObservabilityBlind` alert. ServiceMonitor count: 3 with gateway on, 2 with gateway off.

---

## Next Steps

1. **Commit & PR cycle** (not done — no commit was requested): branch `feat/epic-33-observability-billing`, squash-merge after the AI reviewer APPROVE per the branch/PR workflow. This worklog + sub-worklog 0398 document the session.
2. **D1 follow-up:** operator-bootstrap a scoped `events_gateway` LOGIN role (INSERT-only on `usage_events`/`workspace_events`/`workspace_events_dlq`) + K8s Secret; wire `eventsGateway.database.user`/password. Track as a hardening task.
3. **D2 follow-up (if billing precision becomes a concern):** add gateway-side dedup of overlapping open compute periods per workspace (query `usage_events` before inserting a gap-close `compute_seconds`), or align both emitters on `ws.Status.StartTime`.
4. **End-to-end on a kind cluster:** `local/` scripts to bring up the stack with `monitoring.tsdb.deploy=true`, confirm a real workspace produces `pod_ready` → `compute_seconds` → `pod_terminated` rows in `usage_events` and the gateway/agentd panels light up in Grafana. (Unit/integration tests cover the wiring; a live cluster e2e is the remaining confidence gate.)
5. **Deprecate Epic 12's `reconcileComputeTime` + SSE `onInference`** per the Coexistence section — now superseded by the higher-precision agentd-sourced path. Coordinate with any in-flight Epic 12 work first.

---

## Files Modified

**New code**
- `pkg/events/` — `doc.go`, `types.go`, `writer.go`, `client.go`, `*_test.go`
- `cmd/events-gateway/` — `main.go`, `config.go`, `server.go`, `store.go`, `async_writer.go`, `tsdb.go`, `prompb.go`, `metrics.go`, `*_test.go`
- `cmd/workspace-agentd/events/` — `wal.go`, `client.go`, `metrics.go`, `events.go`, `*_test.go`
- `cmd/workspace-agentd/` — `pod_state.go`, `resource_sampler.go`, `events_wiring.go`, `*_test.go`
- `controller/internal/workspace/` — `events.go`, `gap_detection.go`, `*_test.go`; `controller/internal/controller/events_logging.go`
- `api/internal/services/auth/events.go`, `auth_events*_test.go`

**Migrations** (synced api/ ↔ charts/.../migrations/)
- `000039_workspace_events.{up,down}.sql`, `000040_usage_events_source_agentd.{up,down}.sql`

**Modified (wiring)**
- `cmd/workspace-agentd/main.go`, `server.go`, `healthz_cache.go`, `session_tracker.go`, `agent_config_writer.go`
- `controller/.../reconciler.go`, `recovery_policy.go`, `health.go`, `phase_{creating,active,pending,terminating}.go`, `controller.go`, `controller/main.go`
- `api/internal/services/auth/auth.go`, `api/internal/server/router.go`, `api/internal/services/services.go`
- `api/internal/services/metrics/metrics.go` + interface/mock/call sites (US-33.1)

**Helm** (`charts/llmsafespaces/`)
- New: `templates/events-gateway-{deployment,service,network-policy}.yaml`, `templates/victoria-metrics.yaml`, `templates/vmagent.yaml`
- Modified: `templates/{servicemonitor,prometheus-rules,controller-deployment,controller-service,datastore-network-policy}.yaml`, `templates/_helpers.tpl`, `dashboards/operational.json`, `dashboards/billing.json`, `values.yaml`

**Worklogs**
- `0398_2026-06-19_us-33.1-metrics-cardinality-fixes.md` (sub-agent)
- `0442_2026-06-19_epic-33-observability-billing.md` (this entry)

---

## Follow-up: deployment-layer wiring + e2e (same session)

**Prompted by:** "Is everything wired e2e?" — honest answer was **no**. The Go code was internally consistent and unit-tested, but the K8s deployment layer never injected `EVENTS_GATEWAY_URL`, so in production every producer (agentd/controller/API) defaulted to NoopWriter/nil and **zero events were emitted**. This was the "unwired = dead code" failure the README E2E Wiring Verification section exists to catch — missed in the first validation pass (the milder gateway `/metrics` scrape gap was caught; this more fundamental env-injection gap was not).

### Three gaps closed

1. **agentd → gateway (primary billing path).** `controller/internal/workspace/pod_builder.go` now injects `EVENTS_GATEWAY_URL` + `WORKSPACE_USER_ID` (resolved from the `user-id` label, no DB lookup) into the agentd container when the reconciler's new `EventsGatewayURL` field is set; `WORKSPACE_ID` was already injected. The `workspace-dirs` init now also creates `/pvc/tmp/agentd-wal` (US-33.7 DoD). When unset, vars are omitted and agentd's `StartEvents` returns a nil client (feature off).
2. **API server events.** `api-deployment.yaml` now sets `EVENTS_GATEWAY_URL` (auth/apikey emission, US-33.11).
3. **Controller events + gap-closer.** `controller-deployment.yaml` now passes `--events-gateway-url` (US-33.10).

### How the URL is resolved (billing on by default)
New helper `llmsafespaces.eventsGateway.url` (`_helpers.tpl`): when `eventsGateway.enabled: true` (default) and `url` empty → derives `http://<release>-events-gateway.<ns>.svc:<port>` so billing is on out-of-the-box (Decision 7). Explicit `eventsGateway.url` wins. `enabled: false` + empty → empty everywhere → NoopWriter/nil (emission off).

### Tests added (the previously-missing e2e + wiring assertions)
- **`cmd/events-gateway/e2e_billing_test.go`** — real agentd `GatewayClient` + real WAL (tempdir) → real events-gateway Gin router+handler (httptest) → recording store. Fires `pod_ready` then `pod_terminated`; asserts both arrive over the real HTTP path, then feeds the captured `pod_terminated` into the real `Store.WriteComputeSeconds` and asserts the compute_seconds idempotency key (`compute:<ws>:<startedAtUnix>`) + quantity (rounds to the ~1.1s period) + source. Stable across 5 `-race` runs. No real Postgres in sandbox → DB split (recording store for the network path; real `Store` for derivation), both halves exercising real production code.
- **`controller/.../pod_builder_test.go`** — `TestPodBuilder_ContainerEnv_EventsGatewayInjection`: EVENTS_GATEWAY_URL + WORKSPACE_USER_ID injected when URL set; omitted when unset.
- **`charts/.../chart_test.go`** — `TestEpic33_EventsGatewayURL_{DefaultDerivesFromService,ExplicitOverrideWins,DisabledWhenGatewayOff}`: assert the Helm chart wires the URL into both the API env and controller args across all three cases.

### Verification
- `go build ./...` OK; gofmt clean; `go vet` clean.
- `go test -race ./cmd/events-gateway/...` (incl. e2e) ok; `./controller/...` ok; `./pkg/events/...` ok.
- Chart: `PATH+=/home/sandbox/.local/bin go test ./charts/llmsafespaces/...` ok (helm lint + 3 new wiring tests + existing).
- `helm template` confirmed: default → derived URL on API+controller; explicit `url` → verbatim; `enabled:false` → absent.

### Honest scope note
A live **kind-cluster** e2e (boot Postgres+controller+agentd+gateway, create a workspace, assert a real `compute_seconds` row in Postgres) is the remaining gold-standard confidence gate — not runnable here (no docker/kind/postgres in this sandbox). The Go e2e + pod-builder + chart tests prove the path at every layer that *can* execute in this environment. The kind e2e is the recommended CI addition (see Next Steps).

### Additional files modified this follow-up
- `controller/internal/workspace/reconciler.go` (+`EventsGatewayURL` field), `controller/internal/controller/controller.go` (propagate it), `controller/internal/workspace/pod_builder.go` (env injection + WAL dir), `controller/internal/workspace/pod_builder_test.go` (wiring test)
- `charts/llmsafespaces/templates/_helpers.tpl` (`eventsGateway.url` helper), `api-deployment.yaml`, `controller-deployment.yaml`, `values.yaml` (`eventsGateway.url`), `chart_test.go` (3 wiring tests)
- `cmd/events-gateway/e2e_billing_test.go` (new e2e)

---

## Follow-up 2: Multi-perspective full-layer audit + remediation

**Prompted by:** "Is everything wired e2e?" / "UX layers?" / "What's the right path forward?" — each probe surfaced a gap. The billing-focused validation (1st validator) had caught B1/B2, but never walked every layer. The user asked for a multi-agent, multi-perspective audit then reconcile.

### Audit method
Four independent skeptical validators, each scoped to a distinct layer (zero overlap with the already-audited billing-SQL correctness):
- **Layer A** (network/RBAC/services) — "can the packets flow?"
- **Layer B** (observability: TSDB/datasources/dashboards/alerts) — "do dashboards render with data?"
- **Layer C** (state/durability/DLQ/replay) — "write-only tables, async lifecycle, cross-restart dedup"
- **Layer D** (story-by-story DoD walk + config coherence + e2e gaps)

They reported **12 real findings** + several design flaws + 18 UNTESTED DoD items. Reconciled by dedup (Layer C's #3 double-billing = highest-severity new finding; Layer D's C2 = Layer C-adjacent; Layer B's false-alarm retraction of the `billing.json` postgres-binding premise was honored).

### Findings fixed (all real bugs remediated in one pass)

| # | Finding (layer) | Severity | Fix |
|---|---|---|---|
| 1 | agentd→gateway:4099 **blocked by default-deny egress** → billing dark by default (A) | critical | `workspace-network-policy.yaml` + egress rule to gateway svc (gated `eventsGateway.enabled`) |
| 2 | **DOUBLE BILLING on llm_tokens** — agentd + Epic-12 SSE `onInference` both write non-colliding keys (C) | critical | `app.go`: gate Epic-12 metering on `EVENTS_GATEWAY_URL` (agentd owns inference billing when gateway configured; `metricsSvc.RecordInference` operational counters stay unconditional) |
| 3 | controller gap-close **drops Tier-1 events on gateway failure** — `recordGapClose` unconditional despite swallowed emit error (D) | critical | `gap_detection.go`: `closeOrphanedCompute` returns error; mark-closed only on success; retry-safe via idempotency key |
| 4 | inference `llm_tokens` failures **land in wrong DLQ** (`workspace_events_dlq`, not `usage_events_dlq`) (C) | high | `store.go` `WriteInferenceDLQ` → `usage_events_dlq`; `async_writer.go` routes inference failures there |
| 5 | **No VM datasource provisioned** + `${datasource}` default resolves to nothing (B) | high | `grafana-datasources.yaml` provisions VictoriaMetrics datasource (gated `tsdb.deploy`, `isDefault:true`) |
| 6 | vmagent ClusterRole **missing `nodes/proxy`** → cAdvisor 403 (A) | high | `vmagent.yaml` ClusterRole + `nodes/proxy` |
| 7 | vmagent/gateway can't reach gateway `/metrics` + gateway ServiceMonitor unscrapeable (A) | high | `events-gateway-network-policy.yaml` + vmagent ingress (gated tsdb) + prometheus ingress (gated serviceMonitors) |
| 8 | `gateway_dlq_size` metric **declared but never Set** → always 0 (D) | medium | `async_writer.go` periodic gauge refresh counts both DLQs' unresolved rows |
| 9 | alert references **nonexistent metric** `..._consecutive_failures_max` (B) | medium | `prometheus-rules.yaml` → real metric `llmsafespaces_workspaces_in_recovery` |
| 10 | No scoped `events_gateway` DB role (US-33.5 DoD) (D, was D1) | low | **Deferred** — migrate tooling can't safely provision a LOGIN; documented as hardening follow-up |
| 11 | `monitoring.tsdb.deploy` works without `monitoring.enabled` (gating inversion) (D) | low | **Deferred** — benign (operator-intent); documented |
| 12 | 4 design alerts missing + 2 hardcoded thresholds (B/D) | low | **Deferred** — pre-existing design-doc drift, tracked |

### Confirmed-sound (validators VERIFIED, no action)
Cross-restart gap-detection dedup (DB idempotency key from deterministic `ws.Status.StartTime`); async-writer lifecycle (no goroutine leak, drains on stop); mixed-config safety (gateway-set/controller-unset is safe — events self-contain `started_at`); billing-on-by-default (Decision 7 conformance); Layer B false-alarm retraction honored (`billing.json` SQL panels correctly bind `${pg_datasource}`).

### E2E suite authored
`tests/epic33/billing_e2e_test.go` — env-gated (`LLMSAFESPACES_E2E=1` + `LLMSAFESPACES_PG_DSN`), skips cleanly by default so `go test ./...` stays green. Runnable in CI against a `local/bootstrap.sh` kind cluster. Covers the highest-value UNTESTED DoD items: `pod_ready` in `workspace_events` after Active (US-33.8); compute_seconds open+close with plausible duration (US-33.8); auth_failure row with masked IP (US-33.11); **no double-billing of llm_tokens** (the fix for #2 — asserts source='agentd' only, never source='api'). Remaining 14 UNTESTED items (VM `/health`, dashboard rendering, alert firing, kill-9 PVC WAL survival, etc.) are documented in Layer D's e2e matrix for incremental CI coverage.

### Verification (all green)
- `go build ./...` OK; gofmt clean (all changed dirs); `go vet` clean.
- `go test -race` on events-gateway / workspace-agentd(+events) / controller / pkg/events / metrics / sse / metering / app / tests/epic33 → all `ok`.
- `helm lint` 0 failed. Gating matrix confirmed: default → egress present, no VM/RBAC; full-monitoring → VM datasource (isDefault:true) + nodes/proxy + vmagent/prometheus ingress.
- Regression tests added for each fix (chart network/datasource tests; gap-close retry test proven red-then-green; DLQ-routing + gauge tests; TDD throughout).

### Honest scope
The kind e2e is authored and compiles/skips but **cannot be executed in this sandbox** (no docker/kind). It is the runnable CI gate for the 18 UNTESTED DoD items; the user runs it in CI. The 3 deferred items (#10 scoped role, #11 gating inversion, #12 alert/threshold drift) are documented, low-severity, and do not block billing correctness or DoD conformance for the load-bearing path.

### Files modified this follow-up
- **Chart:** `workspace-network-policy.yaml`, `events-gateway-network-policy.yaml`, `vmagent.yaml`, `grafana-datasources.yaml`, `prometheus-rules.yaml`, `chart_test.go`
- **Controller:** `gap_detection.go`, `gap_detection_test.go`
- **Gateway:** `store.go`, `async_writer.go`, `metrics.go`, `store_test.go`, `async_writer_test.go`
- **API:** `app.go` (double-billing gate)
- **New:** `tests/epic33/billing_e2e_test.go`
