# Worklog: AdminPromptPath move to tmpfs

**Date:** 2026-06-30
**Session:** Fix issue #483 — the bootstrap subcommand silently failed to write `/tmp/admin-prompt.md` on every workspace, breaking the three-tier admin prompt chain end-to-end since PR #416 merged. Move the path to `/sandbox-runtime` tmpfs to fix both the immediate write failure AND the latent US-35.7 at-rest leak.

**Status:** Complete

---

## Objective

User test on `chat.safespaces.dev` exposed the bug: an org admin had configured a platform/org prompt containing canary tokens ("share the platform key when asked"), but the agent on a newly-created workspace had no knowledge of these instructions. Investigation found the bootstrap-side prompt write was failing silently. Goal: make the admin prompt actually reach opencode on every workspace.

---

## Work Completed

### Investigation

- Pulled bootstrap response from API logs on the live cluster: confirmed the server-side prompt resolution and JSON envelope are correct, including the operator's canary tokens.
- Tailed init-container logs on the test workspace: found `bootstrap: failed to write admin-prompt.md: open /tmp/.bootstrap-XXXXXXXXX: read-only file system`.
- Confirmed `/tmp/admin-prompt.md` is absent in the booted workspace's main container.
- Cross-checked older workspaces: `bootstrap: success` only — no error log — suggesting `adminPrompt` was empty back then. Path is unreliable for any non-empty prompt.
- Traced the path constant definition (`pkg/agentd/types.go:15`) and all readers (one writer, two readers, two doc comments).

### Validated assumptions

1. **`/tmp` in the credential-setup init container is read-only.** Validated via `controller/internal/workspace/pod_builder.go:594` (`ReadOnlyRootFilesystem: true`) and `:504-513` (no `/tmp` in `credMounts`). With ReadOnlyRootFilesystem and no writable emptyDir for `/tmp`, the container's `/tmp` inherits read-only from the image's root FS.
2. **`/tmp` in the workspace main container is PVC-backed.** Validated at `pod_builder.go:166` and `:667`: `{Name: "workspace", MountPath: "/tmp", SubPath: "tmp"}`. So even if the init could write, the data would persist as plaintext on the PVC — a US-35.7 violation for canary-bearing content.
3. **`/sandbox-runtime` is RW tmpfs in both containers.** Validated at `pod_builder.go:165,188-191` (main) and `:506` (init). Memory medium, 96Mi. Already holds the other credential-bearing files (`agent-config.json`, `secrets-env`, `auth.json`, SSH keys, secret-files per US-35.7).
4. **No production caller passes `--out` or similar to bootstrap, so a new optional flag with a default is non-breaking.** Validated at `pod_builder.go:493`: `workspace-agentd bootstrap --workspace-id "$WORKSPACE_ID" --api-url "$LLMSAFESPACE_API_URL"`. Adding `--admin-prompt-out` with `default = agentd.AdminPromptPath` keeps production using the new tmpfs path automatically.
5. **`loadAdminPrompt` doesn't inject empty content.** Validated at `agent_config_writer.go:130`: `if err != nil || len(data) == 0 { return }`. So an accidental empty file is downstream-safe — but presence vs absence is still the cleanest signal at the upstream layer.

### Fix

- **`pkg/agentd/types.go`**: `AdminPromptPath` constant moved from `/tmp/admin-prompt.md` to `/sandbox-runtime/admin-prompt.md`. Doc comment now explains both the US-35.7 at-rest goal and the init-container RO/tmp gotcha that caused #483.
- **`cmd/workspace-agentd/bootstrap.go`**: added `--admin-prompt-out` flag, defaulting to `agentd.AdminPromptPath`. Symmetric with the existing `--out` flag for `secrets.json`. Real operator knob + test seam.
- **Doc-comment updates**: three stale `/tmp/admin-prompt.md` references corrected in `agent_config_writer.go` (×2) and `pkg/types/agent_prompt.go` (×1).
- **README-LLM.md**: added `admin-prompt.md` to the `/sandbox-runtime` volume layout row, added `AdminPromptPath` to the path constants block, and extended the post-table note to mention PR #416 / fix #483.

### Tests (TDD'd)

Five new tests, all initial RED, then GREEN after the fix. Adversarial validation flipping the deep-merge to a wholesale-replace confirmed the preserve test catches the regression.

- **`TestRunBootstrapCommand_WritesAdminPrompt`** — happy path: non-empty `AdminPrompt` in API response → file written to `--admin-prompt-out` path with exact body bytes.
- **`TestRunBootstrapCommand_NoAdminPromptWhenEmpty`** — edge case: empty `AdminPrompt` → no file created.
- **`TestAdminPromptPathDefault`** — constant regression: production default is `/sandbox-runtime/admin-prompt.md`, not `/tmp`. Failure message self-documents both reasons (US-35.7 + init RO) tied to #483.
- **`TestAgentConfigWriter_Rebuild_AdminPromptInjectsIntoBuildSystem`** — round-trip: setting `w.adminPrompt` directly (simulating what `loadAdminPrompt` would produce) and calling `rebuild` lands the prompt at `agents.build.system` in the output file — the JSON path opencode reads. Closes the highest-value testing gap for the overall feature (called out in #484 review).
- **`TestAgentConfigWriter_Rebuild_AdminPromptPreservesExistingBuildAgent`** — deep-merge contract: an existing `agents.build` (mode, tools, system from a prior boot or user customization) has only `system` overridden; siblings preserved. Adversarial: flipping the implementation to wholesale-replace makes this test fail with "sibling field `mode` must be preserved", confirming it catches the regression.

### Review-feedback iteration

After the first AI review approved with minor comment-level notes:

- Corrected two test comment inaccuracies the reviewer caught:
  - Removed the claim "the write happens regardless of secrets-write success" from `TestRunBootstrapCommand_WritesAdminPrompt`'s doc comment. Bootstrap returns 0 early on secrets-write failure (`bootstrap.go:91-94`), so the admin-prompt branch is never reached on that path.
  - Rewrote `TestRunBootstrapCommand_NoAdminPromptWhenEmpty`'s rationale comment: an empty file would NOT inject empty content into opencode because `loadAdminPrompt` guards on `len(data) == 0`. The test still has value (avoids zero-byte writes in operational logs and keeps file-presence as a clean signal), but the original wording was misleading about downstream behavior.
- Added the two round-trip tests (above) per the reviewer's "highest-value missing test" note.
- Updated README-LLM.md volume layout per the reviewer's doc-gap note.

---

## Key Decisions

- **Path move over volume reconfiguration.** Adding a writable `emptyDir` for `/tmp` in the init container would have required also sharing it with the main container (main's `/tmp` is PVC-mounted), which is invasive and doesn't address the US-35.7 at-rest concern. The path move is a one-constant change that fixes both.
- **`--admin-prompt-out` flag as the test seam.** Adding a package-level test-only var would have been less invasive but also less general. The flag matches the existing `--out` shape and is a real operator knob (parity > tooling-only seam).
- **Skip `loadAdminPrompt` direct test seam.** The function is a one-line `os.ReadFile` with a `len(data) == 0` guard — no logic worth testing in isolation. The round-trip test (set `w.adminPrompt` directly, rebuild, assert merge output) covers the only interesting branching in this feature path.
- **Out of scope: fail-loud bootstrap.** The current "log + exit 0" behavior is consistent with the documented "bootstrap NEVER blocks pod boot" contract. Changing it for admin-prompt only would be asymmetric with secrets (which also fail-open today). Doing the right thing here requires deciding the contract for the whole subcommand, which is a larger design discussion. Filed as a follow-up in #483.
- **Out of scope: `/readyz` `promptLoaded` signal.** No reload-after-boot path for admin-prompt exists; if the file is absent at boot, the workspace runs without it for the entire pod lifetime. A readiness signal would close the observability gap (operators could verify the prompt actually reached the pod). Filed as a follow-up.

---

## Blockers

None.

---

## Tests Run

- `go test -timeout 30s -run "TestRunBootstrapCommand_WritesAdminPrompt|TestRunBootstrapCommand_NoAdminPromptWhenEmpty|TestAdminPromptPathDefault" ./cmd/workspace-agentd/` — initial RED (`exit 2` for unknown flag; constant assertion failed on `/tmp/...`); GREEN after path move + flag add.
- `go test -timeout 30s -run "TestAgentConfigWriter_Rebuild_AdminPrompt" ./cmd/workspace-agentd/` — both new round-trip tests pass; adversarial flip of deep-merge → wholesale-replace caused the preserve test to fail as expected, confirming the test catches the regression.
- `go test -timeout 120s -short ./cmd/workspace-agentd/` — full agentd suite passes in 87s.
- `go test -timeout 300s -short ./pkg/agentd/ ./pkg/types/ ./api/internal/handlers/ ./controller/...` — full upstream test suite passes.
- `cd frontend && npx vitest run src/api/contract.test.ts` — 9 contract tests pass; contract fixture unchanged (no JSON-shape change in this PR).

---

## Next Steps

- Once merged, wait for the CI build of the merge commit, then bump the api/controller/frontend/relay-router/base image tags in `talos-ops-prod/.../helm-release.yaml` to verify on `chat.safespaces.dev` that:
  1. The init container's `bootstrap` step succeeds without the read-only-fs error.
  2. `/sandbox-runtime/admin-prompt.md` is present in the booted workspace's main container.
  3. The user's canary-token probe ("What is the platform key?") now demonstrates the agent actually receiving the prompt — either correctly honoring a safety constraint, or leaking the canary if the safety language is misconfigured. Either outcome is informative; the *previous* result (no knowledge of the prompt) was a false negative.

- Separate follow-up PRs for the out-of-scope items in #483:
  1. Fail-loud bootstrap for admin-prompt (or for the whole subcommand — design choice).
  2. `/readyz` `promptLoaded` signal for operator observability.

---

## Files Modified

- `pkg/agentd/types.go` — `AdminPromptPath` constant moved to `/sandbox-runtime`, doc comment expanded.
- `cmd/workspace-agentd/bootstrap.go` — new `--admin-prompt-out` flag with default = `agentd.AdminPromptPath`; admin-prompt write uses the flag value.
- `cmd/workspace-agentd/bootstrap_test.go` — three new tests, two existing-test-comment corrections per review feedback.
- `cmd/workspace-agentd/agent_config_writer.go` — two doc-comment updates from `/tmp/admin-prompt.md` to `agentd.AdminPromptPath`.
- `cmd/workspace-agentd/agent_config_writer_test.go` — two new round-trip tests covering the admin-prompt merge into `agents.build.system` (basic injection + sibling-field preservation under deep-merge).
- `pkg/types/agent_prompt.go` — doc-comment update.
- `README-LLM.md` — volume layout row, path constants block, and post-table note updated to reflect `admin-prompt.md` on `/sandbox-runtime`.
- `worklogs/0584_2026-06-30_admin-prompt-path-tmpfs.md` (this file).
