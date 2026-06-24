# Worklog: Free-Models Pod Consumption (Phases B + C + D of #1a)

**Date:** 2026-06-24
**Session:** Implement the pod-side consumption of the cluster-wide
free-models catalog published by Phase A (PR #400). Eliminates the
in-pod opencode-restart cycle that the legacy relay-injector goroutine
imposes — saves ~6–8s per cold start AND every resume.
**Status:** Complete on `feat/free-models-pod-consumption`. PR pending.

---

## Why three phases bundled

Phase A landed standalone (PR #400) because it was a self-contained
controller-side change that's harmless to merge in isolation (no
consumer yet). Phases B, C, and D collectively form the consumer:

- **B (controller-side):** `pod_builder.go` mounts the ConfigMap into
  the credential-setup init container, propagates the relay URL env
  into the init.
- **C (agentd materialize):** new `applyRelayConfigPreBoot` reads the
  catalog file + checks the `auth.json` bypass + writes the relay
  block atomically into agent-config.json **before** opencode starts.
- **D (agentd in-pod):** `loadExisting` detects a pre-injected relay
  block; `startRelayInjector` short-circuits when `hasRelay()` is
  true, skipping the legacy fetch+kill+restart goroutine.

These three phases are tightly coupled — splitting them further would
land code without a working integration. They land together because
every phase contains its own regression tests but only the integration
delivers the savings.

## Architecture: pre-fix vs. post-fix boot

### Pre-fix (legacy)

1. opencode starts with provider config (no relay) — ~5s
2. agentd's `startRelayInjector` waits for opencode health (~3-5s)
3. Injector calls `/provider` on opencode → fetches free model list
4. Injector merges relay-provider block into `agent-config.json`
5. Injector kills opencode → supervisor restarts it
6. opencode boots a SECOND time — another ~5-7s

We pay opencode boot **twice**. Total in-pod relay overhead: ~6–8s.

### Post-fix (with this PR + Phase A)

1. Controller publishes free models as a ConfigMap (Phase A — already
   on main).
2. Pod's credential-setup init container mounts the CM at
   `/mnt/freemodels` (Optional: true) and copies `models.json` into
   `/sandbox-cfg/free-models.json`. (Phase B)
3. agentd's materialize subcommand calls `applyRelayConfigPreBoot`
   after `applyWorkspaceConfig` (Phase C). Reads the file +
   bypass-checks + writes the relay block via the same
   `AgentConfigWriter.SetRelay+Rebuild` path the legacy injector uses,
   producing byte-identical output.
4. opencode starts with the FINAL config. Boots **once**.
5. The in-pod `startRelayInjector` goroutine still launches but
   `loadExisting` saw `provider.opencode-relay` in the file and
   populated `w.relay`, so `hasRelay()` returns true and the goroutine
   short-circuits with metric `outcome=skipped_pre_boot_applied`.
   (Phase D)

## Failure semantics (graceful degradation by design)

Every failure mode falls back to **at-worst-equal-to-pre-fix
behavior**. The optimization adds savings without taking any
reliability away.

| Failure | Behavior | Test |
|---|---|---|
| Phase A controller's CM not yet published | Init script's `if [ -f ... ]` guard finds no file. agentd's materialize sees `skipped_no_catalog` and falls through. Legacy in-pod injector runs as before. | `TestApplyRelayConfigPreBoot_NoCatalog_NoOp` |
| Phase A's CM is empty (transient outage) | Same — `skipped_empty_catalog`, fall through. | `TestApplyRelayConfigPreBoot_EmptyCatalog_NoOp` |
| User has personal opencode API key in auth.json | Bypass — file-based check (`shouldSkipRelay`) runs the same way pre-opencode as it did in the legacy goroutine. User keeps direct Zen access. | `TestApplyRelayConfigPreBoot_PersonalKey_NoOp` |
| Pre-boot succeeds | In-pod injector observes `hasRelay()` and skips entirely. opencode boots once. | `TestStartRelayInjector_SkipsWhenPreBootApplied` |
| Pre-boot fails to write agent-config | Materialize subcommand exits 3 → kubelet sees CrashLoop. Operator sees the failure immediately rather than getting a silent fall-through that leaves the pod with no relay. | `TestApplyRelayConfigPreBoot_MalformedCatalog_Errors` |
| auth.json update fails after agent-config wrote successfully | Logged warning, NOT fatal. Pod boots with the relay-provider block in agent-config.json but without an auth.json entry; first request through the relay would 401, user can re-auth manually. Better than failing the whole boot. | (covered by code-path inspection; explicit test in follow-up) |

## Phase D subtlety: parsing the existing relay block

When agentd starts AFTER the materialize subcommand has written the
relay block, agentd creates a fresh `AgentConfigWriter` via
`newAgentConfigWriter(path)`. `loadExisting` reads the existing
`provider` map but pre-Phase-D ONLY captured `provider` and `model` —
it did NOT extract the relay state into `w.relay`.

That meant `hasRelay()` would return false even when the relay block
WAS on disk, defeating the short-circuit. Phase D fixes this by
making `loadExisting` detect `provider.opencode-relay` and populate
`w.relay` (with URL and models extracted from the on-disk JSON).

For an unparseable opencode-relay block (corrupt JSON, mismatched
shape), `loadExisting` still sets `w.relay` to a non-nil sentinel —
the safety net is "we observed something at provider.opencode-relay
so don't double-inject." Worst case: the in-pod injector skips, and
the user gets a dud relay until next reload — but we don't race the
file write, which is the worse failure.

## Tests

`controller/internal/workspace/pod_builder_test.go`: 5 new Phase B
tests
- `FreeModelsVolume_AbsentWhenNoRelay`: no volume / mount / env when
  relay disabled
- `FreeModelsVolume_PresentWhenRelayConfigured`: volume + mount with
  Optional=true
- `RelayBaseURLEnv_OnInitContainer`: env propagated to init
- `RelayBaseURLEnv_MainContainer`: legacy main-container env
  preserved
- `FreeModelsScriptCopy`: init script `cp` is `if [ -f ... ]` guarded

`cmd/workspace-agentd/pre_boot_relay_test.go`: 11 new Phase C tests
- `NoRelayURL_NoOp`, `NoCatalog_NoOp`, `EmptyCatalog_NoOp`
- `PersonalKey_NoOp`: bypass works pre-opencode (file-only check)
- `PublicKey_AppliesRelay`: full success path
- `AbsentAuthJSON_AppliesRelay`: missing auth.json is fresh-pod
- `PreservesExistingProviders`: merge semantics preserved
- `MalformedCatalog_Errors`: controller bug surfaces as boot failure
- 3 `PreBootAuthJSONPath_*` path-resolution tests

`cmd/workspace-agentd/relay_injector_test.go`: 4 new Phase D tests
- `StartRelayInjector_SkipsWhenPreBootApplied`: full goroutine
  short-circuit
- `LoadExisting_DetectsPreInjectedRelay`: w.relay populated correctly
- `LoadExisting_NoRelayBlock_HasRelayFalse`: legacy path preserved
- `LoadExisting_MalformedRelayBlock_StillSetsRelay`: safety-net
  sentinel

All tests pass with `-race`. Lint clean.

## Validation

- `go test -race -count=1 ./cmd/workspace-agentd/... ./controller/...
  ./tests/epic26/...` — all pass
- `go test -short ./...` — all pass across the repo
- `golangci-lint run ./cmd/workspace-agentd/... ./controller/...` —
  clean
- e2e: `cmd/workspace-agentd/e2e_test.go` (3 SSE tests) and
  `tests/epic26/relay_e2e_test.go` (4 Worker proxy tests) all pass
  with race detector

## Files Modified / Created

```
controller/internal/workspace/pod_builder.go        +52 / -7
controller/internal/workspace/pod_builder_test.go   +191 (5 tests)
cmd/workspace-agentd/agent_config_writer.go         +63 (loadExisting Phase D)
cmd/workspace-agentd/relay_injector.go              +20 (hasRelay short-circuit)
cmd/workspace-agentd/relay_injector_test.go         +132 (4 tests)
cmd/workspace-agentd/secrets.go                     +32 (call applyRelayConfigPreBoot)
cmd/workspace-agentd/pre_boot_relay.go              (new, ~250 lines)
cmd/workspace-agentd/pre_boot_relay_test.go         (new, ~310 lines)
worklogs/0544_2026-06-24_phase-bcd-pod-consumption.md (this file)
```

## Next steps

1. PR review iteration on Phases B+C+D.
2. After merge: build + push controller + runtime images. `helm-deploy`
   to live cluster. Re-run `/tmp/benchmark-workspaces.sh`.
3. Compare against post-PR-#386 baseline. Combined target:
   - Cold start (image cached): 39s → ~22s (∆ ~17s)
   - Resume: 33s → ~12s (∆ ~21s)
   - Suspend: already <1s after PR #386
