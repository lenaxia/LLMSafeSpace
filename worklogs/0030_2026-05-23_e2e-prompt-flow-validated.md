# Worklog 0030: End-to-End Prompt Flow Validated

**Date:** 2026-05-23
**Scope:** Validate prompt round-trip: client → API proxy → opencode → LLM → response
**Status:** Complete

## Objective

Worklog 0029 reported the prompt path was blocked because "opencode HTTP API is
SPA-only and requires MCP for headless session creation." That conclusion was
wrong. This session validates the actual flow end-to-end against the live
home-kubernetes cluster (192.168.3.x).

## What Was Validated

Full prompt round-trip through the LLMSafeSpace stack against
`admin@home-kubernetes`:

1. `PUT /api/v1/workspaces/:id/credentials` — set workspace LLM provider config
2. Create Sandbox CRD referencing the workspace (still no API endpoint for this)
3. `POST /api/v1/sandboxes/:sb/sessions` → opencode `POST /session` → 200, returns `ses_…`
4. `POST /api/v1/sandboxes/:sb/sessions/:sid/message` → opencode `POST /session/:id/message` → 200, returns assistant reply
5. Second turn against same session → model recalls prior reply (workspace-backed history works)
6. `GET /api/v1/sandboxes/:sb/sessions/:sid/message` → returns all 4 messages

**Round-trip latency:** 5.1s for 10861 input → 3 output tokens (provider:
`litellm/default` = qwen3.6 via https://ai.thekao.cloud/v1).

## Why Worklog 0029 Was Wrong

0029 claimed sessions could only be created through:

1. The browser SPA, or
2. The `POST /mcp` endpoint, with MCP being the Phase 4 deliverable

The author hit `POST /experimental/session` and `POST /experimental/workspace`,
got SPA HTML and `ADAPTORS[type]` errors back, and concluded the whole HTTP
API was SPA-only.

The actual opencode HTTP API (https://opencode.ai/docs/server/) exposes:

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/session` | Create session (returns `Session` with id) |
| `POST` | `/session/:id/message` | Send message, wait for reply |
| `POST` | `/session/:id/prompt_async` | Send message asynchronously |
| `GET` | `/session/:id/message` | Get history |

These are documented, headless, and **not** prefixed with `/experimental/`.
The proxy in `api/internal/handlers/proxy.go` already targets the correct
paths (lines 146–176). The author of 0029 guessed wrong endpoint paths and
generalized from those guesses. MCP in opencode is for **registering external
tools** the agent can call (filesystem, GitHub, etc.) — it is not a prerequisite
for session creation.

## What Made the Flow Work

The blocker was always credentials, not the protocol. opencode needs a config
file at `OPENCODE_CONFIG=/tmp/agent-config.json` (copied from
`/sandbox-cfg/credentials`, which the credential-setup init container copies
from a Kubernetes Secret named `workspace-creds-<workspace-id>`, key
`provider-config`).

The provider-config used (truncated):

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "litellm": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "LiteLLM",
      "options": {
        "baseURL": "https://ai.thekao.cloud/v1",
        "apiKey": "<from ~/.bashrc OPENAI_API_KEY>"
      },
      "models": { "default": { "name": "default" } }
    }
  },
  "model": "litellm/default"
}
```

Set via:

```
PUT /api/v1/workspaces/:id/credentials
Body: { "provider": "litellm", "config": <provider-config above> }
```

The API service writes this verbatim into the `provider-config` key of the
secret. The controller's credential-setup init container mounts that secret
at `/mnt/secrets/credentials/` and copies `provider-config` →
`/sandbox-cfg/credentials`. The opencode entrypoint then sets
`OPENCODE_CONFIG=/tmp/agent-config.json` (a copy of `/sandbox-cfg/credentials`).

When the controller mounted the secret, the sandbox started, opencode parsed
the config, and `POST /session/:id/message` succeeded on the first try.

## Notable Observations

1. **No API endpoint to create a Sandbox.** Workspaces have full CRUD
   (`POST /api/v1/workspaces`, etc.), but sandboxes are created out-of-band
   via `kubectl apply`. The proxy routes (`/api/v1/sandboxes/:id/...`) assume
   the sandbox already exists. This is a gap; document or close.

2. **Session history persists in the workspace.** opencode writes session DB
   into `/workspace/.local/opencode/opencode.db` (via `XDG_DATA_HOME=/workspace/.local`).
   Suspending and resuming the workspace should preserve sessions — not yet tested.

3. **Sandbox response includes `patch` parts** listing every file opencode
   touched in `/workspace/.local/opencode/snapshot/...`. Useful for diff/audit
   but verbose; could be stripped at the proxy if response size matters.

4. **Worklog 0029's `OPENAI_API_KEY` env-var injection commit was correctly
   reverted** (`93bd708`) — the proper path is workspace credentials, which
   was already implemented and just unused.

## Cluster State After Session

```
default/workspace.llmsafespace.dev/998a78ff-616c-4807-ac7c-3fe685b4bfb4   Active
default/sandbox.llmsafespace.dev/prompt-test                              Running   pod=prompt-test-0d58b28c   ip=10.69.6.144
default/secret/workspace-creds-998a78ff-616c-4807-ac7c-3fe685b4bfb4       (provider-config)
default/secret/sandbox-pw-prompt-test                                     (opencode basic-auth password)
```

User in Postgres: `17638d7b-ebab-4b19-82b5-3de25f87ce93` / `admin`
API key: `lsp_e34af2ae2318817f7e8dc978e8306a3924da869e38da9abd4bcd1fa285e23a1b`

## Next Steps

1. **Add Sandbox CRUD to the API.** `POST /api/v1/sandboxes`,
   `GET /api/v1/sandboxes`, `DELETE /api/v1/sandboxes/:id`. Right now external
   clients have to hit the K8s API directly to create a sandbox, which breaks
   the API-as-sole-entrypoint principle.

2. **Update the e2e script** (`local/test.sh` Test 6) to also send a prompt
   through the proxy after session create, asserting a non-empty assistant
   reply. The current script only validates `POST /sessions` (creation), not
   `POST /sessions/:id/message` (the actual prompt round-trip).

3. **Suspend/resume + session continuity test.** Suspend the workspace, resume,
   send another message in the same session ID — verify history is preserved.

4. **Strip `patch` parts from proxy responses** (or make it opt-in) — they
   leak workspace internals and inflate every reply by ~2KB.

5. **Correct README-LLM.md worklog summary** for 0029 (if maintained centrally)
   to remove the "blocked on MCP" narrative.

## Files Touched

None. Validation only — no code changes. Cluster artifacts created:

- `secret/workspace-creds-998a78ff-616c-4807-ac7c-3fe685b4bfb4`
- `sandbox.llmsafespace.dev/prompt-test`

## Test Commands Run

```bash
# Set credentials (returns 204)
curl -X PUT -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  --data-binary @/tmp/setcreds-req.json \
  http://127.0.0.1:18080/api/v1/workspaces/$WS/credentials

# Create session (returns Session JSON)
curl -X POST -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{}' http://127.0.0.1:18080/api/v1/sandboxes/prompt-test/sessions

# Send prompt (returns { info, parts: [...{type:"text",text:"PONG"}] })
curl -X POST -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  --data-binary @/tmp/msg.json \
  http://127.0.0.1:18080/api/v1/sandboxes/prompt-test/sessions/$SID/message
```

All returned HTTP 200 with valid responses on first attempt.
