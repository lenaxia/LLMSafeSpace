# LLMSafeSpace V2 Architecture Evolution

**Version:** 2.2
**Date:** 2026-05-21
**Status:** Draft — Authoritative Design Document
**Supersedes:** `WARMINGPOOL.md` (entirely — warm pools removed), selected aspects of `ARCHITECTURE.md` (deployment topology)

---

## Table of Contents

- [User Personas, Requirements, and Non-Requirements](#user-personas-requirements-and-non-requirements)
1. [Motivation](#1-motivation)
2. [Overview of Changes](#2-overview-of-changes)
3. [Usage Model](#3-usage-model)
4. [MCP Server Integration](#4-mcp-server-integration)
5. [Workspaces](#5-workspaces)
6. [Sessions — Suspend, Resume, Multi-Session](#6-sessions--suspend-resume-multi-session)
7. [Agent Architecture](#7-agent-architecture)
8. [Removing Warm Pools](#8-removing-warm-pools)
9. [Security Hardening from k8s-mechanic](#9-security-hardening-from-k8s-mechanic)
10. [CRD Changes](#10-crd-changes)
11. [API Changes](#11-api-changes)
12. [Controller Changes](#12-controller-changes)
13. [Runtime Changes](#13-runtime-changes)
14. [Implementation Roadmap](#14-implementation-roadmap)
15. [Migration Guide](#15-migration-guide)
16. [Risk Assessment](#16-risk-assessment)

---

## User Personas, Requirements, and Non-Requirements

### User Personas

**Persona 1: Developer — Interactive Chat**

Alex is a developer who opens a web portal and chats with an AI agent running inside a sandbox. The agent has access to a full development environment — filesystem, packages, tools. Alex sends prompts, sees streaming responses, reviews code the agent wrote, and asks follow-up questions. They close the browser, come back the next day, and their workspace (files, installed packages, conversation history) is still there.

- Access pattern: Ad-hoc, interactive, real-time
- Latency expectation: Streaming — first token within 1-2s
- Session length: Minutes to hours
- Persistence expectation: Workspace survives across sessions
- Transport: WebSocket or SSE from browser

**Persona 2: AI Agent / Program — Programmatic Async**

Claude, GPT, or a custom script needs an isolated environment to execute code, install dependencies, and run tools. It creates a sandbox, sends a prompt, and gets results back. It may be one-shot ("run these tests") or multi-step ("fix the bug, run tests, submit PR"). The caller may or may not follow up. Results are fetched via API — no streaming required.

- Access pattern: Scheduled, triggered, or chain-of-thought
- Latency expectation: No real-time requirement — complete results within timeout
- Session length: Seconds to minutes per call, but may chain multiple calls
- Persistence expectation: Workspace survives between calls in a multi-step workflow
- Transport: MCP, REST API, or SDK

**Persona 3: Platform Operator — Deployment and Management**

Sam deploys LLMSafeSpace on their Kubernetes cluster. They configure resource quotas, security policies, credential management, and network access. They monitor usage, costs, and health. They don't use sandboxes directly — they manage the platform that serves Personas 1 and 2.

- Access pattern: Infrequent configuration changes, continuous monitoring
- Concerns: Security, resource isolation, cost control, auditability
- Transport: Helm charts, K8s manifests, monitoring dashboards

### Anti-Personas (Who This Is NOT For)

| Anti-Persona | Why | What To Use Instead |
|-------------|-----|-------------------|
| General container orchestration | Not a container platform | Kubernetes directly |
| Real-time multiplayer collaboration | No shared-editing primitives | Figma, Google Docs |
| Untrusted public code execution at massive scale | Single-cluster, authenticated users only | Dedicated sandbox services (E2B, Fly.io Machines) |
| CI/CD pipeline execution | Not a build system | Jenkins, GitHub Actions |
| Bare-metal code execution without AI | Every sandbox runs an AI agent | Docker, Podman |

### Requirements

These are hard constraints. Every design decision must satisfy these.

**R1: Every sandbox is workspace-backed.** No ephemeral sandboxes. Every sandbox has a PVC-mounted persistent filesystem at `/workspace`. If the caller doesn't specify a workspace, one is auto-created.

**R2: Every sandbox runs an AI agent.** `opencode serve` runs as a persistent HTTP server inside every sandbox. The agent handles sessions, conversation history, and tool execution internally. LLMSafeSpace handles lifecycle and access control.

**R3: Two access modes, one infrastructure.** Interactive (WebSocket/SSE) and programmatic (REST/MCP) use the same sandbox, same agent, same workspace. The proxy architecture makes this transparent.

**R4: Credentials never touch the database.** LLM API keys are stored exclusively in K8s Secrets. Never in PostgreSQL, Redis, logs, or API responses. Enforced by Kyverno admission policy at the cluster level.

**R5: Suspend/resume for cost optimization.** Workspaces can be suspended (pod deleted, PVC retained) and resumed (pod recreated). Auto-suspend with configurable idle timeout. The PVC IS the warm state — resume is ~3s.

**R6: MCP compatibility.** LLMSafeSpace must be usable as an MCP server. Any MCP-compatible client (Claude, ChatGPT, VS Code, Cursor) can connect without a custom SDK.

**R7: Security hardening from k8s-mechanic.** Credential isolation via init containers, secret redaction pipeline, PATH-shadowing wrappers in high-security mode, Kyverno admission policies, network policies, exfiltration leak registry.

**R8: Horizontal scalability.** The API server is stateless. All session state lives inside sandbox pods. No sticky sessions required. Multiple API replicas behind a load balancer.

**R9: Source of truth clarity.** K8s CRDs are the source of truth for infrastructure state (workspace phase, sandbox phase, pod IP). PostgreSQL is the source of truth for user-facing metadata (workspace name, user ID, creation timestamps). The two never overlap — PostgreSQL never stores data that the controller owns (phase, conditions, PVC name), and the CRD never stores data that the API owns (user-friendly display name, tags).

### Non-Requirements

These are explicitly out of scope. Decisions that would optimize for these are over-engineering.

**NR1: Sub-second cold start.** 3-5s pod creation is acceptable. Long-lived agents absorb this cost. No warm pools, no pre-provisioned pods.

**NR2: Multi-user collaboration within a workspace.** One user per workspace. RWO PVC enforces one sandbox at a time. No shared-editing, no concurrent-user coordination.

**NR3: Custom container images per sandbox.** Runtimes are predefined (Python, Node.js, Go). Users install packages via `spec.packages` and `spec.initScript`, not custom Dockerfiles.

**NR4: GPU support.** CPU-only sandboxes. GPU passthrough is a future consideration.

**NR5: Cross-cluster workspace migration.** Workspaces are cluster-scoped. No migration between clusters.

**NR6: Built-in web UI.** LLMSafeSpace provides API + MCP only. The chat portal (Persona 1) is a separate frontend that consumes the API.

**NR7: Bare code execution without an agent.** There is no "just run this command" mode. Every sandbox has an AI agent. If the caller wants raw execution, the agent can be instructed to execute and return results.

**NR8: Multi-tenancy at the platform level.** One deployment serves one organization. Namespace isolation within a cluster is sufficient. No cross-org isolation.

---

## 1. Motivation

Since the initial design, three shifts in the AI infrastructure landscape require architectural evolution:

1. **MCP is now the standard tool-calling protocol.** Claude, ChatGPT, VS Code, Cursor, and dozens of other clients all speak MCP. LLMSafeSpace must be usable as an MCP server — not just via custom SDKs.

2. **Agents need to run *inside* sandboxes, not just send code *to* them.** Long-lived autonomous workflows (code review agents, data pipeline agents, research agents) need a persistent environment with their own filesystem, process tree, and identity — where results are fetched later.

3. **Resource management demands session durability.** Keeping pods running forever is expensive. Pods must be suspendable (pod deleted, filesystem preserved) and resumable (pod recreated, filesystem reattached). Multiple sessions within the same workspace must share a filesystem.

Additionally, the k8s-mechanic project has demonstrated a mature security hardening model that LLMSafeSpace should adopt where applicable.

---

## 2. Overview of Changes

| Area | Change | Impact |
|------|--------|--------|
| **Protocol** | Add MCP server alongside REST + WebSocket | New `api/internal/mcp/` package |
| **Resource model** | New `Workspace` CRD — PVC-backed durable environment | New CRD, new reconciler, new service |
| **Lifecycle** | Workspaces can be suspended (pod deleted, PVC retained) and resumed (pod recreated) | Sandbox CRD gains `Suspended`, `Suspending`, `Resuming` phases |
| **Agent** | Every sandbox runs `opencode serve` as a persistent HTTP server; API acts as reverse proxy | Proxy endpoints for session/message/event operations; no separate agent lifecycle |
| **Warm pool** | **Remove entirely** — replace with on-demand pod creation + workspace session management | Remove WarmPool/WarmPod CRDs and reconcilers |
| **Security** | Adopt k8s-mechanic patterns: secret redaction, PATH-shadowing wrappers, Kyverno policies, injection detection | New runtime tooling, Helm chart additions |

---

## 3. Usage Model

LLMSafeSpace V2 supports two usage modes that share the same infrastructure. Every sandbox is workspace-backed — there are no ephemeral sandboxes.

### Mode 1: Interactive Chat

A user opens a chat portal (web UI) and interacts with an agent running inside the sandbox. Think of it as a remote `opencode` session — the agent is already running, the user sends messages, and receives streaming responses in real-time.

```
User (browser) ↔ WebSocket/SSE ↔ LLMSafeSpace API ↔ proxy ↔ opencode serve (in sandbox)
```

- **Transport:** WebSocket or SSE from browser to LLMSafeSpace API; HTTP proxy to opencode inside the sandbox
- **Lifecycle:** Long-lived session. User can send multiple messages. Workspace persists across sessions.
- **Agent:** `opencode serve` runs as a persistent HTTP server inside the sandbox container

### Mode 2: Programmatic / Async

An external program or LLM triggers an agent session programmatically. Results are fetched later. May be one-shot (single prompt, single response) or multi-step (chain prompts based on previous results).

```
LLM / Program → MCP / REST API / SDK → LLMSafeSpace API → proxy → opencode serve (in sandbox)
```

- **Transport:** MCP, REST API, WebSocket streaming, or SDK — all hit the same backend
- **Lifecycle:** One-shot or multi-step. The caller decides whether to follow up.
- **Agent:** Same `opencode serve` instance. Calls create opencode sessions and send messages.

### Shared Infrastructure

Both modes use the same underlying components:

| Component | Role |
|-----------|------|
| **Workspace** | PVC-backed environment. Auto-created with sandbox if not specified. Persists across sessions. |
| **Sandbox** | K8s pod running `opencode serve` as a persistent HTTP server |
| **Session** | An opencode conversation within the sandbox. Multiple sessions per sandbox. |
| **Credentials** | LLM API keys stored as K8s Secrets, mounted via init container |

### Auto-Workspace Creation

Every sandbox is workspace-backed. If a caller creates a sandbox without specifying a `workspaceRef`:

1. The API auto-creates a workspace with default settings (5Gi storage, same runtime, standard security)
2. The sandbox references this auto-created workspace
3. The workspace ID is returned in the sandbox creation response
4. The caller can use this workspace ID for suspend/resume later

This keeps the API simple for one-shot callers while giving workspace controls to callers who need them.

---

## 4. MCP Server Integration

### 4.1 What MCP Provides

The [Model Context Protocol](https://modelcontextprotocol.io) is an open standard that lets AI applications connect to external systems through a client-server model. An MCP server exposes:

- **Tools** — functions the AI can call (create sandbox, execute code, read output)
- **Resources** — data the AI can read (workspace files, execution logs)
- **Prompts** — reusable prompt templates (e.g., "debug this code in a sandbox")

Any MCP-compatible client (Claude, ChatGPT, VS Code Copilot, Cursor) can connect without a custom SDK.

### 4.2 Architecture

```
┌─────────────────────────────┐
│   MCP Client (Claude, etc.) │
│   stdio / SSE transport     │
└──────────┬──────────────────┘
           │
           ▼
┌─────────────────────────────┐
│   LLMSafeSpace MCP Server   │  ← standalone binary
│   (api/internal/mcp/)       │
│                             │
│   Tools:                    │
│   - sandbox_create          │
│   - session_create          │
│   - session_message         │
│   - session_history         │
│   - sandbox_upload_file     │
│   - sandbox_download_file   │
│   - sandbox_terminate       │
│                             │
│   Resources:                │
│   - sandbox://{id}/sessions │
│   - workspace://{id}/files  │
│                             │
│   Prompts:                  │
│   - debug-in-sandbox        │
│   - run-tests               │
│   - code-review             │
└──────────┬──────────────────┘
           │ delegates to
           ▼
┌─────────────────────────────┐
│   LLMSafeSpace API          │
│   REST + WebSocket          │
└─────────────────────────────┘
```

### 4.3 Implementation Strategy

**Option A (Recommended): MCP server as a standalone binary** that reuses the API service layer. This avoids coupling MCP transport concerns with the HTTP server and allows the MCP server to be deployed independently (e.g., as a sidecar, or as a stdio subprocess launched by an MCP client).

```
api/
├── cmd/
│   ├── api/main.go          # existing HTTP API server
│   └── mcp/main.go          # new MCP server binary
├── internal/
│   └── mcp/
│       ├── server.go         # MCP server implementation
│       ├── tools.go          # tool definitions and handlers
│       ├── resources.go      # resource handlers
│       ├── prompts.go        # prompt templates
│       └── transport.go      # stdio + SSE transport
```

**Go MCP SDK:** Use [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) (or equivalent) — the Go MCP SDK that provides server scaffolding, tool registration, and transport handling.

### 4.4 Tool Definitions

The MCP server exposes only the tools an LLM agent needs. Every sandbox runs `opencode serve`, so MCP tools create sandboxes, manage conversation sessions, and transfer files. Workspace and credential management remain in the REST API only — MCP tools do not expose them.

**MCP uses opencode's `prompt_async` endpoint.** MCP serves Persona 2 (programmatic/async callers). The `session_message` MCP tool calls `POST /session/{id}/prompt_async`, which returns 204 immediately. Results arrive via the `GET /event` SSE channel. The MCP server subscribes to this channel, collects the complete response, and returns it as a single tool result. This is cleaner than buffering a streaming HTTP response — the async endpoint is designed exactly for this use case.

Persona 1 (interactive chat) uses `POST /session/{id}/message` directly via the REST/WebSocket API for HTTP streaming responses.

See [Section 7.5](#75-mcp-tools-updated) for full tool definitions.

### 4.5 Authentication

MCP servers accept a configuration object at startup. The LLMSafeSpace MCP server will accept:

```json
{
  "api_url": "https://api.llmsafespace.dev",
  "api_key": "lsp_...",
  "default_runtime": "python:3.10",
  "default_timeout": 300
}
```

The API key is forwarded to the LLMSafeSpace API via `Authorization: Bearer` header. The MCP server itself performs no authentication — it delegates to the API.

### 4.6 Transport

Support both MCP transport modes:

- **stdio** — for local development and when launched as a subprocess by an MCP client (Claude, VS Code)
- **SSE (Server-Sent Events)** — for remote deployment where the MCP server runs as a standalone HTTP service

---

## 5. Workspaces

### 5.1 Concept

A **Workspace** is a durable, PVC-backed environment that outlives any single sandbox pod. It provides:

- **Shared filesystem** — multiple sandboxes (sessions) can mount the same PVC
- **Persistence** — data survives pod deletion, restart, and suspension
- **Identity** — a stable name and ID that agents can reference across sessions
- **Resource quota** — storage size limits, max concurrent sessions

A workspace is the unit of resource management. When you suspend a workspace, all its sandbox pods are deleted but the PVC remains. When you resume, new pods are created mounting the same PVC.

### 5.2 Workspace CRD

```yaml
# pkg/crds/workspace_crd.yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: workspaces.llmsafespace.dev
spec:
  group: llmsafespace.dev
  scope: Namespaced
  names:
    kind: Workspace
    plural: workspaces
    singular: workspace
    shortNames: [ws]
  versions:
  - name: v1
    served: true
    storage: true
    subresources:
      status: {}
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            required: [storage]
            properties:
              owner:
                type: object
                required: [userID]
                properties:
                  userID: { type: string }
              storage:
                type: object
                required: [size]
                properties:
                  size:
                    type: string
                    pattern: "^[0-9]+(Gi|Mi)$"
                  storageClassName:
                    type: string
                  accessMode:
                    type: string
                    enum: [ReadWriteOnce, ReadWriteMany]
                    default: ReadWriteOnce
                    description: "ReadWriteOnce = one sandbox at a time (default). ReadWriteMany = concurrent sandboxes (requires RWX storage class like NFS/CephFS)."
              defaultRuntime:
                type: string
              securityLevel:
                type: string
                enum: [standard, high]
                default: standard
              networkAccess:
                type: object
                properties:
                  egress:
                    type: array
                    items:
                      type: object
                      properties:
                        domain: { type: string }
                  ingress:
                    type: boolean
                    default: false
              autoSuspend:
                type: object
                properties:
                  enabled:
                    type: boolean
                    default: false
                  idleTimeoutSeconds:
                    type: integer
                    default: 3600
              ttlSecondsAfterSuspended:
                type: integer
                default: 0
                description: "Time in seconds before a Suspended workspace is auto-deleted. 0 = never."
              packages:
                type: array
                description: "Packages installed by init container on every pod start. Idempotent — safe to rerun."
                items:
                  type: object
                  required: [runtime, requirements]
                  properties:
                    runtime: { type: string }
                    requirements:
                      type: array
                      items: { type: string }
              initScript:
                type: string
                description: "Shell script run by init container before main container starts. Runs on every pod start (including resume). Use for anything beyond simple package lists."
          status:
            type: object
            properties:
              phase:
                type: string
                enum: [Pending, Active, Suspended, Terminating, Terminated, Failed]
              pvcName:
                type: string
              activeSessions:
                type: integer
              lastActivityAt:
                type: string
                format: date-time
              conditions:
                type: array
                items:
                  type: object
                  properties:
                    type: { type: string }
                    status: { type: string }
                    reason: { type: string }
                    message: { type: string }
                    lastTransitionTime: { type: string, format: date-time }
```

### 5.3 Workspace Lifecycle

```
Pending → Active → Suspended → Active (resume)
                ↘              ↘
                  Terminating    Terminating
                       ↘              ↘
                     Terminated     Terminated
```

- **Pending** — Workspace CRD created, PVC provisioning in progress
- **Active** — PVC bound, at least one sandbox pod may be running
- **Suspended** — All sandbox pods deleted, PVC retained
- **Terminating** — Finalizer cleaning up PVC and sandbox resources
- **Terminated** — All resources cleaned up

### 5.4 PVC Management

The Workspace reconciler creates a PVC with the following spec:

```go
pvc := &corev1.PersistentVolumeClaim{
    ObjectMeta: metav1.ObjectMeta{
        Name:      fmt.Sprintf("workspace-%s", workspace.Name),
        Namespace: workspace.Namespace,
        Labels: map[string]string{
            "app":              "llmsafespace",
            "llmsafespace.dev/workspace": workspace.Name,
        },
        OwnerReferences: []metav1.OwnerReference{
            *metav1.NewControllerRef(workspace, GroupVersion.WithKind("Workspace")),
        },
    },
    Spec: corev1.PersistentVolumeClaimSpec{
        AccessModes: []corev1.PersistentVolumeAccessMode{
            accessModeOrDefault(workspace.Spec.Storage.AccessMode),
        },
        Resources: corev1.VolumeResourceRequirements{
            Requests: corev1.ResourceList{
                corev1.ResourceStorage: resource.MustParse(workspace.Spec.Storage.Size),
            },
        },
    },
}
if workspace.Spec.Storage.StorageClassName != "" {
    pvc.Spec.StorageClassName = &workspace.Spec.Storage.StorageClassName
}
```

### 5.5 Sandboxes within Workspaces

When a sandbox is created with a `workspaceRef`, the sandbox pod mounts the workspace PVC at `/workspace`:

```go
// In PodManager.CreateSandboxPod, when workspaceRef is set:
pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
    Name: "workspace",
    VolumeSource: corev1.VolumeSource{
        PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
            ClaimName: workspace.Status.PVCName,
        },
    },
})

pod.Spec.Containers[0].VolumeMounts = append(
    pod.Spec.Containers[0].VolumeMounts,
    corev1.VolumeMount{
        Name:      "workspace",
        MountPath: "/workspace",
    },
)
```

**Access mode is determined by `spec.storage.accessMode`:**
- `ReadWriteOnce` (RWO, default) — only one sandbox pod at a time. Suspend before resuming. This is the normal case.
- `ReadWriteMany` (RWX) — requires a storage class that supports it (NFS, CephFS). Allows concurrent pods. The user is responsible for coordination.

There is no `maxSessions` field. The reconciler enforces the constraint based on PVC access mode.

### 5.5a State Management: K8s CRD vs PostgreSQL

Two systems store workspace/sandbox state. Their responsibilities are strictly partitioned to avoid conflicts:

| Data | Owner | Source of Truth | Why |
|------|-------|-----------------|-----|
| Workspace phase (Pending/Active/Suspended) | Controller | K8s Workspace CRD | Controller reconciles this. API reads it. |
| PVC name | Controller | K8s Workspace CRD | Controller creates and owns the PVC. |
| Pod IP | Controller | K8s Sandbox CRD | Controller updates from pod status. |
| Sandbox phase | Controller | K8s Sandbox CRD | Controller reconciles this. |
| Conditions | Controller | K8s CRD | Standard K8s pattern. |
| Workspace display name | API | PostgreSQL | User-facing metadata. |
| User ID ownership | Both | K8s CRD (`spec.owner.userID`) is authoritative; PostgreSQL mirrors for query performance | API writes to both; controller reads from CRD. |
| Creation/update timestamps | Both | K8s CRD (`metadata.creationTimestamp`) is authoritative; PostgreSQL mirrors for query performance | K8s manages CRD timestamps. |
| Credential existence | Controller | K8s Secret presence | API creates Secrets; controller reads them. PostgreSQL never stores credentials. |

**Write rules:**
- API server writes to PostgreSQL and creates/updates K8s CRDs (via k8s client)
- API server also updates `status.lastActivityAt` on the Workspace CRD (batched, at most once per 60s) — this is the one status field the API writes, because the proxy handler observes activity, not the controller
- Controller writes to all other K8s CRD status fields — never touches PostgreSQL
- Neither system writes to the other's primary data

**Read rules:**
- API server reads from PostgreSQL for user queries (list workspaces, get details)
- API server reads from K8s CRD status for infrastructure state (phase, pod IP)
- Controller reads from K8s CRDs exclusively — never reads PostgreSQL

### 5.6 Auto-Suspend

When `autoSuspend` is enabled, the Workspace reconciler watches for idle periods (no execution activity for `idleTimeoutSeconds`). Implementation:

1. On each reconcile of an Active workspace, calculate remaining idle time
2. Requeue at `idleTimeoutSeconds * 0.8` from last activity (not a fixed 60s — this scales with the timeout)
3. If `time.Since(lastActivityAt) > idleTimeoutSeconds`, transition to Suspended
4. Activity is recorded by the proxy handler — on each proxied request to a sandbox, the API server patches the Workspace CRD's `status.lastActivityAt`. To avoid excessive K8s API calls, this is batched: the API server maintains an in-memory timestamp per workspace and flushes to the CRD at most once per 60 seconds using a status patch.

If `ttlSecondsAfterSuspended > 0`, the reconciler also requeues for suspended workspaces and transitions to Terminating when the TTL expires. This provides automatic garbage collection.

**Race condition handling:** Between the requeue check and the actual suspend, new activity could arrive. The reconciler performs a final `lastActivityAt` check immediately before deleting pods — if activity occurred after the requeue trigger, the suspend is cancelled and the workspace remains Active.

### 5.7 Credential Management

Users provide LLM API credentials (OpenAI, Anthropic, etc.) per-workspace or per-session. The platform never exposes credentials back to the user, logs, or API responses.

**Storage model:**

Credentials provided through the API are stored as Kubernetes Secrets in the sandbox namespace. Secrets are owner-referenced to the Workspace or Sandbox CRD — they are garbage-collected automatically when the parent resource is deleted.

| Scope | Secret name | Owner reference | Lifecycle |
|-------|-------------|-----------------|-----------|
| Workspace | `workspace-creds-{workspace_name}` | Workspace CRD | Lives until workspace is deleted |
| Session (sandbox) | `sandbox-creds-{sandbox_name}` | Sandbox CRD | Lives until sandbox is deleted |

Session-level credentials override workspace-level credentials. If a sandbox has both, the session credential is used.

**Provisioning flow:**

```
User → API: "Set my OpenAI key for workspace X"
  API:
    1. Authenticate user, verify ownership of workspace X
    2. Create/update K8s Secret: workspace-creds-X
       data:
         provider-config: <encrypted JSON blob>
       ownerReferences: [Workspace/X]
    3. Never store the credential in PostgreSQL
    4. Return 204 No Content (never echo the credential back)

Controller:
  When creating a sandbox pod for workspace X:
    1. Check if sandbox-creds-{sandbox_name} exists (session-level)
    2. If not, check if workspace-creds-{workspace_name} exists (workspace-level)
    3. Mount the secret as a volume in the credential-setup init container
    4. Init container copies to /sandbox-cfg/credentials (emptyDir)
    5. Main container reads /sandbox-cfg/credentials (read-only mount)
    6. entrypoint-common.sh copies to /tmp/agent-config.json, unsets from env
```

**API endpoints:**

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/workspaces/{id}/credentials` | PUT | Set workspace-level credentials |
| `/api/v1/workspaces/{id}/credentials` | DELETE | Remove workspace-level credentials |
| `/api/v1/sandboxes/{id}/credentials` | PUT | Set session-level credentials (overrides workspace) |
| `/api/v1/sandboxes/{id}/credentials` | DELETE | Remove session-level credentials |

**PUT request body:**

```json
{
  "provider": "openai",
  "config": {
    "apiKey": "sk-...",
    "model": "gpt-4o",
    "baseUrl": "https://api.openai.com/v1"
  }
}
```

The API service:
1. Validates the credential by making a lightweight API call (e.g., `GET /models`) — fail fast on bad credentials
2. Marshals the entire `config` object to JSON and stores it as the `provider-config` key in the K8s Secret
3. **Never** stores credentials in PostgreSQL, Redis, or logs
4. **Never** echoes credentials back in API responses

**Security properties:**

- Credentials exist only in K8s Secrets (encrypted at rest if etcd encryption is configured)
- Secrets are namespaced — no cross-namespace access
- Owner references ensure automatic cleanup
- The init container pattern ensures the main container never sees raw secrets in environment variables
- The `redact` binary catches any credential that leaks through stdout/stderr
- Kyverno policy (section 9.6) blocks main containers from directly referencing Secrets in env vars
- Users can only set credentials on resources they own (verified by `spec.owner.userID`)

**OpenCode integration:**

The `provider-config` JSON blob is written directly to the path OpenCode expects. For OpenCode, the config file format is:

```json
{
  "provider": "openai",
  "apiKey": "sk-...",
  "model": "gpt-4o"
}
```

The `entrypoint-common.sh` script copies this to `/tmp/agent-config.json` and sets `OPENCODE_CONFIG=/tmp/agent-config.json`. The `XDG_DATA_HOME=/workspace/.local` env var handles data persistence (see §7.9). No parsing or transformation needed — the user provides exactly what OpenCode expects.

---

## 6. Sessions — Suspend, Resume, Multi-Session

### 6.1 Concept

A **session** is an opencode conversation within a sandbox. There is no separate "Session" CRD or K8s resource — sessions are managed entirely by opencode inside the sandbox pod. Multiple sessions can exist within a single sandbox.

A **sandbox** is a K8s pod running `opencode serve`. It references a workspace via `spec.workspaceRef`. The workspace tracks how many sandboxes reference it via `status.activeSessions` (this counts sandboxes, not opencode sessions).

Suspend and resume are workspace-level operations:

- **Suspend workspace** — Delete all sandbox pods in the workspace, retain the PVC
- **Resume workspace** — Recreate sandbox pods mounting the same PVC
- **Multiple sandboxes** — Sequential (RWO) or concurrent (RWX) sandboxes sharing one PVC

**Suspend/resume and environment persistence:**

When a workspace is suspended and resumed, a new pod is created from the same container image. The PVC at `/workspace` survives, but the container's writable layer (installed packages, environment modifications) is lost. Three mechanisms address this:

1. **Install to the PVC.** Package managers support target directories: `pip install --target /workspace/packages`, `npm install --prefix /workspace/node_modules`. These survive resume because `/workspace` is the PVC.

2. **Declarative packages** (`spec.packages`). The controller adds an init container that runs on every pod start (including resume). It installs packages to `/workspace/packages` — idempotent, fast if already present.

3. **Init script** (`spec.initScript`). For anything beyond simple package lists (apt packages, compilation, custom setup), a user-provided shell script runs in an init container before the main container starts. Stored in a ConfigMap, owner-referenced to the Workspace CRD.

This is the container-native pattern: image + declarative config + PVC = reproducible persistent environment. No snapshotting needed.

### 6.2 Suspend Flow

```
User → API: POST /api/v1/workspaces/{id}/suspend
  API → Controller: Update Workspace phase → Suspending
    Controller:
      1. List all Sandboxes with workspaceRef = this workspace
      2. For each Active sandbox:
         a. Send SIGTERM to opencode process, wait for graceful shutdown
         b. Delete the sandbox pod (not the Sandbox CRD)
         c. Update Sandbox status.phase → Suspended
      3. Update Workspace status.phase → Suspended
```

### 6.3 Resume Flow

```
User → API: POST /api/v1/workspaces/{id}/resume
  API → Controller: Update Workspace phase → Resuming
    Controller:
      1. For each Suspended sandbox in this workspace:
         a. Create a new pod with the same spec
         b. Mount the workspace PVC at /workspace
         c. Update Sandbox status.phase → Running
      2. Update Workspace status.phase → Active
```

### 6.4 Sandbox Phase Changes

The existing Sandbox lifecycle gains a `Suspended` phase:

```
Pending → Creating → Running → Suspending → Suspended → Resuming → Running
                      ↘           ↘
                        Terminating → Terminated
                        Failed
```

New constants:

```go
const (
    PhaseSuspended = "Suspended"
    PhaseSuspending = "Suspending"
    PhaseResuming = "Resuming"
)
```

### 6.5 Session Identity Preservation

When a sandbox is suspended and resumed, the Sandbox CRD persists. The pod gets a new name but the sandbox ID stays the same. This means:

- API clients reference the same sandbox ID across suspend/resume cycles
- Opencode session history is preserved in the workspace PVC (at `/workspace/.local/opencode/storage/`) — see §7.9
- The workspace PVC provides filesystem continuity

---

## 7. Agent Architecture

### 7.1 Concept

Every sandbox runs `opencode serve` as a persistent HTTP server inside the main container. The LLMSafeSpace API acts as a reverse proxy — all agent interactions flow through HTTP requests proxied to the opencode server.

This supports both usage modes (section 3):
- **Interactive:** WebSocket/SSE from user's browser → LLMSafeSpace API → proxy → `opencode serve`
- **Programmatic:** REST/MCP call → LLMSafeSpace API → proxy → `opencode serve`

OpenCode handles sessions, conversation history, and tool execution internally. LLMSafeSpace handles sandbox lifecycle, credential injection, and access control.

### 7.1a OpenCode API Contract (Verified)

The proxy architecture depends on opencode exposing a specific HTTP API. **This contract has been verified against the opencode source code** at `/home/ubuntu/workspace/opencode/`. The server is implemented in TypeScript using the Hono web framework.

**Server startup:** `opencode serve --hostname 0.0.0.0 --port 4096`
- `--hostname` and `--port` are CLI flags (source: `packages/opencode/src/cli/network.ts`)
- Default port: 4096. Default hostname: `127.0.0.1` (must override for sandbox use)

**Authentication:** HTTP **Basic Auth** (NOT Bearer tokens). Controlled by env vars:
- `OPENCODE_SERVER_PASSWORD` — if set, enables Basic Auth on all endpoints
- `OPENCODE_SERVER_USERNAME` — defaults to `"opencode"` if password is set
- If password is NOT set, all endpoints are **unauthenticated** (source: `packages/opencode/src/server/server.ts:88-94`)
- The proxy must use `Authorization: Basic base64(username:password)` headers

**Config loading:** Layered precedence (source: `packages/opencode/src/config/config.ts`):
1. Global user config (`$XDG_CONFIG_HOME/opencode/opencode.json`)
2. `OPENCODE_CONFIG` env var — path to custom config file
3. Project config (`opencode.jsonc` found by walking up from cwd)
4. `OPENCODE_CONFIG_CONTENT` env var — inline JSON (highest precedence)

Config supports `{env:VAR_NAME}` and `{file:/path/to/file}` interpolation.

**Session storage:** Plain JSON files in `$XDG_DATA_HOME/opencode/storage/` (source: `packages/opencode/src/storage/storage.ts`). NOT SQLite. Each entity is a separate `.json` file. The data directory follows XDG conventions — **no dedicated override env var exists**. To persist to PVC, set `XDG_DATA_HOME=/workspace/.local` (sessions stored at `/workspace/.local/opencode/storage/`).

**Key endpoints** (source: `packages/opencode/src/server/routes/`):

| Endpoint | Method | Purpose | Response |
|----------|--------|---------|----------|
| `/global/health` | GET | Health check | `{ "healthy": true, "version": "..." }` |
| `/session` | POST | Create session | Session object (body: `{"title": "..."}`) |
| `/session` | GET | List sessions | Array of sessions |
| `/session/{sessionID}/message` | POST | Send message (HTTP streaming) | JSON stream (body: `{"parts": [{"type": "text", "text": "..."}]}`) |
| `/session/{sessionID}/message` | GET | Get message history | Array of messages with `info` and `parts` |
| `/session/{sessionID}/prompt_async` | POST | Send message async (204, results via `/event`) | 204 No Content (body: `{"parts": [{"type": "text", "text": "..."}]}`) |
| `/session/{sessionID}/abort` | POST | Abort session | Session object |
| `/event` | GET | SSE stream of all events | SSE stream with 30s heartbeat |
| `/global/event` | GET | SSE stream of global events | SSE stream |
| `/config` | GET | Get config | Config object |
| `/config/providers` | GET | List providers | Array of providers |
| `/file/content` | GET | Read file | File content |
| `/find` | GET | Search | Results |

Full API has ~80+ endpoints. OpenAPI spec available at `GET /doc`. The proxy only needs the endpoints listed above; remaining endpoints are available for future use.

**Streaming behavior:**
- `POST /session/{sessionID}/message` uses Hono's `stream()` — **plain HTTP streaming**, NOT SSE protocol. The response is JSON streamed directly.
- `POST /session/{sessionID}/prompt_async` returns 204 immediately. Results appear on the `GET /event` SSE channel. This is the correct endpoint for programmatic/MCP use (Persona 2).
- `GET /event` is a true SSE stream with 30-second heartbeats. Used for real-time event subscriptions (Persona 1).

**Event types on `GET /event` (verified):**

| Event Type | Payload | Use Case |
|---|---|---|
| `server.connected` | `{}` | Connection established |
| `message.updated` | Message info (id, sessionID, role, time, model) | Track message lifecycle |
| `message.part.updated` | Part (id, type, text) | Message content as it arrives |
| `session.updated` | Session info (id, title, summary) | Session metadata changes |
| `session.status` | `{ sessionID, status: { type: "busy" \| "idle" } }` | Processing state |
| `session.error` | `{ sessionID, error: { name, data: { message, statusCode, ... } } }` | Error details |
| `session.idle` | `{ sessionID }` | Processing complete — signal for MCP to return result |
| `session.diff` | `{ sessionID, diff: [...] }` | File changes |

### 7.2 Pod Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  Sandbox Pod                                                  │
│                                                               │
│  ┌────────────────────────────────────────────────────────┐  │
│  │  Init Container: "workspace-setup" (if spec.packages    │  │
│  │  or spec.initScript are set)                             │  │
│  │  1. Install declared packages to /workspace/packages    │  │
│  │  2. Run initScript if provided                          │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                               │
│  ┌────────────────────────────────────────────────────────┐  │
│  │  Init Container: "credential-setup" (if credentials     │  │
│  │  or server password exist for this workspace/sandbox)    │  │
│  │  Read K8s Secret → write to /sandbox-cfg/credentials    │  │
│  │  Read K8s Secret → write to /sandbox-cfg/password       │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                               │
│  ┌────────────────────────────────────────────────────────┐  │
│  │  Init Container: "mode-gate" (if securityLevel=high)    │  │
│  │  Write sentinel: /sandbox-cfg/high-security             │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                               │
│  ┌────────────────────────────────────────────────────────┐  │
│  │  Main Container: "sandbox"                               │  │
│  │                                                         │  │
│  │  Entrypoint: /usr/local/bin/entrypoint-opencode.sh      │  │
│  │  │                                                      │  │
│  │  ├─ Sources entrypoint-common.sh                        │  │
│  │  │   ├─ Materializes credentials: /sandbox-cfg → file  │  │
│  │  │   ├─ Sets XDG_DATA_HOME=/workspace/.local (persist)  │  │
│  │  │   └─ Exports OPENCODE_SERVER_PASSWORD from file       │  │
│  │  │                                                      │  │
│  │  └─ exec opencode serve \                               │  │
│  │         --hostname 0.0.0.0 \                            │  │
│  │         --port 4096                                     │  │
│  │                                                         │  │
│  │  Exposes: HTTP API on port 4096                         │  │
│  │  - POST /session              → create session          │  │
│  │  - POST /session/{id}/message → send prompt             │  │
│  │  - GET  /session/{id}/message → get history             │  │
│  │  - GET  /event                → SSE event stream        │  │
│  │                                                         │  │
│  │  Security:                                              │  │
│  │  - readOnlyRootFilesystem: true                         │  │
│  │  - runAsNonRoot: true                                   │  │
│  │  - capabilities: drop ALL                               │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                               │
│  Volumes:                                                     │
│  ├── sandbox-cfg    (emptyDir) — credentials + sentinels     │
│  ├── workspace      (PVC)     — persistent workspace data    │
│  ├── tmp            (emptyDir) — writable /tmp                │
│  └── sandbox-home   (emptyDir) — writable /home/sandbox       │
└──────────────────────────────────────────────────────────────┘
```

### 7.3 Proxy Architecture

The LLMSafeSpace API discovers the sandbox pod's IP from the Sandbox CRD status (updated by the controller) and proxies HTTP requests to `http://{pod_ip}:4096`:

```
┌─────────────┐     ┌──────────────────┐     ┌─────────────────┐
│  Caller      │     │  LLMSafeSpace    │     │  Sandbox Pod    │
│              │     │  API Server      │     │                 │
│  Browser     │──WS─│  /sandboxes/{id} │────▶│  opencode serve │
│  MCP Client  │─────│  /session/...    │────▶│  :4096          │
│  REST/SDK    │─────│  (reverse proxy) │────▶│                 │
└─────────────┘     └──────────────────┘     └─────────────────┘
```

The API service:
1. Resolves sandbox ID → pod IP (from Sandbox CRD status — always from informer-cached CRD, never direct pod lookup)
2. Authenticates and authorizes the caller
3. Proxies the request to `http://{pod_ip}:4096{path}`
4. Sets the `Authorization: Basic base64(opencode:{password})` header for opencode server authentication (password from controller-generated Secret, readable by API service's SA)

If the pod IP is stale (pod restarted, returned 404 or connection refused), the proxy refreshes the IP from the CRD status and retries once.

### 7.4 API Endpoints (Proxy)

The LLMSafeSpace API exposes opencode's session API under each sandbox:

| LLMSafeSpace Endpoint | Proxies to opencode | Purpose |
|----------------------|---------------------|---------|
| `POST /api/v1/sandboxes/{id}/sessions` | `POST /session` | Create a conversation session |
| `GET /api/v1/sandboxes/{id}/sessions` | `GET /session` | List sessions |
| `POST /api/v1/sandboxes/{id}/sessions/{sessionId}/message` | `POST /session/{sessionId}/message` | Send a message (HTTP streaming response) |
| `POST /api/v1/sandboxes/{id}/sessions/{sessionId}/prompt` | `POST /session/{sessionId}/prompt_async` | Send message async (204, results via events) |
| `GET /api/v1/sandboxes/{id}/sessions/{sessionId}/message` | `GET /session/{sessionId}/message` | Get message history |
| `POST /api/v1/sandboxes/{id}/sessions/{sessionId}/abort` | `POST /session/{sessionId}/abort` | Abort current processing |
| `GET /api/v1/sandboxes/{id}/events` | `GET /event` | SSE stream of all agent events |
| `WS /api/v1/sandboxes/{id}/stream` | SSE `GET /event` | WebSocket wrapper over SSE for browser clients |

The proxy is transparent — request bodies and response streams pass through unchanged. OpenCode handles all session state, conversation history, and tool execution.

### 7.5 MCP Tools (Updated)

MCP tools map directly to the proxy endpoints:

```go
var Tools = []mcp.Tool{
    {
        Name: "sandbox_create",
        Description: "Create a sandbox with an opencode agent server",
        InputSchema: mcp.Schema{
            Type: "object",
            Properties: map[string]mcp.Property{
                "runtime":       {Type: "string", Description: "Runtime (python:3.10, nodejs:18, go:1.21)"},
                "workspace_id":  {Type: "string", Description: "Optional workspace to attach"},
                "security_level":{Type: "string", Description: "standard or high"},
            },
            Required: []string{"runtime"},
        },
    },
    {
        Name: "session_create",
        Description: "Create a conversation session in a sandbox",
        InputSchema: mcp.Schema{
            Type: "object",
            Properties: map[string]mcp.Property{
                "sandbox_id": {Type: "string", Description: "Sandbox ID"},
            },
            Required: []string{"sandbox_id"},
        },
    },
    {
        Name: "session_message",
        Description: "Send a message to an agent session and get a response",
        InputSchema: mcp.Schema{
            Type: "object",
            Properties: map[string]mcp.Property{
                "sandbox_id":  {Type: "string", Description: "Sandbox ID"},
                "session_id":  {Type: "string", Description: "Session ID"},
                "message":     {Type: "string", Description: "The message/prompt to send"},
            },
            Required: []string{"sandbox_id", "session_id", "message"},
        },
    },
    {
        Name: "session_history",
        Description: "Get the message history of a session",
        InputSchema: mcp.Schema{
            Type: "object",
            Properties: map[string]mcp.Property{
                "sandbox_id":  {Type: "string", Description: "Sandbox ID"},
                "session_id":  {Type: "string", Description: "Session ID"},
            },
            Required: []string{"sandbox_id", "session_id"},
        },
    },
    {
        Name: "sandbox_terminate",
        Description: "Terminate a sandbox",
        InputSchema: mcp.Schema{
            Type: "object",
            Properties: map[string]mcp.Property{
                "sandbox_id": {Type: "string", Description: "Sandbox ID"},
            },
            Required: []string{"sandbox_id"},
        },
    },
    {
        Name: "sandbox_upload_file",
        Description: "Upload a file to a sandbox's workspace",
        InputSchema: mcp.Schema{
            Type: "object",
            Properties: map[string]mcp.Property{
                "sandbox_id":  {Type: "string", Description: "Sandbox ID"},
                "path":        {Type: "string", Description: "Remote path"},
                "content_b64": {Type: "string", Description: "Base64-encoded file content"},
            },
            Required: []string{"sandbox_id", "path", "content_b64"},
        },
    },
    {
        Name: "sandbox_download_file",
        Description: "Download a file from a sandbox's workspace",
        InputSchema: mcp.Schema{
            Type: "object",
            Properties: map[string]mcp.Property{
                "sandbox_id": {Type: "string", Description: "Sandbox ID"},
                "path":       {Type: "string", Description: "Remote path"},
            },
            Required: []string{"sandbox_id", "path"},
        },
    },
}
```

### 7.6 Pod Spec (Controller-Side)

The controller builds sandbox pods with init containers for credential/setup and a main container that runs `opencode serve`:

```go
// Main container — runs opencode serve as a persistent HTTP server
mainContainer := corev1.Container{
    Name:    "sandbox",
    Image:   runtimeImage,
    Command: []string{"/usr/local/bin/entrypoint-opencode.sh"},
    Ports: []corev1.ContainerPort{
        {ContainerPort: 4096, Name: "opencode", Protocol: corev1.ProtocolTCP},
    },
    Env: []corev1.EnvVar{
        {Name: "SANDBOX_ID", Value: sandbox.Name},
        {Name: "WORKSPACE_DIR", Value: "/workspace"},
        // NOTE: No SecretKeyRef env vars here.
        // OPENCODE_SERVER_PASSWORD is injected via projected volume
        // in credential-setup init container → /sandbox-cfg/password
        // entrypoint-common.sh reads it from file.
        // This complies with the Kyverno policy that blocks Secret env refs
        // on the main container.
    },
    SecurityContext: &corev1.SecurityContext{
        ReadOnlyRootFilesystem:   ptr(true),
        RunAsNonRoot:             ptr(true),
        AllowPrivilegeEscalation: ptr(false),
        Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
    },
    // ... volume mounts
}
```

The credential-setup init container mounts both the credentials Secret and the password Secret via projected volumes, writing both to the shared emptyDir:

```go
// Init container: credential-setup
initContainers = append(initContainers, corev1.Container{
    Name:    "credential-setup",
    Image:   runtimeImage,
    Command: []string{"/bin/sh", "-c", credentialSetupScript},
    VolumeMounts: []corev1.VolumeMount{
        {Name: "sandbox-cfg", MountPath: "/sandbox-cfg"},
        {Name: "cred-secret", MountPath: "/mnt/secrets/credentials", ReadOnly: true},
        {Name: "pw-secret", MountPath: "/mnt/secrets/password", ReadOnly: true},
    },
})
```

Where `credentialSetupScript` copies `/mnt/secrets/credentials` → `/sandbox-cfg/credentials` and `/mnt/secrets/password/password` → `/sandbox-cfg/password`. The main container mounts `sandbox-cfg` read-only.

A `workspace-setup` readiness probe confirms opencode is accepting connections:

```go
ReadinessProbe: &corev1.Probe{
    ProbeHandler: corev1.ProbeHandler{
        HTTPGet: &corev1.HTTPGetAction{
            Port:   intstr.FromInt(4096),
            Path:   "/global/health",
            Headers: []corev1.HTTPHeader{
                {Name: "Authorization", Value: "Basic " + basicAuthHeader},
            },
        },
    },
    InitialDelaySeconds: 2,
    PeriodSeconds:       5,
},
```

Note: The readiness probe must include Basic Auth headers because opencode requires authentication on all endpoints when `OPENCODE_SERVER_PASSWORD` is set. The probe uses the same password from the Secret.

Init containers (workspace-setup, credential-setup, mode-gate) are described in the pod architecture diagram in §7.2.

### 7.7 Entrypoint Scripts

**`entrypoint-opencode.sh`:**

```bash
#!/usr/bin/env bash
set -euo pipefail
source /usr/local/bin/entrypoint-common.sh

export OPENCODE_CONFIG=/tmp/agent-config.json

# Persist session data to PVC (XDG_DATA_HOME overrides where opencode stores JSON files)
export XDG_DATA_HOME=/workspace/.local

# Read password from file (written by credential-setup init container)
# OpenCode reads OPENCODE_SERVER_PASSWORD env var (NOT a CLI flag)
if [[ -f /sandbox-cfg/password ]]; then
    export OPENCODE_SERVER_PASSWORD=$(cat /sandbox-cfg/password)
fi

exec opencode serve \
    --hostname 0.0.0.0 \
    --port 4096
```

**`entrypoint-common.sh`** — credential materialization (`/sandbox-cfg/credentials` → `/tmp/agent-config.json`), sentinel check.

### 7.8 Agent State Machine

The sandbox phase now directly reflects the opencode server state:

| Sandbox `status.phase` | Meaning |
|------------------------|---------|
| Pending | Pod creating, init containers running |
| Running | `opencode serve` is up, accepting connections |
| Suspending | Graceful shutdown, then pod deletion |
| Suspended | Pod deleted, PVC retained |
| Failed | Pod or init container failed |

No separate `agentStatus` sub-state needed — the opencode server handles sessions internally. Session state is tracked by opencode, not by the Sandbox CRD.

### 7.9 Session Persistence

OpenCode stores session data as individual JSON files in `$XDG_DATA_HOME/opencode/storage/` (verified: `packages/opencode/src/storage/storage.ts`). There is no dedicated "data directory" config field — it follows XDG conventions.

To persist sessions across suspend/resume, the entrypoint sets `XDG_DATA_HOME=/workspace/.local` (on the PVC). Sessions are then stored at `/workspace/.local/opencode/storage/` and survive pod deletion.

The `entrypoint-opencode.sh` script (see §7.7) handles this:
```bash
export XDG_DATA_HOME=/workspace/.local
```

No config file merge is needed for the data directory. The `OPENCODE_CONFIG` file only contains LLM provider credentials (from `/sandbox-cfg/credentials`).

### 7.10 Health Monitoring

The controller monitors sandbox health via:

1. **Kubernetes readiness probe** — HTTP GET to `:4096/global/health` — confirms opencode is accepting connections (returns `{ "healthy": true }`)
2. **Kubernetes liveness probe** — same endpoint — restarts container if unresponsive
3. **Sandbox CRD status** — controller updates `status.phase` and `status.podIP` based on pod conditions

### 7.11 Robustness Considerations

**Proxy reconnect handling:**

The LLMSafeSpace API is stateless. If it restarts, in-flight WebSocket/SSE connections drop. Clients must implement reconnect logic:

- Interactive clients (browsers): reconnect with exponential backoff (1s, 2s, 4s, max 30s). Session state is preserved in opencode — no message loss.
- Programmatic clients: retry the HTTP request. Idempotent endpoints (GET history) are safe to retry. Message sends should use client-side idempotency keys.

**Pod IP staleness:**

When a workspace is suspended and resumed, the pod gets a new IP. The proxy handles this gracefully:

1. First request to old IP fails (connection refused)
2. Proxy refreshes IP from Sandbox CRD status (informer-cached, no API call)
3. Retries the request once with the new IP
4. If still failing, returns 503 with `Retry-After` header

This is transparent to the caller — no client-side changes needed.

**Init container security:**

All init containers (workspace-setup, credential-setup, mode-gate) run with the same restricted security context as the main container:

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  allowPrivilegeEscalation: false
  capabilities:
    drop: ["ALL"]
  readOnlyRootFilesystem: true
```

The `spec.initScript` runs within these constraints. It cannot escalate privileges, access K8s API, or modify the container image. Network policy limits apply to init containers as well.

### 7.12 Scalability Considerations

**Horizontal API scaling:**

The API server is stateless — no in-memory session state. All session state lives in opencode inside the sandbox pod. This means:

- Multiple API replicas can serve requests behind a load balancer
- No sticky sessions required (any replica can proxy to any sandbox)
- WebSocket connections are long-lived — use a connection-aware load balancer (e.g., Envoy with WebSocket support) or limit WS connections per replica

**Controller scaling:**

- The workspace reconciler uses controller-runtime's informer cache — no direct K8s API calls for status lookups
- Auto-suspend idle checks use requeue-based timers, not polling. At 1000+ workspaces, consider sharding workspaces across controller replicas using leader election with namespace partitioning
- Pod IP lookups use the informer-cached Sandbox CRD status exclusively — never direct pod lookups

**Per-sandbox rate limiting:**

Each opencode instance handles one request at a time (per session). The proxy enforces a per-sandbox connection limit (10 concurrent connections) to prevent a single caller from overwhelming an instance. This is implemented in the proxy handler, not in middleware — it needs to count actual in-flight proxy connections.

---

## 8. Removing Warm Pools

### 8.1 Why

Warm pools (pre-provisioned pods for fast startup) are no longer justified:

1. **V2 is designed for long-lived agents** — agents run for hours or days. A 3-5s pod creation cost is negligible.
2. **Workspace sessions replace "warm."** A suspended workspace has its PVC already bound and ready. Resuming (recreating the pod) takes ~3s. This is the new "warm startup."
3. **Warm pools added complexity** — two CRDs (WarmPool, WarmPod), two reconcilers, pool sizing logic, assignment logic, recycling logic.
4. **Workspace sandboxes couldn't use them anyway** — init containers are immutable after pod creation. Warm pods had a fixed spec and couldn't be retrofitted with workspace-specific init containers.
5. **All sandboxes are workspace-backed** — auto-created workspace for callers who don't specify one means no sandboxes bypass the workspace model.

### 8.2 What Changes

**Remove entirely:**

| Item | Location | Action |
|------|----------|--------|
| `WarmPool` CRD | `pkg/crds/`, `controller/internal/resources/` | Delete CRD definition and types |
| `WarmPod` CRD | `pkg/crds/`, `controller/internal/resources/` | Delete CRD definition and types |
| WarmPool reconciler | `controller/internal/warmpool/` | Delete directory |
| WarmPod reconciler | `controller/internal/warmpod/` | Delete directory |
| `RecyclePod()` | `controller/internal/common/pod_manager.go` | Delete function |
| Recycle annotations | `controller/internal/common/constants.go` | Delete `AnnotationRecyclable`, `AnnotationRecycleCount`, `AnnotationLastRecycled` |
| Warm pool assignment | `controller/internal/sandbox/controller.go` | Remove warm pod lookup; always create pod directly |
| Warm pool API endpoints | `api/internal/server/router.go` | Remove warmpool routes |
| Warm pool service | `api/internal/services/warmpool/` | Delete directory |

### 8.3 What Replaces It

**On-demand pod creation** for all sandboxes:

```
Sandbox CRD created
  → Sandbox reconciler creates pod directly (no warm pool lookup)
  → Pod starts with appropriate init containers based on workspace config
  → Pod runs until sandbox terminates or workspace suspends
```

**Suspended workspaces as "warm" state:**

```
Workspace suspended:
  → PVC is bound and ready (~0ms)
  → Pod is deleted
  → Next resume: create new pod mounting existing PVC (~3s)
```

This is equivalent to a warm pool but simpler: the PVC IS the warm state, and pod creation is the only operation.

### 8.4 Impact

- **Complexity:** Remove ~800+ lines of controller code, 2 CRDs, 2 reconcilers
- **Security:** No residual state concerns from pre-provisioned pods
- **Performance:** On-demand pod creation adds ~3-5s startup. Suspended workspace resume is ~3s (PVC already bound). Long-lived agents absorb this cost.
- **Reliability:** Fewer reconcilers = fewer failure modes. No pool sizing bugs, no assignment races.

---

## 9. Security Hardening from k8s-mechanic

k8s-mechanic (`github.com/lenaxia/k8s-mechanic`, on disk at `/home/ubuntu/workspace/k8s-mendabot/`) demonstrates the most security-hardened Kubernetes operator pattern we've encountered — 7 independent defense layers, a formal threat model, pentest reports, and an exfiltration leak registry. The following patterns are directly portable.

### 9.1 Credential Lifecycle and Isolation

**Source:** `k8s-mechanic/internal/jobbuilder/job.go:126-177`

k8s-mechanic **never** puts secrets in the main container's environment. LLMSafeSpace adopts the same principle with a user-facing credential management layer on top.

**End-to-end credential flow:**

```
User provides credential via REST API
  → API validates ownership (spec.owner.userID matches authenticated user)
  → API creates K8s Secret (owner-ref'd to Workspace or Sandbox CRD)
  → Secret exists only in K8s, never in PostgreSQL/Redis/logs

Controller creates sandbox pod:
  → Resolves credential secret (session-level > workspace-level)
  → Mounts secret in credential-setup init container only
  → Init container copies to /sandbox-cfg/credentials (shared emptyDir)

Main container starts:
  → entrypoint-common.sh copies /sandbox-cfg/credentials → /tmp/agent-config.json
  → Child processes never see credentials in `env` output

Cleanup:
  → K8s garbage collection deletes Secret when owner CRD is deleted
  → /tmp and /sandbox-cfg are emptyDirs — destroyed with the pod
```

**RBAC model:**

- The controller's ServiceAccount has full access to manage Sandbox, Workspace CRDs and their associated Secrets/PVCs
- The API service's ServiceAccount can:
  - Create/update/delete Secrets in the sandbox namespace (for credential management)
  - Read the controller-generated password secrets (for proxy authentication)
- The sandbox pod runs under a dedicated ServiceAccount (`sandbox-sa`) with:
  - **No** `get/list/watch` permissions on Secrets (prevents direct secret reads from main container)
  - The `credential-setup` init container reads Secrets via **projected volume mounts** — K8s mounts the secret data into the container filesystem without granting the pod's SA any Secret RBAC permissions. This is the same pattern k8s-mechanic uses: the kubelet (which runs as the node's SA) mounts the volume, not the pod.
  - Both the LLM credentials Secret and the server password Secret are mounted this way
- Owner references on Secrets ensure automatic cleanup via K8s garbage collection

**Server password Secret lifecycle:**

The controller auto-generates a random password for each sandbox and stores it in a K8s Secret (`sandbox-pw-{sandbox_name}`, owner-referenced to the Sandbox CRD). This secret:
- Is mounted via projected volume in the `credential-setup` init container → written to `/sandbox-cfg/password`
- Is readable by the API service's SA (used for proxy authentication headers)
- Is garbage-collected when the Sandbox CRD is deleted
- Is never visible to the main container's process (read from file by entrypoint, never in env)

### 9.2 Sentinel-Based Mode Detection

**Source:** `k8s-mechanic/docker/Dockerfile.agent` (sentinel file pattern)

k8s-mechanic uses a three-layer enforcement for dry-run mode because the LLM must never discover it's in dry-run. For LLMSafeSpace, the requirement is simpler: high-security mode is the sandbox's own configuration, not a secret from it. A single sentinel file (written by init container, mounted read-only in main container) is sufficient.

| Layer | Mechanism | Used in LLMSafeSpace |
|-------|-----------|---------------------|
| 1. Sentinel file | `/sandbox-cfg/high-security` written by init container, read-only mount | **Yes** — tamper-proof via read-only volume |
| 2. `/proc/1/environ` | PID-1 env, kernel-immutable | **No** — unnecessary complexity |
| 3. Environment variable | Shell env, `unset` bypasses | **No** — unreliable |

The wrappers check the sentinel file. If it contains `"true"`, they enforce write-blocking. This is simple, auditable, and sufficient.

### 9.3 Secret Redaction Pipeline

**Source:** `k8s-mechanic/internal/domain/redact.go:1-55` (16 compiled regex rules)

A standalone `redact` binary (`cmd/redact`) is compiled and installed in the runtime image. All tool output passes through it via PATH-shadowing wrappers. If `redact` is missing, wrappers **fail-closed** (exit 1) rather than emitting unredacted output.

Patterns to port (ordered by match priority):

```go
{`(?i)://[^:@\s]*:[^@\s]+@`, `://[REDACTED]@`},                        // URL credentials
{`(?i)(bearer )\S+`, `${1}[REDACTED]`},                                  // Bearer tokens
{`gh[a-z]_[A-Za-z0-9]{36,}`, `[REDACTED-GH-TOKEN]`},                    // GitHub tokens
{`(?i)("password"\s*:\s*)"[^"]*"`, `${1}"[REDACTED]"`},                  // JSON passwords
{`(?i)(password\s*[=:]\s*)\S+`, `${1}[REDACTED]`},                       // password=
{`(?i)(token\s*[=:]\s*)\S+`, `${1}[REDACTED]`},                          // token=
{`(?i)(secret\s*[=:]\s*)\S+`, `${1}[REDACTED]`},                         // secret=
{`(?i)(api[_-]?key\s*[=:]\s*)\S+`, `${1}[REDACTED]`},                    // api_key=
{`(?i)(x-api-key\s*[=:]\s*)\S+`, `${1}[REDACTED]`},                      // x-api-key=
{`(?is)-----BEGIN .*PRIVATE KEY-----.*?-----END .*PRIVATE KEY-----`, `[REDACTED-PEM-KEY]`},
{`(?i)AGE-SECRET-KEY-1[A-Z0-9]{40,}`, `[REDACTED-AGE-KEY]`},            // age keys
{`sk-[a-zA-Z0-9_\-]{4,}[A-Za-z0-9]{16,}`, `[REDACTED-SK-KEY]`},         // OpenAI/Anthropic
{`AKIA[A-Z0-9]{16}`, `[REDACTED-AWS-KEY]`},                              // AWS IAM
{`ey[A-Za-z0-9_\-]{10,}\.ey[A-Za-z0-9_\-]{10,}`, `[REDACTED-JWT]`},     // JWTs
{`(?i)(authorization\s*:\s*)\S+`, `${1}[REDACTED]`},                     // Auth headers
{`[A-Za-z0-9+/]{40,}={0,2}`, `[REDACTED-BASE64]`},                       // Long base64
```

User-extensible via a mounted config file (`/sandbox-cfg/redact-patterns.json`), not an environment variable (regex patterns may contain commas).

**Opt-out for binary output:** Binary data (images, compiled files) should bypass redaction to avoid corruption. The opencode agent handles this internally — tool output that is explicitly binary skips the redactor. Text output always passes through redaction.

**Deployment in LLMSafeSpace:**

1. Compile `redact` binary as a Go cross-platform tool: `pkg/redact/` → `cmd/redact/`
2. Include in base runtime image at `/usr/local/bin/redact`
3. PATH-shadowing wrappers (high-security mode only) pipe all tool output through `redact`

### 9.4 PATH-Shadowing Wrappers

**Source:** `k8s-mechanic/docker/scripts/redact-wrappers/kubectl` (153 lines, two-tier blocking)

k8s-mechanic renames real binaries (e.g., `kubectl` → `kubectl.real`) and installs wrapper scripts at the original path. Wrappers implement two tiers:

| Tier | Scope | What it blocks |
|------|-------|----------------|
| 1 (always-on) | All sandboxes in high-security mode | `apply`, `create`, `delete`, `edit`, `patch`, `replace`, `scale`, `label`, `annotate`, `taint`, `drain`, `cordon`, `uncordon`, `rollout restart/undo` |
| 2 (opt-in hardened) | `high` security level | Additionally blocks `get/describe secret(s)`, `get all`, `exec`, `port-forward` |

Applied to LLMSafeSpace:

```
runtimes/base/tools/wrappers/
├── curl        # Output through redact
├── wget        # Output through redact
└── git         # Output through redact
```

Note: kubectl is not included in sandbox images by default (opencode doesn't use it). If a custom runtime adds kubectl, the operator can add a wrapper following the same pattern.

In Dockerfile (high-security variant):

```dockerfile
# Rename real binaries (kubectl excluded — not present in sandbox images)
RUN mv /usr/bin/git /usr/bin/git.real || true

# Install wrappers (PATH-shadow)
COPY --chmod=755 tools/wrappers/curl    /usr/local/bin/curl
COPY --chmod=755 tools/wrappers/wget    /usr/local/bin/wget
```

Activated when the sentinel file `/sandbox-cfg/high-security` contains `"true"` (written by `mode-gate` init container).

### 9.5 Prompt Injection Detection

**Source:** `k8s-mechanic/internal/domain/injection.go` (5 regex patterns)

When LLM agents run inside sandboxes, their output may be fed back to an LLM. k8s-mechanic detects injection attempts in untrusted input (finding error text) and can suppress them.

```go
// pkg/injection/detect.go
var patterns = []*regexp.Regexp{
    regexp.MustCompile(`(?i)(ignore|disregard|forget)\s{0,10}(all\s+)?(previous|prior)...\s+(instructions?|rules?|prompts?|context)`),
    regexp.MustCompile(`(?i)you\s+are\s+now\s+(in\s+)?(a\s+)?(different|new|maintenance|admin|root|debug)\s+mode`),
    regexp.MustCompile(`(?i)(override|bypass|disable)\s+(all\s+)?(hard\s+)?rules?`),
    regexp.MustCompile(`(?i)system\s*:\s*(you\s+are|act\s+as|behave\s+as)`),
    regexp.MustCompile(`(?i)stop\s+(following|obeying)\s+((the|these|all)\s+)?(rules?|instructions?|guidelines?|prompts?)`),
}

func Detect(text string) bool { ... }
```

Configurable action: `log` (default) or `suppress` (drops the output entirely). The proxy handler marks proxied responses with `injection_detected: true` when injection patterns are found in agent output.

### 9.6 Kyverno Admission Policies

**Source:** `k8s-mechanic/charts/mechanic/templates/kyverno-policy-agent.yaml`

Enforce at the Kubernetes admission level what cannot be bypassed from within a container:

```yaml
# charts/llmsafespace/templates/kyverno/
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: enforce-sandbox-pod-security
spec:
  match:
    any:
    - resources:
        kinds: [Pod]
        selector:
          matchLabels:
            app: llmsafespace
            llmsafespace.dev/component: sandbox
  validate:
    failureAction: Enforce
  rules:
    - name: require-read-only-root-filesystem
      validate:
        pattern:
          spec:
            containers:
            - securityContext:
                readOnlyRootFilesystem: true
    - name: require-non-root
      validate:
        pattern:
          spec:
            containers:
            - securityContext:
                runAsNonRoot: true
                allowPrivilegeEscalation: false
                capabilities:
                  drop: ["ALL"]
    - name: deny-secret-env-vars
      validate:
        message: "Sandbox main containers must not reference Secrets in env vars"
        foreach:
          - list: "request.object.spec.containers[?@.name != 'credential-setup']"
            deny:
              conditions:
                any:
                - key: "{{ element.env[].valueFrom.secretKeyRef || '' }}"
                  operator: NotEquals
                  value: ""
                  message: "Use init container credential injection instead of env secret refs"
```

Note: `OPENCODE_SERVER_PASSWORD` is NOT set via `secretKeyRef` in the pod spec. The entrypoint script reads it from `/sandbox-cfg/password` (a file written by the credential-setup init container) and exports it as an env var at runtime. This satisfies the Kyverno policy — the pod spec has no Secret references on the main container.

### 9.7 Network Policy by Security Level

**Source:** `k8s-mechanic/charts/mechanic/templates/network-policy-agent.yaml`

| Security Level | DNS (53) | K8s API | HTTPS (443) | Scope |
|---------------|----------|---------|-------------|-------|
| standard | Allowed | Allowed | Allowed to user-specified domains | Default for code execution |
| high | Allowed | Blocked | Allowed to LLM API domains only (see below) | Restricted egress |

**Critical: opencode must be able to call LLM APIs (OpenAI, Anthropic, etc.) in ALL security levels.** Denying all HTTPS egress in high-security mode would make the agent non-functional — it could not reach any LLM provider.

High-security network policy allows HTTPS egress only to:
- Domains specified in the workspace's `spec.networkAccess.egress` list (user-provisioned)
- A platform-configured allowlist of known LLM API domains (set via Helm values): `api.openai.com`, `api.anthropic.com`, etc.

All other HTTPS egress is denied. This prevents data exfiltration to arbitrary servers while keeping the agent functional.

If no LLM API domains are configured and no egress rules are specified in high-security mode, the controller rejects the workspace creation with a validation error: "high-security workspace requires at least one LLM API egress domain."

### 9.8 Supply Chain Security

**Source:** `k8s-mechanic/docker/Dockerfile.agent` (SHA256 verification for every binary)

Every binary downloaded in runtime Dockerfiles must have SHA256 verification:

```dockerfile
# Pattern from k8s-mechanic (every single binary):
ARG OPENCODE_VERSION=1.0.0
RUN curl -fsSL "https://github.com/opencode-ai/opencode/releases/download/v${OPENCODE_VERSION}/opencode-linux-${TARGETARCH}" \
      -o /usr/local/bin/opencode \
    && curl -fsSL "https://github.com/opencode-ai/opencode/releases/download/v${OPENCODE_VERSION}/opencode-linux-${TARGETARCH}.sha256" \
      -o /tmp/opencode.sha256 \
    && echo "$(cat /tmp/opencode.sha256)  /usr/local/bin/opencode" | sha256sum --check \
    && chmod +x /usr/local/bin/opencode
```

Additional requirements:

- **Base images pinned to digests** (not tags): `FROM debian:bookworm-slim@sha256:abc123...`
- **Trivy scanning** in CI: `CRITICAL + HIGH`, fail on fixable, `.trivyignore` for accepted exceptions
- **GitHub Actions pinned to commit SHAs** (not tags)
- **gitleaks** pre-commit hook for leaked secrets

### 9.9 Audit Logging

**Source:** k8s-mechanic's `audit: true` structured log pattern

All security-relevant decisions emit structured audit logs:

```go
logger.Info("network blocked",
    "audit", true,
    "sandbox_id", sandboxID,
    "reason", "egress_blocked",
    "domain", "evil.example.com",
    "security_level", "high",
)
```

This enables post-incident analysis without requiring access to application logs.

### 9.10 Exfiltration Leak Registry

**Source:** `k8s-mechanic/docs/SECURITY/EXFIL_LEAK_REGISTRY.md`

Maintain a living document tracking all known exfiltration vectors from sandboxes:

| ID | Vector | Status | Mitigation |
|----|--------|--------|------------|
| EX-001 | stdout/stderr output | Remediated | Redaction pipeline |
| EX-002 | Environment variables (`env`) | Remediated | Credential materialization to file + unset |
| EX-003 | Network egress | Remediated | Network policy per security level |
| EX-004 | Filesystem read of mounted secrets | Remediated | Secrets only in init container; main container mount is read-only |
| EX-005 | `/proc/1/environ` read | Accepted | Contains no secrets after unset |
| EX-006 | API response echoing credentials | Remediated | Credentials API never returns stored values; returns 204 No Content |
| EX-007 | PostgreSQL credential leak | Remediated | Credentials never stored in PostgreSQL — K8s Secrets only |
| EX-008 | Cross-user secret access | Remediated | Owner check on every credential API call; RBAC restricts pod SA |

This registry is updated as new vectors are discovered and mitigations are implemented.

---

## 10. CRD Changes

### 10.1 New CRD: Workspace

See [Section 5.2](#52-workspace-crd) for full definition.

Register in `controller/internal/resources/register.go` alongside existing CRDs.

### 10.2 Sandbox CRD Changes

Add fields:

```yaml
spec:
  workspaceRef:
    type: object
    properties:
      name: { type: string }
      namespace: { type: string }
  # No spec.agent — opencode serve is always running in every sandbox.
  # Session state is managed by opencode internally, not by the Sandbox CRD.

status:
  phase:
    # Add new values:
    enum: [...existing..., Suspended, Suspending, Resuming]
  podIP:
    type: string
    description: "IP address of the running sandbox pod. Used by API proxy. Updated by controller from pod status."
  lastActivityAt:
    type: string
    format: date-time
  # No agentStatus — opencode handles sessions internally.
  # The proxy reads session state directly from opencode, not from the CRD.
```

### 10.3 Removed CRDs

The following CRDs are removed entirely:

- **WarmPool** — no longer needed; on-demand pod creation replaces warm pools
- **WarmPod** — no longer needed; pods are created and owned directly by the Sandbox reconciler

---

## 11. API Changes

### 11.1 New Endpoints

**Workspace management:**

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/workspaces` | GET | List workspaces |
| `/api/v1/workspaces` | POST | Create workspace |
| `/api/v1/workspaces/{id}` | GET | Get workspace details |
| `/api/v1/workspaces/{id}` | DELETE | Delete workspace (terminates PVC) |
| `/api/v1/workspaces/{id}/suspend` | POST | Suspend workspace |
| `/api/v1/workspaces/{id}/resume` | POST | Resume workspace |
| `/api/v1/workspaces/{id}/status` | GET | Get workspace status |
| `/api/v1/workspaces/{id}/credentials` | PUT | Set workspace-level LLM credentials |
| `/api/v1/workspaces/{id}/credentials` | DELETE | Remove workspace-level credentials |
| `/api/v1/sandboxes/{id}/credentials` | PUT | Set session-level LLM credentials (overrides workspace) |
| `/api/v1/sandboxes/{id}/credentials` | DELETE | Remove session-level credentials |

**Session proxy endpoints** (proxy to opencode inside the sandbox):

| Endpoint | Method | Proxies to opencode | Description |
|----------|--------|---------------------|-------------|
| `/api/v1/sandboxes/{id}/sessions` | POST | `POST /session` | Create a conversation session |
| `/api/v1/sandboxes/{id}/sessions` | GET | `GET /session` | List sessions |
| `/api/v1/sandboxes/{id}/sessions/{sessionId}/message` | POST | `POST /session/{sessionId}/message` | Send a message (HTTP streaming response) |
| `/api/v1/sandboxes/{id}/sessions/{sessionId}/prompt` | POST | `POST /session/{sessionId}/prompt_async` | Send message async (204, results via events) |
| `/api/v1/sandboxes/{id}/sessions/{sessionId}/message` | GET | `GET /session/{sessionId}/message` | Get message history |
| `/api/v1/sandboxes/{id}/sessions/{sessionId}/abort` | POST | `POST /session/{sessionId}/abort` | Abort current processing |
| `/api/v1/sandboxes/{id}/events` | GET | `GET /event` | SSE stream of all agent events |
| `/api/v1/sandboxes/{id}/stream` | WS | SSE `/event` | WebSocket wrapper over SSE for browser clients |

These are the same endpoints defined in §7.4. The proxy is transparent — request bodies and response streams pass through unchanged.

### 11.2 Modified Endpoints

| Endpoint | Change |
|----------|--------|
| `POST /api/v1/sandboxes` | Accept `workspaceRef` field; auto-create workspace if not specified |
| `GET /api/v1/sandboxes/{id}/status` | Include `podIP` and `lastActivityAt` |

### 11.3 New Service: Workspace Service

```go
// api/internal/services/workspace/workspace_service.go

type Service struct {
    logger     logger.Logger
    k8sClient  kubernetes.KubernetesClient
    db         database.Database
    cache      cache.Cache
    metrics    metrics.Metrics
}

func (s *Service) CreateWorkspace(ctx context.Context, req *types.CreateWorkspaceRequest) (*types.Workspace, error)
func (s *Service) GetWorkspace(ctx context.Context, id string) (*types.Workspace, error)
func (s *Service) ListWorkspaces(ctx context.Context, opts *types.ListOptions) ([]*types.Workspace, error)
func (s *Service) DeleteWorkspace(ctx context.Context, id string) error
func (s *Service) SuspendWorkspace(ctx context.Context, id string) error
func (s *Service) ResumeWorkspace(ctx context.Context, id string, opts *types.ResumeOptions) error
func (s *Service) GetWorkspaceStatus(ctx context.Context, id string) (*types.WorkspaceStatus, error)
```

### 11.4 Proxy Handler

The session proxy endpoints are handled by a lightweight proxy handler — not a separate service. The handler:

1. Resolves sandbox ID → pod IP from Sandbox CRD status (cached by controller-runtime informer)
2. Authenticates the caller
3. Injects the server password as `Authorization: Basic base64(opencode:{password})` header
4. Proxies the HTTP request (including WebSocket upgrades and SSE streams) to `http://{pod_ip}:4096{path}`
5. On connection failure, refreshes IP from CRD status and retries once

```go
// api/internal/handlers/proxy.go

type ProxyHandler struct {
    k8sClient  kubernetes.KubernetesClient
    logger     logger.Logger
    httpClient *http.Client
}

func (h *ProxyHandler) ProxyToSandbox(w http.ResponseWriter, r *http.Request) {
    sandboxID := chi.URLParam(r, "id")
    // 1. Get pod IP from Sandbox CRD status
    // 2. Build target URL: http://{podIP}:4096{path}
    // 3. Add Authorization header with server password
    // 4. Proxy request (handle WebSocket upgrade, SSE streaming)
    // 5. On connection error: refresh IP, retry once
}
```

Rate limiting per sandbox (max 10 concurrent proxy connections) prevents a single caller from overwhelming an opencode instance.

---

## 12. Controller Changes

### 12.1 New Reconciler: Workspace

```go
// controller/internal/workspace/controller.go

type WorkspaceReconciler struct {
    client.Client
    Scheme         *runtime.Scheme
    PodManager     *common.PodManager
    Recorder       record.EventRecorder
}

func (r *WorkspaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. Fetch Workspace
    // 2. Handle deletion (cleanup PVC, sandboxes)
    // 3. If Pending: create PVC, transition to Active
    // 4. If Active: check auto-suspend idle timeout
    // 5. If Suspending: delete sandbox pods, transition to Suspended
    // 6. If Resuming: recreate sandbox pods, transition to Active
}
```

### 12.2 Sandbox Reconciler Changes

- Handle `Suspending` phase: delete pod, update status to `Suspended`
- Handle `Resuming` phase: create new pod with workspace PVC, update status to `Running`
- Handle `workspaceRef`: mount workspace PVC and inject init containers (workspace-setup, credential-setup, mode-gate)
- Handle server password: generate random password, create Secret, configure projected volume mount for credential-setup
- Remove warm pool lookup — always create pods directly
- Remove recycling logic from deletion handler

---

## 13. Runtime Changes

### 13.1 Base Image Structure

Following k8s-mechanic's `Dockerfile.agent` pattern, the base runtime image becomes a fully self-contained agent execution environment:

```
runtimes/base/
├── Dockerfile
├── security/
│   ├── apparmor-profiles/
│   ├── seccomp-profiles/
│   └── injection-patterns.json
├── tools/
│   ├── cleanup-pod              # existing
│   ├── execution-tracker        # existing
│   ├── health-check             # existing
│   ├── sandbox-monitor          # existing — pod lifecycle monitoring
│   ├── redact/                  # NEW — secret redaction binary (Go, built from cmd/redact/)
│   ├── wrappers/                # NEW — PATH-shadowing wrappers
│   │   ├── curl
│   │   ├── wget
│   │   └── git
│   ├── smoke-test.sh            # NEW — verify all binaries present
│   └── entrypoints/             # NEW — agent entrypoint scripts
│       ├── entrypoint-common.sh      # shared setup (credential materialization, sentinel check)
│       └── entrypoint-opencode.sh    # OpenCode agent runner (additional runners added later)
```

### 13.2 Dockerfile Pattern

Mirroring `k8s-mechanic/docker/Dockerfile.agent`:

```dockerfile
# ── redact build stage ──────────────────────────────────────
FROM golang:1.23-bookworm AS redact-builder
ARG TARGETARCH=amd64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -o /out/redact ./cmd/redact

# ── Runtime image ──────────────────────────────────────────
FROM debian:bookworm-slim@sha256:<pinned-digest>

ARG TARGETARCH=amd64

# Base packages
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash ca-certificates curl git jq unzip \
    && rm -rf /var/lib/apt/lists/*

# SHA256-verified binaries (per k8s-mechanic pattern)
RUN ...

# Redaction binary — always present (used by wrappers and directly)
COPY --from=redact-builder --chmod=755 /out/redact /usr/local/bin/redact

# PATH-shadowing wrappers are NOT installed here.
# They are installed only in the high-security variant (see §13.4).
# The redact binary is included in all images for direct use by opencode.

# Agent entrypoints
COPY --chmod=755 tools/entrypoints/entrypoint-common.sh  /usr/local/bin/
COPY --chmod=755 tools/entrypoints/entrypoint-opencode.sh /usr/local/bin/

# Smoke test
COPY --chmod=755 tools/smoke-test.sh /usr/local/bin/smoke-test.sh
RUN /usr/local/bin/smoke-test.sh

# Non-root user
RUN useradd -u 1000 -m -s /bin/bash sandbox
USER sandbox
WORKDIR /workspace

# Every sandbox runs opencode serve as a persistent HTTP server.
# The controller sets the command to entrypoint-opencode.sh.
ENTRYPOINT ["/usr/local/bin/entrypoint-opencode.sh"]
```

### 13.3 Per-Language Runtime Images

Each language runtime extends the base image with its own toolchain. The entrypoint is always `opencode serve` — it is language-agnostic:

**Python** (`runtimes/python/Dockerfile`):
```dockerfile
FROM llmsafespace/base:latest
# Add Python-specific packages
RUN apt-get update && apt-get install -y python3 python3-pip
# Inherits ENTRYPOINT from base: entrypoint-opencode.sh → opencode serve
```

**Node.js** and **Go** runtimes follow the same pattern — they extend the base image and add their toolchain. The entrypoint is inherited.

If a language-specific pre-start hook is needed (e.g., setting `PYTHONPATH`), the runtime can override the entrypoint to source the common entrypoint first:

```bash
#!/usr/bin/env bash
set -euo pipefail
export PYTHONPATH=/workspace/packages
exec /usr/local/bin/entrypoint-opencode.sh
```

### 13.4 High-Security Variant

For sandboxes with `securityLevel: high`, a separate Dockerfile extends the base image:

```dockerfile
# runtimes/base/Dockerfile.hardened
FROM llmsafespace/base:latest

# Rename real binaries and install wrappers
RUN mv /usr/bin/git /usr/bin/git.real || true

COPY --chmod=755 tools/wrappers/git    /usr/local/bin/git
COPY --chmod=755 tools/wrappers/curl   /usr/local/bin/curl
COPY --chmod=755 tools/wrappers/wget   /usr/local/bin/wget
```

Wrappers check `/sandbox-cfg/high-security` sentinel (written by `mode-gate` init container). In standard sandboxes, the sentinel is absent and wrappers pass through to the real binary. Since standard sandboxes don't have wrappers installed, this is moot — the real binaries are used directly.

The controller selects the hardened image when `spec.securityLevel: high` is set on the workspace.

---

## 14. Implementation Roadmap

### Phase 1: Foundation (Weeks 1-2)

**Goal:** Fix existing code, drop pod recycling, add security hardening.

| Task | Files | Priority |
|------|-------|----------|
| Fix compile errors in router.go and app.go | `api/internal/server/router.go`, `api/internal/app/app.go` | Critical |
| Implement missing HTTP handlers | `api/internal/handlers/` (new) | Critical |
| Write database migrations | `api/migrations/` | Critical |
| Remove warm pool CRDs and reconcilers | `controller/internal/warmpool/`, `controller/internal/warmpod/`, `pkg/crds/` | High |
| Add secret redaction binary to base runtime | `runtimes/base/tools/redact/` | High |
| Port injection detection | `pkg/injection/` | Medium |

### Phase 2: Workspaces (Weeks 3-4)

**Goal:** Introduce Workspace CRD and PVC-backed persistent environments.

| Task | Files | Priority |
|------|-------|----------|
| Define Workspace CRD types | `controller/internal/resources/workspace_types.go` | Critical |
| Implement Workspace reconciler | `controller/internal/workspace/controller.go` | Critical |
| Register Workspace CRD | `controller/internal/resources/register.go`, `pkg/crds/workspace_crd.yaml` | Critical |
| Implement Workspace API service | `api/internal/services/workspace/` | Critical |
| Add workspace endpoints to router | `api/internal/server/router.go` | Critical |
| Add suspend/resume to Sandbox reconciler | `controller/internal/sandbox/controller.go` | Critical |
| Write integration tests | `api/internal/tests/integration/` | Critical |

### Phase 3: Proxy and Sessions (Weeks 5-6)

**Goal:** Enable transparent proxy to opencode serve, supporting both interactive and programmatic usage modes.

| Task | Files | Priority |
|------|-------|----------|
| Implement proxy handler | `api/internal/handlers/proxy.go` | Critical |
| Add session proxy endpoints to router | `api/internal/server/router.go` | Critical |
| Implement WebSocket ↔ SSE bridging | `api/internal/handlers/proxy.go` | Critical |
| Add per-sandbox rate limiting (max 10 concurrent connections) | `api/internal/middleware/rate_limit.go` | High |
| Add pod IP staleness handling (retry with fresh lookup) | `api/internal/handlers/proxy.go` | High |
| Configure opencode data directory for session persistence | `entrypoint-opencode.sh`, runtime config | High |
| Write integration tests (create session → send message → get history) | `api/internal/tests/integration/` | High |
| Add client reconnect guidance to API documentation | `docs/` | Medium |

### Phase 4: MCP Server (Weeks 7-8)

**Goal:** Expose LLMSafeSpace as an MCP server.

| Task | Files | Priority |
|------|-------|----------|
| Add MCP Go SDK dependency | `go.mod` | Critical |
| Implement MCP server core | `api/internal/mcp/server.go` | Critical |
| Define MCP tools | `api/internal/mcp/tools.go` | Critical |
| Define MCP resources | `api/internal/mcp/resources.go` | Medium |
| Define MCP prompts | `api/internal/mcp/prompts.go` | Low |
| Implement stdio transport | `api/internal/mcp/transport.go` | Critical |
| Implement SSE transport | `api/internal/mcp/transport.go` | High |
| Add MCP server entrypoint | `api/cmd/mcp/main.go` | Critical |
| Write MCP integration tests | `api/internal/mcp/tests/` | High |

### Phase 5: Security Hardening (Weeks 9-10)

**Goal:** Apply k8s-mechanic hardening patterns comprehensively.

| Task | Files | Priority |
|------|-------|----------|
| Build PATH-shadowing wrappers | `runtimes/base/tools/wrappers/` | High |
| Create high-security runtime variants | `runtimes/*/Dockerfile.hardened` | High |
| Write Kyverno admission policies | `charts/llmsafespace/templates/kyverno/` | Medium |
| Pin base images to digests | `runtimes/*/Dockerfile` | Medium |
| Add SHA256 verification to Dockerfiles | `runtimes/*/Dockerfile` | Medium |
| Add Trivy scanning to CI | `.github/workflows/` | Medium |
| Create Helm chart | `charts/llmsafespace/` | High |
| Write threat model document | `docs/SECURITY/THREAT_MODEL.md` | High |

---

## 15. Migration Guide

### For Existing Users

1. **Warm pools are removed entirely.** WarmPool and WarmPod CRDs are deleted. All sandboxes create pods on demand. Existing WarmPool/WarmPod resources should be deleted before upgrading.

2. **Workspace CRD** is additive. Existing sandbox workflows that don't reference a workspace will have one auto-created.

3. **Every sandbox now runs `opencode serve`** as a persistent HTTP server. The proxy endpoints (`/sandboxes/{id}/sessions/...`) replace any previous exec-based agent interaction.

4. **MCP server** is a separate binary. Existing REST + WebSocket API is unaffected.

5. **Credentials are stored as K8s Secrets**, not in PostgreSQL. The `workspaces` table tracks metadata only — no credential material.

### Database Migration

```sql
-- Migration 000002: Workspaces
CREATE TABLE workspaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    namespace VARCHAR(255) NOT NULL,
    runtime VARCHAR(255),
    security_level VARCHAR(50) DEFAULT 'standard',
    storage_size VARCHAR(50) DEFAULT '5Gi',
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    deleted_at TIMESTAMP
);

CREATE INDEX idx_workspaces_user_id ON workspaces(user_id);

ALTER TABLE sandboxes ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id);
```

Note: `phase`, `pvc_name`, `conditions`, `pod_ip`, and `lastActivityAt` are NOT in PostgreSQL. These live exclusively in K8s CRD status — the controller is the source of truth (see §5.5a). The API reads infrastructure state from CRDs, not from PostgreSQL.

---

## 16. Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| PVC access mode conflicts (RWO with multiple pods) | Medium | High | Enforce one sandbox per RWO workspace in reconciler |
| Opencode server consumes unbounded resources | High | Medium | Pod-level resource limits (CPU/memory); Kubernetes liveness probe restarts unresponsive instances |
| Users reference arbitrary Secrets by name | — | — | **Eliminated** — users never reference K8s Secrets directly; API creates them on user's behalf |
| Credential stored in etcd without encryption | Medium | High | Document requirement for etcd encryption at rest; Kyverno can enforce `type: Opaque` only |
| Credential validated via API call at set-time | Low | Low | Best-effort validation; invalid credentials are caught when opencode tries to use them and fails |
| Workspace credentials used by wrong user | Low | Critical | Owner check on every credential API call; RBAC restricts Secret access to controller SA only |
| Secret redaction misses patterns | Medium | Medium | Default to redacting; allow opt-out for binary output; regular pattern updates |
| Redaction corrupts binary output | High | Low | Binary output bypasses redaction (handled inside sandbox by opencode agent); text output always passes through redaction |
| Workspace PVC fills up | High | Low | Storage quotas; monitoring; `ttlSecondsAfterSuspended` for auto-cleanup |
| Suspend/resume loses installed packages | Medium | Medium | Declarative `spec.packages` + `spec.initScript` reinstall on every pod start; install-to-PVC convention |
| Resume spec is stale (user wanted different runtime) | Low | Low | Resume accepts optional `runtime` override |
| PVC provisioning slow at scale (>1000 workspaces) | Medium | Medium | Storage class requirements in docs; consider storage quotas |
| Init container failure leaves sandbox stuck | Medium | High | Controller timeout (5 min) transitions sandbox to Failed; termination message captured in status |
| Cold start latency without warm pools (~3-5s) | High | Low | Acceptable for V2 use case (long-lived agents). Node image caching reduces pull time. Suspended workspace resume is ~3s (PVC already bound) |
| Proxy connection drops during API server restart | Medium | Medium | Clients must implement reconnect with exponential backoff; session state preserved in opencode |
| Stale pod IP on proxy request | Low | Low | Proxy retries once with fresh IP from CRD status after connection failure |
| Init script executes arbitrary code in cluster | Medium | Medium | Init container runs with same restricted security context as main container (non-root, drop ALL caps, read-only root); network policy limits egress |
| API server becomes proxy bottleneck at scale | Low | Medium | API server is stateless — horizontally scale behind load balancer; sticky sessions not required |
| WebSocket/SSE bridge leaks connections | Medium | Medium | Connection idle timeout (5 min); goroutine leak detection in proxy handler; per-sandbox connection limit (10) |

---

## Appendix A: New Package Structure

```
api/
├── cmd/
│   ├── api/main.go              # existing
│   └── mcp/main.go              # NEW — MCP server entrypoint
├── internal/
│   ├── mcp/                     # NEW — MCP server implementation
│   │   ├── server.go
│   │   ├── tools.go
│   │   ├── resources.go
│   │   ├── prompts.go
│   │   └── transport.go
│   ├── handlers/                # NEW — HTTP route handlers
│   │   ├── sandbox.go
│   │   ├── workspace.go         # NEW
│   │   ├── proxy.go             # NEW — reverse proxy to opencode
│   │   └── user.go
│   └── services/
│       └── workspace/           # NEW
│           └── workspace_service.go

controller/
├── internal/
│   ├── workspace/               # NEW
│   │   └── controller.go
│   └── (existing packages unchanged except recycling removal)

pkg/
├── redact/                      # NEW — secret redaction engine (ported from k8s-mechanic)
│   ├── redact.go                # Library AND cmd/redact/main.go uses this package
│   └── redact_test.go
├── injection/                   # NEW — prompt injection detection (ported from k8s-mechanic)
│   ├── detect.go
│   └── detect_test.go
└── (existing packages unchanged)

cmd/
├── redact/                      # NEW — standalone redact binary for runtime images
│   └── main.go                  # Imports pkg/redact. This is the canonical source.
│       ├── wrappers/            # NEW — PATH-shadowing wrappers (mirrors k8s-mechanic)
│       │   ├── curl
│       │   ├── wget
│       │   └── git
│       ├── entrypoints/         # NEW — agent entrypoint scripts
│       │   ├── entrypoint-common.sh
│       │   └── entrypoint-opencode.sh
│       └── smoke-test.sh        # NEW
├── python/
│   └── Dockerfile               # Extends base, adds Python toolchain
├── nodejs/
│   └── Dockerfile               # Extends base, adds Node.js toolchain
└── go/
    └── Dockerfile               # Extends base, adds Go toolchain
```

## Appendix B: Dependencies to Add

| Dependency | Purpose | Version |
|-----------|---------|---------|
| `github.com/mark3labs/mcp-go` | MCP server SDK | Latest |
| (no new K8s dependencies) | Workspace uses existing controller-runtime | — |
| (no new API dependencies) | New services use existing pgx, go-redis | — |
