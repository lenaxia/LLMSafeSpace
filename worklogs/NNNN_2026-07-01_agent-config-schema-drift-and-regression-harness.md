# Worklog: fix agent-config schema drift, add opencode-schema regression harness

**Date:** 2026-07-01
**Session:** Fix issue #486 — the agent-config.json writer emitted `agents.build.system` (both key name and field name wrong per opencode's config schema), taking down every session on chat.safespaces.dev after the #484 rollout exposed the latent bug.

**Status:** Complete

---

## Objective

**Immediate:** restore chat session functionality on the live cluster (P0 — every workspace with a non-empty org/platform prompt was returning HTTP 500 on GET session history + POST prompt).

**Structural:** close the class-of-bug where our test suite validates writer *intent* (does the field I emit match the string I put in?) but not the *external contract* with opencode (does opencode accept this config?). This is the same gap that let #486 slip past both my #484 review rounds — my round-trip tests validated the wrong shape and passed.

---

## Work Completed

### Investigation

- Reproduced the 500 with `kubectl exec` + `curl -u opencode:$(cat /sandbox-cfg/password) http://localhost:4096/session/$SES/message` inside a workspace pod — HTTP 500 with `UnknownError err_*`.
- Read opencode's own log at `/workspace/.local/opencode/log/<latest>.log`, found the stack trace ending at `Config.loadInstanceState` with `error=ConfigInvalidError`.
- Inspected `/sandbox-runtime/agent-config.json`; found the `agents.build.system` block.
- Cross-referenced with opencode's config schema at <https://opencode.ai/config.json>: top-level key is `agent` (singular), AgentConfig field is `prompt`, and Config has `additionalProperties: false` → any config with an `agents` key is rejected outright.
- Confirmed on live cluster: stripping the `agents` block from a broken workspace's agent-config.json and killing opencode restored HTTP 200 on `/session/*/message`.

### Validated assumptions

1. **The bug has been latent since PR #416 shipped.** Validated via git blame + reproducing the malformed emit on any commit post-#416 that had `w.adminPrompt != ""`. Was masked by the parallel #416 tmpfs-path bug (fixed in #484): before #484, the admin prompt was never written to disk, so `loadAdminPrompt` returned nothing, so `w.adminPrompt` stayed empty, so `rebuild()` skipped the malformed-emit branch entirely.
2. **The rename is `agents` → `agent` AND `system` → `prompt`.** Both are wrong. Validated by reading `AgentConfig` in the pinned schema (`opencode-config.schema.json`): AgentConfig has a `prompt` field of type string, no `system` field, and the top-level `agent` object has `build`/`plan`/`general`/etc. as property names. `additionalProperties: false` on Config makes both mistakes independently fatal.
3. **`santhosh-tekuri/jsonschema/v6` is already in `go.sum`.** Was pulled in indirectly by another dependency. Only had to promote it to direct in `go.mod`, no new dependency-version bump.
4. **Pinning the schema in-tree is the right call.** Fetch-at-test-time would (a) couple CI to opencode.ai availability, (b) give us undetectable drift when opencode updates the schema mid-run, (c) make failures un-bisectable. Pinning + REFRESH.md gives a clean git-log record of every schema change.
5. **Stripping external `$ref`s to models.dev is the right call.** The models.dev enum is 226 KB and changes weekly. The writer emits arbitrary provider/model strings from user config; we do not gate on models.dev membership. Pinning that too would explode the diff volume for zero contract-testing value. Programmatic strip at load time keeps the surface hermetic without dragging in the enum.

### Root fix

- `cmd/workspace-agentd/agent_config_writer.go`: renamed the emitted key `agents` → `agent`, the build-agent field `system` → `prompt`, internal Go field `w.agentsRaw` → `w.agentRaw`, and the `loadExisting` deserialiser struct tag `agents` → `agent`. Doc-comment in the merge block explains the #486 context so a future contributor understands why the shape matters.

### Regression harness — class-of-bug fix

- Pinned opencode's schema at `cmd/workspace-agentd/testdata/opencode-config.schema.json` (37 KB, refreshed from <https://opencode.ai/config.json>).
- `cmd/workspace-agentd/testdata/REFRESH.md`: refresh procedure, cadence (chart-release or test-failure trigger), and rationale for both the pinning choice and the `models.dev` `$ref`-stripping approach.
- `cmd/workspace-agentd/agent_config_writer_schema_test.go`: new test file with
  - `loadOpencodeSchema` — one-shot loader using `sync.Once`, includes the external-`$ref` stripping logic.
  - `stripExternalRefs` — recursive schema walker replacing every `{"$ref": "https://models.dev/..."}` with a permissive `{"type": "string"}` stub.
  - `assertMatchesOpencodeSchema` — generic authoritative validator any writer test can call. Failure reporter includes the schema's specific complaint (e.g. `additional properties 'agents' not allowed`) so the fix path is obvious.
  - `TestAgentConfigWriter_Rebuild_MatchesOpencodeSchema` — 8-case permutation matrix over the writer's four source inputs. Every combo `rebuild()`s and validates.
  - `TestAgentConfigWriter_Rebuild_MatchesOpencodeSchema_ExistingBuildAgent` — deep-merge branch: on-disk `agent.build` with non-prompt siblings + `adminPrompt` applied on top must still validate.

### Existing tests updated

- `TestAgentConfigWriter_Rebuild_AdminPromptInjectsIntoBuildSystem` → renamed `..._AdminPromptInjectsIntoBuildPrompt`. Assertions now target `agent.build.prompt`.
- `TestAgentConfigWriter_Rebuild_AdminPromptPreservesExistingBuildAgent`: fixture and assertions updated to the singular `agent.build.*` shape; fixture `mode: "subagent"` changed to `"primary"` (both are valid schema-side, but `primary` is more idiomatic for `build`).

Both existing tests validated the wrong shape pre-fix and passed. That is exactly the class of bug the new schema harness closes; renaming them to reflect the correct shape both fixes the assertion and makes the test-name accurate.

---

## Key Decisions

- **Fix the writer at the emission layer, not at a translation layer.** No `agents` → `agent` shim; the on-disk shape is the single source of truth and every code path that produces or reads it should agree with opencode's schema directly. A translation shim would be the wrong abstraction — it papers over the type disagreement rather than aligning them.
- **Permutation matrix over "one representative test."** #486 required a specific combination (`adminPrompt != ""`); empty-adminPrompt paths skipped the malformed emit and looked fine. A single happy-path schema test would have missed #486 the same way my #484 round-trip tests did. The 8-case matrix exercises every source combo, including the ones known to trigger the bug and the ones known not to (empty, providers-only, relay-only) — proving the harness discriminates correctly.
- **Programmatic `$ref` strip over pinning the models.dev enum.** Both are valid; strip is the right call for reasons stated in REFRESH.md.
- **Don't add a test seam for `loadAdminPrompt`.** It's a one-line `os.ReadFile` with a `len(data) == 0` guard. Adding a path-override flag would be production surface for zero test value; the interesting logic is entirely in `rebuild()`, which the schema harness covers exhaustively.
- **Out of scope: the two pre-existing `_ = json.Unmarshal` swallows in `rebuild()`.** Flagged in the reviewer's Robustness section. Not introduced by this PR; degrading to an empty map is safe (empty `agent: {}` is schema-valid). Handled as a follow-up.

---

## Blockers

None.

---

## Tests Run

- `go test -timeout 30s -run TestAgentConfigWriter_Rebuild_MatchesOpencodeSchema ./cmd/workspace-agentd/` — initial RED: 4 of 10 sub-cases failed with `additional properties 'agents' not allowed` (the same complaint opencode threw in prod). Post-fix: all 10 pass.
- `go test -timeout 60s -run TestAgentConfigWriter ./cmd/workspace-agentd/` — full writer test class: 20+ tests, all green.
- `go test -timeout 180s -short ./cmd/workspace-agentd/` — full agentd suite: 200+ tests, 87s, green.
- `go test -timeout 300s -short ./cmd/workspace-agentd/ ./pkg/agentd/ ./pkg/types/` — core packages, green.

Adversarial validation: temporarily reverted the writer fix (changed `agent` back to `agents` in `rebuild()`); the schema harness fired with the exact prod error message and pointed at the offending file path. Restored the fix, harness green.

---

## Next Steps

- **This session:** merge #487 → wait for CI build of the merge commit → bump `talos-ops-prod` image tags → refresh all 5 workspaces → verify chat history + admin prompt both work.
- **Deferred to Workstream B (my own follow-ups from the PR body):**
  1. **API-side observability**: `proxyToWorkspaceWithErrBody` silently proxies upstream 5xx bodies to the client without logging status/body/ref. A structured log line + counter `api_upstream_5xx_total{workspace, path}` on the existing `metricsService.RecordError` surface would surface future regressions in Prometheus without needing `kubectl exec + curl`.
  2. **Frontend UX**: chat page shows silent empty history when message-list 500s. Diagnostic banner ("Workspace agent returned an error: <ref>") is the right UX; users get context and operators get a route from browser DevTools to the incident.
- **Deferred (reviewer-flagged, out of scope):** clean up the two `_ = json.Unmarshal` error-swallowing calls in `rebuild()`. Pre-existing behavior; degrades safely to empty map; low-value follow-up.

---

## Files Modified

- `cmd/workspace-agentd/agent_config_writer.go` — `agents` → `agent` rename, `system` → `prompt`, `agentsRaw` → `agentRaw`, `loadExisting` struct tag, doc comment on the merge block referencing #486.
- `cmd/workspace-agentd/agent_config_writer_schema_test.go` — new file. Loader, `$ref` stripper, generic assertion helper, 8-case permutation matrix test, deep-merge branch test.
- `cmd/workspace-agentd/agent_config_writer_test.go` — two existing tests renamed/updated to the correct shape.
- `cmd/workspace-agentd/testdata/opencode-config.schema.json` — new file. 37 KB pinned copy of opencode's config schema.
- `cmd/workspace-agentd/testdata/REFRESH.md` — new file. Refresh procedure, cadence, rationale.
- `go.mod` — promotes `github.com/santhosh-tekuri/jsonschema/v6` from indirect to direct. No version bump.
- `worklogs/NNNN_2026-07-01_agent-config-schema-drift-and-regression-harness.md` (this file).
