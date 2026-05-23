# LLMSafeSpace

A Kubernetes-first platform for running AI agents in isolated, persistent sandboxes.

Each sandbox runs [`opencode serve`](https://opencode.ai/docs/server/) — a headless HTTP server that drives an LLM agent — backed by a PVC-mounted workspace at `/workspace`. The LLMSafeSpace API service is a stateless reverse proxy in front of the sandbox pods, with auth, ownership checks, and quality-of-life filtering.

Repository: `github.com/lenaxia/llmsafespace`

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  Clients (REST / SSE)                                               │
│         │                                                            │
│         ▼                                                            │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  LLMSafeSpace API (Gin, stateless, horizontally scalable)    │   │
│  │  - Auth (JWT + API keys)                                     │   │
│  │  - Workspace + Sandbox CRUD                                  │   │
│  │  - Reverse proxy to sandbox pods (basic auth, IP refresh)    │   │
│  │  - Patch-part filtering (?verbose=true to keep)              │   │
│  └─────────────────────┬────────────────────────────────────────┘   │
│                        │ K8s API                                     │
│                        ▼                                             │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  Controller (controller-runtime)                              │   │
│  │  - Reconciles Workspace, Sandbox, RuntimeEnvironment,         │   │
│  │    SandboxProfile CRDs                                        │   │
│  │  - Mounts workspace credentials into sandbox pods             │   │
│  └─────────────────────┬────────────────────────────────────────┘   │
│                        │                                             │
│                        ▼                                             │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  Sandbox Pods (one per Sandbox CRD)                           │   │
│  │  - init: workspace-setup + credential-setup                   │   │
│  │  - main: opencode serve --hostname 0.0.0.0 --port 4096        │   │
│  │  - mounts: PVC at /workspace, secret as /sandbox-cfg          │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                      │
│  ┌─────────────────┐  ┌─────────────────┐                           │
│  │ PostgreSQL      │  │ Redis / Valkey  │                           │
│  │ (users, keys,   │  │ (rate limit,    │                           │
│  │  metadata)      │  │  cache, lockout)│                           │
│  └─────────────────┘  └─────────────────┘                           │
└──────────────────────────────────────────────────────────────────────┘
```

### Custom Resource Definitions

Four CRDs in the `llmsafespace.dev/v1` API group:

| Kind | Scope | Purpose |
|------|-------|---------|
| `Workspace` | Namespaced | PVC-backed persistent environment + credentials |
| `Sandbox` | Namespaced | Pod running `opencode serve` against a workspace |
| `SandboxProfile` | Namespaced | Reusable security and resource profile |
| `RuntimeEnvironment` | Cluster | Mapping from runtime name → container image |

### Lifecycle

```
Workspace: Pending → Active → Suspending → Suspended → Resuming → Active
Sandbox:   Pending → Creating → Running → Suspending → Suspended → Resuming → Running
                               ↘                       ↘
                                 Terminating → Terminated
```

Suspending a workspace deletes the sandbox pods but retains the PVC. Resuming creates fresh pods that reattach to the existing PVC, so opencode session history (stored in `/workspace/.local/opencode`) survives suspend/resume.

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
| `DELETE` | `/api/v1/workspaces/:id` | Delete (and its PVC) |
| `POST` | `/api/v1/workspaces/:id/suspend` | Suspend (retain PVC, delete pods) |
| `POST` | `/api/v1/workspaces/:id/resume` | Resume (re-create pods) |
| `GET` | `/api/v1/workspaces/:id/status` | Get phase + conditions |
| `PUT` | `/api/v1/workspaces/:id/credentials` | Set the LLM provider config (see below) |
| `DELETE` | `/api/v1/workspaces/:id/credentials` | Remove provider config |

### Sandboxes

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/sandboxes` | List the caller's sandboxes (paginated) |
| `POST` | `/api/v1/sandboxes` | Create a sandbox (referencing an existing workspace) |
| `GET` | `/api/v1/sandboxes/:id` | Get one sandbox |
| `DELETE` | `/api/v1/sandboxes/:id` | Terminate (deletes pod + CRD) |
| `GET` | `/api/v1/sandboxes/:id/status` | Get phase + pod IP |

### Sessions (proxied to opencode)

These endpoints are reverse-proxied to the sandbox pod's `opencode serve` instance on port 4096. The proxy injects HTTP basic auth for opencode automatically.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/sandboxes/:id/sessions` | Create a session |
| `GET` | `/api/v1/sandboxes/:id/sessions` | List sessions |
| `POST` | `/api/v1/sandboxes/:id/sessions/:sessionId/message` | Send a message; wait for the assistant reply |
| `POST` | `/api/v1/sandboxes/:id/sessions/:sessionId/prompt` | Send a message asynchronously (`204 No Content`) |
| `GET` | `/api/v1/sandboxes/:id/sessions/:sessionId/message` | Fetch session history |
| `POST` | `/api/v1/sandboxes/:id/sessions/:sessionId/abort` | Abort a running session |
| `GET` | `/api/v1/sandboxes/:id/events` | SSE event stream |

#### `?verbose=true` flag

By default, the proxy strips parts of `type=="patch"` from message and history responses. opencode emits a `patch` part for every assistant turn, listing every workspace file it touched (~2 KB per response of internal snapshot paths). For most clients this is noise.

Pass `?verbose=true` on any message or history request to receive the unfiltered response:

```
POST /api/v1/sandboxes/sb-1/sessions/ses_xyz/message?verbose=true
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

The `config` field is an opencode config document. The model and provider you declare here is what every sandbox under this workspace will use.

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

### 4. Create a sandbox

```bash
SB=$(curl -sX POST "$API/api/v1/sandboxes" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{
    \"runtime\":\"base\",
    \"workspaceRef\":\"$WS\",
    \"securityLevel\":\"standard\",
    \"resources\":{\"cpu\":\"500m\",\"memory\":\"512Mi\"}
  }" \
  | jq -r '.metadata.name')
echo "sandbox: $SB"

# Wait for it to come up
while [ "$(curl -s -H "Authorization: Bearer $TOKEN" \
    "$API/api/v1/sandboxes/$SB/status" | jq -r .phase)" != "Running" ]; do
  sleep 2
done
```

### 5. Drive a session

```bash
# Create a session
SID=$(curl -sX POST "$API/api/v1/sandboxes/$SB/sessions" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}' | jq -r '.id')

# Send a prompt
curl -X POST "$API/api/v1/sandboxes/$SB/sessions/$SID/message" \
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

Suspending the workspace deletes the sandbox pod but keeps the PVC. Resuming re-creates pods. Session history (stored in the PVC) survives.

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
    handlers/            # Reverse proxy + watchers
    middleware/          # Auth, rate limit, CORS, etc.
    services/            # Sandbox, Workspace, Auth, Cache, ratelimit
    server/router.go     # Gin route table
    mocks/               # Service mocks for tests

controller/              # Kubernetes operator (controller-runtime)
  internal/
    resources/           # CRD type definitions (kubebuilder annotated)
    sandbox/             # Sandbox reconciler
    workspace/           # Workspace reconciler

runtimes/                # Container images
  base/                  # opencode + redact + entrypoints
  python/, nodejs/, go/  # Language-specific extensions

pkg/                     # Shared Go packages
  apis/llmsafespace/v1/  # CRD Go types
  crds/                  # CRD YAML definitions
  redact/                # Secret redaction
  types/                 # API DTOs

local/                   # bootstrap.sh, test.sh, teardown.sh for kind
worklogs/                # Append-only session history
design/                  # Architecture and design docs (EVOLUTION-V2.md is authoritative)
```

---

## Development

### Prerequisites

- Go 1.23+
- Docker
- A Kubernetes cluster (or `kind`) and `kubectl`
- Helm 3 (for the deployment chart)

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

# Base runtime (opencode + redact + entrypoints)
docker build -f runtimes/base/Dockerfile -t llmsafespace/runtime-base:dev runtimes/base
```

CI builds and pushes these to `ghcr.io/lenaxia/llmsafespace/{api,controller,base}:dev` on every push to `main` (see `.github/workflows/ci.yml`).

---

## Security

- **Pod hardening**: read-only root, `runAsNonRoot`, drop all capabilities, no privilege escalation, AppArmor + seccomp profiles
- **Workspace credentials** are stored exclusively as Kubernetes Secrets — never in PostgreSQL, Redis, or logs
- **Egress filtering** via NetworkPolicies (configurable per Sandbox)
- **API hardening**: rate limiting (Redis-backed), account lockout, restrictive CORS defaults, JWT cache hashing, no token-in-query-string
- **Secret redaction**: 16-rule regex pipeline (`pkg/redact`) used by the runtime to scrub credentials from agent stdout

See `design/SECURITY.md` and `design/EVOLUTION-V2.md §9` for the full threat model.

---

## License

Apache License 2.0. See [LICENSE](LICENSE).
