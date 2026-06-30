# Worklog: MCP credential_create migrated to Epic 55 kind/slug identity

**Date:** 2026-06-30
**Session:** Triage + fix of open issue #435 — the MCP server was the one surface left on the pre-Epic-55 `provider` wire shape after PRs #430–#434 migrated everyone else.
**Status:** Complete

---

## Objective

Resolve issue #435 (tech debt: MCP server still uses legacy 'provider' field, post-Epic-55). The MCP `credential_create` tool sent `{"provider":"x","apiKey":"y"}` as the `value` of an `llm-provider` secret, but the API's `LLMProviderData.Validate()` (post-Epic-55) requires `kind` + `slug`, so every MCP credential create returned HTTP 400 `invalid metadata: kind is required`. Migrate the MCP layer to the same `kind`+`slug` shape the SDK, frontend, and backend already use.

A second triage outcome is recorded under Key Decisions: issue #412 was found already resolved on `main` and closed; issue #454 is blocked on a maintainer decision and was skipped.

---

## Work Completed

### Triage
- Audited all 16 open issues. Classified #412 (stale/resolved), #435 (straightforward fix — this PR), #454 (blocked, skipped). Epics/large items deferred.

### #412 — closed (no PR)
- `go.mod` on `main` already references `github.com/jackc/pgx/v5 v5.9.2` (bumped in commit `93f76e1c`, PR #428). `go.sum` hash matches the analysis in the issue. govulncheck will no longer flag GO-2026-5004. Closed with an evidence comment.

### #435 — MCP credential_create → kind/slug (this PR)
Migrated the MCP layer to the Epic 55 identity model. Mechanical follow-up of PR #433 (SDK), confined to `pkg/mcp` + the MCP canary + two docs.

- `pkg/mcp/client.go`: `CreateCredentialReq.Provider` → `Kind` + `Slug`; renamed `credentialProviderValue` → `llmProviderValue` with `kind`/`slug`/`apiKey`/`baseURL`/`default` (mirrors `secrets.LLMProviderData` JSON tags); `CreateCredential` builds the value from Kind/Slug and defaults the secret `name` to the slug when omitted.
- `pkg/mcp/server.go`: `credential_create` tool schema — replaced the `provider` argument with `kind` (Required, `mcp.Enum`) + `slug` (Required); handler validates kind/slug/api_key present and forwards them.
- `sdks/canary/mcp/main.go`: `runMCPCredCRUD` now creates with `kind`+`slug`; negative cases updated to missing-kind / missing-slug / missing-api_key (was missing-provider / missing-api_key).
- Docs: `US-10.10-...md` tool-arg table and `sdks/canary/TESTPLAN.md` S-MCP-CRED rows updated to kind/slug.

---

## Key Decisions

1. **`kind` enum is a local mirror, not an import.** The MCP server binary (`cmd/mcp`) deliberately does not depend on `pkg/secrets` (it is a thin HTTP-forwarding binary; importing `pkg/secrets` would pull pgx/crypto into it). `validCredentialKinds` in `server.go` duplicates `pkg/secrets.ValidKinds`. Drift is gated by `TestValidCredentialKinds_MatchesSecretsValidKinds`, which imports `pkg/secrets` **test-only** (no production-binary coupling) and asserts the two slices are equal. Single source of truth at the validation layer; duplicated for the schema with a test pinning parity.

2. **No client-side slug-regex/kind-enum hard enforcement in the handler.** The handler checks non-empty kind/slug/api_key (the faithful mechanical translation of the old provider/api_key check) and forwards to the server, which is the authority. The `mcp.Enum` on `kind` is a client-discovery hint, not server-side enforcement. This matches the issue's stated "mechanical follow-up" scope; deeper validation already lives on the API/DB side.

3. **`provider` removed outright (no compat shim).** Per Rule 5 (zero tech debt) and Epic 55's intent: the MCP surface moves to kind/slug now. A caller sending the old `provider` arg gets "kind, slug, and api_key are required". The DB `provider` column's one-release deprecation window is owned by Epic 55, not by this MCP follow-up.

4. **#454 skipped.** `values-cluster.yaml` still pins the garbage-collected `ts-1781285219`, but a prior analysis established the file is documentation-only (not applied by `make helm-deploy`) and the durable fix depends on the GHCR pruning mechanism (ts-pattern vs version-count), which cannot be verified from here. The previous run offered the maintainer three options (A/B/C) and is awaiting a decision. Jumping in unilaterally risks the wrong strategy, so it is not a "straightforward fix" for this pass.

---

## Blockers

None.

---

## Tests Run

- `go test -timeout 30s -race ./pkg/mcp/` — PASS (all, including the 9 new credential tests + parity test).
- `go test -run 'TestValidCredentialKinds|TestCredentialCreate|TestHTTPClient_CreateCredential' -v ./pkg/mcp/` — 9/9 PASS.
- `go build ./...` (root module) — OK.
- `go build ./...` + `go vet ./...` in `sdks/canary/mcp` (separate module) — OK.
- `gofmt -l pkg/mcp/ sdks/canary/mcp/` — clean.
- `go test -short ./pkg/secrets/` — PASS (26.7s). NOTE: `go test ./pkg/secrets/` without `-short` times out at 60s on Redis pool reaper — pre-existing, DB/Redis-dependent integration tests in an environment with no Postgres/Redis. Unrelated to this change (this PR does not modify `pkg/secrets`; the only reference is a test-only read of `secrets.ValidKinds`).
- `golangci-lint` not installed locally; CI runs the lint gate.

---

## Next Steps

- After merge: re-run the MCP canary (`sdks/canary/mcp`) against a live cluster to confirm the `kind`+`slug` create round-trips end-to-end (step 4 of the issue's checklist — "Live-run against the cluster post-fix"). This requires a running API + DB and was out of scope for the local fix.
- Resume issue burn-down. Next candidates to assess: #447 (frontend message-bubble vanish on reconcileOnIdle) and #366 (AuditedProvider wiring) — both need a relevance/complexity read before deciding fix-vs-skip.

---

## Files Modified

- `pkg/mcp/client.go` — `CreateCredentialReq` (Kind/Slug), `llmProviderValue` (was `credentialProviderValue`), `CreateCredential` method.
- `pkg/mcp/server.go` — `validCredentialKinds`, `credentialCreateTool` schema, `credentialCreate` handler.
- `pkg/mcp/server_test.go` — added credential_create handler tests + kind-enum parity test; added `pkg/secrets` test-only import.
- `pkg/mcp/client_test.go` — added `CreateCredential` wire-format + auto-bind tests; added `io` import.
- `sdks/canary/mcp/main.go` — `runMCPCredCRUD` kind/slug shape + negative cases.
- `design/stories/epic-10-multi-tenant-trust/US-10.10-complete-credential-model-experience.md` — tool-arg table.
- `sdks/canary/TESTPLAN.md` — S-MCP-CRED rows.
- `worklogs/0577_2026-06-30_mcp-credential-kind-slug.md` — this worklog.
