# LLMSafeSpace — LLM Implementation Guide

> **Repository:** `github.com/lenaxia/llmsafespace`

**Version:** 1.0
**Last Updated:** 2026-05-21
**Project Status:** Active Development

---

## Table of Contents

1. [Project Overview](#project-overview)
2. [Critical Guidelines & Hard Rules](#critical-guidelines--hard-rules)
3. [Repository Structure](#repository-structure)
4. [Architecture Overview](#architecture-overview)
5. [Technology Stack](#technology-stack)
6. [Worklog Requirements](#worklog-requirements)
7. [Development Workflow](#development-workflow)
8. [Multi-Agent Workflow](#multi-agent-workflow)
9. [Common Commands](#common-commands)
10. [Branch Management](#branch-management)
11. [Testing Requirements](#testing-requirements)

---

## Project Overview

**LLMSafeSpace** is a Kubernetes-first platform for secure code execution focused on LLM agents. It provides isolated sandbox environments where LLM-generated code runs safely, with warm pool support for sub-second sandbox startup, multiple security levels, and SDKs for Python, JavaScript/TypeScript, and Go.

**Core principles:**

- Sandboxes run as isolated Kubernetes pods with defense-in-depth security (gVisor, seccomp, network policies, read-only filesystems)
- Warm pools of pre-initialized pods eliminate cold-start latency for LLM agent workflows
- One unified controller manages all custom resources — no microservice sprawl
- SDK-first design: Python, JavaScript/TypeScript, and Go SDKs expose all functionality
- No direct user access to Kubernetes — all interactions go through the Agent API
- Real-time streaming via WebSocket for interactive agent sessions

**Three deliverables:**

1. `agent-api` — Go API service (Gin + WebSocket) — entry point for all SDK interactions
2. `controller` — Unified Kubernetes operator (controller-runtime) — manages all CRDs
3. `execution-runtime` — Container images (Python, Node.js, Go) — hardened execution environments

**Primary source documents:**

- [`design/ARCHITECTURE.md`](design/ARCHITECTURE.md) — System overview, deployment topology, security model
- [`design/APISERVICE.md`](design/APISERVICE.md) — Detailed API service internal design
- [`design/CONTROLLER.md`](design/CONTROLLER.md) — Authoritative controller specification (all 5 CRDs, reconciliation loops, state machines)
- [`design/SECURITY.md`](design/SECURITY.md) — Defense-in-depth security model
- [`design/NETWORK.md`](design/NETWORK.md) — Network policy design and egress filtering
- [`design/WARMINGPOOL.md`](design/WARMINGPOOL.md) — Warm pool architecture and pod recycling
- [`design/RUNTIMEENV.md`](design/RUNTIMEENV.md) — Runtime environment images and security wrappers
- [`design/IMPLEMENTATION.md`](design/IMPLEMENTATION.md) — Phased implementation plan and risk assessment

---

## Critical Guidelines & Hard Rules

### 0. Test Driven Development (TDD)

**MANDATORY:** Write tests BEFORE writing functional code. Always.

```
Correct workflow:
1. Write test
2. Run test (must fail)
3. Write minimal code to pass
4. Run test (must pass)
5. Refactor if needed
```

**Test requirements:**

- Multiple happy path tests
- Multiple unhappy path tests
- Edge case coverage
- Always use `-timeout` when running tests
- Tests must pass before marking work complete

### 1. Type Safety First

**Always:**

- Define strongly-typed structs for all data structures
- Create domain types for related fields (see `pkg/types/types.go`)
- Use Go types for all CRD specs and statuses

**Never:**

- Use `map[string]interface{}` for structured data
- Use `interface{}` when the type is known
- Pass untyped data between functions

Maps are acceptable only when parsing external JSON/YAML with unknown structure — and even then, convert to a typed struct immediately.

### 2. Idiomatic Go

- Follow Go conventions throughout
- Use `(value, error)` multiple return pattern
- Avoid global state
- Create custom error types for domain-specific errors (see `api/internal/errors/errors.go`)
- Prefer minimal concurrency; add it only when there is clear, measurable benefit

### 3. Explicit Over Implicit

- Explicit error handling — no swallowed errors
- Explicit type declarations
- No magic or hidden behaviour

### 4. Code Quality

- No comments unless strictly necessary and timeless
- Incorrect or outdated comments must be removed or corrected
- Code is self-documenting through clear naming

### 5. Zero Technical Debt

- Do not create adapters for backwards compatibility
- Remove legacy code
- Implement the full final solution
- Never hack tests to pass — fix the root cause

### 6. Uncertainty Protocol

If uncertain about correct behaviour: **ask the user**. Do not guess, assume, or implement workarounds.

### 7. Understand the Architecture First

Before making any change, read the relevant design document(s). Understand how the change fits the overall data flow. Never modify code without knowing why.

Key documents by area:

| Area | Document |
|------|----------|
| System overview | `design/ARCHITECTURE.md` |
| API internals | `design/APISERVICE.md` |
| REST + WebSocket API | `design/API.md` |
| Controller + CRDs | `design/CONTROLLER.md` |
| Reconciliation loops | `design/CONTROLLER-RECONCILIATION.md` |
| Warm pool management | `design/WARMINGPOOL.md`, `design/CONTROLLER-WARMPOOL.md` |
| Security model | `design/SECURITY.md` |
| Network policies | `design/NETWORK.md` |
| Runtime environments | `design/RUNTIMEENV.md` |
| Controller monitoring | `design/CONTROLLER-MONITORING.md` |
| Controller HA | `design/CONTROLLER-HA.md` |
| Error handling | `design/CONTROLLER-ERROR.md` |
| Implementation phases | `design/IMPLEMENTATION.md` |

### 8. Communication Tone

- Neutral, factual, objective
- Not sensational or sycophantic
- Provide honest and critical feedback
- Validate claims with evidence before stating them

---

## Repository Structure

```
llmsafespace/
├── README.md                              # User-facing README
├── README-LLM.md                          # This file
├── go.mod                                 # Root module: github.com/lenaxia/llmsafespace
├── go.sum
├── Makefile                               # Root build/test/lint targets
├── LICENSE                                # Apache 2.0
│
├── api/                                   # Agent API service
│   ├── Makefile                           # API-specific build targets
│   ├── go.sum
│   ├── cmd/
│   │   └── api/
│   │       └── main.go                    # API server entrypoint
│   ├── config/
│   │   └── config.yaml                    # Default configuration
│   ├── internal/
│   │   ├── app/
│   │   │   └── app.go                     # Application bootstrap (Gin router, services, lifecycle)
│   │   ├── config/
│   │   │   ├── config.go                  # Config struct + Viper loading
│   │   │   └── config_test.go
│   │   ├── docs/
│   │   │   └── swagger.go                 # Swagger/OpenAPI documentation
│   │   ├── errors/
│   │   │   └── errors.go                  # Domain error types
│   │   ├── interfaces/
│   │   │   └── interfaces.go              # Service interfaces
│   │   ├── logger/
│   │   │   ├── logger.go                  # Zap logger construction
│   │   │   └── logger_test.go
│   │   ├── middleware/
│   │   │   ├── auth.go                    # JWT + API key authentication
│   │   │   ├── cors.go                    # CORS handling
│   │   │   ├── error_handler.go           # Error response formatting
│   │   │   ├── logging.go                 # Request logging
│   │   │   ├── metrics.go                 # Prometheus metrics middleware
│   │   │   ├── rate_limit.go              # Rate limiting
│   │   │   ├── recovery.go                # Panic recovery
│   │   │   ├── request_id.go              # Request ID injection
│   │   │   ├── security.go                # Security headers
│   │   │   ├── tracing.go                 # Distributed tracing
│   │   │   ├── validation.go              # Request validation
│   │   │   ├── README.md
│   │   │   ├── MISSINGTESTS.md
│   │   │   └── tests/                     # Per-middleware tests
│   │   │       ├── auth_test.go
│   │   │       ├── cors_test.go
│   │   │       ├── error_handler_test.go
│   │   │       ├── logging_test.go
│   │   │       ├── metrics_test.go
│   │   │       ├── middleware_chain_test.go
│   │   │       ├── middleware_test.go
│   │   │       ├── rate_limit_test.go
│   │   │       ├── recovery_test.go
│   │   │       ├── request_id_test.go
│   │   │       ├── security_test.go
│   │   │       ├── tracing_test.go
│   │   │       ├── validation_test.go
│   │   │       └── README.md
│   │   ├── mocks/                         # Service mocks for testing
│   │   │   ├── cache.go
│   │   │   ├── database.go
│   │   │   ├── execution.go
│   │   │   ├── file.go
│   │   │   ├── metrics.go
│   │   │   ├── middleware_mocks.go
│   │   │   ├── ratelimiter.go
│   │   │   ├── session.go
│   │   │   └── warmpool.go
│   │   ├── server/
│   │   │   └── router.go                  # Gin route definitions
│   │   ├── services/                      # Core business logic
│   │   │   ├── services.go                # Service initialization + lifecycle
│   │   │   ├── services_test.go
│   │   │   ├── auth/                      # Authentication (JWT + API key)
│   │   │   │   ├── auth.go
│   │   │   │   └── auth_test.go
│   │   │   ├── cache/                     # Redis cache service
│   │   │   │   ├── cache.go
│   │   │   │   └── cache_test.go
│   │   │   ├── database/                  # PostgreSQL access (pgx)
│   │   │   │   ├── database.go
│   │   │   │   └── database_test.go
│   │   │   ├── execution/                 # Code/command execution via K8s exec
│   │   │   │   ├── execution.go
│   │   │   │   └── execution_test.go
│   │   │   ├── file/                      # File operations via K8s exec
│   │   │   │   ├── file.go
│   │   │   │   └── file_test.go
│   │   │   ├── kubernetes/                # K8s client wrapper
│   │   │   │   └── kubernetes.go
│   │   │   ├── metrics/                   # Prometheus metrics collection
│   │   │   │   ├── metrics.go
│   │   │   │   └── metrics_test.go
│   │   │   ├── sandbox/                   # Sandbox lifecycle management
│   │   │   │   ├── sandbox_service.go
│   │   │   │   ├── sandbox_service_test.go
│   │   │   │   ├── DESIGN.md
│   │   │   │   └── validation/
│   │   │   │       └── validators.go
│   │   │   └── warmpool/                  # Warm pool integration
│   │   │       ├── warmpool_service.go
│   │   │       └── warmpool_service_test.go
│   │   ├── tests/
│   │   │   └── integration/
│   │   │       └── api_flow_test.go
│   │   ├── utilities/
│   │   │   ├── token_extractor.go
│   │   │   └── token_extractor_test.go
│   │   └── validation/
│   │       ├── sandbox.go
│   │       ├── validation.go
│   │       └── warmpool.go
│   ├── migrations/                        # PostgreSQL schema migrations
│   │   ├── 000001_initial_schema.up.sql
│   │   ├── 000001_initial_schema.down.sql
│   │   ├── 001_initial_schema.sql
│   │   └── 001_initial_schema_rollback.sql
│   └── scripts/                           # Operational scripts
│       ├── health-check.sh
│       ├── init-db.sh
│       └── migrate.sh
│
├── controller/                            # Kubernetes operator
│   ├── main.go                            # Controller entrypoint (flags, manager, webhooks)
│   ├── Makefile                           # Controller build targets
│   ├── Dockerfile                         # Controller Docker image
│   ├── bin/
│   │   └── manager                        # Built binary
│   ├── config/
│   │   └── manager/
│   │       └── manager.yaml               # Controller deployment config
│   ├── examples/                          # Example CRD manifests
│   │   ├── runtimeenvironment.yaml
│   │   ├── sandbox.yaml
│   │   ├── sandboxprofile.yaml
│   │   ├── test-sandbox.yaml
│   │   ├── test-warmpool.yaml
│   │   └── warmpool.yaml
│   ├── internal/
│   │   ├── common/                        # Shared utilities
│   │   │   ├── condition_adapter.go
│   │   │   ├── constants.go
│   │   │   ├── leader_election.go
│   │   │   ├── metrics.go
│   │   │   ├── network_policy_manager.go
│   │   │   ├── pod_manager.go
│   │   │   ├── service_manager.go
│   │   │   └── utils.go
│   │   ├── controller/                    # Reconciler registration
│   │   │   ├── controller.go
│   │   │   └── setup.go
│   │   ├── metrics/                       # Controller Prometheus metrics
│   │   │   └── metrics.go
│   │   ├── resources/                     # CRD type definitions + webhooks
│   │   │   ├── register.go
│   │   │   ├── sandbox_types.go
│   │   │   ├── sandbox_deepcopy.go
│   │   │   ├── sandbox_webhook.go
│   │   │   ├── sandboxprofile_types.go
│   │   │   ├── sandboxprofile_deepcopy.go
│   │   │   ├── sandboxprofile_webhook.go
│   │   │   ├── warmpool_types.go
│   │   │   ├── warmpool_deepcopy.go
│   │   │   ├── warmpool_webhook.go
│   │   │   ├── warmpod_types.go
│   │   │   ├── warmpod_deepcopy.go
│   │   │   ├── warmpod_webhook.go
│   │   │   ├── runtimeenvironment_types.go
│   │   │   ├── runtimeenvironment_deepcopy.go
│   │   │   └── runtimeenvironment_webhook.go
│   │   ├── sandbox/                       # Sandbox reconciler
│   │   │   └── controller.go
│   │   ├── warmpod/                       # WarmPod reconciler
│   │   │   └── controller.go
│   │   └── warmpool/                      # WarmPool reconciler
│   │       └── controller.go
│   └── scripts/
│       ├── install-crds.sh
│       └── test-controller.sh
│
├── runtimes/                              # Execution runtime environments
│   ├── base/                              # Base runtime image (shared by all languages)
│   │   ├── Dockerfile
│   │   ├── security/
│   │   │   ├── apparmor-profiles/
│   │   │   │   ├── default.profile
│   │   │   │   └── high-security.profile
│   │   │   └── seccomp-profiles/
│   │   │       └── default.json
│   │   └── tools/
│   │       ├── cleanup-pod
│   │       ├── execution-tracker
│   │       ├── health-check
│   │       └── sandbox-monitor
│   ├── python/
│   │   ├── Dockerfile
│   │   ├── Dockerfile.ml                  # ML-optimized Python runtime
│   │   ├── security/
│   │   │   └── python/
│   │   │       ├── restricted_modules.json
│   │   │       └── sitecustomize.py
│   │   └── tools/
│   │       └── python-security-wrapper.py
│   ├── nodejs/
│   │   ├── Dockerfile
│   │   ├── config/
│   │   │   └── tsconfig.json
│   │   ├── security/
│   │   │   └── nodejs/
│   │   │       └── restricted_modules.json
│   │   └── tools/
│   │       └── nodejs-security-wrapper.js
│   ├── go/
│   │   ├── Dockerfile
│   │   ├── security/
│   │   │   └── go/
│   │   │       └── restricted_packages.json
│   │   └── tools/
│   │       └── go-security-wrapper.go
│   └── tests/
│       ├── run_tests.sh
│       ├── requirements.txt
│       ├── test_runtime.py
│       └── results/
│           ├── junit.xml
│           ├── summary.txt
│           └── test.log
│
├── pkg/                                   # Shared packages (imported by api/ and controller/)
│   ├── README.md
│   ├── config/
│   │   └── kubernetes_config.go           # Kubernetes configuration types
│   ├── crds/                              # CRD YAML definitions
│   │   ├── sandbox_crd.yaml
│   │   ├── warmpool_crd.yaml
│   │   ├── warmpod_crd.yaml
│   │   ├── sandboxprofile_crd.yaml
│   │   └── runtimeenvironment_crd.yaml
│   ├── http/
│   │   └── writer.go                      # BodyCaptureWriter, safe HTTP client
│   ├── interfaces/
│   │   ├── kubernetes.go                  # KubernetesClient interface
│   │   └── logger.go                      # LoggerInterface
│   ├── kubernetes/                        # K8s client utilities
│   │   ├── client.go                      # Client management
│   │   ├── client_crds.go                 # CRD operations
│   │   ├── client_test.go
│   │   ├── informers.go                   # Shared informers
│   │   ├── kubernetes_operations.go       # Operations executor
│   │   └── tests/                         # Comprehensive K8s client tests
│   │       ├── README.md
│   │       ├── client_crds_test.go
│   │       ├── client_test.go
│   │       ├── informers_test.go
│   │       ├── kubernetes_operations_test.go
│   │       ├── main_test.go
│   │       ├── mocks_test.go
│   │       ├── run_tests.sh
│   │       └── test_helpers.go
│   ├── logger/
│   │   ├── logger.go                      # Zap-based structured logging
│   │   └── mock_test.go
│   ├── types/
│   │   ├── types.go                       # All domain types (CRD types, API types, errors)
│   │   ├── doc.go
│   │   └── zz_generated.deepcopy.go       # Auto-generated DeepCopy methods
│   └── utilities/
│       ├── hashing.go                     # SHA-256 hashing utilities
│       ├── masking.go                     # Sensitive data masking
│       └── strings.go                     # String utilities
│
├── mocks/                                 # Generated/convention-based mocks
│   ├── factory.go                         # Mock factory
│   ├── kubernetes/                        # K8s client mocks
│   │   ├── kubernetes_client.go
│   │   ├── llmsafespace_v1.go
│   │   ├── runtimeenvironment.go
│   │   ├── sandbox.go
│   │   ├── sandboxprofile.go
│   │   ├── warmpod.go
│   │   ├── warmpool.go
│   │   └── watch.go
│   ├── logger/
│   │   └── logger.go
│   └── types/
│       ├── session.go
│       └── wsconnection.go
│
│   └── design/                                # Design documents (21 files)
│       ├── ARCHITECTURE.md                    # System overview and data flows
│       ├── API.md                             # REST + WebSocket API specification
│       ├── APISERVICE.md                      # API service internal design
│       ├── IMPLEMENTATION.md                  # Phased implementation plan
│       ├── SECURITY.md                        # Defense-in-depth security model
│       ├── NETWORK.md                         # Network policy design
│       ├── RUNTIMEENV.md                      # Runtime environment images
│       ├── WARMINGPOOL.md                     # Warm pool architecture
│       ├── CONTROLLER.md                      # Authoritative controller spec
│       ├── CONTROLLER-OVERVIEW.md
│       ├── CONTROLLER-ARCHITECTURE.md
│       ├── CONTROLLER-COMPONENTS.md
│       ├── CONTROLLER-CRDS.md
│       ├── CONTROLLER-RECONCILIATION.md       # All reconciliation loops
│       ├── CONTROLLER-MONITORING.md           # Prometheus metrics definitions
│       ├── CONTROLLER-HA.md                   # Leader election and graceful shutdown
│       ├── CONTROLLER-ERROR.md                # Error handling strategy
│       ├── CONTROLLER-CONCLUSION.md
│       ├── CONTROLLER-WORKQUEUE.md            # Unified work queue design
│       ├── CONTROLLER-WARMPOOL.md             # Warm pod allocation and recycling
│       └── story2.1                           # API service implementation story
│
├── hack/                                  # Build and code generation scripts
│   ├── boilerplate.go.txt                 # Code generation boilerplate header
│   ├── kube_codegen.sh                    # Kubernetes code generation script
│   ├── tools.go                           # Tool dependencies
│   ├── update-codegen.sh                  # Code generation update script
│   ├── update-deepcopy.sh                 # DeepCopy regeneration (called by make deepcopy)
│   └── verify-codegen.sh                 # Code generation verification
│
├── .github/
│   ├── renovate.json                      # Renovate bot configuration
│   └── workflows/
│       └── build-runtimes.yml             # CI: Build and test runtime images
│
├── APIIMPLEMENTATION.md                   # API implementation notes
```

**Key principles:**

- Every major folder has a README.md
- READMEs are the first thing to read when entering a folder
- READMEs are short but define rules for reading and editing

---

## Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                                                                              │
│   SDKs (Python / JS-TS / Go)                                                │
│         │                                                                    │
│         ▼                                                                    │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │  Agent API (agent-api)                                              │   │
│   │  ┌──────────┐  ┌──────────┐  ┌───────────┐  ┌──────────────────┐  │   │
│   │  │ REST API │  │WebSocket │  │   Auth    │  │  Rate Limiting   │  │   │
│   │  │ (Gin)    │  │ Stream   │  │ JWT+APIKey│  │  + Validation    │  │   │
│   │  └──────────┘  └──────────┘  └───────────┘  └──────────────────┘  │   │
│   │  ┌──────────┐  ┌──────────┐  ┌───────────┐  ┌──────────────────┐  │   │
│   │  │ Sandbox  │  │Exec Service│ │File Service│ │  WarmPool Svc   │  │   │
│   │  │ Service  │  │(K8s exec) │ │(K8s exec) │ │  (allocation)    │  │   │
│   │  └──────────┘  └──────────┘  └───────────┘  └──────────────────┘  │   │
│   │  ┌──────────┐  ┌──────────┐  ┌───────────┐                         │   │
│   │  │ Database │  │  Cache   │  │  Metrics  │                         │   │
│   │  │ (pgx)    │  │ (Redis)  │  │ (Prom)    │                         │   │
│   │  └──────────┘  └──────────┘  └───────────┘                         │   │
│   └───────────────────────────┬─────────────────────────────────────────┘   │
│                               │ creates sandboxes via K8s API              │
│                               ▼                                             │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │  Kubernetes Cluster                                                 │   │
│   │                                                                     │   │
│   │  ┌───────────────────────────────────────────────────────────────┐ │   │
│   │  │  Sandbox Controller (controller-runtime)                      │ │   │
│   │  │                                                               │ │   │
│   │  │  ┌─────────────┐ ┌─────────────┐ ┌─────────────────────────┐ │ │   │
│   │  │  │   Sandbox   │ │  WarmPool   │ │    WarmPod              │ │ │   │
│   │  │  │ Reconciler  │ │ Reconciler  │ │ Reconciler              │ │ │   │
│   │  │  └─────────────┘ └─────────────┘ └─────────────────────────┘ │ │   │
│   │  │  ┌────────────────────┐ ┌─────────────────────────────────┐  │ │   │
│   │  │  │ SandboxProfile     │ │ RuntimeEnvironment              │  │ │   │
│   │  │  │ Reconciler         │ │ Reconciler                      │  │ │   │
│   │  │  └────────────────────┘ └─────────────────────────────────┘  │ │   │
│   │  │                                                               │ │   │
│   │  │  + Webhook validation for all CRDs                            │ │   │
│   │  │  + Leader election (LeaseLock)                                │ │   │
│   │  │  + Health probes (:8081) + Metrics (:8080)                    │ │   │
│   │  └───────────────────────────────────────────────────────────────┘ │   │
│   │                                                                     │   │
│   │  ┌───────────────────┐  ┌───────────────────┐  ┌───────────────┐  │   │
│   │  │ Sandbox Pods      │  │ Warm Pool Pods    │  │ Network       │  │   │
│   │  │ (per-request)     │  │ (pre-initialized) │  │ Policies      │  │   │
│   │  │ Python/Node/Go    │  │ Ready → Assigned  │  │ (default-deny)│  │   │
│   │  └───────────────────┘  └───────────────────┘  └───────────────┘  │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│   ┌─────────────────────┐  ┌─────────────────┐                              │
│   │ PostgreSQL           │  │ Redis            │                              │
│   │ (users, API keys,    │  │ (sessions,       │                              │
│   │  sandbox metadata,   │  │  caching, rate   │                              │
│   │  audit logs)         │  │  limiting)       │                              │
│   └─────────────────────┘  └─────────────────┘                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Custom Resource Definitions

The controller manages 5 CRDs in the `llmsafespace.dev/v1` API group:

| CRD | Kind | Scope | Short | Purpose |
|-----|------|-------|-------|---------|
| `sandbox_crd.yaml` | `Sandbox` | Namespaced | `sb` | Represents a single code execution sandbox |
| `warmpool_crd.yaml` | `WarmPool` | Namespaced | `wp` | Pool of pre-initialized pods for fast allocation |
| `warmpod_crd.yaml` | `WarmPod` | Namespaced | `wpod` | Individual warm pod within a pool |
| `sandboxprofile_crd.yaml` | `SandboxProfile` | Namespaced | `sbp` | Reusable security and resource profile |
| `runtimeenvironment_crd.yaml` | `RuntimeEnvironment` | Cluster | `rte` | Defines a runtime image (Python, Node.js, Go) |

### Sandbox lifecycle

```
Pending → Creating → Running → Terminating → Terminated
                ↘         ↘         ↘
                  Failed ← ← ← ← ←
```

A sandbox may transition to `Failed` from any state. Warm pods are claimed from pools when `useWarmPool: true` is specified.

### Warm pod lifecycle

```
Pending → Ready → Assigned → Terminating
              ↘        ↘
               (recycled back to Ready when sandbox terminates)
```

### Service initialization order

The API service starts dependencies in a specific order with rollback on failure:

```
Metrics → Database → Cache → Auth → File → Execution → WarmPool → Sandbox
```

Shutdown reverses this order.

---

## Technology Stack

| Component | Technology | Reason |
|-----------|-----------|--------|
| API language | Go 1.23 | Type-safe, strong concurrency, idiomatic for K8s ecosystem |
| API framework | Gin | High-performance HTTP framework with middleware support |
| Controller framework | controller-runtime | Standard Kubernetes controller pattern |
| Database | PostgreSQL (pgx/v5) | Relational data for users, API keys, audit logs |
| Cache | Redis (go-redis/v8) | Sessions, caching, rate limiting, warm pod tracking |
| Auth | JWT (golang-jwt/v5) + API keys | Stateless auth with `lsp_` prefixed API keys |
| WebSocket | Gorilla WebSocket | Real-time streaming of execution output |
| Config | Viper | YAML config + env var overrides |
| Logging | go.uber.org/zap | Structured logging with sensitive data filtering |
| Metrics | Prometheus (client_golang) | Standard K8s observability |
| Validation | go-playground/validator | Request and CRD validation |
| API docs | swaggo/swag | Auto-generated Swagger/OpenAPI |
| Security | unrolled/secure | HTTP security headers |
| Code generation | k8s.io/code-generator | DeepCopy for CRD types |
| Testing | testify, go-sqlmock, miniredis | Unit and integration testing |
| Runtime images | Alpine Linux / Debian slim | Small attack surface for execution environments |

---

## Worklog Requirements

Worklogs are **mandatory**. They are the institutional memory of this project. Every meaningful session must produce a worklog entry. This is not optional.

### When to write a worklog

Write a worklog entry after **any** of the following:

- Completing a user story or part of one
- Making an architectural decision
- Discovering a bug or unexpected behaviour
- Completing a design document
- Running into a blocker
- Starting or finishing a feature branch
- Any session longer than 30 minutes of work

If in doubt: **write the worklog**.

### Worklog file naming

```
NNNN_YYYY-MM-DD_short-description.md
```

- `NNNN` is a zero-padded sequential number starting at `0001`
- Date is the actual date the work was done
- Description is lowercase, hyphen-separated, 3–6 words
- Next entry: check the highest existing number and increment by 1

Examples:

```
0001_2026-05-01_initial-project-setup.md
0002_2026-05-02_api-service-foundation.md
0003_2026-05-03_controller-tdd-sandbox.md
```

### Worklog format

Every worklog entry must follow this exact structure:

```markdown
# Worklog: <Short Title>

**Date:** YYYY-MM-DD
**Session:** <brief description of what this session was about>
**Status:** Complete | In Progress | Blocked

---

## Objective

What was the goal of this session?

---

## Work Completed

### 1. <Area of work>
- Specific thing done
- Specific thing done

### 2. <Area of work>
- Specific thing done

---

## Key Decisions

List any decisions made and the rationale behind them. If a decision was
made without enough information, note that and flag it for follow-up.

---

## Blockers

List anything that is blocking progress. Include what information or action
is needed to unblock. If none, write "None."

---

## Tests Run

List test commands run and their outcomes. If no tests were run, explain why.

---

## Next Steps

What should the next session start with? Be specific enough that a fresh
context can pick up immediately without re-reading everything.

---

## Files Modified

List every file created or modified in this session.
```

### Worklog discipline rules

1. **Write it before ending the session** — not the next day. Memory degrades fast.
2. **Be specific** — vague entries like "worked on controller" are useless. Name the functions, the decisions, the line numbers if relevant.
3. **Document decisions with rationale** — not just what was decided, but why. Future sessions will need to understand the reasoning, not just the outcome.
4. **Record blockers immediately** — if you are blocked, write it down. Do not silently skip the entry.
5. **List every file touched** — this makes it trivial to audit what changed in a session.
6. **Next steps must be actionable** — "continue implementation" is not actionable. "Implement `CreateSandbox()` in `api/internal/services/sandbox/sandbox_service.go` and write tests first per TDD" is actionable.
7. **Never retroactively rewrite a worklog** — worklogs are append-only history. If something was wrong, note the correction in the next entry.

---

## Development Workflow

### Before starting work

1. Read `README-LLM.md` (this file)
2. Read the relevant design document(s) from `design/` — see the table in [Rule 7](#7-understand-the-architecture-first)
3. Read `pkg/README.md` for shared package conventions
4. Check recent git history to understand current state of the area you're modifying

### During work

1. Write tests first — TDD, always
2. Use strongly-typed structs (see `pkg/types/types.go` for existing domain types)
3. Commit at each logical unit of work with a descriptive message

### After completing work

1. Run all tests: `make test` or `go test -timeout 30s -race ./...`
2. Run linter: `make lint`
3. Verify tests pass
4. **Write a worklog entry** (see [Worklog Requirements](#worklog-requirements))
5. Commit everything

---

## Multi-Agent Workflow

This section defines two agent roles and their workflows for collaborative or multi-step development.

**IMPORTANT:** These workflows are MANDATORY when working on epics, user stories, or complex multi-step tasks.

---

### Agent Role 1: Orchestrator Agent

**Purpose:** Coordinate multiple delegations to complete epics, stories, or complex multi-step tasks.

**When to use:**

- Working on epic-level features (e.g., new runtime environment, new CRD)
- User story implementation requiring multiple sub-tasks
- Complex refactoring or architectural changes
- Coordinating work across `api/`, `controller/`, `pkg/`, and `runtimes/`

#### Orchestrator responsibilities

1. **Context distribution** — Ensure all delegations have access to critical documentation
2. **Scope definition** — Define clear boundaries, ownership, and integration points
3. **Quality enforcement** — Validate work meets standards through code review and testing
4. **Gap detection** — Identify and resolve integration gaps between sub-tasks
5. **Integration validation** — Ensure all components work together end-to-end
6. **Testing coordination** — Run comprehensive builds and tests across the entire repository
7. **Worklog management** — Create completion worklogs documenting the entire epic/story

#### Orchestrator workflow (11-step process)

Follow this workflow for all epic/story implementation tasks:

```
1. Context Setup
   └─> Delegate: "Read README-LLM.md, relevant design docs"
   └─> Include: Design constraints, architectural patterns, integration points
   └─> Define: Clear scope, ownership boundaries, expected deliverables

2. Implementation Delegation
   └─> Delegate: User story implementation with TDD requirements
   └─> Prompt detail level: "Fresh developer seeing codebase for first time"
   └─> Include: Specific file references, pattern examples, testing requirements

3. Code Review Delegation
   └─> Delegate: Skeptical code reviewer to validate implementation
   └─> Focus: Integration points, test coverage, gap detection, code quality
   └─> Requirement: Only code + tests count as proof of work (NOT status updates)
   └─> Output: Detailed gap report with code references and fix recommendations

4. Gap Remediation
   └─> Delegate: Fix ALL gaps identified in review (no matter how minor)
   └─> Include: Specific gap descriptions, code locations, fix strategies
   └─> Validate: Each fix with targeted tests

5. Iterative Validation
   └─> Repeat Steps 2–4 until ZERO gaps remain
   └─> Acceptance Criteria: "Story complete in spirit AND letter"
   └─> No compromises: All integration points validated, all tests passing

6. Build and Test Validation
   └─> Run ALL builds and tests, fix ANY failures
   └─> Commands:
       - make build          # ALL packages must build
       - make test           # ALL tests must pass
       - make lint           # No lint errors
   └─> NO TECH DEBT: Fix all failures regardless of relevance to current work
   └─> Zero tolerance: No pre-existing failures acceptable

7. Commit and Push
   └─> git add .
   └─> git commit -m "Descriptive message referencing story/epic"
   └─> git push origin HEAD

8. Worklog Creation
   └─> Create worklog (see Worklog Requirements section)
   └─> Content: Summary, implementation details, test results, next steps
   └─> Commit worklog with code changes

9. Move to Next Story
   └─> Validate no implementation gaps between previous and current story
   └─> Common pitfall: Previous story built/tested but never wired into main code
   └─> If story file missing: Write it first before implementing
   └─> Repeat workflow from Step 1

10. Integration Gap Check
    └─> CRITICAL: Validate integration between stories
    └─> Ask: "Was previous story's code actually integrated into main codebase?"
    └─> Check: Import statements, service registration, CRD schema, type definitions
    └─> Test: End-to-end flow through new and existing code paths

11. Final Validation
    └─> Run full repository test suite one final time
    └─> Confirm all story checklists updated
```

#### Orchestrator delegation guidelines

**Prompt quality standards:**

- Detail level: "Instructions for a developer seeing the codebase for the first time"
- Specificity: Include exact file paths, function names, pattern references
- Context: Provide architectural context, design decisions, trade-offs
- Boundaries: Clear scope limits, what is in/out of scope, integration points
- Examples: Reference similar implementations and established patterns

**Delegation prompt template:**

```
CONTEXT:
- Primary doc: README-LLM.md (your bible)
- Design docs: [List relevant design/ documents]
- CRD types: pkg/types/types.go
- Design constraints: [TDD, type safety, etc.]

SCOPE:
- Objective: [Clear, specific goal]
- Boundaries: [What is included, what is excluded]
- Integration points: [How this connects to existing code]
- Ownership: [Which files/packages this delegation owns]

REQUIREMENTS:
- MUST read README-LLM.md
- MUST read relevant design documents
- MUST follow TDD (tests first)
- MUST use established patterns
- MUST validate integration points
- MUST create worklog

DELIVERABLES:
1. [Specific deliverable 1 with acceptance criteria]
2. [Specific deliverable 2 with acceptance criteria]

SUCCESS CRITERIA:
- All tests passing (make test)
- All builds successful (make build)
- Integration points validated
- Code follows established patterns
- Worklog created
```

#### Orchestrator principles

**Respect other agents:**

- Multiple agents may work simultaneously in the same repository
- NEVER perform indiscriminate destructive git operations (`git checkout .`, `git clean -fd`)
- Define clear ownership boundaries to avoid conflicts between `api/`, `controller/`, `pkg/`

**Thoroughness:**

- Proof of work = code + tests, NOT status updates
- Integration points MUST be identified and updated
- Sufficient end-to-end and integration tests for happy/unhappy paths
- NO gaps acceptable, no matter how minor

**Quality gates:**

- Code review before merge
- ALL tests passing before next story
- ALL builds successful before next story
- Worklog created before task closure

**Proper fixes only:**

- ALWAYS use the proper fix
- NEVER use workarounds, hacks, or shortcuts

---

### Agent Role 2: Delegation Agent

**Purpose:** Execute specific, well-scoped tasks as part of a larger epic or story.

**When to use:**

- Implementing a specific service or reconciler
- Writing tests for a component
- Code review of another agent's work
- Fixing a specific bug or gap
- Integrating a component into the main codebase

#### Delegation agent responsibilities

1. **Context acquisition** — Read ALL assigned documentation (README-LLM.md, design docs)
2. **Scope adherence** — Stay within defined boundaries; ask orchestrator if unclear
3. **Pattern following** — Use established patterns; check similar implementations
4. **TDD compliance** — Write tests FIRST, ensure they fail, then implement
5. **Integration awareness** — Identify and document integration points
6. **Quality standards** — Follow type safety, error handling, logging standards
7. **Worklog creation** — Document work performed if completing a task

#### Delegation agent workflow

**Standard implementation task:**

```
1. Read Required Documentation
   - README-LLM.md (MANDATORY — your bible)
   - Relevant design/ documents
   - pkg/types/types.go for domain types
   - pkg/README.md for shared package conventions

2. Understand Context
   - Review delegation prompt carefully
   - Identify scope boundaries
   - Note integration points
   - Check similar implementations

3. Plan Implementation
   - Break down into sub-tasks
   - Identify test scenarios (happy + unhappy paths)
   - Note which patterns to follow
   - Identify dependencies

4. Write Tests FIRST (TDD)
   - Unit tests (happy paths)
   - Unit tests (unhappy paths)
   - Integration tests where applicable
   - Tests MUST fail initially

5. Implement
   - Follow established patterns
   - Use strongly-typed structs from pkg/types/
   - Handle errors explicitly
   - Follow idiomatic Go

6. Validate
   - All tests pass (make test)
   - Code builds (make build)
   - Integration points work
   - Follow-up questions documented

7. Create Worklog (if task complete)
   - Document what was done
   - Include test results
   - Note any issues or follow-up
   - See Worklog Requirements section

8. Report Back to Orchestrator
   - Clear completion status
   - Any gaps or uncertainties
   - Integration point validation status
   - Recommendations for next steps
```

**Code review task:**

```
1. Read Code with Skeptical Mindset
   - Assume nothing works until proven
   - Check every integration point
   - Verify test coverage (happy + unhappy)
   - Look for edge cases

2. Validate Against Standards
   - README-LLM.md rules followed?
   - TDD practised (tests first)?
   - Type safety maintained?
   - Patterns followed correctly?
   - Error handling comprehensive?

3. Integration Point Analysis
   - Are ALL integration points identified?
   - Are they properly tested?
   - Do end-to-end flows work?
   - Are there hidden dependencies?

4. Gap Identification
   - Document EVERY gap (no matter how minor)
   - Provide code references for each gap
   - Explain WHY it is a gap
   - Recommend HOW to fix it

5. Report Generation
   - Clear gap descriptions
   - Severity assessment
   - Fix recommendations with code examples
   - NO APPROVAL until all gaps fixed
```

#### Delegation agent principles

**Read first, ask later:**

- ALWAYS read README-LLM.md before ANY work
- ALWAYS read relevant design documents
- ALWAYS check `pkg/types/types.go` for existing types before creating new ones
- If information exists in docs, do not ask the orchestrator

**Follow patterns:**

- Check similar implementations in the codebase
- Use established patterns (Gin middleware chain, controller-runtime reconcilers, service lifecycle)
- Do not invent new patterns without approval
- Consistency is critical

**Test-driven development:**

- Tests BEFORE code, always
- Tests must fail initially
- Happy AND unhappy paths
- Integration tests where applicable

**Quality standards:**

- Type safety (structs, not maps)
- Explicit error handling (never ignore errors)
- No TODOs or placeholders
- Complete implementations only

**Communication:**

- Report completion clearly
- Document gaps/uncertainties
- Ask questions when scope is unclear
- Provide recommendations for next steps

---

### Common failure modes

| Role | Failure Mode | Consequence |
|------|-------------|-------------|
| Orchestrator | Insufficient detail in delegation prompts | Delegation confusion, pattern violations |
| Orchestrator | Skipping integration validation | Code works in isolation but fails together |
| Orchestrator | Not aligning api/ and controller/ types | CRD schema drift, runtime failures |
| Delegation | Not reading README-LLM.md | Pattern violations, rule violations |
| Delegation | Scope creep | Conflicts with other agents, boundary violations |
| Delegation | Creating new types instead of using pkg/types/ | Duplicate types, conversion errors |
| Both | No worklog | Lost context, incomplete task tracking |

---

## Common Commands

```bash
# --- Root module ---

# Tidy dependencies
go mod tidy

# Run all tests
make test

# Run tests with verbose output and timeout
go test -timeout 30s -race -v ./...

# Run tests with coverage
make cover

# Format code
make fmt

# Static analysis
make vet

# Lint
make lint

# Build API binary
make build

# Cross-compile for Linux amd64
make build-linux

# Docker build
make docker-build

# --- API service (from api/) ---

# Build API service
cd api && make build

# Run API service locally
cd api && make run

# Run database migrations up
cd api && make migrate-up

# Rollback database migrations
cd api && make migrate-down

# --- Controller ---

# Build controller binary
cd controller && go build -o bin/manager .

# Run controller locally (against current kubeconfig)
cd controller && go run ./main.go --enable-leader-election=false

# Install CRDs into cluster
cd controller && bash scripts/install-crds.sh

# --- Code generation ---

# Regenerate DeepCopy methods (after modifying pkg/types/types.go)
make deepcopy
# Or manually:
# hack/update-deepcopy.sh

# --- Docker (local development) ---

# Build API image
make docker-build

# Run API image
make docker-run
```

---

## Branch Management

**Branch naming:**

- Feature: `feature/short-description`
- Bugfix: `bugfix/issue-description`
- Hotfix: `hotfix/critical-issue`

**Branch workflow:**

1. Create branch from `main`
2. Work in branch with regular commits
3. Write a worklog entry before merging
4. Merge to `main` when complete and all tests pass

---

## Testing Requirements

### TDD workflow

```
1. Write test first
2. Run — must fail
3. Write minimal code to pass
4. Run — must pass
5. Refactor
```

### Coverage requirements

- Multiple happy path cases
- Multiple unhappy path cases
- Edge cases (empty fields, nil slices, very long strings, invalid inputs)
- Error conditions

### Table-driven tests

Use table-driven tests with `t.Run()` for any function with multiple input cases:

```go
func TestCreateSandbox(t *testing.T) {
    tests := []struct {
        name    string
        req     types.CreateSandboxRequest
        wantErr bool
    }{
        {"valid python sandbox", types.CreateSandboxRequest{Runtime: "python:3.10"}, false},
        {"empty runtime", types.CreateSandboxRequest{Runtime: ""}, true},
        {"invalid timeout", types.CreateSandboxRequest{Runtime: "python:3.10", Timeout: -1}, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := svc.CreateSandbox(ctx, tt.req)
            if (err != nil) != tt.wantErr {
                t.Errorf("CreateSandbox() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### Always use timeout

```bash
# Good
go test -timeout 30s -race ./...

# Bad — can hang forever
go test ./...
```

### Mock conventions

- Service mocks live in `api/internal/mocks/` and `mocks/` (root)
- Kubernetes mocks use the interface from `pkg/interfaces/kubernetes.go`
- Use `testify/mock` for mock generation
- Database tests use `go-sqlmock`
- Redis tests use `miniredis` (in-memory Redis)

### Code generation

When modifying API types in `pkg/types/types.go`, you must regenerate the DeepCopy implementations:

```bash
# From project root
make deepcopy

# Verify and commit generated changes
git add pkg/types/zz_generated.deepcopy.go
git commit -m "Update generated DeepCopy code"
```

---

## Configuration Reference

The API service is configured via `api/config/config.yaml` with environment variable overrides via Viper.

| Section | Key | Default | Description |
|---------|-----|---------|-------------|
| `server` | `host` | `0.0.0.0` | Listen address |
| `server` | `port` | `8080` | Listen port |
| `server` | `shutdownTimeout` | `30s` | Graceful shutdown timeout |
| `kubernetes` | `inCluster` | `true` | Use in-cluster config |
| `kubernetes` | `namespace` | `llmsafespace` | Default namespace |
| `database` | `host` | `postgres` | PostgreSQL host |
| `database` | `port` | `5432` | PostgreSQL port |
| `database` | `maxOpenConns` | `25` | Max open connections |
| `redis` | `host` | `redis` | Redis host |
| `redis` | `port` | `6379` | Redis port |
| `redis` | `poolSize` | `20` | Connection pool size |
| `auth` | `jwtSecret` | (empty) | JWT signing secret |
| `auth` | `tokenDuration` | `24h` | Token expiry |
| `auth` | `apiKeyPrefix` | `lsp_` | API key prefix |
| `logging` | `level` | `info` | Log level |
| `logging` | `encoding` | `json` | Log format (json/console) |
| `rateLimiting` | `enabled` | `true` | Enable rate limiting |

---

## Version History

| Version | Date | Changes |
|---------|------|---------|
| 1.0 | 2026-05-21 | Initial creation |
