# Worklog 0029: CI Pipeline + E2E Deployment Validation

**Date:** 2026-05-23
**Scope:** CI/CD, deployment, end-to-end infrastructure validation
**Status:** Complete (infrastructure validated, prompt validation blocked)

## What Changed

### CI Pipeline

Created `.github/workflows/ci.yml` — 4 parallel jobs on every push to main:

| Job | Duration | Description |
|-----|----------|-------------|
| Test | ~4m | `go test -race -short ./...` + `go vet` |
| Build API | ~3m | Docker build + push to `ghcr.io/lenaxia/llmsafespace/api:dev` |
| Build Controller | ~3m | Docker build + push to `ghcr.io/lenaxia/llmsafespace/controller:dev` |
| Build Runtime Base | ~1m | Docker build + push to `ghcr.io/lenaxia/llmsafespace/base:dev` |

All images tagged with both `sha-<commit>` and `dev` (latest from main).

### Deployment to Real Cluster

Deployed LLMSafeSpace to a Talos Linux cluster (4 control-plane + 4 workers):

| Component | Status |
|-----------|--------|
| Postgres (disposable pod) | Running in `default` ns |
| Valkey (disposable pod) | Running in `default` ns |
| API server | Running, healthy (`/livez` + `/readyz` 200) |
| Controller | Running, reconciling CRDs |
| Workspace CRD | Created via API, Phase=Active, PVC provisioned |
| RuntimeEnvironment | Created, pointing to `ghcr.io/lenaxia/llmsafespace/base:dev` |
| Sandbox CRD | Created, pod Running, opencode listening on port 4096 |
| Auth endpoints | Register → JWT, Login → JWT, API key CRUD all working against real Postgres/Valkey |

### What Was Validated

1. **Auth end-to-end**: `POST /auth/register` → user created in Postgres, JWT returned. `POST /auth/login` → bcrypt verify, JWT returned. `POST /auth/api-keys` → `lsp_` key generated, stored, returned once. Listed keys strip secrets. Delete works.

2. **Workspace lifecycle**: API → DB → K8s Workspace CRD → controller reconciles → PVC provisioned → Phase=Active.

3. **Sandbox lifecycle**: Sandbox CRD created → controller creates pod with correct image, volumes, password secret → pod reaches Running → opencode serves `/global/health` (200, `{"healthy":true,"version":"1.2.27"}`).

4. **Image builds**: All three images (api, controller, runtime-base) built by GitHub Actions and pulled by the Talos cluster from ghcr.io.

### What Was NOT Validated

**Sending a prompt through opencode to an LLM and receiving a response.**

Opencode's HTTP API (`/session/*/message`, `/session/*/prompt_async`) requires sessions to exist. Sessions are created by opencode's internal session manager, which is only triggered through:

1. The browser SPA (TUI routes like `tui/submit-prompt` fire events on an internal bus with no headless consumer)
2. The MCP endpoint (`POST /mcp`) which requires `config.type` discriminator — but MCP is not yet implemented (Phase 4)

The `/experimental/session` POST returns the SPA HTML (route catch-all). The `/experimental/workspace` POST fails with `ADAPTORS[type]` error.

**Root cause**: opencode's HTTP server is designed for the browser TUI. Headless programmatic access requires MCP, which is the Phase 4 deliverable.

## Files Created

| File | Purpose |
|------|---------|
| `.github/workflows/ci.yml` | CI pipeline: test + build 3 images |
| `local/deps-postgres.yaml` | Disposable Postgres deployment + service |
| `local/deps-valkey.yaml` | Disposable Valkey deployment + service |

## Temporary Changes (Reverted)

| Change | Commit | Status |
|--------|--------|--------|
| Inject `OPENAI_API_KEY`/`OPENAI_BASE_URL` as pod env vars via controller | `3dcf057` | Reverted (`93bd708`) |

This was used for testing only. The proper implementation should mount the secret as a file and read it at runtime, not expose it in pod env vars.

## Infrastructure Inventory (still running in `default` ns)

| Resource | Type | Notes |
|----------|------|-------|
| `postgres` | Deployment + Service | Disposable, password: `changeme` |
| `valkey` | Deployment + Service | Disposable, no password |
| `llmsafespace-api` | Deployment + Service | Helm-managed |
| `llmsafespace-controller` | Deployment | Helm-managed |
| `llmsafespace-postgres-password` | Secret | Postgres password |
| `workspace-998a78ff-*` | PVC | 1Gi workspace storage |

## Next Steps

1. **Phase 4: MCP Server** — Implement MCP endpoint in `api/internal/mcp/` using `mark3labs/mcp-go`. This is the headless programmatic API that opencode expects for non-browser clients.

2. **Proper LLM credential injection** — Mount `llm-config` secret as file in sandbox pod, read at opencode startup via wrapper script. Never expose in env vars.

3. **"watch channel closed" log noise** — SandboxWatcher spams every ~2s.

4. **Password complexity rules** — Beyond min length 8.
