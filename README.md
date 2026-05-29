# LLMSafeSpace

A Kubernetes-first platform for running AI agents in isolated, persistent workspaces.

Each workspace runs [`opencode serve`](https://opencode.ai/docs/server/) — a headless HTTP server that drives an LLM agent — backed by a PVC-mounted filesystem at `/workspace`. The LLMSafeSpace API service is a stateless reverse proxy in front of the workspace pods, with auth, ownership checks, encrypted secret management, and quality-of-life filtering.

Repository: `github.com/lenaxia/llmsafespace`

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  Clients (REST / SSE / MCP)                                         │
│         │                                                            │
│         ▼                                                            │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  LLMSafeSpace API (Gin, stateless, horizontally scalable)    │   │
│  │  - Auth (JWT + API keys)                                     │   │
│  │  - Workspace CRUD + lifecycle (activate/suspend/resume)       │   │
│  │  - Reverse proxy to workspace pods (basic auth, IP refresh)  │   │
│  │  - Secrets management (zero-knowledge encrypted store)        │   │
│  │  - Settings (admin instance + user preferences)               │   │
│  │  - Patch-part filtering (?verbose=true to keep)              │   │
│  └─────────────────────┬────────────────────────────────────────┘   │
│                        │ K8s API                                     │
│                        ▼                                             │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  Controller (controller-runtime)                              │   │
│  │  - Reconciles Workspace, RuntimeEnvironment CRDs              │   │
│  │  - Manages pod lifecycle, PVC, credential secrets             │   │
│  │  - Health monitoring via workspace-agentd sidecar             │   │
│  └─────────────────────┬────────────────────────────────────────┘   │
│                        │                                             │
│                        ▼                                             │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  Workspace Pods (one per active Workspace CRD)                │   │
│  │  - init: workspace-setup + credential-setup                   │   │
│  │  - main: opencode serve --hostname 0.0.0.0 --port 4096        │   │
│  │  - sidecar: workspace-agentd (health probes, session metadata)│   │
│  │  - mounts: PVC at /workspace, secret as /sandbox-cfg          │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                      │
│  ┌─────────────────┐  ┌─────────────────┐                           │
│  │ PostgreSQL      │  │ Redis / Valkey  │                           │
│  │ (users, keys,   │  │ (rate limit,    │                           │
│  │  secrets,       │  │  cache, lockout,│                           │
│  │  settings)      │  │  DEK cache)     │                           │
│  └─────────────────┘  └─────────────────┘                           │
└──────────────────────────────────────────────────────────────────────┘
```

### Custom Resource Definitions

Two CRDs in the `llmsafespace.dev/v1` API group:

| Kind | Scope | Purpose |
|------|-------|---------|
| `Workspace` | Namespaced | PVC-backed persistent environment + pod running `opencode serve` |
| `RuntimeEnvironment` | Cluster | Mapping from runtime name → container image |

### Lifecycle

```
Workspace: Pending → Active → Suspending → Suspended → Resuming → Active
                              ↘
                                Terminating → Terminated
```

Suspending a workspace deletes the pod but retains the PVC. Resuming creates a fresh pod that reattaches to the existing PVC, so opencode session history (stored in `/workspace/.local/opencode`) survives suspend/resume.

---

## REST API

All endpoints are JSON. Authentication is via `Authorization: Bearer <jwt-or-api-key>`.

### Auth

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/auth/register` | Create a user, returns `{token, user}` |
| `POST` | `/api/v1/auth/login` | Returns `{token, user}` on valid credentials |
| `POST` | `/api/v1/auth/api-keys` | Create a new `lsp_…` API key |
| `GET` | `/api/v1/auth/api-keys` | List the caller's API keys (secret stripped) |
| `DELETE` | `/api/v1/auth/api-keys/:id` | Revoke an API key |

### Workspaces

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/workspaces` | List the caller's workspaces (paginated) |
| `POST` | `/api/v1/workspaces` | Create a workspace |
| `GET` | `/api/v1/workspaces/:id` | Get one workspace |
| `PUT` | `/api/v1/workspaces/:id` | Rename a workspace |
| `DELETE` | `/api/v1/workspaces/:id` | Delete (and its PVC) |
| `POST` | `/api/v1/workspaces/:id/suspend` | Suspend (retain PVC, delete pod) |
| `POST` | `/api/v1/workspaces/:id/resume` | Resume (re-create pod) |
| `POST` | `/api/v1/workspaces/:id/activate` | Activate (resume if suspended, auto-suspend oldest if at cap) |
| `GET` | `/api/v1/workspaces/:id/status` | Get phase + conditions + credential state + agent health |
| `PUT` | `/api/v1/workspaces/:id/credentials` | Set the LLM provider config *(deprecated — use secrets API)* |
| `DELETE` | `/api/v1/workspaces/:id/credentials` | Remove provider config *(deprecated)* |

### Sessions (proxied to opencode)

These endpoints are reverse-proxied to the workspace pod's `opencode serve` instance on port 4096. The proxy injects HTTP basic auth for opencode automatically.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/workspaces/:id/sessions/:sessionId/message` | Send a message; wait for the assistant reply |
| `POST` | `/api/v1/workspaces/:id/sessions/:sessionId/prompt` | Send a message asynchronously (`204 No Content`) |
| `GET` | `/api/v1/workspaces/:id/sessions/:sessionId/message` | Fetch session history |
| `POST` | `/api/v1/workspaces/:id/sessions/:sessionId/abort` | Abort a running session |
| `GET` | `/api/v1/workspaces/:id/events` | SSE event stream |

### Secrets

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/secrets` | Create an encrypted secret |
| `GET` | `/api/v1/secrets` | List secrets (metadata only, never values) |
| `PUT` | `/api/v1/secrets/:id` | Update secret value |
| `DELETE` | `/api/v1/secrets/:id` | Delete a secret |
| `PUT` | `/api/v1/workspaces/:id/bindings` | Set which secrets are bound to a workspace |
| `GET` | `/api/v1/workspaces/:id/bindings` | List bound secrets |

### Settings

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/admin/settings` | Get all instance settings (admin only) |
| `PUT` | `/api/v1/admin/settings/:key` | Update an instance setting |
| `GET` | `/api/v1/users/me/settings` | Get current user's settings |
| `PUT` | `/api/v1/users/me/settings/:key` | Update a user setting |

#### `?verbose=true` flag

By default, the proxy strips parts of `type=="patch"` from message and history responses. opencode emits a `patch` part for every assistant turn, listing every workspace file it touched (~2 KB per response of internal snapshot paths). For most clients this is noise.

Pass `?verbose=true` on any message or history request to receive the unfiltered response:

```
POST /api/v1/workspaces/ws-1/sessions/ses_xyz/message?verbose=true
```

The `verbose` query parameter is consumed by the API proxy and is not forwarded to opencode.

---

## Quickstart

### 1. Authenticate

```bash
API=http://localhost:8080

# Register a new user (returns a JWT)
curl -X POST "$API/api/v1/auth/register" \
  -H "Content-Type: application/json" \
  -d '{"email":"alice@example.com","password":"hunter2hunter2","username":"alice"}'

# Or, login if already registered
TOKEN=$(curl -sX POST "$API/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"alice@example.com","password":"hunter2hunter2"}' \
  | jq -r '.token')
```

### 2. Create a workspace

```bash
WS=$(curl -sX POST "$API/api/v1/workspaces" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-workspace","runtime":"base","storageSize":"1Gi"}' \
  | jq -r '.id')
echo "workspace: $WS"
```

### 3. Set the LLM provider on the workspace

The `config` field is an opencode config document. The model and provider you declare here is what the workspace will use.

```bash
curl -X PUT "$API/api/v1/workspaces/$WS/credentials" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "provider": "litellm",
    "config": {
      "$schema": "https://opencode.ai/config.json",
      "provider": {
        "litellm": {
          "npm": "@ai-sdk/openai-compatible",
          "name": "LiteLLM",
          "options": {
            "baseURL": "https://your-llm-gateway/v1",
            "apiKey": "sk-..."
          },
          "models": { "default": { "name": "default" } }
        }
      },
      "model": "litellm/default"
    }
  }'
```

Any OpenAI-compatible base URL works. Model id format is `<provider-id>/<model-id>`.

### 4. Activate the workspace

```bash
curl -X POST "$API/api/v1/workspaces/$WS/activate" \
  -H "Authorization: Bearer $TOKEN"

# Wait for it to come up
while [ "$(curl -s -H "Authorization: Bearer $TOKEN" \
    "$API/api/v1/workspaces/$WS/status" | jq -r .phase)" != "Active" ]; do
  sleep 2
done
```

### 5. Drive a session

```bash
# Create a session
SID=$(curl -sX POST "$API/api/v1/workspaces/$WS/sessions/new" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  | jq -r '.sessionId')

# Send a prompt
curl -X POST "$API/api/v1/workspaces/$WS/sessions/$SID/message" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model":   {"providerID":"litellm","modelID":"default"},
    "parts":   [{"type":"text","text":"Reply with exactly the word: PONG"}]
  }' \
  | jq '.parts[] | select(.type=="text") | .text'

# → "PONG"
```

### 6. Suspend / resume

Suspending the workspace deletes the pod but keeps the PVC. Resuming re-creates the pod. Session history (stored in the PVC) survives.

```bash
curl -X POST "$API/api/v1/workspaces/$WS/suspend" \
  -H "Authorization: Bearer $TOKEN"

curl -X POST "$API/api/v1/workspaces/$WS/resume" \
  -H "Authorization: Bearer $TOKEN"
```

---

## Repository Layout

```
api/                     # Go API service (Gin) + MCP server
  cmd/api/               # API server entrypoint
  internal/
    handlers/            # Reverse proxy, secrets, settings, credentials, activity, events
    middleware/          # Auth, rate limit, CORS, security, validation, admin guard, etc.
    services/            # Auth, Workspace, Database, Cache, RateLimit, Metrics, SessionIndex
    server/router.go     # Gin route table
    mocks/               # Service mocks for tests

cmd/
  workspace-agentd/      # Sidecar binary for workspace pods (health probes, session metadata)
  mcp/                   # MCP server entrypoint
  redact/                # Redact binary entrypoint

controller/              # Kubernetes operator (controller-runtime)
  internal/
    workspace/           # Workspace reconciler (pod lifecycle, PVC, credentials, health)
    webhooks/            # Validating webhooks (RuntimeEnvironment)
    common/              # Leader election, metrics, utilities

frontend/                # React 19 + TypeScript + Vite SPA

runtimes/                # Container images
  base/                  # opencode + redact + workspace-agentd + entrypoints
  python/, nodejs/, go/  # Language-specific extensions

pkg/                     # Shared Go packages
  apis/llmsafespace/v1/  # CRD Go types (Workspace, RuntimeEnvironment)
  agentd/                # Workspace-agentd sidecar types
  credentials/           # Credential set encryption service
  secrets/               # Zero-knowledge secret store (key wrapping, encryption, audit)
  settings/              # Declarative settings schema + services
  kubernetes/            # K8s client with leader election + typed CRD access
  mcp/                   # MCP server + client
  redact/                # Secret redaction pipeline
  types/                 # API DTOs

charts/llmsafespace/     # Helm chart (API, controller, frontend, CRDs, RBAC, webhooks)
local/                   # bootstrap.sh, test.sh, teardown.sh for kind
design/                  # Architecture and design docs (EVOLUTION-V2.md is authoritative)
```

---

## Development

### Prerequisites

- Go 1.25+
- Docker
- A Kubernetes cluster (or `kind`) and `kubectl`
- Helm 3 (for the deployment chart)
- Node.js 22+ (for the frontend)

### Run all tests

```bash
go test -timeout 90s -race ./...
```

### Local end-to-end on kind

```bash
cd local

# Bootstrap a kind cluster, build images, deploy LLMSafeSpace
./bootstrap.sh

# Run the e2e suite (9 tests). Set LLM_* env vars to enable the prompt
# round-trip and patch-part stripping checks.
LLM_BASE_URL=https://your-llm/v1 \
LLM_API_KEY=sk-... \
LLM_MODEL=default \
./test.sh

# Tear down
./teardown.sh
```

### Build container images

```bash
# API
docker build -f api/Dockerfile -t llmsafespace/api:dev .

# Controller
docker build -f controller/Dockerfile -t llmsafespace/controller:dev .

# Base runtime (opencode + redact + workspace-agentd + entrypoints)
docker build -f runtimes/base/Dockerfile -t llmsafespace/runtime-base:dev runtimes/base

# Frontend
docker build -f frontend/Dockerfile -t llmsafespace/frontend:dev frontend
```

CI builds and pushes these to `ghcr.io/lenaxia/llmsafespace/{api,controller,base,frontend}:dev` on every push to `main` (see `.github/workflows/ci.yml`).

---

## Security

- **Pod hardening**: read-only root, `runAsNonRoot`, drop all capabilities, no privilege escalation, AppArmor + seccomp profiles
- **Zero-knowledge secret store**: user secrets encrypted with per-user DEK (AES-256-GCM), derived from password via HKDF-SHA256. Platform never stores plaintext.
- **Workspace credentials** stored exclusively as Kubernetes Secrets — never in PostgreSQL, Redis, or logs
- **Egress filtering** via NetworkPolicies (configurable per Workspace)
- **API hardening**: rate limiting (Redis-backed, configurable via admin settings), account lockout, restrictive CORS defaults, JWT cache hashing, no token-in-query-string
- **Secret redaction**: 16-rule regex pipeline (`pkg/redact`) used by the runtime to scrub credentials from agent stdout
- **Audit logging**: every secret operation recorded in append-only audit log

See `design/0027_2026-05-24_security-policy-v21.md` and `design/0021_2026-05-21_evolution-v2.md` for the full threat model.

---

## License

Apache License 2.0. See [LICENSE](LICENSE).
