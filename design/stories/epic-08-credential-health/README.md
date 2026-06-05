# Epic 8: Credential Health & Agent Abstraction

**Status:** Planning
**Depends On:** Epic 6 (Collapse Sandbox into Workspace)
**Motivation:** Workspaces silently degrade when agent credentials expire, are deleted, or become invalid. Pods run but sessions hang with no indication of why. This epic adds detection, surfacing, and self-healing for credential and agent health — abstracted behind interfaces to support multiple agent runtimes (opencode, Claude Code, Codex).

---

## Problem Statement

### Current State

1. **No credential validation at mount time.** The `credential-setup` init container writes `{}` when no `workspace-creds-*` secret exists. The entrypoint copies `{}` into the agent config. The agent boots but cannot call any LLM.

2. **No health signal from the agent.** Sandbox pod probes are TCP-only on port 4096. The agent can be `Running` and `Ready` while every LLM call fails with `ERROR error= failed`.

3. **No session-level degradation tracking.** When the LLM provider fails, the session silently hangs. The API proxy passes messages through but has no visibility into whether the agent actually processed them.

4. **No self-healing.** The controller watches `workspace-creds-*` secret changes (hash comparison) and restarts pods on update — but does nothing when the secret is missing or contains invalid data.

5. **Agent coupling.** Health checks, credential formatting, and probe paths are hardcoded to opencode. Adding Claude Code or Codex would require duplicating all of this logic.

### Real-World Scenario (2026-05-26)

- Workspace `fc9ec5c7` had been working for 2+ hours with `providerID=opencode modelID=big-pickle`
- The opencode cloud provider started returning errors at `03:24:24`
- No indication to the user that anything was wrong — session just hung
- `/sandbox-cfg/credentials` was `{}` — no workspace-level credential secret exists
- No mechanism to detect, surface, or recover from this state
- Root cause of the provider failure is unknown — could be transient outage, rate limit, or credential issue

### What We Do NOT Know

- How the `opencode` provider was originally authenticated in the pod — the credential path is not fully traced
- Whether a transient provider error resolves automatically or requires pod restart

**Design principle: do not assume credential origin. The platform tracks what it manages (secrets) and observes what the agent reports (health). It does not assume knowledge of how credentials reached the agent.**

---

## Verified Agent API Surface (opencode)

Per opencode server docs at `https://opencode.ai/docs/server/`, verified against running pods:

| Endpoint | Size | Auth | Response | Use |
|----------|------|------|----------|-----|
| `GET /global/health` | ~50B | Basic | `{ healthy: bool, version: string }` | Process alive check |
| `GET /config/providers` | ~2.5KB | Basic | `{ providers: [...], default: {...} }` | Provider config check (NO `connected` field) |
| `GET /provider` | ~3.5MB | Basic | `{ all: [...], connected: [...] }` | Full model catalog — only source of `connected[]` |
| `GET /session/status` | ~100B | Basic | `{ [sessionID]: SessionStatus }` | Active session states |
| `GET /event` | SSE stream | Basic | Event stream | Real-time session/provider events |

**Validated findings:**
- `/global/health` returns `healthy: true` regardless of provider state — process-only check
- `/config/providers` does NOT have a `connected` field — only `providers[]` and `default`
- `/provider` at 3.5MB is the ONLY source of `connected: [...]` (which providers are actually authenticated)
- `connected` is dynamic — only authenticated providers appear, not just configured ones
- All endpoints require Basic auth — cannot be used from kubelet HTTP probes

**Problem:** The controller needs `connected[]` to determine if providers are actually working, but it costs 3.5MB per call over the network. This is fragile — we'd depend on opencode's undocumented internal schema and burn bandwidth.

**Solution:** Add a workspace agent daemon (`workspace-agentd`) to the pod.

---

## State Analysis

### Independent Variables

| Variable | Possible Values | Source |
|----------|----------------|--------|
| Pod state | NotFound, Pending, Running, Failed, Unknown | K8s API |
| Agent process | alive, dead | `workspace-agentd /v1/healthz` |
| Providers connected | none, ≥1 | `workspace-agentd /v1/readyz` |
| Provider reachable | yes, no, unknown | LLM call outcome (V2 — session error tracking) |
| Credential secret | not exists, exists-empty, exists-invalid, exists-valid | K8s API |

### State Matrix

Rows are pod state × agent state. Columns are credential × provider state. Cell = composite health + action.

| | cred: valid, providers: connected | cred: valid, providers: none | cred: missing/invalid, providers: connected | cred: missing/invalid, providers: none |
|---|---|---|---|---|
| **Pod: Running, agent: alive** | Healthy / none | Degraded / surface warning | **Degraded / surface error (unmanaged workspace)** | **Unhealthy / surface error** |
| **Pod: Running, agent: dead** | Unhealthy / restart pod | Unhealthy / restart pod | Unhealthy / restart pod | Unhealthy / restart pod |
| **Pod: NotFound/Pending** | Wait / requeue | Wait / requeue | Wait / requeue | Wait / requeue |
| **Pod: Failed** | Failed / manual intervention | Failed / manual intervention | Failed / manual intervention | Failed / manual intervention |

**Key insight:** "credential missing" is always an error state. The platform manages credentials via secrets — if no secret exists, the workspace is unmanaged and the platform cannot guarantee it will function. We surface an error and encourage the user to set credentials.

### Condition Combination Logic

```
CompositeHealth =
  if pod not running → Phase reflects this (Pending/Creating/etc.)
  if agent not alive → Unhealthy (pod needs restart)
  if no managed credential secret → Degraded (error: unmanaged workspace)
  if credential secret exists but invalid → Degraded (error: invalid credentials)
  if agent alive AND valid creds AND providers connected → Healthy
  if agent alive AND valid creds AND no providers connected → Degraded (transient, surface warning)
```

---

## Design

### 0. Workspace Agent Daemon (`workspace-agentd`)

**Architecture: Talos-style in-pod management API.**

Instead of the controller talking directly to opencode's 3.5MB endpoints, run a lightweight daemon inside the pod that:
1. Talks to the agent runtime (opencode) locally over `localhost:4096`
2. Exposes a **stable, versioned, tiny API** on port `4097` (no auth needed — localhost only)
3. Returns only what the platform needs — health, connected providers, session errors

This is the same pattern as Talos OS: `apid` wraps the OS and exposes a management API that consumers (controller) talk to. The internal agent (opencode) is an implementation detail.

```
┌─────────────────────────────────────────────────────┐
│                    Pod                               │
│                                                     │
│  ┌──────────────────────────────────────────────┐   │
│  │  Main Container (workspace)                  │   │
│  │                                              │   │
│  │  ┌──────────────┐    ┌────────────────────┐  │   │
│  │  │  opencode    │    │ workspace-agentd   │  │   │
│  │  │  :4096       │◄───│ :4097 (localhost)  │  │   │
│  │  │  (foreground)│    │ (background)       │  │   │
│  │  └──────────────┘    └────────────────────┘  │   │
│  │                                              │   │
│  │  entrypoint starts agentd, then execs opencode│   │
│  └──────────────────────────────────────────────┘   │
│                                                     │
│                        Controller ──► podIP:4097    │
└─────────────────────────────────────────────────────┘
```

**Why same container, not sidecar:**
- **Shared lifecycle** — if opencode dies, the container dies, both restart together. No split-brain where agentd is healthy but opencode is dead.
- **No controller `buildPod` changes** — no sidecar container spec. Just a binary in the base image + entrypoint change.
- **Same network namespace either way** — agentd on localhost:4097 talks to opencode on localhost:4096 regardless of container boundary.
- **No separate resource accounting** — daemon uses ~1MB RAM, not worth a separate container overhead.

**Why this is robust:**
- **We own the API surface.** `workspace-agentd`'s responses are our schema. opencode can change its API every release — we update the daemon's internal implementation, not the controller.
- **Tiny responses.** `/v1/healthz` returns ~100 bytes, not 3.5MB.
- **No auth overhead.** Daemon runs on localhost inside the pod. No password needed for callers.
- **Agent-agnostic externally.** The daemon's API is the same regardless of the underlying agent. Adding Claude Code or Codex means changing the daemon's backend, not the controller.
- **Extensible.** This daemon becomes the management plane for the workspace — can grow to expose credential rotation, session inspection, resource usage, Talos-style.

#### Daemon API (V1)

```
GET /v1/healthz       → { "healthy": bool, "version": string, "uptime_seconds": int }
                         Agent process alive. Used by kubelet for liveness probe.

GET /v1/readyz        → { "ready": bool, "providers_connected": ["opencode"],
                          "providers_configured": 1, "agent_version": "1.2.27",
                          "agent_type": "opencode" }
                         At least one provider connected. Used by kubelet for readiness probe.

GET /v1/statusz       → { "healthy": bool, "ready": bool, "connected": [...],
                          "providers_configured": 1, "sessions_active": 0,
                          "sessions_error": 0, "last_error": "",
                          "agent_type": "opencode", "agent_version": "1.2.27",
                          "uptime_seconds": 3600 }
                         Full status. Used by controller for health checks every 5 min.
```

#### Daemon Implementation

```go
// cmd/workspace-agentd/main.go
//
// Statically compiled (~3MB binary), baked into the base image.
// Runs as a background process in the main workspace container.

package main

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "strings"
    "sync"
    "time"

    "github.com/anomalyco/llmsafespace/pkg/agent"
    "github.com/anomalyco/llmsafespace/pkg/agentd"
)

const (
    agentAddr  = "http://localhost:4096"
    listenAddr = "0.0.0.0:4097"
)

type AgentClient interface {
    IsHealthy(ctx context.Context) (healthy bool, version string, err error)
    ConnectedProviders(ctx context.Context) ([]string, error)
    ConfiguredProviderCount(ctx context.Context) (int, error)
}

// OpenCodeClient implements AgentClient by calling opencode's local API.
// All calls go over localhost — no network overhead.
type OpenCodeClient struct {
    password string // read from /sandbox-cfg/password at startup
}

func (c *OpenCodeClient) IsHealthy(ctx context.Context) (bool, string, error) {
    // GET localhost:4096/global/health (50 bytes)
    // Returns {healthy, version}
}

func (c *OpenCodeClient) ConnectedProviders(ctx context.Context) ([]string, error) {
    // GET localhost:4096/provider (3.5MB over localhost)
    // Stream-parse: find "connected" key, extract array.
    // Over localhost the transfer is ~1ms. Called at most once every 30s
    // (readiness probe) + once per /v1/statusz call.
    // Result cached for 30s to coalesce probe + statusz calls.
}

func (c *OpenCodeClient) ConfiguredProviderCount(ctx context.Context) (int, error) {
    // GET localhost:4096/config/providers (2.5KB over localhost)
    // Count providers array.
}

func main() {
    pw, _ := os.ReadFile("/sandbox-cfg/password")
    client := &OpenCodeClient{password: strings.TrimSpace(string(pw))}

    startedAt := time.Now()
    var cache struct {
        sync.Mutex
        connected     []string
        configured    int
        lastFetchedAt time.Time
    }

    http.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
        healthy, version, err := client.IsHealthy(r.Context())
        if err != nil {
            w.WriteHeader(502)
            json.NewEncoder(w).Encode(agentd.HealthzResponse{
                Healthy: false, Version: "", UptimeSeconds: 0,
            })
            return
        }
        json.NewEncoder(w).Encode(agentd.HealthzResponse{
            Healthy:       healthy,
            Version:       version,
            UptimeSeconds: int(time.Since(startedAt).Seconds()),
        })
    })

    http.HandleFunc("/v1/readyz", func(w http.ResponseWriter, r *http.Request) {
        connected, configured := cachedConnected(r.Context(), client, &cache)
        ready := len(connected) > 0
        json.NewEncoder(w).Encode(agentd.ReadyzResponse{
            Ready:               ready,
            ProvidersConnected:  connected,
            ProvidersConfigured: configured,
            AgentType:           string(agent.AgentTypeOpenCode),
            AgentVersion:        "unknown", // populated from /global/health below
        })
    })

    http.HandleFunc("/v1/statusz", func(w http.ResponseWriter, r *http.Request) {
        healthy, version, _ := client.IsHealthy(r.Context())
        connected, configured := cachedConnected(r.Context(), client, &cache)
        ready := len(connected) > 0
        json.NewEncoder(w).Encode(agentd.StatuszResponse{
            Healthy:             healthy,
            Ready:               ready,
            Connected:           connected,
            ProvidersConfigured: configured,
            SessionsActive:      0, // V2: read from /session/status
            SessionsError:       0, // V2: accumulated from /session/status
            LastError:           "",
            AgentType:           string(agent.AgentTypeOpenCode),
            AgentVersion:        version,
            UptimeSeconds:       int(time.Since(startedAt).Seconds()),
        })
    })

    fmt.Fprintf(os.Stderr, "workspace-agentd listening on %s\n", listenAddr)
    http.ListenAndServe(listenAddr, nil)
}

const connectedCacheTTL = 30 * time.Second

func cachedConnected(ctx context.Context, client AgentClient, cache *struct {
    sync.Mutex
    connected     []string
    configured    int
    lastFetchedAt time.Time
}) ([]string, int) {
    cache.Lock()
    defer cache.Unlock()
    if time.Since(cache.lastFetchedAt) < connectedCacheTTL && cache.connected != nil {
        return cache.connected, cache.configured
    }
    connected, _ := client.ConnectedProviders(ctx)
    configured, _ := client.ConfiguredProviderCount(ctx)
    cache.connected = connected
    cache.configured = configured
    cache.lastFetchedAt = time.Now()
    return connected, configured
}
```

#### Daemon Build

The daemon is a separate Go module in `cmd/workspace-agentd/` with its own `go.mod`. Built in a multi-stage Dockerfile step in `runtimes/base/Dockerfile`, alongside the existing `redact` builder:

```dockerfile
FROM golang:1.25-bookworm AS agentd-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -o /out/workspace-agentd .
```

Then in the runtime stage:

```dockerfile
COPY --from=agentd-builder --chmod=755 /out/workspace-agentd /usr/local/bin/workspace-agentd
```

#### Entrypoint Change

The daemon starts as a background process in the existing entrypoint — no new container:

```bash
# entrypoint-opencode.sh
#!/usr/bin/env bash
set -euo pipefail
source /usr/local/bin/entrypoint-common.sh

eval "$(mise activate bash)"

export OPENCODE_CONFIG=/tmp/agent-config.json
export XDG_DATA_HOME=/workspace/.local

if [[ -f /sandbox-cfg/password ]]; then
    export OPENCODE_SERVER_PASSWORD="$(cat /sandbox-cfg/password)"
fi

workspace-agentd &

exec opencode serve --hostname 0.0.0.0 --port 4096
```

If `workspace-agentd` crashes, opencode keeps running — the daemon is observatory, not critical path. The controller detects daemon absence when health checks fail (no response on :4097 → `ConsecutiveHealthFailures` increments).

#### Probe Changes

The controller's `buildPod` changes probe configuration from TCP to HTTP via the daemon:

```go
// Before (current):
ReadinessProbe: &corev1.Probe{
    ProbeHandler: corev1.ProbeHandler{
        TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(4096)},
    },
    InitialDelaySeconds: 5,
    PeriodSeconds:       10,
    TimeoutSeconds:      3,
    FailureThreshold:    3,
},

// After:
ReadinessProbe: &corev1.Probe{
    ProbeHandler: corev1.ProbeHandler{
        HTTPGet: &corev1.HTTPGetAction{
            Path: "/v1/readyz",
            Port: intstr.FromInt(4097),
        },
    },
    InitialDelaySeconds: 10,
    PeriodSeconds:       15,
    TimeoutSeconds:      3,
    FailureThreshold:    5,
},

// Before (current):
LivenessProbe: &corev1.Probe{
    ProbeHandler: corev1.ProbeHandler{
        TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(4096)},
    },
    InitialDelaySeconds: 15,
    PeriodSeconds:       30,
    TimeoutSeconds:      5,
    FailureThreshold:    3,
},

// After:
LivenessProbe: &corev1.Probe{
    ProbeHandler: corev1.ProbeHandler{
        HTTPGet: &corev1.HTTPGetAction{
            Path: "/v1/healthz",
            Port: intstr.FromInt(4097),
        },
    },
    InitialDelaySeconds: 15,
    PeriodSeconds:       30,
    TimeoutSeconds:      5,
    FailureThreshold:    6,
},
```

**This gives us real readiness probes** — the pod goes NotReady when no providers are connected. No more "Running and Ready while every LLM call fails."

#### Evolution Path

| Phase | Capability | Endpoints |
|-------|-----------|-----------|
| V1 (this epic) | Health + readiness + connected providers | `/v1/healthz`, `/v1/readyz`, `/v1/statusz` |
| V2 | Session error tracking, error categorization | `/v1/sessions`, `/v1/errors` |
| V3 | Credential hot-reload (no pod restart) | `POST /v1/credentials` |
| V4 | Full Talos-style management API | `/v1/resources/*`, gRPC, streaming |

### 1. Shared Types Package (`pkg/agentd/types.go`)

The daemon and controller must use the **same Go types** for the API contract. A shared package prevents drift:

```go
// pkg/agentd/types.go
//
// Shared between workspace-agentd (daemon) and the controller.
// Daemon uses these to serialize responses.
// Controller uses these to deserialize responses.
// A compile error in either direction if the schema changes.

package agentd

type HealthzResponse struct {
    Healthy       bool   `json:"healthy"`
    Version       string `json:"version"`
    UptimeSeconds int    `json:"uptime_seconds"`
}

type ReadyzResponse struct {
    Ready               bool     `json:"ready"`
    ProvidersConnected  []string `json:"providers_connected"`
    ProvidersConfigured int      `json:"providers_configured"`
    AgentVersion        string   `json:"agent_version"`
    AgentType           string   `json:"agent_type"`
}

type StatuszResponse struct {
    Healthy             bool     `json:"healthy"`
    Ready               bool     `json:"ready"`
    Connected           []string `json:"connected"`
    ProvidersConfigured int      `json:"providers_configured"`
    SessionsActive      int      `json:"sessions_active"`
    SessionsError       int      `json:"sessions_error"`
    LastError           string   `json:"last_error"`
    AgentType           string   `json:"agent_type"`
    AgentVersion        string   `json:"agent_version"`
    UptimeSeconds       int      `json:"uptime_seconds"`
}
```

The daemon imports `pkg/agentd` to build responses. The controller imports `pkg/agentd` to parse responses. **Any schema change is a compile error in the consumer.**

### 2. Agent Runtime Interface (Controller/API Side)

Abstract credential validation and formatting behind an interface so the API server is agent-agnostic. The controller does NOT use this interface for health checks — it talks directly to `workspace-agentd`.

```go
// pkg/agent/agent.go (new package)

type AgentType string

const (
    AgentTypeOpenCode   AgentType = "opencode"
    AgentTypeClaudeCode AgentType = "claude-code"
    AgentTypeCodex      AgentType = "codex"
)

type CredentialState string

const (
    CredentialStatePresent CredentialState = "Present"
    CredentialStateMissing CredentialState = "Missing"
    CredentialStateInvalid CredentialState = "Invalid"
)

type CredentialCheckResult struct {
    State   CredentialState `json:"state"`
    Agent   AgentType       `json:"agent"`
    Message string          `json:"message,omitempty"`
}

type AgentRuntime interface {
    Type() AgentType

    // ValidateCredentials checks structural validity of credential data.
    // Does NOT make API calls against the provider.
    ValidateCredentials(ctx context.Context, rawConfig []byte) (*CredentialCheckResult, error)

    // FormatCredentials transforms raw provider config into agent-native format.
    FormatCredentials(rawConfig []byte) ([]byte, error)
}
```

**What was removed from the interface:**
- `CheckHealth()` — controller calls `workspace-agentd` directly on `:4097`, no need for agent-specific health logic in the controller
- `HealthEndpoint()` — implementation detail
- `ProbeSpec()` — probes now use daemon's `/v1/healthz` and `/v1/readyz`, no agent-specific config
- `EntrypointCommand()` — not needed, entrypoint is per-runtime image

### 2. Agent Runtime Implementations

#### OpenCode Agent

```go
// pkg/agent/opencode/opencode.go

type OpenCodeAgent struct{}

func (a *OpenCodeAgent) Type() AgentType { return AgentTypeOpenCode }

func (a *OpenCodeAgent) ValidateCredentials(ctx context.Context, rawConfig []byte) (*CredentialCheckResult, error) {
    if len(rawConfig) == 0 || string(rawConfig) == "{}" {
        return &CredentialCheckResult{State: CredentialStateMissing, Agent: AgentTypeOpenCode, Message: "empty config"}, nil
    }
    var config map[string]interface{}
    if err := json.Unmarshal(rawConfig, &config); err != nil {
        return &CredentialCheckResult{State: CredentialStateInvalid, Agent: AgentTypeOpenCode, Message: "invalid JSON"}, nil
    }
    if len(config) == 0 {
        return &CredentialCheckResult{State: CredentialStateMissing, Agent: AgentTypeOpenCode, Message: "empty config object"}, nil
    }
    return &CredentialCheckResult{State: CredentialStatePresent, Agent: AgentTypeOpenCode}, nil
}

func (a *OpenCodeAgent) FormatCredentials(rawConfig []byte) ([]byte, error) {
    return rawConfig, nil
}
```

#### Agent Registry

```go
// pkg/agent/registry.go

var registry = map[AgentType]AgentRuntime{
    AgentTypeOpenCode: &opencode.OpenCodeAgent{},
}

func Get(agentType AgentType) (AgentRuntime, error) {
    agent, ok := registry[agentType]
    if !ok {
        return nil, fmt.Errorf("unknown agent type: %s", agentType)
    }
    return agent, nil
}

func Register(agentType AgentType, agent AgentRuntime) {
    registry[agentType] = agent
}
```

#### Agent Type Resolution

The `RuntimeEnvironment` CRD will carry the agent type. Until then, resolved from workspace spec:

```go
func agentTypeForWorkspace(ws *v1.Workspace) agent.AgentType {
    // Future: read from RuntimeEnvironment CRD status.agentType
    return agent.AgentTypeOpenCode
}
```

### 3. Controller Changes

#### New Workspace Conditions

Add to `workspace_types.go` alongside existing `Ready`, `PVCReady`, `PodRunning`, `Suspended`:

```go
WorkspaceConditionCredentialsAvailable WorkspaceConditionType = "CredentialsAvailable"
WorkspaceConditionAgentHealthy         WorkspaceConditionType = "AgentHealthy"
```

Add typed reason constants to prevent typos (pattern matches existing `WorkspacePhase*` style):

```go
// CredentialAvailable reasons
const (
    ReasonCredentialsValid           = "CredentialsValid"
    ReasonCredentialSecretNotFound   = "CredentialSecretNotFound"
    ReasonCredentialEmpty            = "CredentialEmpty"
    ReasonCredentialInvalid          = "CredentialInvalid"
    ReasonCredentialCheckError       = "CredentialCheckError"
    ReasonCredentialValidationError  = "CredentialValidationError"
)

// AgentHealthy reasons
const (
    ReasonAgentHealthy        = "AgentHealthy"
    ReasonAgentUnhealthy      = "AgentUnhealthy"
    ReasonAgentDegraded       = "AgentDegraded"
    ReasonHealthCheckFailed   = "HealthCheckFailed"
)
```

All `setCondition` calls use these constants — never raw strings. A typo is a compile error.

Add to `WorkspaceStatus`:

```go
LastHealthCheckAt         *metav1.Time `json:"lastHealthCheckAt,omitempty"`
ConsecutiveHealthFailures int32        `json:"consecutiveHealthFailures,omitempty"`
```

#### Health Check Interval — Separate from Active Requeue

The `handleActive` requeue is 30s (`requeueActive`). Health checks run on a **separate, longer interval** (5 minutes) tracked via `status.LastHealthCheckAt`:

```go
const (
    healthCheckInterval         = 5 * time.Minute
    healthCheckBackoffInterval  = 15 * time.Minute
    healthCheckFailureThreshold = 3
    healthCheckGracePeriod      = 2 * time.Minute
)

func (r *WorkspaceReconciler) shouldRunHealthCheck(ws *v1.Workspace) bool {
    if ws.Status.StartTime != nil && time.Since(ws.Status.StartTime.Time) < healthCheckGracePeriod {
        return false
    }
    interval := healthCheckInterval
    if ws.Status.ConsecutiveHealthFailures >= healthCheckFailureThreshold {
        interval = healthCheckBackoffInterval
    }
    if ws.Status.LastHealthCheckAt == nil {
        return true
    }
    return time.Since(ws.Status.LastHealthCheckAt.Time) >= interval
}
```

#### Credential Check in Reconciler

Runs on every reconcile (cheap — K8s API call only, no HTTP to pod). 

**Note on `setCondition`:** The controller currently does not have a `setCondition` helper for `WorkspaceCondition`. The existing `common.SetCondition` uses `metav1.Condition` (not the CRD's `WorkspaceCondition`). A new helper must be added that appends/updates `WorkspaceCondition` entries in `workspace.Status.Conditions` and sets `LastTransitionTime` on changes. This is part of US-8.5.

```go
func (r *WorkspaceReconciler) checkCredentialState(ctx context.Context, ws *v1.Workspace) {
    agent := agent.Get(agentTypeForWorkspace(ws))

    secretName := fmt.Sprintf("workspace-creds-%s", ws.Name)
    secret := &corev1.Secret{}
    err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: ws.Namespace}, secret)

    if errors.IsNotFound(err) {
        r.setCondition(ws, v1.WorkspaceConditionCredentialsAvailable, "False",
            v1.ReasonCredentialSecretNotFound, "No workspace credential secret exists")
        return
    }
    if err != nil {
        r.setCondition(ws, v1.WorkspaceConditionCredentialsAvailable, "Unknown",
            v1.ReasonCredentialCheckError, err.Error())
        return
    }

    rawConfig := secret.Data["provider-config"]
    result, err := agent.ValidateCredentials(ctx, rawConfig)
    if err != nil {
        r.setCondition(ws, v1.WorkspaceConditionCredentialsAvailable, "Unknown",
            v1.ReasonCredentialValidationError, err.Error())
        return
    }

    switch result.State {
    case agent.CredentialStateMissing:
        r.setCondition(ws, v1.WorkspaceConditionCredentialsAvailable, "False",
            v1.ReasonCredentialEmpty, result.Message)
    case agent.CredentialStateInvalid:
        r.setCondition(ws, v1.WorkspaceConditionCredentialsAvailable, "False",
            v1.ReasonCredentialInvalid, result.Message)
    case agent.CredentialStatePresent:
        r.setCondition(ws, v1.WorkspaceConditionCredentialsAvailable, "True",
            v1.ReasonCredentialsValid, "")
    }
}
```

#### Agent Health Check in Reconciler

Only runs when `shouldRunHealthCheck` returns true. Calls the daemon on `:4097` (no auth, tiny response):

```go
func (r *WorkspaceReconciler) checkAgentHealth(ctx context.Context, ws *v1.Workspace) {
    if !r.shouldRunHealthCheck(ws) {
        return
    }
    if ws.Status.PodIP == "" {
        return
    }

    endpoint := fmt.Sprintf("http://%s:4097/v1/statusz", ws.Status.PodIP)

    req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
    resp, err := http.DefaultClient.Do(req)

    now := metav1.Now()
    ws.Status.LastHealthCheckAt = &now

    if err != nil {
        ws.Status.ConsecutiveHealthFailures++
        r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "Unknown",
            v1.ReasonHealthCheckFailed, err.Error())
        return
    }
    defer resp.Body.Close()

    var status agentd.StatuszResponse
    json.NewDecoder(resp.Body).Decode(&status)

    if !status.Healthy {
        ws.Status.ConsecutiveHealthFailures++
        r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "False",
            v1.ReasonAgentUnhealthy, "agent process not responding")
        return
    }

    ws.Status.ConsecutiveHealthFailures = 0

    if !status.Ready || len(status.Connected) == 0 {
        r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "False",
            v1.ReasonAgentDegraded, fmt.Sprintf("no providers connected (configured=%d, connected=%v)",
                status.ProvidersConfigured, status.Connected))
        return
    }

    r.setCondition(ws, v1.WorkspaceConditionAgentHealthy, "True",
        v1.ReasonAgentHealthy, fmt.Sprintf("connected=%v sessions=%d version=%s",
            status.Connected, status.SessionsActive, status.AgentVersion))
}
```

#### Pod Probes — Upgraded to HTTP via Daemon

Probes now hit the daemon on port 4097 (no auth needed) instead of raw TCP on 4096.

| Probe | Before | After | Improvement |
|-------|--------|-------|-------------|
| Liveness | TCP :4096 | HTTP GET :4097/v1/healthz | Checks agent process is alive (not just port open) |
| Readiness | TCP :4096 | HTTP GET :4097/v1/readyz | **Pod goes NotReady when no providers connected** |

**Before:** Pod shows "Running + Ready" even when every LLM call fails.
**After:** Pod goes NotReady when `connected[]` is empty — removed from service, frontend can show degraded state.

#### Init Container Fix

The init container script is built in Go in `buildCredentialSetupInit`. Currently:

```go
credScript := `
if [ -f /mnt/secrets/credentials/provider-config ]; then
  cp /mnt/secrets/credentials/provider-config /sandbox-cfg/credentials
else
  echo '{}' > /sandbox-cfg/credentials
fi
cp /mnt/secrets/password/password /sandbox-cfg/password
`
```

Fix — remove the `else` branch:

```go
credScript := `
if [ -f /mnt/secrets/credentials/provider-config ]; then
  cp /mnt/secrets/credentials/provider-config /sandbox-cfg/credentials
fi
cp /mnt/secrets/password/password /sandbox-cfg/password
`
```

The `entrypoint-common.sh` already handles the missing file case correctly:

```bash
if [[ -f /sandbox-cfg/credentials ]]; then
    cp /sandbox-cfg/credentials /tmp/agent-config.json
else
    echo '{}' > /tmp/agent-config.json
fi
```

No change needed to `entrypoint-common.sh`.

### 4. API Changes

#### WorkspaceStatusResult Extension

The type already exists in `pkg/types/types.go`. Extend it:

```go
// Existing fields
type WorkspaceStatusResult struct {
    Phase          string                     `json:"phase"`
    PVCName        string                     `json:"pvcName,omitempty"`
    ActiveSessions int                        `json:"activeSessions"`
    LastActivityAt *time.Time                 `json:"lastActivityAt,omitempty"`
    Message        string                     `json:"message,omitempty"`
    Conditions     []WorkspaceConditionResult `json:"conditions,omitempty"`
    // New fields
    CredentialState CredentialStateResult `json:"credentialState"`
    AgentHealth     AgentHealthResult     `json:"agentHealth"`
}

type CredentialStateResult struct {
    Available bool   `json:"available"`
    Reason    string `json:"reason,omitempty"`
    Message   string `json:"message,omitempty"`
}

type AgentHealthResult struct {
    Status              string `json:"status"` // Healthy, Degraded, Unhealthy, Unknown
    ProvidersConfigured int    `json:"providersConfigured"`
    AgentVersion        string `json:"agentVersion,omitempty"`
    Connected           []string `json:"connected,omitempty"`
    Message             string `json:"message,omitempty"`
    LastCheckedAt       string `json:"lastCheckedAt,omitempty"` // RFC3339
}
```

Populated from CRD conditions:

```go
func credStateFromConditions(conditions []v1.WorkspaceCondition) CredentialStateResult {
    for _, c := range conditions {
        if c.Type == v1.WorkspaceConditionCredentialsAvailable {
            return CredentialStateResult{
                Available: c.Status == "True",
                Reason:    c.Reason,
                Message:   c.Message,
            }
        }
    }
    return CredentialStateResult{Available: false, Reason: "NotChecked"}
}

func agentHealthFromConditions(conditions []v1.WorkspaceCondition) AgentHealthResult {
    for _, c := range conditions {
        if c.Type == v1.WorkspaceConditionAgentHealthy {
            status := "Unknown"
            if c.Status == "True" {
                status = "Healthy"
            } else if c.Status == "False" {
                status = "Degraded"
            }
            return AgentHealthResult{
                Status:  status,
                Message: c.Message,
            }
        }
    }
    return AgentHealthResult{Status: "Unknown"}
}
```

#### Credential Validation on Set

`SetCredentials` already exists with signature `(ctx, userID, workspaceID string, req types.SetCredentialsRequest)`. Add validation before creating the secret:

```go
func (s *Service) SetCredentials(ctx context.Context, userID, workspaceID string, req types.SetCredentialsRequest) error {
    // ... existing ownership check ...

    agent := agentpkg.Get(agent.AgentTypeOpenCode) // TODO: resolve from workspace
    result, err := agent.ValidateCredentials(ctx, req.Config)
    if err != nil {
        return fmt.Errorf("credential validation failed: %w", err)
    }
    if result.State != agentpkg.CredentialStatePresent {
        return fmt.Errorf("invalid credentials: %s", result.Message)
    }

    formatted, err := agent.FormatCredentials(req.Config)
    if err != nil {
        return fmt.Errorf("credential formatting failed: %w", err)
    }

    // ... existing secret create/update with formatted config ...
}
```

### 5. SSE Events

Emit `workspace.health` events when credential or agent health conditions change:

```json
{
    "type": "workspace.health",
    "workspace_id": "fc9ec5c7-...",
    "data": {
        "credential_state": {
            "available": false,
            "reason": "CredentialSecretNotFound",
            "message": "No workspace credential secret exists"
        },
        "agent_health": {
            "status": "Degraded",
            "message": "no providers connected"
        }
    }
}
```

Events are emitted only on **condition transitions** (status or reason changes), not on every health check. Tracked by comparing previous condition to new condition in the reconciler.

### 6. Self-Healing

#### Credential Secret Lifecycle

| Event | Action |
|-------|--------|
| Secret created | Reconcile workspace → hash check → pod restart if new creds |
| Secret updated | Reconcile workspace → hash check → pod restart if changed |
| Secret deleted | Reconcile workspace → set `CredentialsAvailable=False` → if annotation `llmsafespace.dev/suspend-on-cred-loss=true`, transition to `Suspending` |

The controller watches `workspace-creds-*` secrets via `mapCredSecretToWorkspaces`. Delete events must also trigger reconcile — **currently broken (US-8.0 blocker)**.

#### Session Drain on Credential Rotation

When credentials change and a pod restart is triggered (existing behavior in `handleActive`):
1. Controller detects hash change → deletes pod → transitions to `Creating`
2. Controller recreates pod with new credentials
3. Any active sessions are lost — acceptable for credential rotation (security > session continuity)

Future enhancement: emit SSE `workspace.health` event with `CredentialRotated` before pod deletion so the frontend can warn the user.

---

## Stories

| Story | Title | Dependencies | Estimated Complexity |
|-------|-------|-------------|---------------------|
| US-8.0 | **Fix broken secret watch** (blocker) | None | M |
| US-8.1 | workspace-agentd binary + shared types package (`pkg/agentd/types.go`) + entrypoint integration + Dockerfile build | None | M |
| US-8.2 | HTTP readiness/liveness probes via daemon (change `buildPod`) | US-8.1 | S |
| US-8.3 | Agent Runtime Interface (controller/API side) + OpenCode implementation | None | M |
| US-8.4 | Credential Validation on SetCredentials | US-8.3 | S |
| US-8.5 | Credential Health Conditions on Workspace CRD | US-8.3 | M |
| US-8.6 | Agent Health Check in Controller (5-min interval, grace period, backoff) | US-8.1, US-8.5 | M |
| US-8.7 | Init Container Fix (remove `{}` else branch in `buildCredentialSetupInit`) | None | S |
| US-8.8 | API CredentialState + AgentHealth in Status Response | US-8.5, US-8.6 | M |
| ~~US-8.9~~ | ~~SSE workspace.health Events (transition-only)~~ | ~~US-8.8~~ | ~~M~~ |
| US-8.10 | Self-Healing: Suspend on Credential Loss | US-8.5 | S |

> **US-8.9 Status (2026-06-05):** Superseded and generalized. The original story scoped push events to `workspace.health` only. The decision is to implement a broader push notification system for any backend→frontend state change (health, credential reload, workspace updates, system announcements). Epic 28's `UserEventBroker` is ready infrastructure. See GitHub issue #43 for the generalized design. US-8.9 as originally scoped is closed; the broader capability will be designed separately.

---

## Assumptions

1. **workspace-agentd responses are tiny (<1KB) and stable.** We own the schema — it only changes when we change the daemon. The daemon's internal calls to opencode can change between versions; only the external API matters.
2. **The daemon calling opencode's `/provider` (3.5MB) locally is acceptable.** Over localhost this is fast (~1ms for the data transfer, ~50ms for opencode to generate). Called at most once every 30s (for readiness probes, cached for 30s) and once every 5 min (for controller statusz checks). The 30s cache coalesces multiple callers.
3. **Provider connected != provider reachable.** `connected[]` tells us which providers are authenticated, not which are currently responding to LLM calls. Reachability can only be observed from actual LLM call outcomes. V2 will track session errors via the daemon to detect this.
4. **Credential validation is structural only.** We validate JSON structure, not whether the API key works. A test call would leak credentials and add latency.
5. **Agent type is resolved from workspace/runtime config.** Currently hardcoded to `opencode`. The `RuntimeEnvironment` CRD will gain an `agentType` field. The daemon abstracts this — controller doesn't need to know.
6. **Missing credential secret is an error state.** The platform manages credentials via secrets. No secret means the workspace is unmanaged — the platform cannot guarantee function, cannot rotate credentials, cannot audit access. `CredentialsAvailable=False` is surfaced as an error, not a warning.
7. **opencode API schema is not formally stable.** This is now contained within the daemon — if opencode changes, only the daemon's backend needs updating. The controller is insulated.

---

## Validation Results

### VALIDATED (tested with evidence)

| Assumption | Result | Evidence |
|------------|--------|----------|
| Controller can reach sandbox pod IPs over HTTP | **PASS** | Created curl-test pod → `200 {"healthy":true,"version":"1.2.27"}` from `10.69.6.126:4096` (opencode port). No network policies blocking. Same network path applies to :4097 (workspace-agentd). |
| `/global/health` returns `{healthy, version}` with Basic auth | **PASS** | Tested on 10/10 pods. All return `{"healthy":true,"version":"1.2.27"}`. Always returns healthy=true regardless of provider state. |
| `/config/providers` returns provider list | **PASS** | 2502 bytes on all 10 pods (same default opencode Zen provider). Returns `{providers:[...], default:{...}}`. No `connected` field. |
| Agent requires Basic auth | **PASS** | 401 without auth on all endpoints. 200 with correct `opencode:password`. |
| Password stored in `workspace-pw-{id}` secret | **PASS** | Verified on multiple pods. |
| Init container writes `{}` when no secret exists | **PASS** | Read init container script in `buildCredentialSetupInit` — `echo '{}' > /sandbox-cfg/credentials` in else branch. |
| Agent provider init is fast (<1s) | **PASS** | Log analysis: provider init takes 59ms (`duration=59`). Provider inits lazily on first session request, not at boot. 2-minute grace period is very conservative. |
| `/provider` `connected[]` is dynamic | **PASS** | `connected: ["opencode"]` while `all` has 40+ providers. Only authenticated providers appear. |

### VALIDATED — ISSUES FOUND

| Assumption | Result | Evidence | Impact on Design |
|------------|--------|----------|------------------|
| `/config/providers` has `connected` field | **FAIL** | Only `/provider` (3.5MB) has `connected: ["opencode"]`. `/config/providers` (2.5KB) only has `providers[]` and `default`. | Daemon must call `/provider` for `connected[]`. Acceptable over localhost with 30s caching. |
| Secret watch triggers reconcile on create/delete | **FAIL** | Created `workspace-creds-*` secret → waited 10s → no reconcile. Deleted → no reconcile. Controller logs show no events. | **Critical bug: secret watch is broken.** Added US-8.0 (blocker). Root cause unknown. |

### NOT VALIDATED (requires production data)

| Assumption | How to Validate |
|------------|----------------|
| 5-minute health check interval is appropriate | Monitor after deployment. Adjust MTTD vs resource usage. |
| 3 failures → 15-min backoff is the right threshold | Operational tuning after deployment. |
| `/config/providers` stays under 1MB with custom providers | Monitor response sizes in production. |

---

## Testing Requirements

Per README-LLM.md Rule 0 (TDD). Every story has unit, integration, and E2E tests defined.

### Unit Tests

| Story | Tests |
|-------|-------|
| **US-8.0** | `mapCredSecretToWorkspaces`: matching prefix → returns request; non-matching → nil; non-Secret object → nil. Secret delete event → returns request. Verify with fake client + fake watch. |
| **US-8.1** | `OpenCodeClient.IsHealthy`: mock HTTP server returning 200/503/timeout. `OpenCodeClient.ConnectedProviders`: mock `/provider` returning 3.5MB JSON with connected array; verify stream-parse extracts `connected` correctly. Cache: two calls within 30s → second uses cache. `OpenCodeClient.ConfiguredProviderCount`: mock `/config/providers`. `/v1/healthz` handler: daemon returns correct JSON. `/v1/readyz` handler: returns ready=true when connected non-empty, ready=false when empty. `/v1/statusz` handler: full response. |
| **US-8.2** | `buildPod` probe spec: verify readiness uses HTTPGet on :4097/v1/readyz. Verify liveness uses HTTPGet on :4097/v1/healthz. Verify no TCPSocket probe remains. |
| **US-8.3** | `AgentRuntime` interface compliance: OpenCode agent implements both methods. `ValidateCredentials`: valid JSON → Present; `{}` → Missing; empty bytes → Missing; invalid JSON → Invalid. `FormatCredentials`: passthrough identity. Registry: Get("opencode") → returns OpenCodeAgent; Get("unknown") → error. Register adds new type. |
| **US-8.4** | `SetCredentials` with valid config → creates secret. Invalid JSON → returns error. Empty config → returns error. Verify `provider-config` key in created secret. |
| **US-8.5** | `setCondition` helper: appends new condition; updates existing condition; sets LastTransitionTime only on status/reason change. `checkCredentialState`: secret exists with valid data → condition True/Reason=ReasonCredentialsValid. Secret not found → False/Reason=ReasonCredentialSecretNotFound. Secret exists but empty data → False/Reason=ReasonCredentialEmpty. Secret exists but invalid JSON → False/Reason=ReasonCredentialInvalid. Get error → Unknown/Reason=ReasonCredentialCheckError. Verify condition does NOT change workspace phase. |
| **US-8.6** | `shouldRunHealthCheck`: nil LastHealthCheckAt → true. Within 5 min → false. After 5 min → true. Within grace period (2 min after StartTime) → false. After grace period → true. Backoff: ConsecutiveHealthFailures >= 3 → 15 min interval. `checkAgentHealth`: mock :4097/statusz returning healthy+ready → True/ReasonAgentHealthy. Mock returning healthy+!ready → False/ReasonAgentDegraded. Mock returning !healthy → False/ReasonAgentUnhealthy. Mock connection refused → Unknown/ReasonHealthCheckFailed. ConsecutiveHealthFailures increments on failure, resets to 0 on success. Verify `agentd.StatuszResponse` type is used (no anonymous struct). |
| **US-8.7** | Verify init container script: when `/mnt/secrets/credentials/provider-config` absent → no file written to `/sandbox-cfg/credentials`. When present → file copied. Password always copied. |
| **US-8.8** | `credStateFromConditions`: True condition → {available:true}. False condition → {available:false, reason}. No matching condition → {reason:"NotChecked"}. `agentHealthFromConditions`: True → "Healthy". False → "Degraded". No match → "Unknown". |
| **US-8.10** | Reconcile on secret delete → sets CredentialsAvailable=False. With annotation `llmsafespace.dev/suspend-on-cred-loss=true` → transitions to Suspending. Without annotation → only sets condition, stays Active. |

### Integration Tests

| Story | Tests |
|-------|-------|
| **US-8.1** | Build workspace-agentd binary. Start mock opencode server on :4096 with known responses. Start workspace-agentd. Hit all three endpoints and verify response structure. Test with opencode returning errors → daemon degrades gracefully. Test with opencode not started → daemon returns 502 on healthz. |
| **US-8.4** | Full `SetCredentials` flow with fake K8s client: valid request creates `workspace-creds-{id}` secret with correct data. Invalid request returns error and does NOT create secret. |
| **US-8.6** | Controller integration with envtest: create Workspace CR → pod created → mock :4097 returns healthy → condition set. Delete secret → reconcile → condition updated. |
| **US-8.9** | Controller integration with envtest: update workspace conditions → verify SSE event emitted with correct `workspace.health` payload. Update to same condition → no event emitted. |

### E2E Tests

| Story | Tests |
|-------|-------|
| **US-8.2** | Create workspace with credentials. Verify pod starts and becomes Ready. Remove credentials (delete secret, restart pod). Verify pod goes NotReady when no providers connected. |
| **US-8.8** | `GET /api/v1/workspaces/:id/status` returns `credentialState` and `agentHealth` fields. Test all state matrix permutations: workspace with valid creds → credentialState.available=true. Workspace without creds → credentialState.available=false. Workspace with healthy agent → agentHealth.status="Healthy". Workspace with degraded agent → agentHealth.status="Degraded". |
| **US-8.9** | SSE client subscribes to workspace events. Update credentials → receive `workspace.health` event with credentialState change. Agent goes degraded → receive `workspace.health` event with agentHealth change. Verify no event on consecutive identical states. |
| **US-8.10** | Create workspace with credentials. Delete credential secret. Verify workspace transitions to Suspending (if annotation set) or stays Active with CredentialsAvailable=False. Re-add credentials → verify workspace recovers. |
