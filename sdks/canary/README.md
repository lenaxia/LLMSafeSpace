# SDK Canaries

Scheduled canary tests for the Go, Python, and TypeScript SDKs, plus the MCP server. These run as Fission functions every 1–15 minutes and as CI steps on every PR.

## Structure

```
sdks/canary/
├── TESTPLAN.md             # Full test plan with all scenarios and reasoning
├── Makefile                # make canary-ci, make canary-all
├── go/
│   ├── go.mod              # canary module (replaces sdk/go locally)
│   ├── result.go           # Runner, Result, helpers
│   ├── config.go           # Config, wait helpers, SDK client factories
│   └── scenarios/
│       ├── s-health/       # S-HEALTH — GET /livez, /readyz, /health
│       ├── s-auth/         # S-AUTH — valid key, JWT login, bad credentials
│       ├── s-auth-config/  # S-AUTH-CONFIG — public /auth/config endpoint
│       ├── s-logout/       # S-LOGOUT — JWT revocation via POST /auth/logout
│       ├── s-apikey/       # S-APIKEY — API key CRUD + post-delete rejection
│       ├── s-user-settings/# S-USER-SETTINGS — GET/PUT /users/me/settings
│       ├── s-ws-crud/      # S-WS-CRUD — workspace CRUD + storage validation
│       ├── s-ws-status/    # S-WS-STATUS — status response shape
│       ├── s-secret-crud/  # S-SECRET-CRUD — CRUD, name validation, duplicates
│       ├── s-secret-reveal/# S-SECRET-REVEAL — password reauth gate
│       ├── s-secret-audit/ # S-SECRET-AUDIT — GET /secrets/audit
│       ├── s-secret-bindings/# S-SECRET-BINDINGS — bindings idempotency
│       ├── s-env-vars/     # S-ENV-VARS — workspace env var API layer
│       ├── s-cred-crud/    # S-CRED-CRUD — LLM credential CRUD
│       ├── s-error-format/ # S-ERROR-FORMAT — error shape + proxy errors
│       ├── d-ws-lifecycle/ # D-WS-LIFECYCLE — suspend/resume/restart + status fields
│       ├── d-session-ensure/# D-SESSION-ENSURE — auto-resume, list, rename, GET
│       ├── d-session-msg/  # D-SESSION-MSG — message + verbose + lastActivityAt
│       ├── d-prompt-async/ # D-PROMPT-ASYNC — prompt_async + SSE session.idle
│       ├── d-cred-bind/    # D-CRED-BIND — bind + reload + unbind + reload-empty
│       ├── d-cred-model-flow/ # D-CRED-MODEL-FLOW — flagship end-to-end
│       ├── d-suspend-resume-session/ # D-SUSPEND-RESUME-SESSION — history survives
│       └── d-sse-events/   # D-SSE-EVENTS — SSE broker delivers events
├── python/
│   ├── canary.py           # Runner, Config, helpers
│   └── scenarios/
│       ├── s_auth.py
│       ├── s_ws_crud.py
│       ├── s_secret_crud.py
│       ├── s_error_format.py
│       ├── d_cred_model_flow.py
│       └── d_suspend_resume_session.py
├── typescript/
│   ├── canary.ts           # Runner, Config, helpers
│   └── scenarios/
│       ├── s-auth.ts
│       ├── s-ws-crud.ts
│       ├── s-error-format.ts
│       └── d-cred-model-flow.ts
├── mcp/
│   ├── main.go             # MCP server canaries (stdio transport)
│   └── go.mod
└── fission/
    └── canary-functions.yaml  # Fission Function + TimeTrigger manifests
```

## Running Locally

```bash
# Prerequisites
export LLMSAFESPACES_URL=http://localhost:8080
export LLMSAFESPACES_API_KEY=lsp_...
export LLMSAFESPACES_EMAIL=canary1@llmsafespaces.test
export LLMSAFESPACES_PASSWORD=...

# All ci:fast scenarios (all SDKs, no LLM key needed)
make -f sdks/canary/Makefile canary-ci

# Single scenario
go run ./sdks/canary/go/scenarios/s-auth/

# Full suite (requires live cluster + LLM credentials)
export LLMSAFESPACES_LLM_API_KEY=sk-...
export LLMSAFESPACES_LLM_MODEL=anthropic/claude-haiku-4-5
make -f sdks/canary/Makefile canary-all

# MCP canaries
go build -o /tmp/mcp ./cmd/mcp/
MCP_BINARY=/tmp/mcp go run ./sdks/canary/mcp/
```

## Two-Tier Model

| Tier | Schedule | What it catches |
|---|---|---|
| Shallow (ci:fast) | 1 min | API down, auth regression, serialization bug, error format drift |
| Deep | 5–15 min | Controller regression, agent startup failure, credential injection broken, LLM integration broken |

See [TESTPLAN.md](TESTPLAN.md) for the full scenario specifications, environment variables, alert policy, and test account requirements.

## Test Accounts

Three accounts must be provisioned before deploying:

| Account | Env var | Purpose |
|---|---|---|
| `canary1@llmsafespaces.test` | `LLMSAFESPACES_API_KEY` | Primary canary account |
| `canary2@llmsafespaces.test` | `LLMSAFESPACES_API_KEY_USER2` | Cross-user isolation (S-OWNERSHIP) |
| `canary-rotate@llmsafespaces.test` | — | Key rotation / password change scenarios |

Bootstrap:
```bash
# After deploying to a new cluster
go run ./sdks/canary/go/cmd/seed-accounts/ \
  --url https://your.instance.com \
  --out /tmp/canary-keys.env
source /tmp/canary-keys.env
```

## Fission Deployment

```bash
# Create the canary secrets
kubectl create secret generic llmsafespaces-canary-secrets \
  --namespace fission \
  --from-literal=LLMSAFESPACES_URL=https://your.instance.com \
  --from-literal=LLMSAFESPACES_API_KEY=lsp_... \
  ...

# Deploy all function manifests
kubectl apply -f sdks/canary/fission/

# Verify functions are registered
fission fn list | grep canary
```

## Result Format

Every canary returns JSON:

```json
{
  "scenario": "auth",
  "sdk": "go-sdk",
  "passed": 8,
  "failed": 0,
  "duration_s": 1.23,
  "checks": [
    {"name": "valid-key: Auth.Me no error", "passed": true},
    {"name": "invalid-key: returns AuthError", "passed": true}
  ]
}
```

HTTP status: `200` if all checks passed, `500` if any failed. Fission TimeTrigger alerts on non-zero exit (Tier 2 alert policy: page on 2+ consecutive 500s).
