# LLMSafeSpace — LLM Implementation Guide

> **Repository:** `github.com/lenaxia/llmsafespace`

**Version:** 1.11
**Last Updated:** 2026-06-08
**Project Status:** Active Development

---

## Table of Contents

1. [Project Overview](#project-overview)
2. [Critical Guidelines & Hard Rules](#critical-guidelines--hard-rules)
3. [Repository Structure](#repository-structure)
4. [Architecture Overview](#architecture-overview)
5. [Relay Config Subsystem](#relay-config-subsystem)
6. [Technology Stack](#technology-stack)
7. [Worklog Requirements](#worklog-requirements)
8. [Development Workflow](#development-workflow)
9. [Multi-Agent Workflow](#multi-agent-workflow)
10. [PR Review Guide](#pr-review-guide)
11. [Common Commands](#common-commands)
12. [Branch Management](#branch-management)
13. [Testing Requirements](#testing-requirements)

---

## Project Overview

**LLMSafeSpace** is a Kubernetes-first platform for running AI agents securely in isolated sandboxes. Every sandbox runs `opencode serve` as a persistent HTTP server with a PVC-backed persistent workspace. The API acts as a reverse proxy to the agent, supporting both interactive chat and programmatic (MCP/REST) access.

**Core principles:**

- Every sandbox runs an AI agent (`opencode serve`) — no bare code execution
- Every sandbox is workspace-backed — PVC-mounted persistent filesystem at `/workspace`
- Workspaces can be suspended (pod deleted, PVC retained) and resumed (~3s)
- Credentials stored exclusively in K8s Secrets — never in PostgreSQL, Redis, or logs
- LLMSafeSpace is an MCP server — any MCP-compatible client can connect
- Stateless API server — horizontally scalable, no sticky sessions required

**Three deliverables:**

1. `api` — Go API service (Gin) + MCP server — reverse proxy to sandbox agents, workspace/credential management
2. `controller` — Kubernetes operator (controller-runtime) — manages Sandbox, Workspace, SandboxProfile, RuntimeEnvironment CRDs
3. `runtimes` — Container images (Python, Node.js, Go) — hardened environments with `opencode serve`, `redact` binary, credential injection

**Authoritative design document:**

- [`design/EVOLUTION-V2.md`](design/EVOLUTION-V2.md) — V2 architecture (v2.4). Supersedes all V1 design docs for the areas it covers.

**V1 design docs (reference only — superseded by EVOLUTION-V2.md where they conflict):**

- [`design/ARCHITECTURE.md`](design/ARCHITECTURE.md) — System overview, deployment topology, security model
- [`design/CONTROLLER.md`](design/CONTROLLER.md) — Controller specification (V1 CRDs, reconciliation loops)
- [`design/SECURITY.md`](design/SECURITY.md) — Defense-in-depth security model
- [`design/NETWORK.md`](design/NETWORK.md) — Network policy design and egress filtering
- [`design/WARMINGPOOL.md`](design/WARMINGPOOL.md) — Warm pool architecture (REMOVED in V2)
- Other `design/CONTROLLER-*.md` files contain detailed V1 controller documentation

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

**Test requirements (all are mandatory — none are optional):**

- Multiple happy path tests
- Multiple unhappy path tests (errors, invalid inputs, boundary failures, dependency failures)
- Edge case coverage
- End-to-end integration tests that exercise the real wiring (router → service → K8s/DB/Redis or fakes thereof) — unit tests alone are not sufficient
- Always use `-timeout` when running tests
- Tests must pass before marking work complete

**Definition of done:**

A task is **not** done until it has been demonstrated to be integrated properly via passing e2e/integration tests. "It compiles", "unit tests pass", or "it works in isolation" do not satisfy this requirement. Code that is built but never wired into the live request path is incomplete work.

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

**Engineering principles — every change must be:**

- **SOLID** — single responsibility, open/closed, Liskov-substitutable, interface-segregated, dependency-inverted
- **Robust** — handles failures, partial states, and adversarial inputs without corruption
- **Reliable** — deterministic, repeatable, no flaky behaviour
- **Maintainable** — clear naming, small functions, obvious data flow; the next reader should not need a map
- **Scalable** — no hidden O(n²) loops, no per-request allocations of expensive resources, no global locks on hot paths
- **Performant** — measure before optimising; do not pessimise (e.g. unnecessary copies, N+1 queries, synchronous I/O on hot paths)
- **Secure** — input validated, outputs sanitised, secrets never logged, least-privilege by default
- **Not over-engineered** — no speculative abstractions, no premature generalisation, no frameworks-for-the-sake-of-frameworks
- **Not overly complex** — prefer the simplest design that satisfies the requirement; if a junior engineer cannot read it, simplify
- **Idiomatic** — follow the conventions of the language and the surrounding codebase (Go idioms here; see Rule 2)
- **Faithful to the ask** — meet the spirit AND the letter of the requirement; do not solve a different problem because it is easier

**Comments and self-documentation:**

- No comments unless strictly necessary and timeless
- Incorrect or outdated comments must be removed or corrected
- Code is self-documenting through clear naming

### 5. Zero Technical Debt

- Do not create adapters for backwards compatibility
- Remove legacy code
- Implement the full final solution
- Never hack tests to pass — fix the root cause
- **No pre-existing errors are acceptable.** "Pre-existing" is not an excuse. If you encounter errors, warnings, or broken behaviour in the codebase — even if you did not introduce them — fix them. We are the only ones working on this codebase; every error is our responsibility. Leave the codebase in a zero-error state after every session.

### 6. Uncertainty Protocol

If uncertain about correct behaviour: **ask the user**. Do not guess, assume, or implement workarounds.

### 7. Assumptions: State, Then Validate

Every non-trivial change rests on assumptions about the system (data shape, caller behaviour, library semantics, deployment environment, ordering, concurrency, error modes, etc.). These assumptions cause most production bugs when they go unstated and unchecked.

**Mandatory protocol:**

1. **State assumptions up front.** Before writing code, list every assumption the change relies on. Write them in the worklog, the PR description, or a comment block at the top of the design discussion. "It is obvious" is not an excuse — write it down.
2. **Validate every assumption.** For each one, identify how you will prove it true:
   - Read the relevant source/spec/doc
   - Run a query, probe the running cluster, or write a quick test
   - Check git history or existing tests
   - Ask the user if it cannot be validated mechanically
3. **If you cannot validate it, do not rely on it.** Either find a way to validate it, redesign so the assumption is unnecessary, or ask the user. Never proceed on an unvalidated assumption.
4. **Record the validation result.** In the worklog, next to each assumption, record what proved it (e.g. "verified via `pkg/kubernetes/client_test.go:142`" or "confirmed by `kubectl get sandbox -o yaml` on cluster X").
5. **Treat failed validations as findings.** A disproved assumption is a bug or design flaw. Surface it; do not work around it silently.

This rule is non-negotiable. The most common failure mode in this codebase has been silent assumption drift — code that "should work" because someone assumed a behaviour that was never true (see worklogs 0030 and 0033 for examples).

### 8. Understand the Architecture First

Before making any change, read the relevant design document(s). Understand how the change fits the overall data flow. Never modify code without knowing why.

Key documents by area:

| Area | Document |
|------|----------|
| **V2 Architecture** | `design/EVOLUTION-V2.md` (authoritative) |
| V2 Implementation stories | `design/stories/README.md` |
| System overview (V1) | `design/ARCHITECTURE.md` |
| Controller + CRDs (V1) | `design/CONTROLLER.md` |
| Reconciliation loops (V1) | `design/CONTROLLER-RECONCILIATION.md` |
| Security model | `design/SECURITY.md`, `design/EVOLUTION-V2.md §9` |
| Network policies | `design/NETWORK.md` |
| Runtime environments (V1) | `design/RUNTIMEENV.md` |
| Error handling (V1) | `design/CONTROLLER-ERROR.md` |

### 9. Communication Tone

- Neutral, factual, objective
- Not sensational or sycophantic
- Provide honest and critical feedback
- Validate claims with evidence before stating them

### 10. Never Force Push Without Explicit Permission

**NEVER use `git push --force` or `git push --force-with-lease` unless the user has explicitly told you it is okay to force push.**

Force pushing rewrites shared history and can destroy a collaborator's work. The only acceptable scenarios are:

1. The user directly instructs you: "force push" or "push --force"
2. You are fixing a CI-rejected commit (e.g. repolint worklog numbering) and no other collaborator has pulled the broken commit
3. You are working on a private branch that no one else has ever pushed to

**Always prefer `git pull --rebase` + normal `git push` over force pushing.** If you pushed a broken commit, first ask the user if force push is acceptable, describe why it's needed, and wait for confirmation.

### 11. Adversarial Self-Review

After implementing any non-trivial change, **before marking it complete**, conduct a structured adversarial review in three phases.

#### Phase 1: Identify Weaknesses, Gaps, and Failure Modes

Explicitly ask:

1. **Where are the gaps?** What did the design not cover? What edge cases are unhandled? What requirements were omitted?
2. **Where is it weak?** Which parts are fragile, tightly coupled, or depend on implicit ordering?
3. **Where will it fail?** Under what conditions (concurrency, partial failure, invalid state, resource exhaustion, adversarial input) will the implementation behave unexpectedly?
4. **What did I assume without verifying?** Re-read the assumptions list. For each one, ask: "Did I actually validate this, or did I just believe it?"
5. **What would a skeptical reviewer reject?** If someone with no context read this diff, what would they flag?
6. **Why might this code be wrong?** Take the adversarial view — assume the implementation is incorrect or misses the mark, and prove otherwise.

#### Phase 2: Validate Each Finding

For every criticism generated in Phase 1:

1. **Is the finding real?** Re-read the code, re-run the test, reproduce the scenario. Do not take findings at face value.
2. **Is it a bug, a design flaw, or a false alarm?**
   - **Real bug:** Fix it before proceeding. Do not defer.
   - **Design flaw:** Surface with proposed remediation. Do not proceed without addressing.
   - **False alarm:** Document why it is not a real issue (one sentence with evidence). Do not silently dismiss.
3. **If uncertain:** Escalate to the user rather than dismissing or guessing.
4. **Only validated findings make it into the record.** Unvalidated claims, guesses, and assumed-but-unverified assertions are discarded. They have no place in a worklog, PR description, or review report.

#### Phase 3: Remediate or Document

- Real findings must be fixed with regression tests before the change is complete.
- False alarms must be documented with rationale (one sentence is sufficient).
- The change is not ready until Phase 2 returns zero real findings.

This is not optional introspection — it is a mandatory validation gate. Code that has not survived its own adversarial review is not ready for commit.

See also the [Adversarial Assessment](#adversarial-assessment) section in the PR Review Guide for expanded criteria used during pull request review.

---

## Repository Structure

```
llmsafespace/
├── README.md                              # User-facing README
├── README-LLM.md                          # This file
├── go.mod                                 # Root module: github.com/lenaxia/llmsafespace
├── go.sum
├── Makefile                               # Root build/test/lint targets
├── LICENSE                                # AGPL-3.0-or-later
├── NOTICE                                 # Copyright + commercial license offer
│
├── cmd/                                   # Top-level binaries
│   ├── redact/
│   │   └── main.go                        # Standalone redact binary (imports pkg/redact)
│   └── mcp/
│       └── main.go                        # MCP server entrypoint (imports api/internal/mcp)
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
│   │   ├── handlers/                      # Gin HTTP route handlers
│   │   │   ├── sandbox.go                 # Sandbox lifecycle handlers
│   │   │   ├── workspace.go               # Workspace lifecycle handlers
│   │   │   ├── proxy.go                   # Reverse proxy to opencode serve
│   │   │   └── user.go                    # User management handlers
│   │   ├── interfaces/
│   │   │   └── interfaces.go              # Service interfaces
│   │   ├── logger/
│   │   │   ├── logger.go                  # Zap logger construction
│   │   │   └── logger_test.go
│   │   ├── mcp/                           # MCP server implementation
│   │   │   ├── server.go                  # MCP server core
│   │   │   ├── tools.go                   # Tool definitions and handlers
│   │   │   ├── resources.go               # Resource handlers
│   │   │   ├── prompts.go                 # Prompt templates
│   │   │   └── transport.go               # stdio + SSE transport
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
│   │   │   ├── metrics.go
│   │   │   ├── middleware_mocks.go
│   │   │   ├── ratelimiter.go
│   │   │   ├── sandbox.go
│   │   │   └── workspace.go
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
│   │   │   └── workspace/                 # Workspace lifecycle management
│   │   │       ├── workspace_service.go
│   │   │       └── workspace_service_test.go
│   │   ├── tests/
│   │   │   └── integration/
│   │   │       └── api_flow_test.go
│   │   ├── utilities/
│   │   │   ├── token_extractor.go
│   │   │   └── token_extractor_test.go
│   │   └── validation/
│   │       ├── sandbox.go
│   │       ├── validation.go
│   │       └── workspace.go
│   ├── migrations/                        # PostgreSQL schema migrations
│   │   ├── 000001_initial_schema.up.sql
│   │   ├── 000001_initial_schema.down.sql
│   │   ├── 000002_workspaces.up.sql       # V2: Workspace table + sandbox workspace_id FK
│   │   └── 000002_workspaces.down.sql
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
│   │   └── workspace.yaml
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
│   │   │   ├── workspace_types.go         # V2: Workspace CRD type
│   │   │   ├── workspace_deepcopy.go
│   │   │   ├── workspace_webhook.go
│   │   │   ├── sandbox_types.go           # V2: extended with workspaceRef, podIP, suspend phases
│   │   │   ├── sandbox_deepcopy.go
│   │   │   ├── sandbox_webhook.go
│   │   │   ├── sandboxprofile_types.go
│   │   │   ├── sandboxprofile_deepcopy.go
│   │   │   ├── sandboxprofile_webhook.go
│   │   │   ├── runtimeenvironment_types.go
│   │   │   ├── runtimeenvironment_deepcopy.go
│   │   │   └── runtimeenvironment_webhook.go
│   │   ├── sandbox/                       # Sandbox reconciler
│   │   │   └── controller.go
│   │   └── workspace/                     # Workspace reconciler
│   │       └── controller.go
│   └── scripts/
│       ├── install-crds.sh
│       └── test-controller.sh
│
├── runtimes/                              # Execution runtime environments
│   ├── base/                              # Base runtime image (shared by all languages)
│   │   ├── Dockerfile                     # V2: builds redact, installs opencode, entrypoints
│   │   ├── security/
│   │   │   ├── apparmor-profiles/
│   │   │   │   ├── default.profile
│   │   │   │   └── high-security.profile
│   │   │   └── seccomp-profiles/
│   │   │       └── default.json
│   │   └── tools/
│   │       ├── entrypoints/               # Agent entrypoint scripts
│   │       │   ├── entrypoint-common.sh   # Credential materialization + setup
│   │       │   └── entrypoint-opencode.sh # opencode serve runner
│   │       └── smoke-test.sh              # Verify all required binaries present
│   ├── python/
│   │   ├── Dockerfile                     # Extends base; adds Python toolchain
│   │   └── Dockerfile.ml                  # ML-optimized Python runtime
│   ├── nodejs/
│   │   └── Dockerfile                     # Extends base; adds Node.js toolchain
│   ├── go/
│   │   └── Dockerfile                     # Extends base; adds Go toolchain
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
│   │   ├── workspace_crd.yaml             # V2: Workspace CRD
│   │   ├── sandbox_crd.yaml
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
│   ├── redact/                            # Secret redaction engine (ported from k8s-mechanic)
│   │   ├── redact.go                      # 16 compiled regex rules; used by cmd/redact
│   │   └── redact_test.go
│   ├── types/
│   │   ├── types.go                       # API transfer object types (CreateSandboxRequest, etc.)
│   │   └── doc.go
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
│   │   └── workspace.go
│   ├── logger/
│   │   └── logger.go
│   └── types/
│       └── wsconnection.go
│
├── design/                                # Design documents
│   ├── EVOLUTION-V2.md                    # V2 authoritative design (supersedes conflicting V1 docs)
│   ├── stories/                           # User story specifications
│   │   ├── README.md
│   │   └── epic-*/                        # Per-epic story files
│   ├── ARCHITECTURE.md                    # System overview (V1, reference only)
│   ├── API.md                             # REST + WebSocket API specification (V1)
│   ├── SECURITY.md                        # Defense-in-depth security model
│   ├── NETWORK.md                         # Network policy design
│   ├── RUNTIMEENV.md                      # Runtime environment images (V1)
│   ├── WARMINGPOOL.md                     # Warm pool architecture (REMOVED in V2)
│   ├── CONTROLLER.md                      # Controller spec (V1)
│   └── CONTROLLER-*.md                    # Detailed V1 controller documentation
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
└── APIIMPLEMENTATION.md                   # API implementation notes
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
│   MCP Clients / Browser / REST / SDK                                        │
│         │                                                                    │
│         ▼                                                                    │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │  LLMSafeSpace API (stateless, horizontally scalable)               │   │
│   │  ┌──────────┐  ┌──────────┐  ┌───────────┐  ┌──────────────────┐  │   │
│   │  │ REST API │  │  SSE     │  │   Auth    │  │  Rate Limiting   │  │   │
│   │  │ (Gin)    │  │ Stream   │  │ JWT+APIKey│  │  + Validation    │  │   │
│   │  └──────────┘  └──────────┘  └───────────┘  └──────────────────┘  │   │
│   │  ┌──────────┐  ┌──────────┐  ┌───────────┐  ┌──────────────────┐  │   │
│   │  │ Sandbox  │  │Workspace │  │  Proxy    │  │  MCP Server      │  │   │
│   │  │ Service  │  │ Service  │  │ Handler   │  │  (stdio/SSE)     │  │   │
│   │  └──────────┘  └──────────┘  └───────────┘  └──────────────────┘  │   │
│   │  ┌──────────┐  ┌──────────┐  ┌───────────┐                         │   │
│   │  │ Database │  │  Cache   │  │  Metrics  │                         │   │
│   │  │ (pgx)    │  │ (Redis)  │  │ (Prom)    │                         │   │
│   │  └──────────┘  └──────────┘  └───────────┘                         │   │
│   └───────────────────────────┬─────────────────────────────────────────┘   │
│                               │ CRD + Secret operations via K8s API         │
│                               ▼                                              │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │  Kubernetes Cluster                                                 │   │
│   │                                                                     │   │
│   │  ┌───────────────────────────────────────────────────────────────┐ │   │
│   │  │  Controller (controller-runtime)                               │ │   │
│   │  │  ┌─────────────┐ ┌──────────────┐ ┌─────────────────────────┐│ │   │
│   │  │  │   Sandbox   │ │  Workspace   │ │ SandboxProfile          ││ │   │
│   │  │  │ Reconciler  │ │ Reconciler   │ │ Reconciler              ││ │   │
│   │  │  └─────────────┘ └──────────────┘ └─────────────────────────┘│ │   │
│   │  │  ┌────────────────────────────────────────────────────────┐   │ │   │
│   │  │  │ RuntimeEnvironment Reconciler                           │   │ │   │
│   │  │  └────────────────────────────────────────────────────────┘   │ │   │
│   │  └───────────────────────────────────────────────────────────────┘ │   │
│   │                                                                     │   │
│   │  ┌───────────────────────────────────────────────────────────────┐ │   │
│   │  │  Sandbox Pods (each runs opencode serve :4096)                │ │   │
│   │  │  ┌──────────────────┐  ┌──────────────────┐                  │ │   │
│   │  │  │ init: workspace- │  │ init: credential- │                  │ │   │
│   │  │  │ setup (packages, │  │ setup (creds →    │                  │ │   │
│   │  │  │ initScript)      │  │ /sandbox-cfg)     │                  │ │   │
│   │  │  ├──────────────────┤  └──────────────────┘                  │ │   │
│   │  │  │ main: opencode serve --hostname 0.0.0.0 --port 4096       │ │   │
│   │  │  │ security: readOnlyRoot, runAsNonRoot, drop ALL caps        │ │   │
│   │  │  └──────────────────────────────────────────────────────────┘  │ │   │
│   │  │  Volumes: PVC at /workspace + /home/sandbox (subPath:home) + emptyDirs (/tmp, /sandbox-cfg)  │ │   │
│   │  └───────────────────────────────────────────────────────────────┘ │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│   ┌─────────────────────┐  ┌─────────────────┐                              │
│   │ PostgreSQL           │  │ Redis            │                              │
│   │ (user metadata,      │  │ (caching, rate   │                              │
│   │  workspace names,    │  │  limiting)        │                              │
│   │  sandbox metadata)   │  │                   │                              │
│   └─────────────────────┘  └─────────────────┘                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Custom Resource Definitions

The controller manages 4 CRDs in the `llmsafespace.dev/v1` API group (V2 — WarmPool/WarmPod removed):

| CRD | Kind | Scope | Short | Purpose |
|-----|------|-------|-------|---------|
| `workspace_crd.yaml` | `Workspace` | Namespaced | `ws` | PVC-backed persistent environment |
| `sandbox_crd.yaml` | `Sandbox` | Namespaced | `sb` | K8s pod running `opencode serve` |
| `sandboxprofile_crd.yaml` | `SandboxProfile` | Namespaced | `sbp` | Reusable security and resource profile |
| `runtimeenvironment_crd.yaml` | `RuntimeEnvironment` | Cluster | `rte` | Defines a runtime image (Python, Node.js, Go) |

### CRD type ownership

CRD types exist in two locations with strictly separate roles:

| Location | Purpose |
|----------|---------|
| `controller/internal/resources/*_types.go` | **Authoritative** — kubebuilder-annotated, used by the controller, generated deepcopy |
| `pkg/types/types.go` | **API transfer objects only** — REST request/response shapes (`CreateSandboxRequest`, etc.). No generated deepcopy. |

These are intentionally different types. The API types are transfer objects; the controller types are CRD schemas. They must not be merged.

### Sandbox lifecycle (V2)

```
Pending → Creating → Running → Suspending → Suspended → Resuming → Running
                       ↘           ↘
                         Terminating → Terminated
                         Failed
```

Suspend/resume is workspace-level. Suspended workspace retains PVC; resuming creates a new pod (~3s).

### Workspace lifecycle (V2)

```
Pending → Active → Suspending → Suspended → Resuming → Active
                 ↘               ↘           ↘
                   Terminating     Terminating  Terminating
                        ↘               ↘           ↘
                      Terminated     Terminated   Terminated
```

### State management: K8s CRD vs PostgreSQL

| Data | Owner | Source of Truth |
|------|-------|-----------------|
| Workspace/Sandbox phase | Controller | K8s CRD status |
| PVC name, pod IP | Controller | K8s CRD status |
| Conditions | Controller | K8s CRD status |
| `status.lastActivityAt` (workspace) | API server (batched, ≤60s flush) | K8s CRD status |
| Workspace display name | API | PostgreSQL |
| User ID ownership | Both | K8s CRD (`spec.owner.userID`) authoritative; PostgreSQL mirrors for query perf |
| Creation/update timestamps | Both | K8s CRD authoritative; PostgreSQL mirrors |
| Credentials | Controller | K8s Secrets (never PostgreSQL) |

### Service initialization order

The API service starts dependencies in a specific order with rollback on failure:

```
Metrics → Database → Cache → Auth → Sandbox → Workspace
```

Shutdown reverses this order.

---

## Relay Config Subsystem

### Overview

The relay config subsystem manages how `agent-config.json` — the file opencode reads for provider credentials — is built and kept correct across the pod lifetime. Multiple processes write to this file, which has been the source of several confirmed production bugs.

**Volume layout on every workspace pod:**

| Mount | Type | Persists across pod restart? | Owner |
|---|---|---|---|
| `/workspace` | Longhorn PVC (root, no subPath) | Yes | User workspace data, opencode.db, auth.json |
| `/home/sandbox` | Longhorn PVC (`subPath: home`) | Yes | SSH keys, secrets base dir, enricher cache, tool caches |
| `/sandbox-cfg` | emptyDir (memory, ro) | No — ephemeral per pod, read-only | Secrets mounted by controller at pod start |
| `/tmp` | emptyDir (memory) | No | agent-config.json, secrets-env |

**Key path constants** (`pkg/agentd/types.go`):

```
AgentConfigPath  = "/tmp/agent-config.json"
SecretsBasePath  = "/home/sandbox/.secrets"   ← deleted by reset() on every reload
SecretsEnvPath   = "/tmp/secrets-env"
```

**opencode config loading order** (validated from opencode 1.15.12 binary):

opencode merges config files via recursive deep-merge, last writer wins:
1. Global XDG config: `~/.config/opencode/opencode.jsonc`
2. Project config: `findUp(["opencode.json","opencode.jsonc"], cwd, {rootFirst:true})`
3. `OPENCODE_CONFIG` env var path — **always appended last, always wins**

`OPENCODE_CONFIG=/tmp/agent-config.json` is set by `entrypoint-opencode.sh`. Therefore `agent-config.json` overrides all other config for any key it sets. opencode does **not** hot-reload this file — it is only read at process startup.

**auth.json location** (validated): `XDG_DATA_HOME=/workspace/.local` is set before `exec workspace-agentd`, so agentd inherits it. `authJSONPath = /workspace/.local/opencode/auth.json` — on the PVC, persistent across pod restarts.

---

### Writers of agent-config.json (as of 2026-06-08)

There are **four** distinct write paths to `agent-config.json`:

| Writer | File | When | Produces |
|---|---|---|---|
| `FlushProviders` | `pkg/agentd/secrets/secrets.go:623` | Boot materialize + every `/v1/reload-secrets` | Provider credentials only — no relay config |
| `applyWorkspaceConfig` | `cmd/workspace-agentd/secrets.go:203` | Boot materialize only (after FlushProviders) | Adds `model` key with `providerID/modelID` form |
| `startRelayInjector` goroutine | `cmd/workspace-agentd/relay_injector.go:423` | Once per pod lifetime at ~T+7s | Merges `disabled_providers` + `opencode-relay` block |
| `reloadSecretsHandler` re-merge | `cmd/workspace-agentd/secrets.go:362` | After every FlushProviders in reload handler | Restores relay config after FlushProviders clobbered it |

None of these write paths are atomic with each other. The design relies on:
1. Boot sequence being strictly ordered (FlushProviders → applyWorkspaceConfig → relay injector fires later)
2. `reloadMu` mutex in `reloadSecretsHandler` serialising concurrent reload calls
3. opencode not hot-reloading the config file (so TOCTOU between FlushProviders and re-merge is benign)
4. `atomic.Pointer[[]relayModel]` in `relay_injector.go` coordinating between the injector goroutine and the reload handler

---

### Bug Status (as of 2026-06-08)

#### Bug 1 — Relay config clobbered by credential bind — ✅ Fixed (PR #65)

**Root cause:** `FlushProviders` wrote only credential-sourced providers, clobbering the relay injector's `disabled_providers` + `opencode-relay` block on every credential bind.

**Fix implemented:** `reloadSecretsHandler` stores the relay model list in `activeRelayModels` (`atomic.Pointer`) after successful injection. On every credential reload, after `FlushProviders`, `reloadSecretsHandler` calls `buildRelayConfig` to re-merge the relay block (`cmd/workspace-agentd/secrets.go:349-372`). The current implementation uses re-merge with atomic coordination rather than a single-writer design. See "How the relay config subsystem works (as-built)" below for the full write sequence.

**Verified on cluster:** `workspace 1aa87aec`, 2026-06-08. After fix: credential bind no longer removes relay config.

#### Bug 2 — Model enricher cache always cold — ✅ Fixed (PR #65)

**Root cause:** Enricher wrote cache to `/home/sandbox/.secrets` which `reset()` deletes on every reload.

**Fix implemented:** `enricherCacheDir` defaults to `$HOME/.local/state/llmsafespace` (`cmd/workspace-agentd/secrets.go:91`), which is on the workspace PVC (`subPath: home`) and is never deleted by `reset()`. 24-hour TTL is now actually exercised.

#### Bug 3 — Personal opencode key → broken free model routing — ✅ Fixed (PR #67 follow-up)

**Root cause:** `relayActive` is a static boolean set at API startup (`api/internal/app/app.go:158`) from `LLMSAFESPACE_INFERENCE_RELAY_URL`. It is applied identically to all workspaces. A workspace where the relay injector was skipped (personal opencode key) has no `opencode-relay` provider, but `annotateModels(relayActive=true)` was still remapping all zero-cost opencode models to `providerID="opencode-relay"`. The frontend shows these models as selectable. Inference fails.

**Fix implemented:**

The discriminating signal — whether the relay injector actually ran for a specific pod — is exposed as `RelayInjected bool` in `agentd.ReadyzResponse` (`pkg/agentd/types.go`), populated from `getActiveRelayModels() != nil` in the readyz handler (`cmd/workspace-agentd/main.go`).

`annotateModels` now takes `(raw, relayGloballyEnabled, relayInjected bool)`. Remap only fires when both flags are true. `relayInjected=false` covers both Phase 1 (~7s window before injection completes, acceptable brief window of wrong providerID) and personal-key (relay skipped, remap must never fire).

`ListModels` calls `fetchRelayInjected` on cache miss. `fetchRelayInjected` calls `/v1/readyz` (not `/v1/statusz`) with `Authorization: Bearer <password>` (not Basic auth). Using readyz is critical because statusz has no latency upper bound — it makes multiple synchronous HTTP calls to opencode under a mutex. Readyz is cache-based and fast.

`SetModel` → `patchAgentModel` → `resolveModelIDFromCatalog` uses the same `fetchRelayInjected` guard. `patchAgentModel` now returns `(resolved string, error)` so `SetModel` can pass the resolved `providerID/modelID` to `metricsRecorder` without a second catalog + statusz fetch (previously: 3× GET /provider + 2× GET /v1/statusz per SetModel call on a relay model).

**Previously not triggered:** No users have personal opencode keys.

#### Bug 4 — Cascade: clobbered relay → silent inference failure — ✅ Fixed (Bug 1 fix eliminates it)

#### Gap 5 — Concurrent /v1/reload-secrets calls — ✅ Fixed (PR #67 follow-up)

**Root cause:** Two simultaneous reloads raced through `Materializer.reset()` → `RemoveAll(SecretsBaseDir)` + `RemoveAll(SSHDir)`, then both `appendFile`'d to `SecretsEnvPath` — producing duplicate env var entries.

**Fix implemented:** `reloadMu sync.Mutex` in `cmd/workspace-agentd/secrets.go` wraps the `Materialize` → `EnrichProviders` → `FlushProviders` → relay re-merge block. The `proc.restart()` call is excluded from the lock to avoid holding it during the ~5s SIGTERM window.

#### Gap 6 — Model cache not evicted after credential bind — ✅ Fixed (PR #67 follow-up)

**Root cause:** `defaultModelCache.Evict(workspaceID)` was only called in `SetModel`, not after `doReload`. After a credential bind changed the provider list, the 5s TTL caused stale model lists.

**Fix implemented:** `defaultModelCache.Evict(workspaceID)` called at the end of `doReload` after a successful agentd response (`api/internal/handlers/secrets.go:530`).

#### Gap 7 — Relay URL secret partially logged — ✅ Fixed (PR #67 follow-up)

**Root cause:** `relay_injector.go` logged `cfg.RelayURL[:min(len,50)]`. The relay URL has the form `https://relay.safespaces.dev/<secret>` — the host is 30 chars, leaving 20 chars of the secret visible in pod logs accessible via `kubectl logs`.

**Fix implemented:** `relayURLHost()` helper extracts only the scheme+host for logging (`cmd/workspace-agentd/relay_injector.go:53-59`). Log field renamed from `relayURL` to `relayHost`.

---

### Known design fragilities (documented, not bugs)

1. **Multiple writers of agent-config.json.** The four-writer design is correct given the current boot sequence and `reloadMu` serialisation, but it is fragile. A future change that reorders the boot sequence or adds a new write path could reintroduce relay clobbering. The single-writer `WriteAgentConfig` design (described in previous versions of this section) would eliminate this fragility but requires a non-trivial refactor of `FormatOpenCodeConfig`, the reload handler, and the relay injector. Tracked as a future cleanup item.

2. **One-shot relay injector.** The injector goroutine runs once per pod lifetime. If the opencode credential changes after the injector has run (personal key → public key), the relay is not re-evaluated. The user must restart the pod. A re-triggerable injector (channel-based state machine) would handle this automatically.

3. **In-memory model cache is per-API-replica.** `SetModel` evicts on the replica that handled the request; other replicas serve stale data for up to 5 seconds. Future: Redis-backed cache for cross-replica consistency (US-30.11).

4. **`resolveModelWithProvider` non-determinism on collision.** When two providers in `agent-config.json` share a model ID, Go map iteration is non-deterministic — `resolveModelWithProvider` returns whichever provider the runtime visits first. In practice, provider model IDs are namespaced and do not collide, but this is not enforced.

---

### Implementation status summary

| Item | Status | File |
|---|---|---|
| Bug 1 — relay clobbered | ✅ Fixed (re-merge approach) | `cmd/workspace-agentd/secrets.go:349` |
| Bug 2 — enricher cache cold | ✅ Fixed | `cmd/workspace-agentd/secrets.go:91` |
| Bug 3 — relayActive static flag | ✅ Fixed (relayInjected from readyz) | `api/internal/handlers/models.go:fetchRelayInjected` |
| Bug 4 — cascade silent failure | ✅ Fixed (via Bug 1) | — |
| Gap 5 — concurrent reload race | ✅ Fixed | `cmd/workspace-agentd/secrets.go:reloadMu` |
| Gap 6 — cache not evicted after bind | ✅ Fixed | `api/internal/handlers/secrets.go:530` |
| Gap 7 — relay URL in logs | ✅ Fixed | `cmd/workspace-agentd/relay_injector.go:431` |

---

### Confirmed Bugs (production-active as of 2026-06-08)

#### Bug 1 — Relay config clobbered by credential bind (critical)

**Confirmed root cause:** `PUT /api/v1/workspaces/:id/bindings` calls `pushSecretsToAgent` → `doReload` → `POST /v1/reload-secrets` on agentd → `reloadSecretsHandler` → `FlushProviders(opencode.FormatOpenCodeConfig)` → `atomicWrite` (O_TRUNC) on `agent-config.json`. `FormatOpenCodeConfig` produces a config with only credential-sourced providers — no `disabled_providers`, no `opencode-relay`. The relay injector's config is overwritten.

**Observed:** Workspace `1aa87aec` at 07:01:20 UTC 2026-06-08. `PUT /bindings` triggered the reload. Pod had correct relay config from T+7s at boot. After reload: `connected[] = ["opencode", "thekao"]` — 43-model opencode catalog visible to user alongside thekao models.

**Scope:** Affects every workspace on every credential bind while the pod is running.

#### Bug 2 — Model enricher cache always cold (high)

**Confirmed root cause:** `enrichProviderModels` writes cache to `cacheDir = /home/sandbox/.secrets`. `Materializer.reset()` calls `RemoveAll(/home/sandbox/.secrets)` at the start of every `Materialize` call. The 24-hour TTL advertised in comments is never exercised. Every credential bind makes a live HTTP call to the provider's `/models` endpoint.

**Measured impact:** `ai.thekao.cloud/v1/models` responds in ~138ms — currently tolerable. The 5-second API client timeout (`reloadHTTPClient`) provides headroom. But for any slow or unavailable custom endpoint, enrichment silently blocks the full reload window.

#### Bug 3 — Personal opencode key → broken free model routing (high)

**Confirmed root cause:** `relayActive` is a static global flag in `SecretsHandler`, set from `LLMSAFESPACE_INFERENCE_RELAY_URL` at API startup — applied identically to all workspaces. A user who explicitly binds a personal opencode credential causes `shouldSkipRelay=true` on their pod (relay not injected). But `annotateModels(relayActive=true)` still remaps all zero-cost opencode models to `providerID="opencode-relay"`. These models pass the `connectedSet["opencode"]` filter (the filter uses the pre-remap provider ID). The frontend shows them as selectable free-tier models. Inference fails: `PATCH /global/config` sends `"opencode-relay/big-pickle"` but no `opencode-relay` provider exists on the pod.

**Priority ordering** (validated from `GetWorkspaceCredentials` SQL): `(source_type='explicit') DESC, within_priority DESC`. An explicitly-bound personal key beats the auto-applied admin free-tier credential for the `opencode` provider. User's key wins; `apiKey="public"` is dropped. Pod has no relay.

**Currently not triggered:** No users have personal opencode keys at present. Architecturally broken for when they do.

#### Bug 4 — Cascade: clobbered relay → silent inference failure (high)

**Confirmed root cause:** When Bug 1 fires, `modelExistsInCatalog` still returns `true` for relay model IDs (e.g. `"big-pickle"`) because it checks the flat model ID against the catalog, which includes it from the `opencode` provider. `resolveModelIDFromCatalog` returns `"opencode-relay/big-pickle"`. `PATCH /global/config` fails silently. `SetModel` returns `{model, applied:false}`. The user sees the model as selected but every inference fails. No error is surfaced to the user.

**Fix:** Closing Bug 1 eliminates this cascade entirely.

---

### How the relay config subsystem works (as-built)

The relay config subsystem has four writers of `agent-config.json`. All coordination is via:
1. `reloadMu sync.Mutex` in `reloadSecretsHandler` — serialises concurrent reload calls
2. `atomic.Pointer[[]relayModel]` in `relay_injector.go` — the relay injector sets this on success; reloadSecretsHandler reads it to decide whether to re-merge relay after FlushProviders
3. opencode reads `agent-config.json` once at startup — not hot-reloaded

#### Agent-config.json write sequence (boot)

```
materialize command:
  Materializer.reset()      → deletes agent-config.json
  FlushProviders()          → writes provider credentials (thekao, etc.)
  applyWorkspaceConfig()    → reads workspace-config.json, adds model key with providerID/modelID

~T+7s (goroutine, after opencode is healthy):
  startRelayInjector()      → fetchFreeModels → buildRelayConfig (merge) → WriteFile
                            → setActiveRelayModels(models)   ← coordination artifact
                            → proc.restart()                 ← opencode reboots with relay config
```

#### Agent-config.json write sequence (credential reload)

```
reloadSecretsHandler:
  reloadMu.Lock()
  Materializer.reset()      → deletes agent-config.json
  FlushProviders()          → rewrites provider credentials
  if getActiveRelayModels() != nil:
    buildRelayConfig()      → re-merges relay config
    os.WriteFile()          → restores relay block
  reloadMu.Unlock()
  proc.restart()            → opencode reboots
```

#### RelayInjected signal flow

The API server needs to know whether the relay injector ran for a specific pod
so it can correctly annotate the model catalog. The signal flows:

```
relay_injector.go:
  setActiveRelayModels() → atomic.Pointer[[]relayModel] (non-nil after success)

agentd /v1/readyz:
  getActiveRelayModels() != nil → ReadyzResponse.RelayInjected = true
  readyz uses: healthCache.Snapshot() (atomic, no I/O)
             + cachedState() (providerCache, 15s TTL; live calls on miss, bounded by 5s)

API server (ListModels cache miss):
  fetchRelayInjected() → GET /v1/readyz (Bearer token, port 4098, 5s total timeout)
                       → ReadyzResponse.RelayInjected
  → cached in modelCachePayload with 5s TTL alongside model list
```

**Stale window:** `relayInjected` can take up to **5s + 15s = 20s** to reflect a
relay injection that has just completed:
- The model cache TTL is 5s — a cache hit may serve the previous `relayInjected=false` value
  for up to 5s after the cache was written.
- The `providerCache` inside readyz has a 15s TTL — a readyz call may return stale
  `connected[]` data for up to 15s after relay injection.
- In the worst case, a `ListModels` request at T=1s caches `relayInjected=false` until
  T=6s; relay injection completes at T=7s; the cache expires at T=6s but the next readyz
  call may read stale `providerCache` for another 15s — making the first correct response
  appear at approximately T=21s.

This is acceptable: the Phase 1 window is ~7s, and users are unlikely to interact with
the workspace within the first 20s of pod boot. The stale window is purely cosmetic
(models show `providerID="opencode"` instead of `"opencode-relay"`) and self-corrects.

#### Why the annotateModels remap is effectively dead code (but kept)

The remap guard `relayGloballyEnabled && relayInjected && p.ID=="opencode"` can never
be true simultaneously:
- `relayInjected=true` means `setActiveRelayModels()` was called, which means
  `disabled_providers:["opencode"]` was written to `agent-config.json` and opencode restarted.
- After restart, `opencode` is absent from `connected[]` in `/provider`.
- The `connectedSet` filter above the remap removes `p.ID=="opencode"` from the loop.
- The remap condition is unreachable in Phase 2.

In Phase 1 (`relayInjected=false`), the remap is suppressed — correctly, because:
- Personal key: relay was skipped, `opencode-relay` provider doesn't exist on this pod.
- Phase 1 window: free models briefly show `providerID="opencode"`. After T+7s the
  cache expires and the next `ListModels` returns the correct Phase 2 state.

The remap code is kept as defense-in-depth: if a future opencode version keeps `opencode`
in `connected[]` despite `disabled_providers`, the guard correctly remaps rather than
silently routing to a disabled provider.

#### Backwards compatibility

Pods running the old image (before `RelayInjected` was added to `ReadyzResponse`) return
`/v1/readyz` JSON without the `relay_injected` field. Go's `json.Decode` sets
`RelayInjected=false` (zero value). This is safe:
- Old Phase 2 pods already have `opencode-relay` in `connected[]` (not `opencode`).
- The remap guard `p.ID=="opencode"` is never triggered for old Phase 2 pods.
- Old pods are fully correct with `relayInjected=false`.

Validated on live cluster 2026-06-08: `connected=["opencode-relay"]` on old Phase 2 pod
(image `ts-1780939444`).


## Technology Stack

| Component | Technology | Reason |
|-----------|-----------|--------|
| API language | Go 1.23 | Type-safe, strong concurrency, idiomatic for K8s ecosystem |
| API framework | Gin | High-performance HTTP framework with middleware support |
| Controller framework | controller-runtime | Standard Kubernetes controller pattern |
| Database | PostgreSQL (pgx/v5) | Relational data for users, API keys, workspace metadata |
| Cache | Redis (go-redis/v8) | Caching, rate limiting |
| Auth | JWT (golang-jwt/v5) + API keys | Stateless auth with `lsp_` prefixed API keys |
| MCP server | mark3labs/mcp-go | MCP server SDK (stdio + SSE transports) |
| Config | Viper | YAML config + env var overrides |
| Logging | go.uber.org/zap | Structured logging with sensitive data filtering |
| Metrics | Prometheus (client_golang) | Standard K8s observability |
| Validation | go-playground/validator | Request and CRD validation |
| API docs | swaggo/swag | Auto-generated Swagger/OpenAPI |
| Security | unrolled/secure | HTTP security headers |
| Code generation | k8s.io/code-generator | DeepCopy for controller CRD types |
| Testing | testify, go-sqlmock, miniredis | Unit and integration testing |
| Runtime images | Debian bookworm-slim (digest-pinned) | Small attack surface; SHA256-verified binaries |
| Runtime manager | mise (jdx/mise) | Polyglot runtime manager — agents install Python/Node/Go/etc. without root |
| Secret redaction | pkg/redact (internal) | 16-rule regex pipeline; prevents credential leaks in agent output |

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

### Worklog 0046 (2026-05-27): Streaming UX — User Echo, Thinking Blocks, Bubble Overflow
- Replaced `@tanstack/react-virtual` in `MessageList` with flex column — virtualizer's absolute positioning caused streaming bubble to overflow on top of other messages
- `transformHistory` in `messages.ts` now preserves `thinking`/`reasoning` part types (was filtering to `text` only, causing thinking blocks to vanish after streaming)
- User echo fix: `sentTextRef` tracks sent text and strips exact/prefix matches from both `message.part.updated` snapshots and accumulated deltas. Previous `messageID`/`role` filters were dead code — those fields don't exist in SSE event properties (validated via backend test data in `proxy_filter_test.go`, `session_tracker_test.go`, `stream_events_test.go`)
- Thinking rendering: same visual treatment for streaming and completed — rounded border, brain icon, `border-l-2` blockquote. Streaming shows expanded with pulsing icon; completed wraps in collapsible `<details>`
- Nested SSE format unwrapping: `parseStreamEvent` handles both flat `{type, properties}` and nested `{directory, payload: {type, properties}}` opencode event formats
- E2E test SSE data format fixed from nested to flat (matching actual backend output)
- **Blocked:** Thinking and text still render as single unformatted blob during streaming. Debug logging deployed to diagnose actual SSE event structure from opencode. Need browser console output to determine if thinking is sent as separate part type or mixed into text.
- 369 frontend tests passing; 8 files modified across 3 commits (`54cb589`, `46dd2ac`, `c30d6e9`)

### Worklog 0033 (2026-05-23): Cluster Validation, Scheme Conversion Root Cause, First-User-Admin
- Validated worklog 0032 changes against the home-kubernetes cluster running pinned `sha-e8cdbc8`
- Discovered the actual root cause of the "watch channel closed" log spam: `pkg/kubernetes/client_crds.go` was using `serializer.NewCodecFactory(scheme.Scheme)` without `WithoutConversion()`. Watch event decoder called `DecoderToVersion(s, nil)` and tried to convert to a non-existent internal hub version, producing a 500 error event for every Sandbox event delivered. Fix: append `.WithoutConversion()`. TDD-verified with three new codec tests in `pkg/kubernetes/client_test.go`.
- Implemented worklog 0032 followup #3: first registered user is auto-promoted to admin. Added `DatabaseService.CountUsers`. Four new TDD tests in auth service. CountUsers errors fail closed (refuse registration, do not silently default to admin).
- After deploying `sha-5ca1f91`: zero Warn/Error log entries in 5+ min uptime; sandbox phase reporting via `GET /sandboxes/:id/status` confirms watcher is consuming events correctly.
- Cluster validation: fresh user (no permission rows) creates sandbox via API; foreign workspace blocked; admin role bypasses; first-user-becomes-admin works on fresh DB.
- `charts/llmsafespace/values.yaml` documented to recommend `sha-`/`ts-` pinning over moving `:dev` tag.

### Worklog 0032 (2026-05-23): CI Versioning, Permissions Model, Watch Loop Hardening
- CI: every image now tagged with `ts-<unix>` (sortable, shared across all images in one workflow run), `sha-<commit>` (immutable), `dev` (latest from main); semver tags on `v*.*.*` releases
- Removed legacy `build-runtimes.yml` (V1, built unused python/nodejs/go runtime images)
- Permissions model rewritten: dropped `CheckPermission` from sandbox create/terminate; admin role bypasses ownership; non-admins must own the workspace they attach to (via existing `workspaceService.GetWorkspace`)
- Watch loop hardened: ResourceVersion threading + bookmarks + 410 Gone reset + error-event handling; clean apiserver-driven cycles now log at Debug not Warn — kills the "watch channel closed" log spam
- TDD: 7 new watch-loop tests written first, 6 verified to fail against legacy implementation; 4 new permissions tests + 6 existing updated

### Worklog 0031 (2026-05-23): Sandbox CRUD API + Verbose Flag + Test Coverage
- Sandbox CRUD: `POST/GET/DELETE /api/v1/sandboxes`, `GET /api/v1/sandboxes/:id/status` — wired SandboxService into router on a separate Gin group from the proxy (so List/Create are not gated by sandbox ownership middleware)
- `?verbose=true` query param on message + history endpoints; default strips parts where `type=="patch"` (~2KB/response saved)
- `local/test.sh` extended to 9 tests: prompt round-trip with assertion, verbose flag verification, sandbox CRUD via API, session-history continuity across pod recycle (LLM_BASE_URL/LLM_API_KEY/LLM_MODEL gate the LLM-dependent steps)
- README.md rewritten from scratch for V2 (warm pools removed, REST API surface, `?verbose=true` documented)
- 12 new sandbox CRUD router tests + 7 new patch-stripping handler tests

### Worklog 0030 (2026-05-23): E2E Prompt Flow Validated, Worklog 0029 Misdiagnosis
- End-to-end prompt round-trip validated against real cluster: client → API proxy → opencode `POST /session/:id/message` → LLM → response
- Worklog 0029's "MCP required" claim refuted: opencode's documented `POST /session` is headless. The real blocker was credentials, not protocol.
- Workspace credentials API path validated: `PUT /api/v1/workspaces/:id/credentials` → secret → controller mount → opencode config

### Worklog 0029 (2026-05-23): CI Pipeline + E2E Deployment Validation
- CI pipeline: test + build API/controller/runtime-base images on every push to main
- Deployed to real Talos cluster; auth, workspace, sandbox lifecycles validated end-to-end
- Opencode boots and serves HTTP in sandbox pod; prompt validation blocked on MCP (Phase 4)

### Worklog 0028 (2026-05-23): Rate Limiting, CORS, Account Lockout, Security Fixes
- Rate limiter service created (Redis-backed), wired into global middleware stack, configurable via env vars
- CORS hardened: default `AllowedOrigins: []` + `AllowCredentials: false` (was wildcard+credentials)
- Account lockout: N failed login attempts → temporary lock (Redis-backed, configurable)
- Token extraction: disabled query param + cookie by default (M4), only Authorization header
- JWT cache keys hashed with MD5 before storing in Redis (M2)
- Double response write fix in middleware/auth.go (M5)
- 7 new env vars for security config; 11 new TDD tests
- 27 Go test packages passing with `-race`

### Worklog 0027 (2026-05-23): Auth Endpoints + Security Hardening
- Implemented 5 auth endpoints: register, login, API key create/list/delete
- Security audit identified 16 findings; fixed 7 (H2 email enumeration, H3 error leaking, H1 body size limits, C1+H4 rate limiter IP fallback, M1 JWT jti, M3 input sanitization, L1 bcrypt cost)
- 49 new/updated tests: 15 service-level TDD, 19 router e2e, 15 security e2e
- Shell e2e script: `local/test-auth.sh` (17 test cases)
- Updated interfaces, mocks, database service, auth service, router
- All 26 Go test packages passing with `-race`

### Worklog 0026 (2026-05-22): E2E on Kind — 22 Bug Fixes
- Built local testing infra (`local/bootstrap.sh`, `local/test.sh`, `local/teardown.sh`)
- 22 verified production bugs fixed
- 8/8 e2e tests passing
- Validated opencode boots and serves HTTP in sandbox pod

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
6. **Next steps must be actionable** — "continue implementation" is not actionable. "Implement `CreateSandbox()` in `pkg/secrets/secret_service.go` and write tests first per TDD" is actionable.
7. **Never retroactively rewrite a worklog** — worklogs are append-only history. If something was wrong, note the correction in the next entry.

---

## Development Workflow

### Before starting work

1. Read `README-LLM.md` (this file)
2. Read the relevant design document(s) from `design/` — see the table in [Rule 8](#8-understand-the-architecture-first)
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

### Go module downloads in restricted environments

If `proxy.golang.org` is unreachable (common in sandboxed/air-gapped dev environments), use `GOPROXY=direct` to download modules directly from source repositories (GitHub, etc.):

```bash
# Download all modules (bypassing proxy.golang.org and sum.golang.org)
GOPROXY=direct GONOSUMCHECK=* GONOSUMDB=* go mod download

# Run tests with direct proxy
GOPROXY=direct GONOSUMCHECK=* GONOSUMDB=* go test -timeout 120s -short ./...

# Build with direct proxy
GOPROXY=direct GONOSUMCHECK=* GONOSUMDB=* go build ./...
```

This works whenever the source repos (e.g. github.com) are reachable even if the Go module proxy is not.

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

Follow this workflow for all epic/story implementation tasks. Steps 2–5 form the **Validator Loop** — they are MANDATORY and must run until the validator returns zero findings. There is no "good enough" exit.

```
1. Context Setup
   └─> Delegate: "Read README-LLM.md, relevant design docs"
   └─> Include: Design constraints, architectural patterns, integration points
   └─> Define: Clear scope, ownership boundaries, expected deliverables
   └─> Require: Implementer states all assumptions up front and validates each
       (see Critical Guidelines Rule 7 — Assumptions: State, Then Validate)

2. Implementation Delegation
   └─> Delegate: User story implementation with TDD requirements
   └─> Prompt detail level: "Fresh developer seeing codebase for first time"
   └─> Include: Specific file references, pattern examples, testing requirements
   └─> Require: Happy-path tests + unhappy-path tests + e2e integration tests
   └─> Require: Stated assumptions list with validation evidence per assumption

3. Skeptical Validator Delegation (MANDATORY)
   └─> Delegate to a SEPARATE sub-agent acting as a skeptical validator
   └─> Validator's job: assume nothing works; prove every claim
   └─> Validator must check:
       - Every stated assumption — is it actually true? (re-validate independently)
       - Every integration point — is the code wired into the live request path?
       - Test coverage — happy + unhappy + e2e/integration all present and meaningful?
       - Engineering principles (Rule 4) — SOLID, robust, secure, not over-engineered, idiomatic?
       - Spirit AND letter of the ask — does the implementation actually solve what was asked?
       - Tech debt — any TODOs, hacks, workarounds, commented-out code, dead code?
   └─> Output: Detailed findings report with code references and severity
   └─> Validator MUST NOT also be the implementer (independence is the point)

4. Findings Triage and Remediation Delegation
   └─> Before fixing: validate each finding is REAL and not a false alarm
       (re-read the code, re-run the test, confirm the failure mode)
   └─> Document false alarms with rationale; do NOT silently dismiss findings
   └─> Delegate fixes for ALL real findings to a remediation sub-agent
       (no matter how minor — zero tech debt tolerance)
   └─> Each fix must include a regression test
   └─> Remediation agent must NOT introduce new assumptions without validating them

5. Re-Validate (LOOP)
   └─> Send the remediated code BACK to a skeptical validator
   └─> If new findings: return to Step 4
   └─> If zero findings: exit the loop
   └─> NO compromises: the loop continues until validator returns zero real findings
   └─> Acceptance Criteria: "Story complete in spirit AND letter, zero tech debt"

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
   └─> git pull --rebase (if push is rejected due to remote changes)
   └─> git -C /workspace/LLMSafeSpace push origin main

8. Worklog Creation
   └─> Create worklog (see Worklog Requirements section)
   └─> Content: Summary, stated assumptions + validation evidence,
       implementation details, validator findings + resolutions,
       test results, next steps
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

## PR Review Guide

Every PR must be reviewed against the rubric below before merging. Score each dimension 1–10; a score of **9 or higher** is required on every dimension. For each dimension, list specific remediation items needed to reach ≥9.

### Quality Rubric & Scoring

#### Robustness

**Definition:** Handles failures, partial states, and adversarial inputs without corruption or data loss.

| Score | Criteria |
|-------|----------|
| 1–3 | No error handling; panics on unexpected input; no recovery from partial failure |
| 4–6 | Basic error returns but some paths silently ignored; no retry/backoff; crashes on dependency failure |
| 7–8 | All errors handled explicitly; retry with backoff on transient failures; graceful degradation |
| 9–10 | Every failure mode enumerated and tested; circuit breakers; defensive coding against all inputs; provably correct under partial failure |

**To reach ≥9:**
- Verify every function handles its documented error returns
- Add integration tests for each dependency failure (DB down, Redis down, K8s API unreachable)
- Eliminate all silent error swallowing (`_ = fn()` without comment)
- Validate all external inputs at the boundary
- Confirm recovery from partial state (e.g., half-written CRD status → rollback or retry)

#### Scalability

**Definition:** Performance characteristics hold as load, data volume, and concurrency increase.

| Score | Criteria |
|-------|----------|
| 1–3 | O(n²) or worse on hot paths; no pagination; global locks on every request |
| 4–6 | Linear scans where indexed lookups exist; per-request expensive allocations; no connection pooling |
| 7–8 | Bounded loops; pagination on list endpoints; connection pooling; no per-request resource exhaustion |
| 9–10 | Verified O(1) or O(log n) on all hot paths; horizontal scalability demonstrated; no hidden N+1 queries; resource limits enforced |

**To reach ≥9:**
- Profile for N+1 query patterns (database and K8s API)
- Verify all list endpoints use pagination with configurable limits
- Check for unbounded goroutine creation or slice growth
- Confirm connection pools are sized and reused
- Ensure no per-request lock acquisition on shared resources

#### Maintainability

**Definition:** Code is readable, well-structured, and follows established patterns; a new contributor can modify it confidently.

| Score | Criteria |
|-------|----------|
| 1–3 | No tests; no doc comments; monolithic functions; inconsistent naming |
| 4–6 | Some tests but low coverage; mixed patterns; unclear data flow; magic numbers |
| 7–8 | Good test coverage; clear naming; small focused functions; follows project conventions |
| 9–10 | Self-documenting code; no unnecessary comments; consistent patterns throughout; a junior engineer can read and modify safely |

**To reach ≥9:**
- Verify all functions are reasonably small (≤50 lines or justified exceptions)
- Confirm naming follows Go conventions and project style
- Ensure no duplicate or near-duplicate code
- Check that every struct has a clear single responsibility
- Remove any TODOs, FIXMEs, or commented-out code

#### Reliability

**Definition:** Deterministic, repeatable behaviour; no flaky tests; consistent results across environments.

| Score | Criteria |
|-------|----------|
| 1–3 | Non-deterministic behaviour; race conditions; flaky tests ignored |
| 4–6 | Some races handled; tests occasionally flaky; no timeout on external calls |
| 7–8 | Race-free in normal operation; stable tests; timeouts on all external calls |
| 9–10 | Race-free at high concurrency; all tests pass consistently with `-race`; timeout and deadline propagation everywhere |

**To reach ≥9:**
- Run tests with `-race` and verify zero races
- Ensure all external calls have timeouts (`context.WithTimeout`)
- Fix any flaky tests; document if genuinely non-deterministic
- Verify no shared mutable state across goroutines without synchronisation
- Confirm idempotency of all mutation endpoints

#### Performance

**Definition:** Efficient use of CPU, memory, and I/O; no unnecessary pessimisation.

| Score | Criteria |
|-------|----------|
| 1–3 | Unbounded memory allocations; synchronous I/O on hot paths; no caching |
| 4–6 | Some caching but misses common patterns; unnecessary copies of large objects |
| 7–8 | Proper use of pointers, reuse, and pooling; async I/O where beneficial; cache headers |
| 9–10 | Benchmark-driven optimisation; zero-copy paths where possible; measured and documented trade-offs |

**To reach ≥9:**
- Check for unnecessary heap allocations in hot loops
- Verify JSON marshal/unmarshal is not on every response (cache when possible)
- Ensure no synchronous I/O inside a hot handler without justification
- Profile with realistic load before claiming performance is adequate

#### Security

**Definition:** Input validated, outputs sanitised, secrets never logged, least-privilege by default.

| Score | Criteria |
|-------|----------|
| 1–3 | No input validation; secrets logged; no auth on endpoints |
| 4–6 | Basic validation but bypassable; secrets may leak in error messages; broad permissions |
| 7–8 | All inputs validated at boundary; secrets filtered from logs; least-privilege RBAC |
| 9–10 | Defence in depth; no user data in error messages; injection-proof by construction; security tests for every control |

**To reach ≥9:**
- Verify no secrets appear in logs, error messages, or responses
- Check all user input is validated (length, type, range, allowed characters)
- Confirm permission checks happen in the service layer, not just the handler
- Ensure SQL injection is impossible (parameterised queries only)
- Add security-specific tests for every control (see Auth section)
- Verify rate limiting and body size limits are applied

#### Test Coverage & Quality

**Definition:** Tests exist at the right levels, cover happy+unhappy paths, and are reliable.

| Score | Criteria |
|-------|----------|
| 1–3 | No tests, or tests don't actually assert anything |
| 4–6 | Some unit tests but no unhappy paths; no integration tests |
| 7–8 | Good unit coverage + unhappy paths + integration/e2e tests; table-driven |
| 9–10 | Comprehensive coverage at all levels; TDD followed; tests run with `-race`; no flaky tests |

**To reach ≥9:**
- Verify table-driven tests cover both happy and unhappy paths
- Confirm e2e/integration tests exercise the real wiring (router → service → store)
- Ensure tests run cleanly with `-race` and `-count=1`
- Check for test utility functions that reduce boilerplate
- Verify no tests depend on external services without a mock/fake

#### SOLID Compliance

**Definition:** Follows Single Responsibility, Open/Closed, Liskov Substitution, Interface Segregation, and Dependency Inversion principles. Every type has one clear reason to change; abstractions are stable; dependencies flow inward.

| Score | Criteria |
|-------|----------|
| 1–3 | Violates multiple SOLID principles; god objects; concrete coupling everywhere; impossible to test in isolation |
| 4–6 | Some SRP violations; mixed abstraction levels; some coupling to concrete types; partial testability |
| 7–8 | Mostly SOLID; clear interfaces; dependency injection; small focused types; testable in isolation |
| 9–10 | Fully SOLID by construction; every type has one reason to change; abstractions are caller-shaped not implementation-shaped; high-level modules never import low-level details |

**To reach ≥9:**
- Verify every type has a single, clear responsibility (ask "what is the one thing this does?")
- Confirm interfaces are small (1–3 methods) and designed for the caller's need, not the implementation's
- Ensure no concrete type is depended on where an interface would serve
- Validate that adding a new variant (runtime environment, sandbox profile, auth provider) does not require modifying existing types (open/closed)
- Check that high-level policy modules (services, controllers) do not import low-level detail modules (database drivers, K8s client internals)

#### Right-Sized Complexity

**Definition:** The code is exactly as complex as it needs to be — no more (over-engineered), no less (under-engineered). Abstractions earn their keep. 10 is perfect; scores decrease in either direction.

| Score | Criteria |
|-------|----------|
| 10 | Perfectly sized — abstraction level matches the problem; every interface has ≥2 implementations or a clear imminent need; no speculative generality; a junior engineer can follow the flow |
| 7–9 | Slightly off — one unnecessary abstraction layer OR one missing abstraction that would simplify callers. Functions and type boundaries are mostly right |
| 4–6 | Noticeably off — speculative abstractions with no current consumer, or monoliths that should be split. Multiple indirection layers without value |
| 1–3 | Severely wrong — framework-in-disguise (unnecessary factories/visitors/strategies for a simple CRUD path), or giant monolithic functions with no decomposition. Actively reduces productivity |

**To reach ≥9:**
- For every interface, ask: "Does this have (or will it imminently have) ≥2 implementations, or is it speculative generality?"
- For every function >30 lines, ask: "Can this be decomposed without forcing the reader to hold more state in their head?"
- Remove any abstraction that has exactly one concrete implementation and no second implementation planned
- Verify that adding a new feature requires adding code (new types, new files), not modifying the abstraction layer
- Confirm the simplest correct solution was chosen — not the most general, not the most clever

### E2E Wiring Verification

Beyond scoring individual dimensions, every PR must verify that all expected user workflows and system pathways are fully wired end-to-end. "Wired" means the code is connected through the full request path — entry point, middleware, service/controller logic, data store interaction, response propagation, and error handling at every step.

#### Process

1. **List every expected workflow** affected by this PR:
   - User-facing operations (create sandbox, send message, suspend workspace, etc.)
   - System operations (reconciliation loop, webhook validation, credential injection, etc.)
   - Background operations (cache eviction, metrics collection, health checks)
   - Error/recovery paths (dependency failure, invalid state, timeout)

2. **For each workflow, trace the full path:**
   - Entry point (REST endpoint, CRD event, CLI command, timer)
   - Middleware/authorisation layer
   - Service/controller logic
   - Data store interaction (DB, Redis, K8s API)
   - Response or propagation back to caller
   - Error handling and rollback at every step

3. **Confirm wiring with evidence:**
   - Integration test that exercises the real path (router → service → store)
   - Or, for paths that cannot be integration-tested, a documented manual verification with output
   - **"It compiles" or "unit tests pass" is NOT sufficient** — the actual wiring must be demonstrated

4. **Identify and flag unwired code:**
   - Any handler, service, or function that was built but never called from a live request path
   - Any code path guarded by a dead conditional (env var never set, feature flag never enabled)
   - These are not acceptable — either wire them or remove them

5. **Common wiring failures to check:**
   - New handler not registered in the router
   - New service not initialised in the service bootstrap (`services.go`)
   - New CRD type not registered in the scheme
   - New reconciler not added to the controller setup
   - New migration not included in the startup sequence
   - New middleware not added to the chain
   - New error type not handled in the error handler middleware
   - New permission not checked in the authorisation layer
   - New mock missing a method (silent no-op in tests)

This verification must be documented in the final PR review report. Unwired code is dead code and is not acceptable.

### Adversarial Assessment

In addition to the rubric scoring, every PR must undergo a structured adversarial review (see also [Rule 11 — Adversarial Self-Review](#11-adversarial-self-review)). This is a mandatory validation gate.

#### Phase 1: Identify Weaknesses, Gaps, and Failure Modes

Assume the code is wrong until proven otherwise. Proactively search for:

1. **Architectural gaps:** What scenarios did the design not cover? What happens when system state doesn't match the designer's expectations?
2. **Failure modes:** Under what conditions will this code fail? Consider:
   - Concurrency (two requests at once, race conditions, stale reads)
   - Partial failure (DB write succeeds but K8s write fails, or vice versa)
   - Resource exhaustion (OOM, disk full, too many open files, connection pool exhausted)
   - Invalid state (CRD in unexpected phase, orphaned resources, missing labels)
   - Timing dependencies (operation A must complete before B, but nothing enforces ordering)
   - Adversarial input (malformed JSON, very long strings, unexpected types, injection attempts)
3. **Wrong assumptions:** Every assumption the code relies on — list each one and ask "what if this is false?" (see [Rule 7 — Assumptions: State, Then Validate](#7-assumptions-state-then-validate))
4. **Incorrectness:** Places where the code does the wrong thing even when inputs are valid:
   - Wrong status code returned
   - Data mutated without authorisation
   - Rollback not performed when a multi-step operation fails mid-way
   - Resource leak (goroutine, file handle, DB connection, K8s watch)
5. **Omitted requirements:** Features the PR should have but doesn't:
   - Missing input validation
   - Missing authentication/authorisation checks
   - Missing logging for debugging
   - Missing metrics for monitoring
   - Missing timeout/deadline propagation

#### Phase 2: Validate Each Finding

After generating the adversarial findings list, validate every single one:

1. **Is the finding real?** Re-read the code, re-run the test, reproduce the scenario. Do not take any finding at face value.
2. **Is it a bug, a design flaw, or a false alarm?**
   - **Real bug:** Fix it before proceeding. Do not defer.
   - **Design flaw:** Surface with proposed remediation. Do not merge without addressing.
   - **False alarm:** Document why it is not a real issue (one sentence with evidence). Do not silently dismiss.
3. **If uncertain:** Escalate to the user/stakeholder rather than dismissing or guessing.
4. **Only validated findings go into the final report.** Unvalidated claims are discarded — they have no place in a review.

#### Phase 3: Final Report

The final PR review report must contain:

- Scores for each quality dimension (1–10) with specific remediation items
- E2E wiring verification results — which workflows were traced, evidence for each, and any unwired code identified
- List of validated adversarial findings (real bugs and design flaws)
- List of false alarms with rationale for each
- A pass/fail recommendation — fail unless all real findings are fixed, no unwired code exists, and all dimensions score ≥9

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
- Multiple unhappy path cases (errors, invalid inputs, dependency failures)
- Edge cases (empty fields, nil slices, very long strings, invalid inputs)
- Error conditions
- **End-to-end integration tests** that exercise the real wiring (router → service → store/cluster). A task is not complete until an e2e/integration test demonstrates the change works as part of the system, not just in isolation.

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

When modifying CRD types in `controller/internal/resources/*_types.go`, you must regenerate the DeepCopy implementations:

```bash
# From project root
make deepcopy

# Verify and commit generated changes
git add controller/internal/resources/*_deepcopy.go
git commit -m "Update generated DeepCopy code"
```

`pkg/types/types.go` contains API transfer objects only — no generated deepcopy. Manual `DeepCopy` methods are implemented only where needed (types passed by pointer across goroutine boundaries).

---

## Authentication & Authorization

### Endpoints

| Endpoint | Method | Auth | Purpose |
|----------|--------|------|---------|
| `/api/v1/auth/register` | POST | Public | Create user, return JWT |
| `/api/v1/auth/login` | POST | Public | Email+password login, return JWT |
| `/api/v1/auth/api-keys` | POST | JWT/API Key | Generate `lsp_`-prefixed API key |
| `/api/v1/auth/api-keys` | GET | JWT/API Key | List user's API keys (secrets stripped) |
| `/api/v1/auth/api-keys/:id` | DELETE | JWT/API Key | Revoke an API key |

### Security Controls

| Control | Implementation | Validated By |
|---------|---------------|-------------|
| Password hashing | bcrypt cost 12 | `auth_test.go:TestRegister_Success` |
| Email enumeration prevention | Identical generic errors for duplicate email, wrong password, nonexistent user, inactive user | `router_auth_security_test.go:TestRegister_DuplicateEmail_GenericError`, `TestLogin_WrongPassword_GenericError`, `TestLogin_InactiveUser_GenericError` |
| Password never in response | `json:"-"` on `User.PasswordHash`; verified in e2e tests | `TestRegister_PasswordNotInResponse`, `TestLogin_PasswordNotInResponse` |
| API key secrets stripped on list | `ListAPIKeys` zeroes `Key` field before return | `TestListAPIKeys_SecretsStripped` |
| API key secret returned only on creation | `CreateAPIKey` returns full key; `ListAPIKeys` strips it | `TestCreateAPIKey_SecretOnlyOnCreation` |
| Body size limits | `http.MaxBytesReader` (1 MiB) on all auth endpoints | `TestRegister_BodyTooLarge_Rejected` |
| Sanitized binding errors | Binding failures return generic "invalid request body" | `TestRegister_InvalidJSON_SanitizedError` |
| No internal error leakage | Service errors return generic messages; details logged server-side only | `TestRegister_DuplicateEmail_GenericError` |
| JWT includes `jti` claim | Enables per-token revocation (not per-user) | `auth_test.go:TestGenerateToken` |
| API keys use `crypto/rand` | 32-byte random keys with `lsp_` prefix | `auth_test.go:TestCreateAPIKey_Success` |
| JWT cache keys hashed before Redis storage | `hashToken()` uses MD5 to prevent raw JWT exposure in Redis | `auth.go:hashToken` |
| Token extraction: header-only by default | Query param and cookie extraction disabled | `token_extractor_test.go:Query parameter disabled by default` |
| Rate limiter wired into global middleware stack | `ratelimit.Service` backed by Redis + in-memory token bucket | `ratelimit_test.go` |
| Rate limiter IP fallback | Falls back to `c.ClientIP()` when no API key in context | `rate_limit.go:54-58` |
| Rate limiter IP fallback | Falls back to `c.ClientIP()` when no API key in context | `rate_limit.go:54-58` |
| Protected endpoints require auth | API key CRUD behind `AuthMiddleware()` | `TestAPIKeyEndpoints_RequireAuth` |
| Wrong HTTP method rejection | Only POST on register/login, returns 404 | `TestRegister_RejectsGet`, `TestLogin_RejectsGet` |

### E2E Testing

Go tests: `go test -race ./api/internal/server/... -run "TestRegister|TestLogin|TestCreateAPIKey|TestListAPIKeys|TestDeleteAPIKey|TestAPIKeyEndpoints"`

Shell script against running server: `./local/test-auth.sh http://localhost:8080`

---

## Sandbox API

The API exposes full CRUD for Sandboxes (replacing the previous kubectl-only flow).

### Endpoints

| Endpoint | Method | Auth | Purpose |
|----------|--------|------|---------|
| `/api/v1/sandboxes` | GET | API key/JWT | List the caller's sandboxes (paginated: `?limit=&offset=`) |
| `/api/v1/sandboxes` | POST | API key/JWT | Create a sandbox; body is `types.CreateSandboxRequest` |
| `/api/v1/sandboxes/:id` | GET | API key/JWT | Get one sandbox (returns 404 if user does not own it) |
| `/api/v1/sandboxes/:id` | DELETE | API key/JWT | Terminate (deletes pod + CRD + DB metadata) |
| `/api/v1/sandboxes/:id/status` | GET | API key/JWT | Get phase + pod IP + resource usage |

### Authorization model

Sandbox CRUD is wired on a **separate** Gin group from the proxy group (`registerSandboxCRUDRoutes` in `api/internal/server/router.go`). It does **not** apply the proxy's `sandboxOwnershipMiddleware` because:

1. List/Create have no `:id` to check
2. Service-level methods (`GetSandbox`, `TerminateSandbox`) perform their own ownership checks
3. The GET handler additionally compares `sb.Labels["user-id"]` to the authenticated user — sandboxes the user does not own return 404 (not 403; do not leak existence)

### Request flow

```
POST /api/v1/sandboxes
  → sanitizeBindError on bad JSON → 400
  → CreateSandbox(ctx, req)
      → validate req
      → check user exists in DB
      → check permission "sandbox:create"
      → if no workspaceRef, auto-create workspace
      → build CRD; Create(crd) in K8s
      → CreateSandbox(meta) in DB; on failure delete CRD
      → return *types.Sandbox (201 Created)
```

### Body shape

```go
type CreateSandboxRequest struct {
    Runtime       string                `json:"runtime"`        // required: e.g. "base", "python:3.11"
    SecurityLevel string                `json:"securityLevel,omitempty"`
    Timeout       int                   `json:"timeout,omitempty"`
    UserID        string                `json:"userId"`         // overwritten by auth context
    Resources     *ResourceRequirements `json:"resources,omitempty"`
    NetworkAccess *NetworkAccess        `json:"networkAccess,omitempty"`
    WorkspaceRef  string                `json:"workspaceRef,omitempty"`
}
```

The router always overwrites `UserID` with the authenticated user from the JWT/API key context; clients cannot impersonate.

---

## Session Proxy

The session endpoints are reverse-proxied to the sandbox pod's `opencode serve` instance on port 4096 (HTTP basic auth `opencode:<password from sandbox-pw-<id> Secret>`).

### Endpoints

| Endpoint | Method | Opencode target |
|----------|--------|-----------------|
| `/api/v1/sandboxes/:id/sessions` | POST | `POST /session` |
| `/api/v1/sandboxes/:id/sessions` | GET | `GET /session` |
| `/api/v1/sandboxes/:id/sessions/:sessionId/message` | POST | `POST /session/:id/message` |
| `/api/v1/sandboxes/:id/sessions/:sessionId/prompt` | POST | `POST /session/:id/prompt_async` |
| `/api/v1/sandboxes/:id/sessions/:sessionId/message` | GET | `GET /session/:id/message` |
| `/api/v1/sandboxes/:id/sessions/:sessionId/abort` | POST | `POST /session/:id/abort` |
| `/api/v1/sandboxes/:id/events` | GET | `GET /event` (SSE) |

All proxy routes pass through `sandboxOwnershipMiddleware`, which loads the Sandbox CRD, verifies `sb.Labels["user-id"]` matches the authenticated user, and caches the CRD on `c.Set("sandbox", sb)` to avoid a second K8s read in the proxy handler.

### `?verbose=true` flag

opencode emits a `patch` part on every assistant turn listing every workspace file it touched (`/workspace/.local/opencode/snapshot/...`). Each one is ~2 KB of internal snapshot paths and is rarely useful to the caller.

The proxy strips parts where `type == "patch"` from `SendMessage` and `GetHistory` responses by default. Pass `?verbose=true` to disable filtering.

| Flag | Behavior |
|------|----------|
| (default) | `parts[]` filtered: `patch` entries removed |
| `?verbose=true` | `parts[]` returned unmodified |
| `?verbose=false` (or any other value) | Same as default — strip patch parts |

The `verbose` query parameter is consumed by the proxy and **must not** be forwarded to opencode (it would be ignored, but stripping prevents future opencode versions from rejecting it as unknown). See `stripVerboseQuery` in `api/internal/handlers/proxy.go`.

The filter only runs when:

- The handler called `proxyToSandbox(..., filterParts=true)` (only `SendMessage` and `GetHistory`)
- The response `Content-Type` contains `application/json`
- The response status is 2xx

For non-JSON or non-2xx responses, the body is streamed unmodified. SSE streaming endpoints (`/events`, `/prompt_async`) always pass `filterParts=false` and are never buffered.

### Implementation notes

- `stripPatchParts(body []byte) ([]byte, error)` handles both opencode response shapes:
  - `{info, parts: [...]}` for `POST /message`
  - `[{info, parts: [...]}, ...]` for `GET /message` (history)
- Filtering uses `json.RawMessage` for unknown fields so the round-trip is lossless except for the explicitly removed parts
- On filter-time JSON parse failure, the original bytes are returned with a warning logged (defensive: never lose the response)

---

## Configuration Reference

The API service is configured via `api/config/config.yaml` with environment variable overrides via Viper.

| Section | Key | Default | Env Var | Description |
|---------|-----|---------|---------|-------------|
| `server` | `host` | `0.0.0.0` | `LLMSAFESPACE_SERVER_HOST` | Listen address |
| `server` | `port` | `8080` | `LLMSAFESPACE_SERVER_PORT` | Listen port |
| `server` | `shutdownTimeout` | `30s` | — | Graceful shutdown timeout |
| `kubernetes` | `inCluster` | `true` | — | Use in-cluster config |
| `kubernetes` | `namespace` | `llmsafespace` | — | Default namespace |
| `database` | `host` | `postgres` | — | PostgreSQL host |
| `database` | `port` | `5432` | — | PostgreSQL port |
| `database` | `password` | (empty) | `LLMSAFESPACE_DATABASE_PASSWORD` | PostgreSQL password |
| `database` | `maxOpenConns` | `25` | — | Max open connections |
| `redis` | `host` | `redis` | — | Redis host |
| `redis` | `port` | `6379` | — | Redis port |
| `redis` | `password` | (empty) | `LLMSAFESPACE_REDIS_PASSWORD` | Redis password |
| `redis` | `poolSize` | `20` | — | Connection pool size |
| `auth` | `jwtSecret` | (empty) | `LLMSAFESPACE_AUTH_JWTSECRET` | JWT signing secret (required) |
| `auth` | `tokenDuration` | `24h` | — | Token expiry |
| `auth` | `apiKeyPrefix` | `lsp_` | — | API key prefix |
| `auth` | `lockoutEnabled` | `false` | `LLMSAFESPACE_AUTH_LOCKOUTENABLED` | Enable account lockout after failed logins |
| `auth` | `lockoutAttempts` | `0` | `LLMSAFESPACE_AUTH_LOCKOUTATTEMPTS` | Failed attempts before lockout (e.g. `5`) |
| `auth` | `lockoutDuration` | `0` | `LLMSAFESPACE_AUTH_LOCKOUTDURATION` | Lockout duration (e.g. `15m`) |
| `security` | `allowedOrigins` | (empty) | `LLMSAFESPACE_SECURITY_ALLOWEDORIGINS` | Comma-separated CORS origins (e.g. `https://app.example.com,https://admin.example.com`) |
| `security` | `allowCredentials` | `false` | `LLMSAFESPACE_SECURITY_ALLOWCREDENTIALS` | Allow credentials in CORS |
| `rateLimiting` | `enabled` | `false` | `LLMSAFESPACE_RATELIMITING_ENABLED` | Enable rate limiting |
| `rateLimiting` | `defaultLimit` | `100` | `LLMSAFESPACE_RATELIMITING_DEFAULTLIMIT` | Requests per window |
| `rateLimiting` | `defaultWindow` | `1m` | `LLMSAFESPACE_RATELIMITING_DEFAULTWINDOW` | Window duration |
| `rateLimiting` | `burstSize` | `20` | `LLMSAFESPACE_RATELIMITING_BURSTSIZE` | Burst allowance |
| `logging` | `level` | `info` | — | Log level |
| `logging` | `encoding` | `json` | — | Log format (json/console) |

---

## Version History

| Version | Date | Changes |
|---------|------|---------|
| 1.11 | 2026-06-08 | Added Relay Config Subsystem section: confirmed bugs (Bug 1 relay clobber, Bug 2 enricher cache, Bug 3 personal key routing, Bug 4 cascade failure), volume layout, opencode config merge order, design (relay-state.json, WriteAgentConfig single writer, re-triggerable injector, credential fingerprint, defaultModel resolution), and Gap 5/6 fixes with implementation checklist |
| 1.10 | 2026-06-04 | Added PR Review Guide with 1–10 rubric scoring for robustness, scalability, maintainability, reliability, performance, security, test coverage, SOLID compliance, and right-sized complexity — each with remediation steps to reach ≥9; added E2E wiring verification section (workflow tracing, evidence requirements, common wiring failures); added adversarial assessment section with Phase 1 (identify weaknesses/gaps/failure modes), Phase 2 (validate each finding), Phase 3 (final report); expanded Rule 11 with three-phase structure and "only validated findings" rule; cross-referenced Rule 7 (Assumptions) and Rule 11 throughout |
| 1.9 | 2026-05-27 | Frontend streaming UX fixes (user echo, thinking blocks, bubble overflow); SSE format unwrapping; tested against real cluster; 369 frontend tests passing |
| 1.8 | 2026-05-23 | Engineering principles (SOLID/robust/secure/idiomatic/not over-engineered) added to Rule 4; new Rule 7 mandates stating and validating assumptions; TDD now requires happy + unhappy + e2e integration tests with explicit definition of done; orchestrator workflow restructured around a mandatory skeptical-validator → fix → re-validate loop with false-alarm triage |
| 1.5 | 2026-05-23 | Sandbox CRUD via API (`/api/v1/sandboxes`), `?verbose=true` flag (strips opencode `patch` parts by default), README.md rewritten for V2 |
| 1.4 | 2026-05-23 | Rate limiting wired, CORS hardened (no wildcard+credentials), account lockout, all configurable via env vars |
| 1.3 | 2026-05-23 | Auth endpoints (register, login, API key CRUD) with security hardening and e2e tests |
| 1.2 | 2026-05-22 | Repository structure, architecture, CRD ownership table, tech stack, and code generation section fully aligned with EVOLUTION-V2.md |
| 1.1 | 2026-05-22 | Updated for V2 architecture: warm pools removed, workspace/agent model, MCP server, proxy architecture |
| 1.0 | 2026-05-21 | Initial creation |
