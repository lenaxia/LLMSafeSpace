# Docker Deployment Design

**Status:** Draft v5
**Date:** 2026-05-23
**Changes from v3:** See Appendix A (§18) for diff rationale

---

## 1. Assumptions

Every assumption below is validated with evidence from the codebase.

| # | Assumption | Validation | Implication |
|---|-----------|------------|-------------|
| A1 | Runtime entrypoints can run without K8s | `entrypoint-opencode.sh` reads `/sandbox-cfg/password` (file) and `/sandbox-cfg/credentials` (file). No K8s API calls. The K8s init container is just a delivery mechanism for these files. | Docker must deliver password and credentials via files or env vars |
| A2 | `opencode serve` authenticates via HTTP Basic Auth (user `opencode`, password from `OPENCODE_SERVER_PASSWORD`) | `proxy.go:328`: `req.SetBasicAuth("opencode", password)`. Entryoint sets `OPENCODE_SERVER_PASSWORD` from file at `entrypoint-opencode.sh:9` | Docker provider must set `OPENCODE_SERVER_PASSWORD` env var or write `/sandbox-cfg/password` file |
| A3 | Password generation is the controller's job in K8s | `controller.go:467-493`: `ensurePasswordSecret()` generates 32-char random password, stores in K8s Secret `sandbox-pw-{name}` | In Docker mode (no controller), the API must generate the password |
| A4 | Credentials are delivered via K8s init container | `controller.go:658-730`: `credential-setup` init copies `workspace-creds-{ref}` Secret → `/sandbox-cfg/credentials`. Entrypoint reads this file at `entrypoint-common.sh:3` | Docker has no init containers; need alternative delivery |
| A5 | The API binary has no migration logic | `main.go`: no migration calls. `database.go:57-61`: `Start()` just logs. `migrate.sh` uses external `golang-migrate` CLI. `go.mod`: no `golang-migrate` dependency | Docker mode needs a migration strategy (new dependency or docker-compose init service) |
| A6 | Workspace phase lives exclusively in CRD `status.phase` | `000002_workspaces.up.sql`: no `phase` column. `workspace_service.go` reads/writes phase only via CRD | Docker mode needs a phase storage mechanism (DB column) |
| A7 | Docker bridge DNS resolves container names to IPs | Docker Engine 20.10+ feature: containers on user-defined bridge networks resolve each other by name | Proxy can target `http://sandbox-{id}:4096` instead of PodIP |
| A8 | Docker event stream is ephemeral and can break | Docker documentation: `GET /events` is a streaming endpoint that can close unexpectedly | Watch implementation must reconnect with backoff |
| A9 | Container pause/unpause maps to suspend/resume | Docker `pause` freezes all processes (cgroups freezer). `unpause` resumes them. opencode serve resumes where it left off | Workspace suspend = pause all sandbox containers for that workspace |
| A10 | `types.Sandbox` embeds `metav1.TypeMeta` and `metav1.ObjectMeta` — K8s apimachinery types in the API response DTO | `types.go:34-36`: `Sandbox struct { metav1.TypeMeta, metav1.ObjectMeta, ... }`. `convertCRDToAPI` (`sandbox_service.go:385-412`) copies these directly from the CRD. In Docker mode there is no CRD. | Docker mode must construct `metav1.ObjectMeta` manually (Name, Namespace, Labels, CreationTimestamp). `TypeMeta` is constant. See §8.3. |
| A11 | `ListSandboxes` enriches each item with live phase/usage from K8s CRD status | `sandbox_service.go:238-248`: per-item `Sandboxes(ns).Get(sb.ID)` for Phase, StartTime, CPUUsage, MemoryUsage | Docker mode: per-item `Inspect()` or batch `ContainerList` for phase. See §8.4. |
| A12 | `TerminateSandbox` deletes the CRD using `sandbox.Namespace` from the CRD's `ObjectMeta` | `sandbox_service.go:301`: `Sandboxes(sandbox.Namespace).Delete(...)`. `sandbox` is `*types.Sandbox` returned by `GetSandbox()`, which copies `ObjectMeta.Namespace` from the CRD | Docker mode: no namespace. Use fixed namespace or empty string. See §8.5. |

---

## 2. Motivation

LLMSafeSpace is Kubernetes-first. The controller, CRDs, PVCs, and secret
management all assume a K8s cluster. This creates a high barrier for:

- **Homelab users** running sandboxes on a single Docker host
- **Developers** wanting a fast local loop without kind/K3s overhead
- **Evaluators** wanting to try the platform before committing to K8s

This document designs a Docker backend behind a provider abstraction. The API
service handles sandbox lifecycle directly — no controller, no CRDs, no Helm.

### Scope

| In scope | Out of scope |
|----------|--------------|
| Sandbox create/terminate lifecycle | Multi-node orchestration |
| Workspace persistent volumes (Docker named volumes) | Horizontal scaling |
| Credential injection into sandbox containers | Network policy enforcement |
| Proxy to sandbox `opencode serve` | Warm pool / pre-provisioning |
| docker-compose.yaml for single-command startup | Multi-tenant production |
| Suspend/resume via container pause/unpause | SandboxProfile support |

### Key tradeoff

The Docker socket grants unrestricted host access. Acceptable for homelab/dev
where the user already has root. Not acceptable for multi-tenant production.

---

## 3. Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│  Docker Host                                                     │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────────┐ │
│  │  docker-compose managed                                     │ │
│  │                                                             │ │
│  │  ┌──────────────┐  ┌────────────┐  ┌─────────┐             │ │
│  │  │  API         │  │ PostgreSQL │  │ Redis    │             │ │
│  │  │  (Gin)       │──│ 17-alpine  │──│ 7-alpine│             │ │
│  │  │  :8080       │  └────────────┘  └─────────┘             │ │
│  │  │  docker.sock │                                       │ │
│  │  └──────┬───────┘                                       │ │
│  └─────────┼─────────────────────────────────────────────────┘ │
│            │ Docker Engine API                                   │
│  ┌─────────┼─────────────────────────────────────────────────┐ │
│  │  Dynamic containers (sandbox-{id})                         │ │
│  │                                                             │ │
│  │  ┌──────────────┐  ┌──────────────┐                       │ │
│  │  │ sb-abc123    │  │ sb-def456    │  ...                  │ │
│  │  │ opencode:4096│  │ opencode:4096│                       │ │
│  │  │ vol: ws-aaa  │  │ vol: ws-bbb  │                       │ │
│  │  └──────────────┘  └──────────────┘                       │ │
│  │                                                             │ │
│  │  Named volumes: llmsafespace-ws-{id}                       │ │
│  └─────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────┘
```

### K8s → Docker mapping

| K8s concept | Docker equivalent | Source of truth |
|-------------|-------------------|-----------------|
| Sandbox CRD | DB row + container | DB for metadata, Docker for runtime state |
| Workspace CRD | DB row + named volume | DB for metadata + phase |
| K8s Secret (`sandbox-pw-{id}`) | `sandbox_passwords` table | PostgreSQL |
| K8s Secret (`workspace-creds-{id}`) | `workspace_credentials` table | PostgreSQL |
| Init container `credential-setup` | Inline entrypoint wrapper (see §6.2) | Writes files from env vars |
| Controller reconciliation | API direct lifecycle | Immediate, not eventual |
| Pod IP → proxy target | Container name on bridge network | Docker DNS |
| CRD watcher (phase changes) | Docker event stream | Docker Events API |
| PVC | Docker named volume | Docker |
| RuntimeEnvironment CRD | Config image map | config.yaml / env vars |
| SandboxProfile CRD | Not supported | Default profile only |

---

## 4. K8s Coupling Audit

### Files with K8s dependencies

| File | Coupling | Lines |
|------|----------|-------|
| `api/internal/app/app.go` | Creates `kubernetes.Client`, passes to services + proxy | `:33-45` |
| `api/internal/services/services.go` | Passes `KubernetesClient` to sandbox/workspace constructors | `:58` |
| `api/internal/services/sandbox/sandbox_service.go` | 6 calls to `k8sClient.LlmsafespaceV1().Sandboxes()`; builds `v1.Sandbox` CRDs; imports `pkg/apis/llmsafespace/v1` | `:141,162,183,190,238,301` |
| `api/internal/services/workspace/workspace_service.go` | 10 K8s calls (CRDs + Secrets); builds `v1.Workspace` CRDs; imports `pkg/apis/llmsafespace/v1` | `:110,128,172,244,271,287,309,323,345,403-451` |
| `api/internal/handlers/proxy.go` | 8 K8s calls (sandbox get, secrets, workspace get); `v1.Sandbox` in callbacks | `:106,117,199,275,494,527,671,684` |
| `api/internal/handlers/crd_watcher.go` | Pure K8s: `Sandboxes().Watch()` | entire file |
| `api/internal/server/router.go` | `sandboxOwnershipMiddleware` returns `*v1.Sandbox` | `:420-440` |

---

## 5. Backend Interfaces

Two interfaces. Each K8s/Docker implementation is a single struct satisfying
both. The split follows existing domain boundaries in the codebase:
`SandboxService` owns sandbox lifecycle, `WorkspaceService` owns workspace
lifecycle.

### Why two interfaces, not four

The v1 design had four interfaces: `SandboxProvider`, `WorkspaceProvider`,
`PasswordStore`, `CredentialStore`. Passwords and credentials are not separate
domains — they are tightly coupled to their parent entity:

- **Passwords** are generated when a sandbox is provisioned, stored alongside
  the sandbox, and consumed by the proxy handler for that sandbox. The
  password lifecycle exactly matches the sandbox lifecycle.
- **Credentials** are set per-workspace, stored under the workspace, and
  mounted into sandbox containers via the workspace reference. The credential
  lifecycle exactly matches the workspace lifecycle.

Separating them into four interfaces would scatter related concerns across
multiple structs and create coordination overhead (which struct holds the DB
reference? who owns the password cleanup on sandbox destruction?). Two
interfaces keep related state together.

### Interface definitions

```go
// pkg/interfaces/backend.go
package interfaces

import (
    "context"
    "time"
)

// SandboxBackend abstracts sandbox container lifecycle and password storage.
//
// In K8s mode, the controller generates passwords and stores them in K8s Secrets.
// The API's proxy handler reads passwords from those secrets. This backend
// wraps both the CRD operations and the secret reads.
//
// In Docker mode, there is no controller. The backend generates passwords,
// stores them in PostgreSQL, and injects them into containers. The proxy
// handler reads from the same store.
type SandboxBackend interface {
    // Provision creates and starts a sandbox container/pod. The backend
    // generates a random password, persists it, and arranges for it to be
    // available to opencode serve inside the container. Returns the runtime
    // state including the address the proxy should target.
    Provision(ctx context.Context, params SandboxParams) (*SandboxState, error)

    // Inspect returns current runtime state.
    Inspect(ctx context.Context, sandboxID string) (*SandboxState, error)

    // Destroy stops and removes the sandbox and deletes the stored password.
    // Cleanup is best-effort: if the container is already gone, the password
    // is still deleted.
    Destroy(ctx context.Context, sandboxID string) error

    // Watch returns a channel emitting phase changes for managed sandboxes.
    // The caller cancels ctx to stop. The implementation must reconnect on
    // stream failure.
    Watch(ctx context.Context) (<-chan SandboxPhaseChange, error)

    // GetPassword retrieves the stored password for proxy authentication.
    GetPassword(ctx context.Context, sandboxID string) (string, error)

    // List returns runtime states for all sandboxes matching the given labels.
    // Used by ListSandboxes for batch enrichment without per-item API calls.
    List(ctx context.Context, labels map[string]string) ([]SandboxState, error)
}

// WorkspaceBackend abstracts workspace volume lifecycle, phase management,
// and credential storage.
//
// SetPhase semantics differ by implementation:
//   - K8s: updates the Workspace CRD status. The controller watches and
//     reconciles (stops/starts pods).
//   - Docker: updates the DB phase column AND performs container side effects
//     (pause/unpause sandbox containers). There is no controller to delegate to.
type WorkspaceBackend interface {
    // ProvisionVolume creates the persistent volume. Returns the volume name.
    ProvisionVolume(ctx context.Context, params VolumeParams) (string, error)

    // DestroyVolume removes the persistent volume.
    DestroyVolume(ctx context.Context, workspaceID string) error

    // GetPhase returns the current workspace phase.
    GetPhase(ctx context.Context, workspaceID string) (string, error)

    // SetPhase transitions workspace to newPhase. Returns error if the
    // current phase does not match expectedCurrent (optimistic concurrency).
    SetPhase(ctx context.Context, workspaceID, expectedCurrent, newPhase string) error

    // GetCredentials returns stored credential config.
    GetCredentials(ctx context.Context, workspaceID string) ([]byte, error)

    // SetCredentials stores or replaces credential config.
    SetCredentials(ctx context.Context, workspaceID string, data []byte) error

    // DeleteCredentials removes stored credentials.
    DeleteCredentials(ctx context.Context, workspaceID string) error
}
```

### Types

```go
// SandboxParams is the input to Provision. The service layer derives these
// from CreateSandboxRequest + config.
type SandboxParams struct {
    ID           string
    Runtime      string            // python, nodejs, go, base
    Image        string            // resolved container image
    WorkspaceRef string            // workspace ID for volume mount
    UserID       string            // owner
    Labels       map[string]string // applied to container/pod
    Timeout      int               // seconds, 0 = use default
    CPU          string            // optional, e.g. "2"
    Memory       string            // optional, e.g. "2g"
    Credentials  []byte            // workspace credentials (nil if none)
}

// SandboxState is the observable runtime state. Backend-agnostic.
type SandboxState struct {
    ID           string
    Phase        string            // Running, Suspended, Terminated, Failed, etc.
    Address      string            // proxy target: PodIP (K8s) or container name (Docker)
    Port         int               // opencode port (default 4096)
    Labels       map[string]string
    WorkspaceRef string
    StartedAt    time.Time
}

// SandboxPhaseChange is emitted by Watch.
type SandboxPhaseChange struct {
    SandboxID string
    OldPhase  string
    NewPhase  string
}

// VolumeParams is the input to ProvisionVolume.
type VolumeParams struct {
    ID     string
    UserID string
    Labels map[string]string
}
```

### SetPhase and SRP

`SetPhase` in Docker mode performs container side effects (pause/unpause). This
could be seen as violating SRP — why does the workspace backend know about
containers?

The answer: in K8s mode, the workspace controller (`controller/internal/workspace/`)
already stops pods when suspending a workspace. The coordination exists in K8s
too — it's just split between the API (writes CRD status) and the controller
(reconciles pods). In Docker mode there is no controller, so the backend must
perform both steps. The backend is the Docker-specific implementation of the
same coordination that K8s splits across two processes. The interface remains
clean: `SetPhase(ctx, id, expectedCurrent, newPhase)` is a single operation
from the caller's perspective.

---

## 6. Docker Backend Implementation

### File structure

```
api/internal/provider/docker/
├── provider.go    // Constructor, Docker client init, network setup
├── sandbox.go     // SandboxBackend implementation
└── workspace.go   // WorkspaceBackend implementation
```

One struct `Backend` implements both `SandboxBackend` and `WorkspaceBackend`:

```go
type Backend struct {
    client  *dockerclient.Client
    db      interfaces.DatabaseService
    logger  pkginterfaces.LoggerInterface
    config  DockerConfig
}

var (
    _ interfaces.SandboxBackend   = (*Backend)(nil)
    _ interfaces.WorkspaceBackend = (*Backend)(nil)
)
```

### 6.1 Sandbox Provisioning

```
Provision:
  1. Generate sandbox ID (uuid.New().String(), "sb-" prefix)
  2. Generate cryptographically random password (32 bytes, hex)
  3. Store password in sandbox_passwords table
  4. Resolve image from config (runtime name → image tag)
  5. Create Docker container:
     - Name: sandbox-{id}
     - Image: from config
     - Entrypoint: inline init script (see §6.2)
     - Env:
       OPENCODE_SERVER_PASSWORD={password}     (direct env var — entrypoint preserves it
                                                 if /sandbox-cfg/password file doesn't exist)
       LLMSAFESPACE_CREDENTIALS={base64}        (base64-encoded credentials, or empty)
     - Mounts:
       llmsafespace-ws-{workspaceRef}:/workspace  (named volume)
       tmpfs:/tmp                                 (rw,noexec,nosuid,size=100m)
       tmpfs:/sandbox-cfg                         (rw,noexec,nosuid,size=10m)
     - Network: llmsafespace-sandbox bridge
     - Labels: llmsafespace.dev/sandbox-id, user-id, workspace, runtime, managed=true
     - RestartPolicy: unless-stopped
     - User: 1000:1000
  5. Start container
  6. On start failure: remove container, delete password, return error
  7. Return SandboxState{Address: "sandbox-{id}", Port: 4096}
```

### 6.2 Password and credential delivery

**The problem:** K8s uses an init container (`credential-setup`) that copies
password from a K8s Secret mount to `/sandbox-cfg/password` and credentials
from another Secret mount to `/sandbox-cfg/credentials`. Docker has no init
containers.

**The solution:** Docker overrides the entrypoint with an inline init script
that writes `/sandbox-cfg` files from env vars, then execs the original
entrypoint. This replicates the init container behavior without modifying
runtime images.

```go
entrypoint := "/bin/sh", "-c",
    "mkdir -p /sandbox-cfg && " +
    "printf '%s' \"$OPENCODE_SERVER_PASSWORD\" > /sandbox-cfg/password && " +
    "if [ -n \"$LLMSAFESPACE_CREDENTIALS\" ]; then " +
    "  printf '%s' \"$LLMSAFESPACE_CREDENTIALS\" | base64 -d > /sandbox-cfg/credentials; " +
    "fi && " +
    "exec /usr/local/bin/entrypoint-opencode.sh"
```

This works because:
- `entrypoint-opencode.sh:8-10` reads `/sandbox-cfg/password` if it exists and
  sets `OPENCODE_SERVER_PASSWORD`. The file will exist because the init script
  writes it.
- `entrypoint-common.sh:3-4` reads `/sandbox-cfg/credentials` if it exists.
  The init script writes it only when credentials are provided.
- The runtime images have `/bin/sh` and `base64` available (Debian bookworm-slim).

**Why not just set env vars directly?**

Setting `OPENCODE_SERVER_PASSWORD` directly works for passwords (the entrypoint
only overwrites if the file exists, and without the init script the file won't
exist). But credentials go through `entrypoint-common.sh` which reads
`/sandbox-cfg/credentials` — there is no env var fallback. The init script
approach handles both consistently.

### 6.3 Container creation

```go
func (b *Backend) Provision(ctx context.Context, params SandboxParams) (*SandboxState, error) {
    sandboxID := "sb-" + uuid.New().String()
    password := generatePassword(32)
    if err := b.db.SetSandboxPassword(ctx, sandboxID, password); err != nil {
        return nil, fmt.Errorf("storing password: %w", err)
    }

    containerName := "sandbox-" + sandboxID
    volName := "llmsafespace-ws-" + params.WorkspaceRef

    labels := buildLabels(params)
    env := buildEnv(password, params.Credentials)
    entrypoint := buildInitEntrypoint()

    createResp, err := b.client.ContainerCreate(ctx,
        &container.Config{
            Image:      params.Image,
            Hostname:   containerName,
            Labels:     labels,
            Env:        env,
            Entrypoint: entrypoint,
            ExposedPorts: nat.PortSet{"4096/tcp": {}},
            User:       "1000:1000",
        },
        &container.HostConfig{
            Binds: []string{volName + ":/workspace"},
            Tmpfs: map[string]string{
                "/tmp":         "rw,noexec,nosuid,size=100m",
                "/sandbox-cfg": "rw,noexec,nosuid,size=10m",
            },
            RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
        },
        &network.NetworkingConfig{
            EndpointsConfig: map[string]*network.EndpointSettings{
                b.config.Network: {},
            },
        },
        nil,
        containerName,
    )
    if err != nil {
        b.db.DeleteSandboxPassword(ctx, sandboxID)
        return nil, fmt.Errorf("creating container: %w", err)
    }

    if err := b.client.ContainerStart(ctx, createResp.ID, types.ContainerStartOptions{}); err != nil {
        b.client.ContainerRemove(ctx, createResp.ID, types.ContainerRemoveOptions{Force: true})
        b.db.DeleteSandboxPassword(ctx, sandboxID)
        return nil, fmt.Errorf("starting container: %w", err)
    }

    return &SandboxState{
        ID:           sandboxID,
        Phase:        "Running",
        Address:      containerName,
        Port:         4096,
        Labels:       labels,
        WorkspaceRef: params.WorkspaceRef,
    }, nil
}
```

### 6.4 Inspect — container state to phase

```go
func (b *Backend) Inspect(ctx context.Context, sandboxID string) (*SandboxState, error) {
    inspect, err := b.client.ContainerInspect(ctx, "sandbox-"+sandboxID)
    if err != nil {
        return nil, fmt.Errorf("inspecting container: %w", err)
    }

    state := &SandboxState{
        ID:           sandboxID,
        Phase:        containerStateToPhase(inspect.State),
        Address:      "sandbox-" + sandboxID,
        Port:         4096,
        Labels:       inspect.Config.Labels,
        WorkspaceRef: inspect.Config.Labels["llmsafespace.dev/workspace"],
    }
    if inspect.State.StartedAt != "" {
        state.StartedAt, _ = time.Parse(time.RFC3339, inspect.State.StartedAt)
    }
    return state, nil
}

func containerStateToPhase(s *types.ContainerState) string {
    switch {
    case s.Running:
        return "Running"
    case s.Paused:
        return "Suspended"
    case s.Dead:
        return "Failed"
    case s.Status == "created":
        return "Creating"
    case s.Status == "exited":
        return "Terminated"
    case s.Status == "removing":
        return "Terminating"
    default:
        return "Unknown"
    }
}

// containerListStateToPhase maps Docker container list state strings
// (ContainerList returns State as a string, not a ContainerState struct).
func containerListStateToPhase(state string) string {
    switch state {
    case "running":
        return "Running"
    case "paused":
        return "Suspended"
    case "exited", "dead":
        return "Terminated"
    case "created":
        return "Creating"
    case "removing":
        return "Terminating"
    default:
        return "Unknown"
    }
}
```

### 6.5 Batch List for status enrichment

```go
func (b *Backend) List(ctx context.Context, labels map[string]string) ([]SandboxState, error) {
    filterArgs := filters.NewArgs()
    for k, v := range labels {
        filterArgs.Add("label", k+"="+v)
    }
    containers, err := b.client.ContainerList(ctx, types.ContainerListOptions{
        All:     true,
        Filters: filterArgs,
    })
    if err != nil {
        return nil, fmt.Errorf("listing containers: %w", err)
    }
    states := make([]SandboxState, 0, len(containers))
    for _, c := range containers {
        states = append(states, SandboxState{
            ID:           c.Labels["llmsafespace.dev/sandbox-id"],
            Phase:        containerListStateToPhase(c.State),
            Address:      c.Names[0], // e.g. "/sandbox-sb-abc123"
            Port:         4096,
            Labels:       c.Labels,
            WorkspaceRef: c.Labels["llmsafespace.dev/workspace"],
        })
    }
    return states, nil
}
```

### 6.6 Event watcher with reconnection

Docker event streams are ephemeral (A8). The watcher must reconnect with
backoff. On reconnect, the baseline state may have changed, so the watcher
re-lists containers to avoid stale events.

```go
func (b *Backend) Watch(ctx context.Context) (<-chan SandboxPhaseChange, error) {
    ch := make(chan SandboxPhaseChange, 64)

    go func() {
        defer close(ch)
        known := b.snapshotPhases(ctx)

        for {
            if err := b.watchOnce(ctx, ch, known); err != nil {
                if ctx.Err() != nil {
                    return
                }
                b.logger.Error("Docker event stream error, reconnecting", err)
                select {
                case <-ctx.Done():
                    return
                case <-time.After(2 * time.Second):
                }
                known = b.snapshotPhases(ctx)
            }
        }
    }()

    return ch, nil
}

func (b *Backend) snapshotPhases(ctx context.Context) map[string]string {
    known := make(map[string]string)
    containers, err := b.client.ContainerList(ctx, types.ContainerListOptions{
        All: true,
        Filters: filters.NewArgs(
            filters.KeyValues{"label": "llmsafespace.dev/managed=true"},
        ),
    })
    if err != nil {
        return known
    }
    for _, c := range containers {
        id := c.Labels["llmsafespace.dev/sandbox-id"]
        if id == "" {
            continue
        }
        known[id] = containerListStateToPhase(c.State)
    }
    return known
}

func (b *Backend) watchOnce(ctx context.Context, ch chan<- SandboxPhaseChange, known map[string]string) error {
    f := filters.NewArgs()
    f.Add("type", "container")
    f.Add("label", "llmsafespace.dev/managed=true")

    eventsCh, errCh := b.client.Events(ctx, types.EventsOptions{Filters: f})

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case event := <-eventsCh:
            sandboxID := event.Actor.Attributes["llmsafespace.dev/sandbox-id"]
            if sandboxID == "" {
                continue
            }
            newPhase := dockerActionToPhase(event.Action)
            if newPhase == "" {
                continue
            }
            oldPhase, existed := known[sandboxID]
            known[sandboxID] = newPhase
            if existed && oldPhase != newPhase {
                select {
                case ch <- SandboxPhaseChange{SandboxID: sandboxID, OldPhase: oldPhase, NewPhase: newPhase}:
                default:
                }
            }
        case err, ok := <-errCh:
            if !ok {
                return fmt.Errorf("event stream closed")
            }
            if err != nil {
                return fmt.Errorf("event stream: %w", err)
            }
        }
    }
}

func dockerActionToPhase(action string) string {
    switch action {
    case "start", "unpause":
        return "Running"
    case "pause":
        return "Suspended"
    case "die", "stop", "destroy":
        return "Terminated"
    default:
        return ""
    }
}
```

### 6.7 Destroy — best-effort cleanup

```go
func (b *Backend) Destroy(ctx context.Context, sandboxID string) error {
    containerName := "sandbox-" + sandboxID

    timeout := 10
    if err := b.client.ContainerStop(ctx, containerName, &container.StopOptions{Timeout: &timeout}); err != nil {
        if !client.IsErrNotFound(err) {
            b.logger.Error("Failed to stop container", err, "sandboxID", sandboxID)
        }
    }

    if err := b.client.ContainerRemove(ctx, containerName, types.ContainerRemoveOptions{Force: true}); err != nil {
        if !client.IsErrNotFound(err) {
            b.logger.Error("Failed to remove container", err, "sandboxID", sandboxID)
        }
    }

    if err := b.db.DeleteSandboxPassword(ctx, sandboxID); err != nil {
        b.logger.Error("Failed to delete password", err, "sandboxID", sandboxID)
    }

    return nil
}
```

### 6.8 Workspace suspend/resume

`SetPhase` in Docker mode combines the DB update with container side effects.
This mirrors what the K8s workspace controller does (watches CRD phase changes
and stops/starts pods).

```go
func (b *Backend) SetPhase(ctx context.Context, workspaceID, expectedCurrent, newPhase string) error {
    current, err := b.db.GetWorkspacePhase(ctx, workspaceID)
    if err != nil {
        return fmt.Errorf("reading phase: %w", err)
    }
    if current != expectedCurrent {
        return fmt.Errorf("phase conflict: expected %q, got %q", expectedCurrent, current)
    }

    switch newPhase {
    case "Suspending":
        if err := b.pauseWorkspaceContainers(ctx, workspaceID); err != nil {
            return fmt.Errorf("pausing containers: %w", err)
        }
        newPhase = "Suspended"
    case "Resuming":
        if err := b.unpauseWorkspaceContainers(ctx, workspaceID); err != nil {
            return fmt.Errorf("unpausing containers: %w", err)
        }
        newPhase = "Active"
    }

    return b.db.SetWorkspacePhase(ctx, workspaceID, newPhase)
}

func (b *Backend) pauseWorkspaceContainers(ctx context.Context, workspaceID string) error {
    containers, _ := b.client.ContainerList(ctx, types.ContainerListOptions{
        All: true,
        Filters: filters.NewArgs(
            filters.KeyValues{"label": "llmsafespace.dev/workspace=" + workspaceID},
        ),
    })
    var firstErr error
    for _, c := range containers {
        if err := b.client.ContainerPause(ctx, c.ID); err != nil {
            b.logger.Error("Failed to pause container", err, "containerID", c.ID)
            if firstErr == nil {
                firstErr = err
            }
        }
    }
    return firstErr
}

func (b *Backend) unpauseWorkspaceContainers(ctx context.Context, workspaceID string) error {
    containers, _ := b.client.ContainerList(ctx, types.ContainerListOptions{
        All: true,
        Filters: filters.NewArgs(
            filters.KeyValues{"label": "llmsafespace.dev/workspace=" + workspaceID},
        ),
    })
    var firstErr error
    for _, c := range containers {
        if err := b.client.ContainerUnpause(ctx, c.ID); err != nil {
            b.logger.Error("Failed to unpause container", err, "containerID", c.ID)
            if firstErr == nil {
                firstErr = err
            }
        }
    }
    return firstErr
}
```

### 6.9 Network setup

```go
func ensureNetwork(ctx context.Context, client *dockerclient.Client, name string) error {
    networks, err := client.NetworkList(ctx, types.NetworkListOptions{
        Filters: filters.NewArgs(filters.KeyValues{"name": name}),
    })
    if err != nil {
        return fmt.Errorf("listing networks: %w", err)
    }
    for _, n := range networks {
        if n.Name == name {
            return nil
        }
    }
    _, err = client.NetworkCreate(ctx, name, types.NetworkCreate{
        Driver:     "bridge",
        Scope:      "local",
        Attachable: true,
        Labels:     map[string]string{"llmsafespace.dev/managed": "true"},
    })
    return err
}
```

---

## 7. K8s Backend Wrapper

The K8s backend wraps existing `KubernetesClient` calls. It translates between
`SandboxState`/`SandboxParams` and `v1.Sandbox`/`v1.Workspace` CRD types.

```
api/internal/provider/kubernetes/
├── provider.go    // Constructor, wraps KubernetesClient
├── sandbox.go     // SandboxBackend: CRD operations + Secret reads
└── workspace.go   // WorkspaceBackend: CRD operations + Secret operations
```

Key translations:

| Backend method | K8s implementation |
|---------------|-------------------|
| `Provision` | Build `v1.Sandbox` CRD with `GenerateName: "sb-"`, call `Sandboxes(ns).Create()`. Read back assigned name from `created.Name`. Return `SandboxState{ID: created.Name, Phase: "Pending"}`. Controller handles pod creation and password generation asynchronously. |
| `Inspect` | `Sandboxes(ns).Get(id)`. Map `crd.Status.Phase` + `crd.Status.PodIP` → `SandboxState`. |
| `Destroy` | `Sandboxes(ns).Delete(id)`. K8s GC cleans up pod + secrets via owner references. |
| `Watch` | `Sandboxes(ns).Watch()`. Map CRD phase changes to `SandboxPhaseChange`. |
| `GetPassword` | `CoreV1().Secrets(ns).Get("sandbox-pw-"+id)`. Read `Data["password"]`. Controller creates this secret. |
| `List` | `Sandboxes(ns).List(opts)` with label selector. Map CRDs to `SandboxState` slice. |
| `ProvisionVolume` | Build `v1.Workspace` CRD, call `Workspaces(ns).Create()`. Controller creates PVC. |
| `GetPhase` | `Workspaces(ns).Get(id)`. Read `crd.Status.Phase`. |
| `SetPhase` | `Workspaces(ns).UpdateStatus(crd)`. Controller watches and reconciles. |
| `GetCredentials` | `CoreV1().Secrets(ns).Get("workspace-creds-"+id)`. Read `Data["provider-config"]`. |

**Important difference in `Provision`**: In K8s mode, `Provision` creates a CRD
with `GenerateName: "sb-"` and the K8s API server assigns a unique name.
`Provision` reads it back from `created.Name` and returns it in
`SandboxState.ID`. Phase is Pending (controller reconciles async).
Password is NOT generated by `Provision` — the controller does it later
via `ensurePasswordSecret`. `GetPassword` reads from the K8s Secret that
the controller creates.

In Docker mode, `Provision` generates the ID (`uuid.New().String()`) and
password upfront. Creates and starts the container synchronously.
Returns `SandboxState{ID: generatedID, Phase: "Running"}`.

The service layer does not generate the ID — it passes an empty ID to
`Provision` and uses the returned `SandboxState.ID` (see §8.5).

---

## 8. Service Layer Refactoring

### 8.1 SandboxService

Replace `KubernetesClient` with `SandboxBackend`:

```go
type Service struct {
    logger           pkginterfaces.LoggerInterface
    backend          interfaces.SandboxBackend       // was: k8sClient
    dbService        apiinterfaces.DatabaseService
    cacheService     apiinterfaces.CacheService
    metricsService   apiinterfaces.MetricsService
    workspaceService apiinterfaces.WorkspaceService
    config           *Config
}
```

The constructor changes:

```go
// Before:
func New(logger, k8sClient, dbService, ...) (*Service, error) {
    if k8sClient == nil { return nil, fmt.Errorf("kubernetes client cannot be nil") }
}

// After:
func New(logger, backend, dbService, ...) (*Service, error) {
    if backend == nil { return nil, fmt.Errorf("sandbox backend cannot be nil") }
}
```

`CreateSandbox` changes:

```go
// Before:
crd := buildCRDFromRequest(req, workspaceID, namespace)
created, _ := s.k8sClient.LlmsafespaceV1().Sandboxes(ns).Create(crd)
meta.ID = created.Name

// After:
image := s.resolveImage(req.Runtime)
params := interfaces.SandboxParams{
    ID:           "",  // backend generates (Docker) or K8s assigns (GenerateName)
    Runtime:      req.Runtime,
    Image:        image,
    WorkspaceRef: workspaceID,
    UserID:       req.UserID,
    Labels:       buildLabels(req),
    Timeout:      req.Timeout,
}
state, _ := s.backend.Provision(ctx, params)
meta.ID = state.ID
```

`resolveImage` is new — maps runtime name to image tag using config. In K8s
mode, the controller does this via `RuntimeEnvironment` CRD lookup. In Docker
mode, the config's `docker.runtimeImages` map provides the same function.

Rollback on DB failure:

```go
// Before:
s.k8sClient.LlmsafespaceV1().Sandboxes(ns).Delete(created.Name, metav1.DeleteOptions{})

// After:
s.backend.Destroy(ctx, sandboxID)
```

CRD conversion functions (`buildCRDFromRequest`, `convertCRDToAPI`, etc.) are
replaced with simpler `SandboxState` → `types.Sandbox` conversion. The
`types.Sandbox` DTO still embeds `metav1.TypeMeta` and `metav1.ObjectMeta`
(a pre-existing design issue — out of scope to fix).

### 8.2 WorkspaceService

Replace `KubernetesClient` with `WorkspaceBackend`:

```go
type Service struct {
    logger         pkginterfaces.LoggerInterface
    backend        interfaces.WorkspaceBackend
    dbService      apiinterfaces.DatabaseService
    cacheService   apiinterfaces.CacheService
    metricsService apiinterfaces.MetricsService
    config         *Config
}
```

`SuspendWorkspace`:

```go
// Before:
crd, _ := s.k8sClient.LlmsafespaceV1().Workspaces(ns).Get(id, opts)
if crd.Status.Phase != Active { return error }
crd.Status.Phase = Suspending
s.k8sClient.LlmsafespaceV1().Workspaces(ns).UpdateStatus(crd)

// After:
phase, _ := s.backend.GetPhase(ctx, workspaceID)
if phase != "Active" { return error }
s.backend.SetPhase(ctx, workspaceID, "Active", "Suspending")
```

Credential operations:

```go
// Before:
clientset.CoreV1().Secrets(ns).Get(ctx, secretName, opts)
clientset.CoreV1().Secrets(ns).Create(ctx, secret, opts)

// After:
s.backend.GetCredentials(ctx, workspaceID)
s.backend.SetCredentials(ctx, workspaceID, req.Config)
```

### 8.3 ProxyHandler

Replace `k8sClient` with `sandboxBackend` + `workspaceBackend`:

```go
type ProxyHandler struct {
    sandboxBackend   interfaces.SandboxBackend
    workspaceBackend  interfaces.WorkspaceBackend
    httpClient       *http.Client
    logger           pkginterfaces.LoggerInterface

    // All existing cache/session fields unchanged
    pwCache    map[string]string
    pwCacheMu  sync.RWMutex
    // ...

    activityUpdater ActivityUpdater
    stopCh          chan struct{}
    startOnce       sync.Once
    stopOnce        sync.Once
}
```

Sandbox lookup:

```go
// Before:
sandbox, _ := h.k8sClient.LlmsafespaceV1().Sandboxes(ns).Get(id, metav1.GetOptions{})
podIP := sandbox.Status.PodIP

// After:
state, _ := h.sandboxBackend.Inspect(ctx, sandboxID)
targetHost := state.Address  // container name in Docker, PodIP in K8s
```

Password:

```go
// Before:
secret, _ := h.k8sClient.Clientset().CoreV1().Secrets(ns).Get(ctx, "sandbox-pw-"+id, opts)
pw := string(secret.Data["password"])

// After:
pw, _ := h.sandboxBackend.GetPassword(ctx, sandboxID)
```

Phase watching:

```go
eventCh, _ := h.sandboxBackend.Watch(h.ctx)
go func() {
    for change := range eventCh {
        h.onPhaseChange(change.SandboxID, change.OldPhase, change.NewPhase)
    }
}()
```

The `onPhaseChange` callback logic is unchanged (invalidate caches, stop SSE).

Ownership middleware:

```go
// Before:
sb, _ := proxyHandler.GetSandboxCRD(sandboxID)
ownerID := sb.Labels["user-id"]

// After:
state, _ := proxyHandler.InspectSandbox(ctx, sandboxID)
ownerID := state.Labels["user-id"]
```

### 8.4 Initialization (app.go)

```go
func New(cfg *config.Config, log *logger.Logger) (*App, error) {
    var sandboxBackend interfaces.SandboxBackend
    var workspaceBackend interfaces.WorkspaceBackend

    switch cfg.Sandbox.Provider {
    case "docker":
        d, err := docker.New(cfg, log)
        if err != nil {
            return nil, fmt.Errorf("initializing Docker backend: %w", err)
        }
        sandboxBackend = d
        workspaceBackend = d

    case "kubernetes", "":
        k8sClient, err := kubernetes.New(&cfg.Kubernetes, log)
        if err != nil {
            return nil, fmt.Errorf("initializing K8s client: %w", err)
        }
        k := k8sprovider.New(k8sClient, log, cfg.Kubernetes.Namespace)
        sandboxBackend = k
        workspaceBackend = k

    default:
        return nil, fmt.Errorf("unknown sandbox provider: %s", cfg.Sandbox.Provider)
    }

    svc, _ := services.New(cfg, log, sandboxBackend, workspaceBackend)
    proxyHandler, _ := handlers.NewProxyHandler(sandboxBackend, workspaceBackend, log, nil)
    // ...
}
```

### 8.5 K8s Provision semantics — GenerateName vs deterministic ID

In K8s mode, `buildCRDFromRequest` (`sandbox_service.go:356-382`) creates a
CRD with `GenerateName: "sb-"`. The K8s API server assigns a unique name.
The service reads it back from `created.Name`.

The backend abstraction must handle this difference:

- **K8s backend `Provision()`**: Creates the CRD with `GenerateName: "sb-"`,
  reads back the assigned name from the created CRD, and returns it in
  `SandboxState.ID`. Phase is Pending (controller reconciles async).
  Password is NOT generated by `Provision` — the controller does it later
  via `ensurePasswordSecret`. `GetPassword` reads from the K8s Secret that
  the controller creates.

- **Docker backend `Provision()`**: Generates the ID (`uuid.New().String()`)
  and password upfront. Creates and starts the container synchronously.
  Returns `SandboxState{ID: generatedID, Phase: "Running"}`.

The service layer must not generate the ID itself — it passes an empty ID
to `Provision` and uses the returned `SandboxState.ID`:

```go
state, err := s.backend.Provision(ctx, interfaces.SandboxParams{
    ID:           "",  // backend generates or K8s assigns
    Runtime:      req.Runtime,
    Image:        image,
    WorkspaceRef: workspaceID,
    UserID:       req.UserID,
    Labels:       buildLabels(req),
    Timeout:      req.Timeout,
})
meta.ID = state.ID
```

### 8.6 types.Sandbox DTO — K8s apimachinery coupling

`types.Sandbox` (`types.go:34-40`) embeds `metav1.TypeMeta` and
`metav1.ObjectMeta`. `convertCRDToAPI` (`sandbox_service.go:385-391`) copies
these directly from the CRD. In Docker mode there is no CRD to copy from.

The `convertStateToAPI` function for Docker mode must construct
`metav1.ObjectMeta` manually:

```go
func convertStateToAPI(state *interfaces.SandboxState, meta *types.SandboxMetadata) *types.Sandbox {
    return &types.Sandbox{
        TypeMeta: metav1.TypeMeta{
            APIVersion: "llmsafespace.dev/v1",
            Kind:       "Sandbox",
        },
        ObjectMeta: metav1.ObjectMeta{
            Name:              state.ID,
            Namespace:         "default",
            Labels:            state.Labels,
            CreationTimestamp: metav1.NewTime(state.StartedAt),
        },
        Spec: types.SandboxSpec{
            Runtime:     meta.Runtime,
            SecurityLevel: "standard",
            Timeout:     meta.Timeout,
        },
        Status: types.SandboxStatus{
            Phase: state.Phase,
            PodIP: state.Address,
        },
    }
}
```

This is ugly — the DTO layer depends on K8s apimachinery. But it's a
pre-existing problem (out of scope). The manual construction works because
`metav1.ObjectMeta` is just a Go struct with JSON tags; it doesn't require a
K8s API server to populate.

The K8s backend's `convertCRDToAPI` continues to copy directly from the CRD.

### 8.7 ListSandboxes — live status enrichment

`ListSandboxes` (`sandbox_service.go:238-248`) enriches each DB row with live
CRD status (Phase, StartTime, CPU/Memory usage). One K8s API call per row.

For Docker mode, two approaches:

**Option A: Per-item Inspect** — call `sandboxBackend.Inspect(ctx, sb.ID)` for
each row. Simple but N+1 Docker API calls.

**Option B: Batch ContainerList** — single `docker.ContainerList` with label
filter, build a map of ID→phase, then enrich each row from the map. One Docker
API call for all sandboxes.

**Decision: Option B.** The Docker SDK's `ContainerList` returns all containers
matching a label filter in one call. Build a map, then iterate the DB results:

```go
// After: Docker mode
states, _ := s.backend.List(ctx, map[string]string{"llmsafespace.dev/managed": "true"})
stateMap := make(map[string]*interfaces.SandboxState, len(states))
for i := range states {
    stateMap[states[i].ID] = &states[i]
}
for _, sb := range sandboxes {
    item := types.SandboxListItem{ID: sb.ID, ...}
    if state, ok := stateMap[sb.ID]; ok {
        item.Phase = state.Phase
    }
    items = append(items, item)
}
```

This uses the `List` method on `SandboxBackend` (defined in §5). The K8s
backend implements `List` via `Sandboxes(ns).List(opts)` with label selector.

### 8.8 TerminateSandbox — namespace handling

`TerminateSandbox` (`sandbox_service.go:301`) deletes the CRD using
`sandbox.Namespace` from the `types.Sandbox.ObjectMeta.Namespace` field. In
Docker mode, `ObjectMeta.Namespace` is always `"default"` (see §8.6). The
backend's `Destroy` method ignores namespace (Docker has none). No code change
needed in the service — it passes the sandbox ID, and the backend handles the
rest.

### 8.9 ActivityTracker — K8s coupling

`ActivityTracker` (`activity.go:109-119`) writes `LastActivityAt` to the
Workspace CRD status using `retry.RetryOnConflict`. This is K8s-specific.

Replace the K8s client dependency with a simple interface:

```go
type ActivityRecorder interface {
    RecordActivity(ctx context.Context, workspaceID string, t time.Time) error
    Start() error
    Stop() error
}
```

Two implementations:
- **K8s**: wraps existing `ActivityTracker` logic (CRD status update with
  conflict retry). Uses `WorkspaceBackend.SetPhase`-style CRD update.
- **Docker**: single SQL `UPDATE workspaces SET last_activity_at = $1 WHERE id = $2`.

The `ProxyHandler` depends on `ActivityRecorder` instead of constructing
`ActivityTracker` directly.

### 8.10 SSETracker — PodIP resolution

`SSETracker` (`session_tracker.go`) calls `h.getPodIPForSSE` which does
`h.k8sClient.LlmsafespaceV1().Sandboxes(ns).Get(id)` to resolve the container
address.

After refactoring, `getPodIPForSSE` calls `h.sandboxBackend.Inspect(ctx, id)`
and returns `state.Address`. This is already covered by the proxy handler
refactoring in §8.3 — `getPodIPForSSE` is a method on `ProxyHandler` that
uses the same backend.

### 8.11 CreateSandbox — credential fetching

In K8s mode, the controller's init container fetches credentials from K8s
Secrets and writes them to `/sandbox-cfg/credentials`. The sandbox service
does NOT fetch credentials.

In Docker mode, there is no controller. The sandbox service must fetch
credentials from the workspace backend and pass them to the sandbox backend:

```go
// In CreateSandbox, after determining workspaceRef:
var credentials []byte
if workspaceID != "" {
    if creds, err := s.workspaceBackend.GetCredentials(ctx, workspaceID); err == nil {
        credentials = creds
    }
}

state, err := s.backend.Provision(ctx, interfaces.SandboxParams{
    // ...
    Credentials: credentials,
})
```

This works in both modes: in K8s mode, the K8s backend's `Provision` creates
a CRD (credentials are irrelevant — the controller's init container fetches
them independently). In Docker mode, the Docker backend injects credentials
into the container via the inline entrypoint (§6.2).

---

## 9. Configuration

### config.yaml additions

```yaml
sandbox:
  provider: "kubernetes"    # "kubernetes" (default) or "docker"

docker:
  host: "unix:///var/run/docker.sock"
  network: "llmsafespace-sandbox"
  runtimeImages:
    base: "llmsafespace/runtime-base:dev"
    python: "llmsafespace/runtime-python:dev"
    nodejs: "llmsafespace/runtime-nodejs:dev"
    go: "llmsafespace/runtime-go:dev"
```

### Environment variable overrides

| Env var | Default | Notes |
|---------|---------|-------|
| `LLMSAFESPACE_SANDBOX_PROVIDER` | `kubernetes` | Toggle between modes |
| `LLMSAFESPACE_DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker API endpoint |
| `LLMSAFESPACE_DOCKER_NETWORK` | `llmsafespace-sandbox` | Bridge network name |
| `LLMSAFESPACE_DOCKER_RUNTIME_IMAGE_*` | (see config) | Per-runtime image tag |

---

## 10. Database Schema

### New tables

```sql
-- 000003_docker_mode.up.sql

CREATE TABLE IF NOT EXISTS sandbox_passwords (
    sandbox_id TEXT PRIMARY KEY,
    password   TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS workspace_credentials (
    workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    config       BYTEA NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Phase storage for Docker mode (K8s stores phase in CRD status).
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS phase TEXT DEFAULT 'Active';
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS volume_name TEXT DEFAULT '';
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS last_activity_at TIMESTAMPTZ;
```

```sql
-- 000003_docker_mode.down.sql

DROP TABLE IF EXISTS workspace_credentials;
DROP TABLE IF EXISTS sandbox_passwords;
ALTER TABLE workspaces DROP COLUMN IF EXISTS last_activity_at;
ALTER TABLE workspaces DROP COLUMN IF EXISTS volume_name;
ALTER TABLE workspaces DROP COLUMN IF EXISTS phase;
```

In K8s mode, these columns remain empty. The tables are unused but harmless.

### DatabaseService additions

```go
GetSandboxPassword(ctx context.Context, sandboxID string) (string, error)
SetSandboxPassword(ctx context.Context, sandboxID, password string) error
DeleteSandboxPassword(ctx context.Context, sandboxID string) error

GetWorkspaceCredentials(ctx context.Context, workspaceID string) ([]byte, error)
SetWorkspaceCredentials(ctx context.Context, workspaceID string, data []byte) error
DeleteWorkspaceCredentials(ctx context.Context, workspaceID string) error

GetWorkspacePhase(ctx context.Context, workspaceID string) (string, error)
SetWorkspacePhase(ctx context.Context, workspaceID, phase string) error
UpdateWorkspaceActivity(ctx context.Context, workspaceID string, t time.Time) error
```

---

## 11. Migration Strategy

Migrations are currently an external process (golang-migrate CLI run via Helm
hook). The API binary has no migration logic (A5). Two options:

**Option A: Embed migrations in the API binary.**

Add `github.com/golang-migrate/migrate/v4` as a Go dependency with the
PostgreSQL driver. Embed SQL files via `embed.FS`. Run `migrate.Up()` in
`main.go` before `app.New()` when `cfg.Sandbox.Provider == "docker"`. This
gives the `docker compose up` user a zero-extra-step experience.

New dependency: `github.com/golang-migrate/migrate/v4` (Apache 2.0, mature).

**Option B: docker-compose init service.**

Add a `migrate` service to docker-compose that runs the migration image and
exits. The API service uses `depends_on` with `condition: service_completed_successfully`.

This avoids a new Go dependency but requires the user to have the `migrate/migrate`
image available (or build it).

**Recommendation: Option A.** It eliminates the external tool dependency and
matches the `docker compose up` → works UX. The golang-migrate library is a
single import that runs before the HTTP server starts.

---

## 12. docker-compose.yaml

```yaml
services:
  api:
    build:
      context: .
      dockerfile: api/Dockerfile
    ports:
      - "${LLMSAFESPACE_PORT:-8080}:8080"
    environment:
      LLMSAFESPACE_SANDBOX_PROVIDER: docker
      LLMSAFESPACE_DOCKER_HOST: unix:///var/run/docker.sock
      LLMSAFESPACE_SERVER_HOST: "0.0.0.0"
      LLMSAFESPACE_SERVER_PORT: "8080"
      LLMSAFESPACE_DATABASE_HOST: postgres
      LLMSAFESPACE_DATABASE_PORT: "5432"
      LLMSAFESPACE_DATABASE_USER: llmsafespace
      LLMSAFESPACE_DATABASE_PASSWORD: changeme
      LLMSAFESPACE_DATABASE_DATABASE: llmsafespace
      LLMSAFESPACE_REDIS_HOST: redis
      LLMSAFESPACE_REDIS_PORT: "6379"
      LLMSAFESPACE_AUTH_JWTSECRET: ${JWT_SECRET:-dev-secret-change-me}
      LLMSAFESPACE_LOGGING_DEVELOPMENT: "true"
      LLMSAFESPACE_LOGGING_LEVEL: debug
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    networks:
      - default
      - sandbox

  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_DB: llmsafespace
      POSTGRES_USER: llmsafespace
      POSTGRES_PASSWORD: changeme
    volumes:
      - postgres-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U llmsafespace"]
      interval: 5s
      timeout: 5s
      retries: 5
    ports:
      - "${POSTGRES_PORT:-5432}:5432"

  redis:
    image: redis:7-alpine
    command: redis-server --save "" --appendonly no
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 5s
      retries: 5
    ports:
      - "${REDIS_PORT:-6379}:6379"

networks:
  sandbox:
    name: llmsafespace-sandbox
    driver: bridge

volumes:
  postgres-data:
```

---

## 13. Security

### Docker socket

Mounting `/var/run/docker.sock` grants unrestricted host access. For cautious
users, a Docker socket proxy (e.g., Tecnativa/docker-socket-proxy) can
whitelist specific API calls.

### Network isolation

Sandbox containers are on `llmsafespace-sandbox` bridge network. The API
bridges both default (Postgres/Redis) and sandbox networks. Sandboxes cannot
reach Postgres or Redis.

### Sandbox container hardening

Same runtime images as K8s mode. Container security context:
- User 1000:1000 (matches runtime-base `useradd -u 1000`)
- tmpfs for /tmp and /sandbox-cfg (matches K8s emptyDir)
- No privileged mode
- `ReadonlyRootfs: true` in HostConfig (matches K8s `readOnlyRootFilesystem`)

### Credentials

Stored in PostgreSQL without application-level encryption. This matches the
K8s baseline: K8s Secrets are base64-encoded, not encrypted at rest, unless
etcd encryption is configured. For at-rest encryption, use PostgreSQL TDE or
filesystem-level encryption (LUKS).

---

## 14. Feature Parity

| Feature | K8s | Docker | Notes |
|---------|-----|--------|-------|
| Create/terminate sandbox | CRD+pod | Container | Full |
| Get sandbox status | CRD status | Container inspect | Full |
| List sandboxes | DB+CRD | DB+inspect | Full |
| Proxy to sandbox | PodIP:4096 | ContainerName:4096 | Full |
| SSE streaming | CRD watch | Docker events | Full |
| Patch filtering | Yes | Yes | Unchanged — proxy-level |
| Create/delete workspace | CRD+PVC | Volume | Full |
| Suspend/resume | CRD status → controller | Container pause | Full |
| Credentials | K8s Secret | PostgreSQL | Full |
| Auth, rate limiting | Yes | Yes | Unchanged |
| MCP server | Yes | Yes | Unchanged |
| SandboxProfile | CRD | Default only | Not supported |
| RuntimeEnvironment | CRD | Config | Static images |
| Network policies | K8s | Bridge only | Limited |
| Horizontal scaling | Yes | No | Single instance |
| Workspace init scripts | Init container | Not supported | Deferred |

---

## 15. Implementation Stories

### Story 1: Backend Interfaces + Types

**New:** `pkg/interfaces/backend.go`
**Modified:** `api/internal/config/config.go`, `api/config/config.yaml`

Define `SandboxBackend`, `WorkspaceBackend`, and associated types. Add
`Sandbox.Provider` and `Docker.*` config sections.

**Acceptance:** Interfaces compile with zero K8s imports. Config loads.

---

### Story 2: Database Schema + Service

**New:** `api/migrations/000003_docker_mode.up.sql`, `000003_docker_mode.down.sql`
**Modified:** `api/internal/services/database/database.go`, `api/internal/interfaces/interfaces.go`

Add `sandbox_passwords` and `workspace_credentials` tables. Add `phase`,
`volume_name`, `last_activity_at` columns to `workspaces`. Add new
DatabaseService methods.

**Acceptance:** Migration applies and rolls back. New methods tested.

---

### Story 3: K8s Backend Wrapper

**New:** `api/internal/provider/kubernetes/provider.go`, `sandbox.go`, `workspace.go`

Wrap existing `KubernetesClient` calls behind `SandboxBackend` and
`WorkspaceBackend`. Pure adapter — no new K8s logic.

**Acceptance:** All interface methods implemented. Existing test suite passes.

---

### Story 4: Docker Backend

**New:** `api/internal/provider/docker/provider.go`, `sandbox.go`, `workspace.go`

Docker Engine API implementation. Container lifecycle, event watcher, volume
management, password/credential storage via PostgreSQL, network setup.

**Dependencies:** Story 1, Story 2

**Acceptance:** All interface methods implemented. Container lifecycle works.
Event watcher reconnects on failure.

---

### Story 5: Service + Proxy Refactoring

**Modified:** `sandbox_service.go`, `workspace_service.go`, `services.go`,
`proxy.go`, `router.go`, `app.go`

Replace `KubernetesClient` with backend interfaces throughout the service and
handler layers.

**Dependencies:** Story 3 (K8s mode must keep working)

**Acceptance:** Zero direct K8s client calls in service or handler code.
Full K8s test suite passes.

---

### Story 6: Docker Compose + E2E

**New:** `docker-compose.yaml`
**Modified:** `Makefile`

**Dependencies:** Story 4, Story 5

**Acceptance:** `docker compose up` starts a working system. End-to-end:
register → create workspace → create sandbox → proxy message → terminate.
Workspace data survives `docker compose down && docker compose up`.

---

## 16. Dependencies

| Package | Purpose | License |
|---------|---------|---------|
| `github.com/docker/docker` | Docker Engine SDK | Apache 2.0 |
| `github.com/golang-migrate/migrate/v4` | Embedded DB migrations | Apache 2.0 |

---

## 17. Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| Docker socket exposure | Host compromise | Document threat model; socket proxy for cautious users |
| Docker API version drift | Container operations fail | SDK negotiates version; document minimum Docker 24.0+ |
| Entrypoint inline init script | If runtime images change entrypoints, inline script breaks | Pin runtime image versions; test init script in CI |
| Event stream drops events | Stale phase during reconnect | Snapshot baseline on reconnect; 2s backoff |
| Create+Start not atomic | Container created but not started (orphan) | Cleanup on Start failure: remove + delete password |
| `ReadonlyRootfs` compatibility | Some opencode operations may need writable paths | tmpfs mounts for /tmp and /sandbox-cfg match K8s emptyDir; /workspace is a named volume (writable) |

---

## 18. Appendix A: v3 → v4 Changes

Every change from v3 to v4 addresses a validated gap where the design was
incomplete or incorrect relative to the actual codebase.

| Change | Why | Evidence |
|--------|-----|----------|
| Added assumptions A10, A11, A12 | Three gaps discovered during validation | `types.go:34-36`, `sandbox_service.go:238-248,301` |
| Added `List` to `SandboxBackend` interface (§5) | `ListSandboxes` does per-item CRD enrichment. Docker mode needs batch alternative to avoid N+1 Docker API calls. | `sandbox_service.go:238-248` |
| Added §8.5 — K8s GenerateName vs Docker deterministic ID | K8s uses `GenerateName: "sb-"` on CRDs. Design had the service generating IDs, but K8s backend must use `GenerateName`. | `sandbox_service.go:357-361` |
| Added §8.6 — types.Sandbox DTO coupling | `convertCRDToAPI` copies `metav1.ObjectMeta` from CRD. Docker mode has no CRD. Must document how to construct these manually. | `types.go:34-36`, `sandbox_service.go:385-391` |
| Added §8.7 — ListSandboxes batch enrichment | Design was silent on how per-item status enrichment works in Docker mode. | `sandbox_service.go:238-248` |
| Added §8.8 — TerminateSandbox namespace | `TerminateSandbox` uses `sandbox.Namespace` from CRD ObjectMeta. Docker mode has no namespace. | `sandbox_service.go:301` |
| Added §8.9 — ActivityTracker replacement | `ActivityTracker` uses K8s `retry.RetryOnConflict` + CRD status update. Design mentioned `ActivityUpdater` interface but never defined it. | `activity.go:109-119` |
| Added §8.10 — SSETracker PodIP resolution | `getPodIPForSSE` uses K8s client. Design didn't mention SSE tracker. | `proxy.go:670-678` |
| Added §8.11 — Credential fetching by sandbox service | In K8s, the controller's init container fetches credentials. In Docker, the sandbox service must fetch them from the workspace backend. Design had `SandboxParams.Credentials` but no code showing who fetches. | `controller.go:658-730` vs `sandbox_service.go:83-171` |
| `SandboxParams.ID` changed to empty string | Service should not generate the ID — K8s backend uses `GenerateName`, Docker backend generates UUID. The service uses the returned `SandboxState.ID`. | `sandbox_service.go:357-361` |

---

## 19. Appendix B: v4 → v5 Fixes

Targeted corrections to code-level errors found during final review.

| Change | Why | Evidence |
|--------|-----|----------|
| Fixed §6.1 provisioning step numbering (3,3,4 → 3,4,5) | Typo: duplicate step 3 | Visual inspection |
| Added `containerListStateToPhase` in §6.4 | Referenced in §6.5 (`List`) and §6.6 (`snapshotPhases`) but never defined. `ContainerList` returns `State` as a string, not a `ContainerState` struct. | Docker SDK `types.Container.State` is `string` |
| Added `unpauseWorkspaceContainers` in §6.8 | Referenced in `SetPhase` for "Resuming" case but implementation was missing | `SetPhase` calls `b.unpauseWorkspaceContainers(ctx, workspaceID)` |
| Fixed `convertStateToAPI` in §8.6 — removed `Workspace: state.WorkspaceRef` | `types.SandboxSpec` has no `Workspace` field. CRD has `Spec.WorkspaceRef` but the API DTO does not. Added `SecurityLevel: "standard"` and `Timeout: meta.Timeout` instead. | `types.go:43-70` — no Workspace field on SandboxSpec |
| Removed redundant "This requires adding List" paragraph in §8.7 | `List` was already added to `SandboxBackend` interface in §5. The paragraph duplicated this information. | §5 already defines `List` method |
