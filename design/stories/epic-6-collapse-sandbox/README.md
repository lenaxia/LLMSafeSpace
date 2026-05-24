# Epic 6: Collapse Sandbox into Workspace

**Status:** Planning (v2 — revised after critical review)
**Created:** 2026-05-24
**Priority:** High

## Rationale

Sandbox CRD is de facto 1:1 with Workspace. The indirection creates bugs (workspace-has-no-sandbox, create-sandbox dance), adds ~3000 lines of maintenance, and confuses the user model. Collapsing it eliminates an entire CRD, controller, API service, and a class of integration bugs.

## Validated Assumptions

| # | Assumption | Evidence |
|---|-----------|----------|
| A1 | No code creates >1 sandbox per workspace | Frontend `Sidebar.tsx:28` creates exactly one. MCP `client.go:305` takes `sandboxes[0]`. No loop creates multiple. |
| A2 | Session index DB uses workspace_id, not sandbox_id | `migrations/000003_session_index.up.sql` PRIMARY KEY `(workspace_id, session_id)`. No sandbox_id column. |
| A3 | Proxy maps keyed by sandboxID | `proxy.go:48,51,54,57` — `pwCache`, `wsConfig`, `activeSess`, `connCount`. Must rekey to workspaceID. |
| A4 | SandboxProfile CRD is dead code at runtime | `sandbox_webhook.go:49-63` validates existence only. `buildSandboxPodWithContext` never reads it. |
| A5 | MCP client calls `/sandboxes` routes | `client.go:175,180,195,218,227,281`. Must rewrite to `/workspaces/`. |
| A6 | `local/test.sh` calls `/sandboxes` routes | Lines 279,301,329,336,350,602,624,717,785. |
| A7 | RBAC includes sandbox-specific rules | `charts/.../rbac.yaml:10-15,111-119`. |
| A8 | Workspace controller manages Sandbox CRD lifecycle | `workspace/controller.go:333,393-401,445,473,504-547`. |
| A9 | RuntimeEnvironment CRD used by sandbox controller and API sandbox service | `runtime_resolver.go`, `sandbox_service.go:525-545`. |
| A10 | Password secret named `sandbox-pw-{name}` | `sandbox/controller.go:703`. |
| A11 | Proxy ownership reads `user-id` label on Sandbox CRD | `router.go:562`. |
| A12 | DB has `sandboxes` and `sandbox_labels` tables with FK constraints | `migrations/001_initial_schema.sql:22-38`. `execution_history`, `file_operations`, `package_installations` all reference `sandbox_id`. |
| A13 | `SandboxMetadata` type used in DB layer, service layer, mocks | `types.go:312-313`, `database.go:271,340,502`, `mocks/database.go:85-109`. |
| A14 | CORS is broken — `DefaultSecurityConfig()` has `AllowedOrigins: []string{}` | `security.go:59`. Blocks all cross-origin POST from `safespace.thekao.cloud`. |
| A15 | Pod name is deterministic: `{name}-{uid[:8]}` | `sandbox/controller.go:731`. Provides idempotency on re-create. |
| A16 | `buildPodSecurityContext` reads `sandbox.Spec.SecurityContext` for RunAsUser/RunAsGroup | `sandbox/controller.go:900-916`. These types must be preserved. |
| A17 | `SandboxWatcher` is a separate file (`crd_watcher.go`, 239 lines) with 25+ tests | `crd_watcher.go:28`, `crd_watcher_test.go`. Must be rewritten as `WorkspaceWatcher`. |
| A18 | `SecurityConfig` CORS middleware is separate from `SecurityMiddleware` | `security.go:59` (empty origins), `cors.go:39-41` (separate middleware with `*` default). Two middleware layers — CORS block comes from `SecurityMiddleware`. |

## Design Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Add `Creating` phase to Workspace | Pod startup takes seconds. Proxy needs to distinguish "pod starting" from "pod running". Without Creating, Active means nothing useful until PodIP is set. Matches current sandbox lifecycle. |
| D2 | No `spec.Suspend` field — use phase transitions only | Controllers must only update status, not spec. API already has `SuspendWorkspace` (sets phase to Suspending) and `ResumeWorkspace` (sets phase to Resuming). Timeout enforcement writes `status.Phase = Suspending`, not spec. |
| D3 | Timeout suspends, not terminates | User data is preserved. This is a behavioral change from current sandbox timeout (which terminates). Rationale: sandboxes had no persistent storage, so terminating was lossless. Workspaces have PVCs, so terminating loses data. |
| D4 | Pod name: `{workspaceName}-{uid[:8]}` | Same deterministic pattern as sandbox. Provides idempotency — if controller crashes after Create but before status update, next reconcile gets AlreadyExists. |
| D5 | Requeue interval: 30s in Active | Same as current sandbox controller. Single requeue for all periodic checks (idle timer, timeout, credential hash). |
| D6 | Drop `FilesystemConfig` and `StorageConfig` types | `FilesystemConfig` is hardcoded in pod spec (ReadOnlyRoot=true, writablePaths=/tmp,/workspace). `StorageConfig` is replaced by `WorkspaceStorageConfig`. No need for CRD-level config of these. |
| D7 | Keep `SecurityContext` type (RunAsUser, RunAsGroup, SeccompProfile) on WorkspaceSpec | `buildPodSecurityContext` reads these. Defaults to UID/GID 1000. |

## User Model (After)

```
Create workspace  -> PVC + pod created, phase: Pending → Creating → Active
Use it            -> Sessions, chat, proxy (all by workspace ID)
Suspend           -> Pod deleted, PVC kept (~3s), phase: Suspending → Suspended
Resume            -> New pod, same data (~3s), phase: Resuming → Creating → Active
Timeout           -> Pod deleted, PVC kept, phase: Suspending → Suspended
Delete            -> Everything gone, phase: Terminating → Terminated
```

## User Interaction Scenarios

All user-facing flows after this epic. Every scenario must work end-to-end.

### Browser (Human) Flows

| # | Scenario | Steps | Key Endpoints |
|---|----------|-------|---------------|
| H1 | First-time user | Register → login → create workspace → auto-session → chat | `POST /auth/register`, `POST /workspaces`, `POST /workspaces/:id/sessions/new` |
| H2 | Return to active workspace | Login → select workspace → select session → view history → send message | `GET /workspaces`, `GET /workspaces/:id/sessions`, `GET /workspaces/:id/sessions/:sid/message`, `POST .../message` |
| H3 | Return to suspended workspace | Login → select workspace → see "Suspended" → click Activate → wait → chat | `POST /workspaces/:id/activate`, poll `GET /workspaces/:id/status` |
| H4 | Create new session | Click "+" → EnsureSession → navigate to new session | `POST /workspaces/:id/sessions/new` |
| H5 | Workspace in Creating/Resuming | See spinner → poll status → auto-enable when Active | `GET /workspaces/:id/status` |
| H6 | Restart workspace | Settings → restart → pod recreated → reconnect | `POST /workspaces/:id/restart` |
| H7 | Delete workspace | Settings → delete → removed from list | `DELETE /workspaces/:id` |
| H8 | Set credentials | Settings → add LLM API key → saved to K8s Secret | `PUT /workspaces/:id/credentials` |
| H9 | SSE real-time events | While chatting, receive session status updates | `GET /workspaces/:id/events` (EventSource) |
| H10 | Tab close during stream | sendBeacon fires abort | `POST /workspaces/:id/sessions/:sid/abort` |

### Programmatic (MCP Client) Flows

| # | Scenario | Steps | Key Endpoints |
|---|----------|-------|---------------|
| P1 | Create + use workspace | `workspace_create` → `workspace_activate` → `session_create` → `session_message` | `POST /workspaces`, `POST /workspaces/:id/activate`, `POST /workspaces/:id/sessions`, `POST .../prompt` + SSE |
| P2 | Resume suspended workspace | `workspace_activate` → `session_create` → `session_message` | `POST /workspaces/:id/activate`, `POST /workspaces/:id/sessions` |
| P3 | Read history | `session_history` | `GET /workspaces/:id/sessions/:sid/message` |
| P4 | Stop workspace | `workspace_stop` | `POST /workspaces/:id/suspend` |
| P5 | Set credentials programmatically | REST `PUT /workspaces/:id/credentials` with API key auth | Direct REST call |

### REST API (SDK/Script) Flows

| # | Scenario | Key Endpoints |
|---|----------|---------------|
| R1 | Full lifecycle | `POST /workspaces` → poll status → `POST /workspaces/:id/sessions` → `POST .../message` → `DELETE /workspaces/:id` |
| R2 | Credential rotation | `PUT /workspaces/:id/credentials` → pod auto-restarts (credential hash change) |
| R3 | Workspace restart | `POST /workspaces/:id/restart` → poll status until Active |
| R4 | List workspaces | `GET /workspaces?limit=20&offset=0` |
| R5 | Session management | `GET /workspaces/:id/sessions`, `PUT /workspaces/:id/sessions/:sid/title` |

### Controller (Internal) Flows

| # | Scenario | Trigger | Behavior |
|---|----------|---------|----------|
| C1 | Workspace created | CRD created | PVC → password secret → pod → Active |
| C2 | Pod crash | Pod disappears | Transient recovery up to MaxRetries → recreate pod |
| C3 | Idle timeout | No activity for N seconds | Phase → Suspending → delete pod → Suspended |
| C4 | TTL expiry | Suspended for N seconds | Phase → Terminating → delete PVC → Terminated |
| C5 | Credential change | `workspace-creds-*` secret updated | Bump restart generation → pod recreated |
| C6 | Restart requested | `spec.RestartGeneration` bumped | Delete pod → Creating → Active |

## Story List

| Story | Title | Scope |
|-------|-------|-------|
| US-6.0 | Fix CORS | Add `safespace.thekao.cloud` to allowed origins |
| US-6.1 | Rewrite Workspace CRD Types | Add Creating phase; pod-level fields; SecurityContext |
| US-6.2 | Workspace Reconciler Owns Pod | Absorb sandbox reconciler pod lifecycle |
| US-6.3 | API Workspace Service Changes | RestartWorkspace; rewrite EnsureSession; remove sandbox methods |
| US-6.4 | Remove Sandbox CRD, Controller, Service | Delete everything sandbox-specific + DB migration |
| US-6.5 | Proxy Rekeyed to Workspace ID | All routes and lookups use workspace ID + WorkspaceWatcher |
| US-6.6 | Frontend Simplification | Remove all sandbox awareness from React app |
| US-6.7 | MCP Client + Scripts Update | Rewrite MCP client paths, test scripts |
| US-6.8 | Helm Chart Cleanup | RBAC, CRDs, webhooks |

## Dependency Graph

```
US-6.0 (CORS fix) — independent, ship now

US-6.1 (types)  -->  US-6.2 (controller)  -->  US-6.3 (API service)  -->  US-6.4 (delete sandbox)
                                                                             |
                                                                             +--> US-6.5 (proxy)
                                                                             |         |
                                                                             |         +--> US-6.6 (frontend)
                                                                             |         +--> US-6.7 (MCP/scripts)
                                                                             |
                                                                             +--> US-6.8 (helm)
```

US-6.5, US-6.6, US-6.7, US-6.8 can run in parallel after US-6.4.

## Test Strategy

| Story | Test Type | Framework | Minimum Coverage |
|-------|-----------|-----------|-----------------|
| US-6.1 | Unit | Go testing | Webhook validation for every new field |
| US-6.2 | Unit + envtest | controller-runtime envtest | Every phase transition happy path + 3 error paths per phase |
| US-6.3 | Unit + integration | Go testing + mocks | Every new/modified service method happy + error path; EnsureSession with suspended/active/creating workspace |
| US-6.4 | N/A (deletion) | Build + grep | `make build` passes; zero sandbox references in Go code |
| US-6.5 | Unit + integration | Go testing | Every proxy route with mock K8s client; workspace CRD with PodIP set/not set |
| US-6.6 | Build + unit | vitest + npm build | `npm run build` passes; `npm run test` passes; zero sandbox references in frontend |
| US-6.7 | Unit | Go testing | MCP client path construction for every method; integration test with mock server |
| US-6.8 | Unit | helm lint + helm template | Zero sandbox references in rendered output |
| All | E2E | Manual on cluster + `local/test.sh` | Create workspace → pod starts → session → message → suspend → resume → delete |

## Estimated Impact

- ~4500 lines removed
- ~2000 lines modified/added
- Net ~2500 line reduction
- 2 fewer CRDs (Sandbox, SandboxProfile), 1 fewer controller, 1 fewer API service

## Deployment Strategy

**Hard cut** — this project is not live. No zero-downtime migration needed.

Pre-deploy steps:
```bash
kubectl delete sandboxes --all -A
kubectl delete sandboxprofiles --all -A
kubectl delete crd sandboxes.llmsafespace.dev sandboxprofiles.llmsafespace.dev
```

Then deploy new images + Helm chart. All existing workspace PVCs are preserved. Users will need to create new sessions (existing session state was in sandbox pods, which are deleted).

Data loss accepted:
- `execution_history`, `file_operations`, `package_installations` DB tables dropped (sandbox-keyed, no migration value)
- Sandbox pod state lost (expected — PVC data preserved via workspace)

## Package Dependency Map (Post-Collapse)

Import graph after epic completion. No circular dependencies.

```
pkg/apis/llmsafespace/v1/          ← LEAF (no internal imports)
  workspace_types.go                  WorkspacePhase, WorkspaceSpec, WorkspaceCondition, PodSecurityContext
  runtimeenvironment_types.go         RuntimeEnvironment (unchanged)

pkg/interfaces/kubernetes.go       ← imports v1
  LLMSafespaceV1Interface:            Workspaces(), RuntimeEnvironments()
  WorkspaceInterface:                 CRUD + Watch
  RuntimeEnvironmentInterface:        Get, List

pkg/types/types.go                 ← LEAF (no internal imports)
  API transfer objects only

controller/internal/common/        ← imports v1, controller-runtime
  utils.go:                           AddFinalizer, RemoveFinalizer, SetCondition, IsPodReady (GENERIC — keep)
  leader_election.go:                 (GENERIC — keep)
  metrics.go:                         (GENERIC — keep)
  constants.go:                       REWRITE: remove all Sandbox*, keep generic labels/annotations
  condition_adapter.go:               DELETE (sandbox-specific; workspace uses WorkspaceCondition directly)
  network_policy_manager.go:          DELETE (sandbox-specific; workspace reconciler builds NetworkPolicy inline)
  pod_manager.go:                     DELETE (sandbox-specific; workspace reconciler builds pod inline per US-6.2)
  service_manager.go:                 DELETE (sandbox-specific; workspace pods don't need a Service — proxy uses PodIP)

controller/internal/workspace/     ← imports common, v1, controller-runtime
  controller.go:                      Reconciler (absorbs pod lifecycle from sandbox)
  runtime_resolver.go:                MOVED from sandbox/ (imports only v1 + controller-runtime/client)
  constants.go:                       NEW: WorkspaceFinalizer, MaxTransientFailures, TransientFailureResetWindow

api/internal/handlers/             ← imports v1, pkg/interfaces, api/internal/interfaces
  proxy.go:                           ProxyHandler (workspace-keyed)
  crd_watcher.go:                     WorkspaceWatcher (watches v1.Workspace)
  activity.go:                        ActivityTracker (workspace-keyed)
  sse_tracker.go:                     SSETracker (workspace-keyed)

api/internal/services/workspace/   ← imports v1, pkg/interfaces, api/internal/interfaces
  workspace_service.go:               All workspace + session operations

pkg/mcp/                           ← imports only net/http (talks to API via REST)
  client.go:                          HTTPClient (workspace paths only)
  server.go:                          MCP tool definitions
```

### Extraction Decisions

| Item | Decision | Rationale |
|------|----------|-----------|
| `runtime_resolver.go` | Move to `controller/internal/workspace/` | Only imports `v1` + `client`. No shared consumers after sandbox deletion. |
| `MaxTransientFailures`, `TransientFailureResetWindow` | Move to `controller/internal/workspace/constants.go` | Only used by workspace reconciler after collapse. |
| `common/utils.go` (AddFinalizer, SetCondition, IsPodReady) | Keep in `common/` | Generic utilities used by workspace reconciler and potentially future controllers. |
| `common/condition_adapter.go` | Delete | Converts `v1.SandboxCondition` ↔ `metav1.Condition`. Workspace uses `WorkspaceCondition` with its own helpers. |
| `common/pod_manager.go` | Delete | Builds sandbox pods. Workspace reconciler builds pods inline (different spec). |
| `common/network_policy_manager.go` | Delete | Takes `*v1.Sandbox`. Workspace reconciler creates NetworkPolicy inline from `workspace.Spec.NetworkAccess`. |
| `common/service_manager.go` | Delete | Creates K8s Service for sandbox. Workspace pods are accessed via PodIP directly (no Service needed). |
| `common/metrics.go` | Keep in `common/` | Generic Prometheus metrics registration. |
| `common/leader_election.go` | Keep in `common/` | Generic leader election setup. |

### Circular Import Prevention

No circular imports exist because:
1. `pkg/apis/llmsafespace/v1/` is a leaf — imports only `k8s.io/apimachinery`
2. `pkg/interfaces/` imports `v1` but nothing else internal
3. `controller/internal/workspace/` imports `common` and `v1` — never the reverse
4. `api/internal/handlers/` imports `v1` and `pkg/interfaces` — never controller packages
5. `pkg/mcp/` imports nothing internal — communicates via HTTP only
