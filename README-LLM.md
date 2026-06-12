# LLMSafeSpace — LLM Implementation Guide

> **Repository:** `github.com/lenaxia/llmsafespace`

**Version:** 1.13
**Last Updated:** 2026-06-12
**Project Status:** Active Development

---

## Table of Contents

1. [Project Overview](#project-overview)
2. [Critical Guidelines & Hard Rules](#critical-guidelines--hard-rules)
3. [Repository Structure](#repository-structure)
4. [Architecture Overview](#architecture-overview)
5. [Relay Config Subsystem](#relay-config-subsystem)
6. [Storage Settings](#storage-settings)
7. [Technology Stack](#technology-stack)
8. [Worklog Requirements](#worklog-requirements)
9. [Development Workflow](#development-workflow)
10. [Multi-Agent Workflow](#multi-agent-workflow)
11. [PR Review Guide](#pr-review-guide)
12. [Common Commands](#common-commands)
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

1. `api` — Go API service (Gin) + MCP server — reverse proxy to workspace agents, workspace/credential/secret management, session tracking, event streaming
2. `controller` — Kubernetes operator (controller-runtime) — manages Workspace CRD (pod lifecycle, PVC, credentials, health monitoring via agentd sidecar), validating webhooks for Workspace and RuntimeEnvironment
3. `runtimes` — Container images (Python, Node.js, Go) — hardened environments with `opencode serve`, `redact` binary, credential injection

**Authoritative design document:**

- [`design/0021_2026-05-21_evolution-v2.md`](design/0021_2026-05-21_evolution-v2.md) — V2 architecture (v2.4). Supersedes all V1 design docs for the areas it covers.

**V1 design docs (reference only — superseded by evolution-v2.md where they conflict):**

- `design/0001_2025-03-05_architecture.md` — System overview, deployment topology, security model
- `design/0003_2025-03-05_controller.md` — Controller specification (V1 CRDs, reconciliation loops)
- `design/0005_2025-03-05_security.md` — Defense-in-depth security model
- `design/0007_2025-03-05_network.md` — Network policy design and egress filtering
- `design/0006_2025-03-05_runtimeenv.md` — Runtime environments

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
| **V2 Architecture** | `design/0021_2026-05-21_evolution-v2.md` (authoritative) |
| V2 Implementation stories | `design/stories/` |
| Security model | `design/0027_2026-05-24_security-policy-v21.md`, `design/0021 §9` |
| System overview (V1) | `design/0001_2025-03-05_architecture.md` |
| Controller + CRDs (V1) | `design/0003_2025-03-05_controller.md` |
| Runtime environments (V1) | `design/0006_2025-03-05_runtimeenv.md` |
| Network policies (V1) | `design/0007_2025-03-05_network.md` |

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
├── cmd/           # Top-level binaries (api, mcp, redact, repolint, seal-key, workspace-agentd)
├── api/           # Go API service (Gin) + MCP server — reverse proxy, workspace/credential/secret management
├── controller/    # Kubernetes operator (controller-runtime) — Workspace reconciler, validating webhooks
├── runtimes/      # Container images (Python, Node.js, Go) with opencode serve, redact binary
├── pkg/           # Shared packages imported by api/ and controller/ (see CRD type ownership below)
├── mocks/         # Shared test mocks
├── sdks/          # Client SDKs (Go, TypeScript, Python, Java, VS Code extension) from OpenAPI spec
├── workers/       # Cloudflare Workers (inference-relay)
├── frontend/      # React 19 + TypeScript + Vite SPA
├── charts/        # Helm chart (25 templates, 662-line values.yaml)
├── design/        # Design documents — 0021_evolution-v2.md is authoritative for V2
├── hack/          # Build and code generation scripts
├── local/         # kind bootstrap/test/teardown scripts
├── tests/         # End-to-end integration tests
└── .github/       # CI/CD workflows + AI prompt templates
```

**Before editing:** Read each folder's `README.md` for rules and conventions. Folders missing a `README.md` should have one added.

**CRD type ownership:** `pkg/apis/llmsafespace/v1/` holds authoritative kubebuilder-annotated CRD types (Workspace, RuntimeEnvironment). `pkg/types/` holds API transfer objects only (request/response DTOs) — not CRD schemas. These must not be merged.

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
│   │  │ Workspace│  │  Proxy   │  │  Secrets  │  │  MCP Server      │  │   │
│   │  │ Service  │  │ Handler  │  │  Service  │  │  (stdio/SSE)     │  │   │
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
│   │  │  ┌──────────────────┐  ┌────────────────────────────────────┐ │ │   │
│   │  │  │  Workspace       │  │  Validating Webhooks               │ │ │   │
│   │  │  │  Reconciler      │  │  (Workspace + RuntimeEnvironment)  │ │ │   │
│   │  │  └──────────────────┘  └────────────────────────────────────┘ │ │   │
│   │  └───────────────────────────────────────────────────────────────┘ │   │
│   │                                                                     │   │
│   │  ┌───────────────────────────────────────────────────────────────┐ │   │
│   │  │  Workspace Pods (each runs opencode serve :4096)             │ │   │
│   │  │  ┌──────────────────┐  ┌──────────────────┐                  │ │   │
│   │  │  │ init: workspace- │  │ init: credential- │                  │ │   │
│   │  │  │ setup (packages, │  │ setup (creds →    │                  │ │   │
│   │  │  │ initScript)      │  │ /sandbox-cfg)     │                  │ │   │
│   │  │  ├──────────────────┤  └──────────────────┘                  │ │   │
│   │  │  │ main: opencode serve --hostname 0.0.0.0 --port 4096       │ │   │
│   │  │  │ security: readOnlyRoot, runAsNonRoot, drop ALL caps        │ │   │
│   │  │  └──────────────────────────────────────────────────────────┘  │ │   │
│   │  │  Volumes: PVC at /workspace (subPath:workspace) + /home/sandbox (subPath:home) + /tmp (subPath:tmp) + emptyDir /sandbox-cfg  │ │   │
│   │  └───────────────────────────────────────────────────────────────┘ │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│   ┌─────────────────────┐  ┌─────────────────┐                              │
│   │ PostgreSQL           │  │ Redis            │                              │
│   │ (user metadata,      │  │ (caching, rate   │                              │
│   │  workspace names,    │  │  limiting, DEK    │                              │
│   │  secrets, settings)  │  │  cache)           │                              │
│   └─────────────────────┘  └─────────────────┘                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Custom Resource Definitions

The controller manages 2 CRDs in the `llmsafespace.dev/v1` API group:

| CRD | Kind | Scope | Short | Purpose |
|-----|------|-------|-------|---------|
| `workspace.yaml` | `Workspace` | Namespaced | `ws` | PVC-backed persistent environment + pod running `opencode serve` |
| `runtimeenvironment.yaml` | `RuntimeEnvironment` | Cluster | `rte` | Defines a runtime image (Python, Node.js, Go) |

V1 CRDs (Sandbox, SandboxProfile, WarmPool, WarmPod) have been removed. The Workspace CRD absorbs all sandbox and profile functionality.

### CRD type ownership

CRD types exist in two locations with strictly separate roles:

| Location | Purpose |
|----------|---------|
| `pkg/apis/llmsafespace/v1/` | **Authoritative** — kubebuilder-annotated CRD types (Workspace, RuntimeEnvironment), used by both controller and API service |
| `pkg/types/` | **API transfer objects only** — REST request/response shapes (`CreateWorkspaceRequest`, etc.). Not CRD schemas. |

These are intentionally different types. The API types are transfer objects; the CRD types are Kubernetes schemas. They must not be merged.

### Workspace lifecycle (V2)

```
Pending → Creating → Active → Suspending → Suspended → Resuming → Active
             │                   ↘           ↘           ↘
             └──→ Failed           Terminating            Terminating
                                      ↘                       ↘
                                    Terminated              Terminated
```

Nine phases: `Pending`, `Creating`, `Active`, `Suspending`, `Suspended`, `Resuming`, `Terminating`, `Terminated`, `Failed`.

Suspend deletes the pod but retains the PVC. Activating a suspended workspace re-creates the pod (~3s). Session history in the PVC survives.

### State management: K8s CRD vs PostgreSQL

| Data | Owner | Source of Truth |
|------|-------|-----------------|
| Workspace phase | Controller | K8s CRD status |
| PVC name, pod IP | Controller | K8s CRD status |
| Conditions | Controller | K8s CRD status |
| `status.lastActivityAt` (workspace) | API server (batched, ≤60s flush) | K8s CRD status |
| Workspace display name | API | PostgreSQL |
| User ID ownership | Both | K8s CRD (`spec.owner`) authoritative; PostgreSQL mirrors for query perf |
| Creation/update timestamps | Both | K8s CRD authoritative; PostgreSQL mirrors |
| Credentials | Controller | K8s Secrets (never PostgreSQL) |
| User auth data (passwords, API keys, DEKs) | API | PostgreSQL |
| Encrypted secrets | API | PostgreSQL (zero-knowledge encrypted) |
| Settings | API | PostgreSQL |

### Service initialization order

The API service starts dependencies in a specific order with rollback on failure:

```
Metrics → Database → Cache → Auth → Workspace → SessionIndex → Secrets → Settings → ProviderCredentials
```

Shutdown reverses this order.

---

## Relay Config Subsystem

### Overview

The relay config subsystem manages how `agent-config.json` — the file opencode reads for provider credentials — is built and kept correct across the pod lifetime. Multiple processes write to this file, which has been the source of several confirmed production bugs.

**Volume layout on every workspace pod:**

| Mount | Type | Persists across pod restart? | Owner |
|---|---|---|---|
| `/workspace` | Longhorn PVC (`subPath: workspace`) | Yes | User workspace data, opencode.db, auth.json |
| `/home/sandbox` | Longhorn PVC (`subPath: home`) | Yes | SSH keys, secrets base dir, enricher cache, tool caches |
| `/tmp` | Longhorn PVC (`subPath: tmp`) | Yes — agentd rewrites `agent-config.json` and `secrets-env` on each credential cycle; other files persist | agent-config.json, secrets-env |
| `/sandbox-cfg` | emptyDir (memory, ro) | No — ephemeral per pod, read-only | Secrets mounted by controller at pod start |

**Key path constants** (`pkg/agentd/types.go`):

```
AgentConfigPath  = "/tmp/agent-config.json"
SecretsBasePath  = "/home/sandbox/.secrets"   ← deleted by reset() on every reload
SecretsEnvPath   = "/tmp/secrets-env"
```

Note: `/tmp` is now a PVC subPath (`subPath: tmp`), not an emptyDir. The `workspace-dirs` init container unconditionally creates this directory on every pod start. The agentd `Materializer.reset()` deletes and rewrites `agent-config.json` and `secrets-env` on each credential cycle, so those specific files are always freshly written. Other files written to `/tmp` by packages or agent processes persist across pod restarts.

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

### Known design fragilities (documented, not bugs)

1. **Multiple writers of agent-config.json.** The four-writer design is correct given the current boot sequence and `reloadMu` serialisation, but it is fragile. A future change that reorders the boot sequence or adds a new write path could reintroduce relay clobbering. The single-writer `WriteAgentConfig` design (described in previous versions of this section) would eliminate this fragility but requires a non-trivial refactor of `FormatOpenCodeConfig`, the reload handler, and the relay injector. Tracked as a future cleanup item.

2. **One-shot relay injector.** The injector goroutine runs once per pod lifetime. If the opencode credential changes after the injector has run (personal key → public key), the relay is not re-evaluated. The user must restart the pod. A re-triggerable injector (channel-based state machine) would handle this automatically.

3. **In-memory model cache is per-API-replica.** `SetModel` evicts on the replica that handled the request; other replicas serve stale data for up to 5 seconds. Future: Redis-backed cache for cross-replica consistency (US-30.11).

4. **`resolveModelWithProvider` non-determinism on collision.** When two providers in `agent-config.json` share a model ID, Go map iteration is non-deterministic — `resolveModelWithProvider` returns whichever provider the runtime visits first. In practice, provider model IDs are namespaced and do not collide, but this is not enforced.

---

### How the relay config subsystem works (as-built)

The relay config subsystem has four writers of `agent-config.json`. All coordination is via:
1. `reloadMu sync.Mutex` in `reloadSecretsHandler` — serialises concurrent reload calls
2. `atomic.Pointer[[]relayModel]` in `relay_injector.go` — the relay injector sets this on success; reloadSecretsHandler reads it to decide whether to re-merge relay after FlushProviders
3. opencode reads `agent-config.json` once at startup — not hot-reloaded

#### Agent-config.json write sequence (boot)

1. `Materializer.reset()` deletes agent-config.json → `FlushProviders()` writes provider credentials → `applyWorkspaceConfig()` adds model key with providerID/modelID
2. ~T+7s: `startRelayInjector()` fetches free models → merges relay config → writes file → sets `activeRelayModels` → restarts opencode

#### Agent-config.json write sequence (credential reload)

1. `reloadMu.Lock()` → `Materializer.reset()` → `FlushProviders()` rewrites credentials → if relay active: `buildRelayConfig()` re-merges relay block → `reloadMu.Unlock()`
2. `proc.restart()` reboots opencode with updated config

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

#### annotateModels remap — dead code (tech debt to remove)

The remap guard `relayGloballyEnabled && relayInjected && p.ID=="opencode"` is unreachable in Phase 2 (because `disabled_providers` removes `opencode` from `connected[]`) and correctly suppressed in Phase 1. The code is defense-in-depth for a hypothetical future opencode change but is effectively dead code. Should be removed as tech debt — the `disabled_providers` mechanism is the correct solution.

---

## Storage Settings

### Settings involved

| Setting key | Schema default | Admin UX label | Where enforced |
|---|---|---|---|
| `workspace.defaultStorageSize` | `15Gi` | Default Storage | API service at workspace create time |
| `workspace.defaultStorageClass` | `""` | Storage Class | API service at workspace create time |

Both are Tier 2 (admin-mutable) `instance_settings` entries stored in PostgreSQL and served by the settings service (`pkg/settings/instance_service.go`). The admin UX reads them via `GET /admin/settings` and writes via `PUT /admin/settings/{key}` (`api/internal/handlers/settings.go`).

**Removed settings:**
- `workspace.maxStorageSize` — removed. PVC size is set once at creation and never changed; the admission webhook (`webhooks.maxWorkspaceStorageGi: 1024 Gi` in `values.yaml`) is the correct infrastructure-level ceiling. A dynamic DB-backed cap that only applied to the API path added complexity without meaningful safety.
- `workspace.defaultResources.ephemeralStorage` — removed. With `readOnlyRootFilesystem: true` and all writable paths on PVC subPaths or `Medium: Memory` emptyDirs, ephemeral storage is consumed only by kubelet container log files. This is governed by kubelet's own log rotation (default 10 Mi × 5 files = 50 Mi), not by a tunable admin setting. The pod builder hardcodes `"1Gi"` as the ephemeral limit (`controller/internal/workspace/pod_builder.go:232`).

### `workspace.defaultStorageSize` — full trace

1. **Frontend** (`frontend/src/api/workspaces.ts`): `storageSize` is intentionally omitted from the create workspace payload — the API resolves the default.
2. **API service** (`api/internal/services/workspace/workspace_service.go`): on `CreateWorkspace`, if `req.StorageSize` is empty, `instanceSettings.GetString(ctx, "workspace.defaultStorageSize")` supplies it.
3. The resolved size is written into `WorkspaceSpec.Storage.Size` in the CRD, persisted to the `workspace_metadata` PostgreSQL table, and returned in API responses as `storageSize`.

**Side effects of changing `defaultStorageSize`:**
- Affects only **new** workspaces. Existing PVCs are never resized.
- Takes effect immediately on the next workspace creation — no redeploy needed.
- The hard ceiling is `webhooks.maxWorkspaceStorageGi` (default `1024 Gi`, Helm value) enforced at the Kubernetes admission layer for all paths including direct `kubectl apply`.

### Ephemeral storage — hardcoded, not admin-configurable

The pod builder sets `ephemeral-storage` request and limit to `"1Gi"` (hardcoded at `controller/internal/workspace/pod_builder.go:232`). This limit exists solely to cap **container log volume** on node disk. With `readOnlyRootFilesystem: true` and all writable paths on the PVC, the container overlay filesystem receives no writes; the only ephemeral storage consumer is kubelet's stdout/stderr log files, which kubelet's own rotation already caps at ~50 Mi.

**What contributes to ephemeral storage on a workspace pod:**

| Source | Counts toward ephemeral storage? | Notes |
|---|---|---|
| Container writable layer (overlay FS) | No | `readOnlyRootFilesystem: true` — EROFS for all unmounted paths |
| Container log files (stdout/stderr) | **Yes** | Kubelet writes to `/var/log/pods/` on node disk; kubelet rotation caps at ~50 Mi |
| `/tmp` (PVC `subPath: tmp`) | No | PVC-backed |
| `/workspace` (PVC `subPath: workspace`) | No | PVC-backed |
| `/home/sandbox` (PVC `subPath: home`) | No | PVC-backed |
| `/sandbox-cfg` (emptyDir, `Medium: Memory`) | No | Counts toward memory, not ephemeral storage |

The webhook cap (`webhooks.maxWorkspaceEphemeralStorageGi: 100` in `values.yaml`) is still enforced by the validating webhook against `spec.resources.ephemeralStorage` on the Workspace CRD for any operator who explicitly sets a custom ephemeral storage value.

---

## Technology Stack

| Component | Technology | Reason |
|-----------|-----------|--------|
| API language | Go 1.25 | Type-safe, strong concurrency, idiomatic for K8s ecosystem |
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

<Per-session entries — one ### subsection per logical unit of work>

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

1. **Install pre-commit hooks** — run `make install-hooks` immediately after cloning. This is not optional. Every commit runs repolint, gofmt, goimports, golangci-lint, and helm-render checks. Without hooks installed, broken commits reach CI and waste time.
2. Read `README-LLM.md` (this file)
3. Read the relevant design document(s) from `design/` — see the table in [Rule 8](#8-understand-the-architecture-first)
4. Read `pkg/README.md` for shared package conventions
5. Check recent git history to understand current state of the area you're modifying

### Branch and PR workflow (MANDATORY)

**Never push directly to main.** Every change — no matter how small — follows this cycle:

1. **Create a feature branch** from main: `feat/`, `fix/`, `test/`, `chore/`, or `security/` prefix.
2. **Do the work** — TDD, write code, run tests locally.
3. **Push the branch and open a PR.**
4. **Wait for the automated review** — the AI reviewer triggers on every PR open and push.
5. **Read every finding.** Fix all real issues. Push to the same branch (triggers re-review).
6. **Iterate** — repeat steps 4–5 until the automated reviewer posts **APPROVE**.
7. **Merge** — only after approval. Use squash merge.
8. **Write a worklog entry** if the session was substantive.

This applies to humans and AI agents equally. No exceptions. The review-iterate-approve-merge cycle is the quality gate — skipping it defeats the purpose of having it.

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
   └─> Require: Assumptions stated and validated (per Rule 7)

2. Implementation Delegation
   └─> Delegate: User story implementation (per Rule 0 — TDD)
   └─> Prompt detail level: "Fresh developer seeing codebase for first time"
   └─> Include: Specific file references, pattern examples
   └─> Require: Happy + unhappy + e2e integration tests (per Rule 0)
   └─> Require: Stated assumptions list with validation evidence (per Rule 7)

3. Skeptical Validator Delegation (MANDATORY)
   └─> Delegate to a SEPARATE sub-agent acting as a skeptical validator
   └─> Validator's job: assume nothing works; prove every claim
   └─> Validator must check (per Rule 11):
       - Stated assumptions — actually true? (re-validate independently)
       - Integration points — wired into the live request path?
       - Test coverage — happy + unhappy + e2e/integration all present and meaningful?
       - Engineering principles (per Rule 4)
       - Spirit AND letter of the ask
       - Tech debt — any TODOs, hacks, workarounds, dead code?
   └─> Output: Detailed findings report with code references and severity
   └─> Validator MUST NOT also be the implementer (independence is the point)

4. Findings Triage and Remediation Delegation
   └─> Validate each finding is REAL (per Rule 11 Phase 2)
   └─> Document false alarms with rationale; do NOT silently dismiss
   └─> Delegate fixes for ALL real findings (per Rule 5 — zero tech debt)
   └─> Each fix must include a regression test

5. Re-Validate (LOOP)
   └─> Send remediated code BACK to a skeptical validator
   └─> If new findings: return to Step 4
   └─> If zero findings: exit the loop
   └─> NO compromises: loop continues until validator returns zero real findings

6. Build and Test Validation
   └─> Run: `make build && make test && make lint`
   └─> Fix ALL failures regardless of relevance to current work (per Rule 5)

7. Commit and Push
   └─> git add/commit/push with descriptive message referencing story/epic

8. Worklog Creation
   └─> Create worklog per Worklog Requirements section

9. Move to Next Story
   └─> Validate no integration gaps between previous and current story
   └─> Repeat from Step 1

10. Integration Gap Check
    └─> Validate integration between stories (imports, service registration, CRD schema)

11. Final Validation
    └─> Run full repository test suite one final time
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

1. **Context acquisition** — Read ALL assigned documentation (per Rule 8)
2. **Scope adherence** — Stay within defined boundaries; ask orchestrator if unclear
3. **Pattern following** — Use established patterns; check similar implementations
4. **TDD compliance** — Per Rule 0
5. **Integration awareness** — Identify and document integration points
6. **Quality standards** — Per Rules 1–5 (type safety, error handling, zero tech debt)
7. **Worklog creation** — Document work performed if completing a task

#### Delegation agent workflow

**Standard implementation task:**

```
1. Read Required Documentation (per Rule 8)
2. Understand Context — review delegation prompt, scope boundaries, integration points
3. Plan Implementation — break into sub-tasks, identify test scenarios and patterns
4. Write Tests FIRST (per Rule 0)
5. Implement — follow established patterns (per Rules 1–4)
6. Validate — `make test && make build`, verify integration points
7. Create Worklog (per Worklog Requirements section)
8. Report Back to Orchestrator — completion status, gaps, integration validation
```

**Code review task (per Rule 11):**

```
1. Read Code with Skeptical Mindset — assume nothing works until proven
2. Validate Against Standards — rules followed? TDD? type safety? patterns?
3. Integration Point Analysis — all identified, tested, end-to-end flows work?
4. Gap Identification — document every gap with code references and fix recommendations
5. Report Generation — clear descriptions, severity, NO APPROVAL until all gaps fixed
```

#### Delegation agent principles

- **Read first, ask later:** Always read README-LLM.md and relevant docs before work (per Rule 8). Check `pkg/types/types.go` for existing types before creating new ones.
- **Follow patterns:** Check similar implementations; use established patterns. Do not invent new patterns without approval.
- **TDD:** Tests before code, always (per Rule 0).
- **Quality:** Type safety (per Rule 1), explicit error handling (per Rule 3), no TODOs or placeholders (per Rule 5).
- **Communication:** Report completion clearly, document gaps/uncertainties, ask when scope is unclear.

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
| 9–10 | Every failure mode enumerated and tested; circuit breakers; defensive coding against all inputs; provably correct under partial failure. **Verify:** every function handles documented error returns; integration tests for each dependency failure; no silent error swallowing; external inputs validated at boundary; recovery from partial state |

**Definition:** Performance characteristics hold as load, data volume, and concurrency increase.

| Score | Criteria |
|-------|----------|
| 1–3 | O(n²) or worse on hot paths; no pagination; global locks on every request |
| 4–6 | Linear scans where indexed lookups exist; per-request expensive allocations; no connection pooling |
| 7–8 | Bounded loops; pagination on list endpoints; connection pooling; no per-request resource exhaustion |
| 9–10 | Verified O(1) or O(log n) on all hot paths; horizontal scalability demonstrated; no hidden N+1 queries; resource limits enforced. **Verify:** no N+1 query patterns; list endpoints use pagination; no unbounded goroutines/slice growth; connection pools sized and reused; no per-request lock on shared resources |

#### Maintainability

**Definition:** Code is readable, well-structured, and follows established patterns; a new contributor can modify it confidently.

| Score | Criteria |
|-------|----------|
| 1–3 | No tests; no doc comments; monolithic functions; inconsistent naming |
| 4–6 | Some tests but low coverage; mixed patterns; unclear data flow; magic numbers |
| 7–8 | Good test coverage; clear naming; small focused functions; follows project conventions |
| 9–10 | Self-documenting code; no unnecessary comments; consistent patterns throughout; a junior engineer can read and modify safely. **Verify:** functions ≤50 lines; naming follows Go conventions; no duplicate/near-duplicate code; every struct has single responsibility; no TODOs/FIXMEs/commented-out code |

#### Reliability

**Definition:** Deterministic, repeatable behaviour; no flaky tests; consistent results across environments.

| Score | Criteria |
|-------|----------|
| 1–3 | Non-deterministic behaviour; race conditions; flaky tests ignored |
| 4–6 | Some races handled; tests occasionally flaky; no timeout on external calls |
| 7–8 | Race-free in normal operation; stable tests; timeouts on all external calls |
| 9–10 | Race-free at high concurrency; all tests pass consistently with `-race`; timeout and deadline propagation everywhere. **Verify:** tests pass with `-race`; all external calls have timeouts; no flaky tests; no shared mutable state without synchronisation; all mutation endpoints idempotent |

#### Performance

**Definition:** Efficient use of CPU, memory, and I/O; no unnecessary pessimisation.

| Score | Criteria |
|-------|----------|
| 1–3 | Unbounded memory allocations; synchronous I/O on hot paths; no caching |
| 4–6 | Some caching but misses common patterns; unnecessary copies of large objects |
| 7–8 | Proper use of pointers, reuse, and pooling; async I/O where beneficial; cache headers |
| 9–10 | Benchmark-driven optimisation; zero-copy paths where possible; measured and documented trade-offs. **Verify:** no unnecessary heap allocations in hot loops; JSON marshal/unmarshal not on every response; no synchronous I/O in hot handler without justification; profiled with realistic load |

#### Security

**Definition:** Input validated, outputs sanitised, secrets never logged, least-privilege by default.

| Score | Criteria |
|-------|----------|
| 1–3 | No input validation; secrets logged; no auth on endpoints |
| 4–6 | Basic validation but bypassable; secrets may leak in error messages; broad permissions |
| 7–8 | All inputs validated at boundary; secrets filtered from logs; least-privilege RBAC |
| 9–10 | Defence in depth; no user data in error messages; injection-proof by construction; security tests for every control. **Verify:** no secrets in logs/errors/responses; user input validated (length/type/range/chars); permission checks in service layer; parameterised queries only; security tests for every control; rate limiting and body size limits applied |

#### Test Coverage & Quality

**Definition:** Tests exist at the right levels, cover happy+unhappy paths, and are reliable.

| Score | Criteria |
|-------|----------|
| 1–3 | No tests, or tests don't actually assert anything |
| 4–6 | Some unit tests but no unhappy paths; no integration tests |
| 7–8 | Good unit coverage + unhappy paths + integration/e2e tests; table-driven |
| 9–10 | Comprehensive coverage at all levels; TDD followed; tests run with `-race`; no flaky tests. **Verify:** table-driven tests cover happy and unhappy paths; e2e/integration tests exercise real wiring; tests pass with `-race -count=1`; test utilities reduce boilerplate; no tests depend on external services without mock/fake |

#### SOLID Compliance

**Definition:** Follows Single Responsibility, Open/Closed, Liskov Substitution, Interface Segregation, and Dependency Inversion principles. Every type has one clear reason to change; abstractions are stable; dependencies flow inward.

| Score | Criteria |
|-------|----------|
| 1–3 | Violates multiple SOLID principles; god objects; concrete coupling everywhere; impossible to test in isolation |
| 4–6 | Some SRP violations; mixed abstraction levels; some coupling to concrete types; partial testability |
| 7–8 | Mostly SOLID; clear interfaces; dependency injection; small focused types; testable in isolation |
| 9–10 | Fully SOLID by construction; every type has one reason to change; abstractions are caller-shaped not implementation-shaped; high-level modules never import low-level details. **Verify:** every type has single responsibility; interfaces are small (1–3 methods) and caller-shaped; no concrete dependency where interface would serve; new variants don't require modifying existing types; high-level modules don't import low-level details |

#### Right-Sized Complexity

**Definition:** The code is exactly as complex as it needs to be — no more (over-engineered), no less (under-engineered). Abstractions earn their keep. 10 is perfect; scores decrease in either direction.

| Score | Criteria |
|-------|----------|
| 10 | Perfectly sized — abstraction level matches the problem; every interface has ≥2 implementations or a clear imminent need; no speculative generality; a junior engineer can follow the flow. **Verify:** every interface has ≥2 implementations or imminent need; functions >30 lines justifiable; no single-implementation abstractions; new features add code not modify abstraction layer; simplest correct solution chosen |
| 7–9 | Slightly off — one unnecessary abstraction layer OR one missing abstraction that would simplify callers. Functions and type boundaries are mostly right |
| 4–6 | Noticeably off — speculative abstractions with no current consumer, or monoliths that should be split. Multiple indirection layers without value |
| 1–3 | Severely wrong — framework-in-disguise (unnecessary factories/visitors/strategies for a simple CRUD path), or giant monolithic functions with no decomposition. Actively reduces productivity |

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

In addition to the rubric scoring, every PR must undergo a structured adversarial review per [Rule 11 — Adversarial Self-Review](#11-adversarial-self-review). Apply Rule 11 Phases 1–2 as written, with these PR-specific additions:

**PR-specific omissions checklist (add to Phase 1):**

- Missing input validation
- Missing authentication/authorisation checks
- Missing logging for debugging
- Missing metrics for monitoring
- Missing timeout/deadline propagation

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

## Testing Requirements

### TDD and coverage requirements

See [Rule 0 — Test Driven Development](#0-test-driven-development-tdd) for the mandatory TDD workflow, test requirements, and definition of done.

### Table-driven tests

Use table-driven tests with `t.Run()` for any function with multiple input cases:

```go
func TestCreateWorkspace(t *testing.T) {
    tests := []struct {
        name    string
        req     types.CreateWorkspaceRequest
        wantErr bool
    }{
        {"valid workspace", types.CreateWorkspaceRequest{Runtime: "base", Name: "test"}, false},
        {"empty name", types.CreateWorkspaceRequest{Runtime: "base", Name: ""}, true},
        {"invalid storage size", types.CreateWorkspaceRequest{Runtime: "base", Name: "test", StorageSize: "-1"}, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := svc.CreateWorkspace(ctx, tt.req)
            if (err != nil) != tt.wantErr {
                t.Errorf("CreateWorkspace() error = %v, wantErr %v", err, tt.wantErr)
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

When modifying CRD types in `pkg/apis/llmsafespace/v1/*_types.go`, you must regenerate the DeepCopy implementations:

```bash
# From project root
make deepcopy

# Verify and commit generated changes
git add pkg/apis/llmsafespace/v1/zz_generated.deepcopy.go
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
| Protected endpoints require auth | API key CRUD behind `AuthMiddleware()` | `TestAPIKeyEndpoints_RequireAuth` |
| Wrong HTTP method rejection | Only POST on register/login, returns 404 | `TestRegister_RejectsGet`, `TestLogin_RejectsGet` |

### E2E Testing

Go tests: `go test -race ./api/internal/server/... -run "TestRegister|TestLogin|TestCreateAPIKey|TestListAPIKeys|TestDeleteAPIKey|TestAPIKeyEndpoints"`

Shell script against running server: `./local/test-auth.sh http://localhost:8080`

---

## API Reference

The complete REST API is documented in `README.md` under "REST API". The API has 83 routes covering:

- **Auth** (8 routes): register, login, logout, me, API key CRUD
- **Workspaces** (9 routes): CRUD + suspend, activate, restart, status, agent reload
- **Session management** (5 routes): list, ensure, rename, mark-seen, active
- **Session proxy** (7 routes): message, prompt, history, get, abort, delete, SSE events — reverse-proxied to the workspace pod's `opencode serve` on port 4096
- **Questions & Permissions** (5 routes): list/reply/reject agent questions and permission requests
- **Events** (2 routes): user-scoped SSE stream, bulk agent reload
- **Secrets** (8 routes): CRUD + audit + reveal + bindings — zero-knowledge encrypted store
- **Workspace bindings** (3 routes): set/get bindings, reload-secrets
- **Workspace env** (3 routes): set/get/delete environment variables
- **Models** (2 routes): list available models, set default model
- **Terminal** (2 routes): ticket + WebSocket proxy
- **Admin provider credentials** (8 routes): CRUD + auto-apply rules
- **User provider credentials** (7 routes): CRUD + bindings
- **Settings** (6 routes): admin instance + user preferences + schemas
- **Account** (3 routes): key rotation, password change, recovery
- **Infrastructure** (4 routes): livez, health, readyz, metrics

### `?verbose=true` flag

By default, the proxy strips parts of `type=="patch"` from message and history responses. opencode emits a `patch` part for every assistant turn, listing every workspace file it touched (~2 KB per response of internal snapshot paths). For most clients this is noise.

Pass `?verbose=true` on any message or history request to receive the unfiltered response.

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
| 1.13 | 2026-06-12 | Removed redundant Bug Status, Confirmed Bugs, Implementation Status, Branch Management sections; simplified repo structure, worklog template, multi-agent workflow, PR adversarial assessment; folded scoring bullets into tables; compressed relay write sequences and version history; removed backwards compat; updated annotateModels remap note |
| 1.12 | 2026-06-11 | Fixed repo structure, CRD count, architecture diagram, API reference, tech stack, SSE paths, route docs |
| 1.11 | 2026-06-08 | Added relay config subsystem: bugs, volume layout, config merge order, design, gap fixes |
| 1.10 | 2026-06-04 | Added PR Review Guide (1–10 rubric, E2E wiring verification, adversarial assessment); expanded Rule 11 |
| 1.9 | 2026-05-27 | Frontend streaming UX fixes (user echo, thinking blocks, bubble overflow) |
| 1.8 | 2026-05-23 | Engineering principles in Rule 4; Rule 7 assumptions; TDD definition of done; validator loop |
| 1.5 | 2026-05-23 | Sandbox CRUD API, `?verbose=true` flag, README.md rewritten for V2 |
| 1.4 | 2026-05-23 | Rate limiting, CORS hardening, account lockout |
| 1.3 | 2026-05-23 | Auth endpoints with security hardening and e2e tests |
| 1.2 | 2026-05-22 | Repo structure, architecture, CRD ownership, tech stack aligned with EVOLUTION-V2 |
| 1.1 | 2026-05-22 | Updated for V2 architecture |
| 1.0 | 2026-05-21 | Initial creation |
