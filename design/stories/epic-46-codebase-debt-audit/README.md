# Epic 46: Codebase Debt Audit & Abstraction Foundation

**Status:** Proposed
**Created:** 2026-06-18
**Author:** Audit session (see worklog 0337)
**Parent design doc:** [`design/0044_2026-06-18_epic-46-codebase-debt-audit.md`](../../0044_2026-06-18_epic-46-codebase-debt-audit.md)

---

## Stories (ranked by effort vs ROI)

ROI = (correctness + testability + maintainability impact) ÷ engineering days.
**Suggested execution order is the table order** — top to bottom.

| Story | Title | Effort | ROI | Depends on |
|-------|-------|--------|-----|------------|
| [US-46.1](./US-46.1-fix-epic-numbering-and-dup-design-doc.md) | Fix epic numbering collisions + delete duplicate `0018` design doc | Trivial (0.25d) | Very High | — |
| [US-46.2](./US-46.2-remove-dead-code-stubs.md) | Delete `GCPDriver` stub (annotateModels guard stays — defense-in-depth) | Small (0.25d) | Very High | — |
| [US-46.3](./US-46.3-split-pkg-types-types-go.md) | Split `pkg/types/types.go` (71 types → per-domain files) | Small (1d) | High | — |
| [US-46.4](./US-46.4-introduce-domain-error-type.md) | Introduce `DomainError` type + mapping convention | Small (1d) | High | — |
| [US-46.5](./US-46.5-propagate-context-everywhere.md) | Replace 187 `context.TODO/Background` with propagated ctx | Medium (2d) | High | — |
| [US-46.6](./US-46.6-split-workspace-agentd-main-go.md) | Split `cmd/workspace-agentd/main.go` (1451 → ≤300 lines/file) | Medium (3d) | High | — |
| [US-46.7](./US-46.7-define-service-shaped-interfaces.md) | Define `Service`-shaped interfaces where ≥2 consumers exist | Medium (2d) | High | US-46.4 |
| [US-46.8](./US-46.8-type-the-settings-subsystem.md) | Replace `map[string]any` in Settings with typed registry | Medium (2d) | Medium-High | — |
| [US-46.9](./US-46.9-type-mcp-request-bodies.md) | Replace `map[string]any` MCP request bodies with structs | Small (1d) | Medium | — |
| [US-46.10](./US-46.10-single-writer-agent-config-json.md) | Consolidate 4-writer `agent-config.json` into one writer | Large (4d) | High | US-46.6 |
| [US-46.11](./US-46.11-workspace-password-provider-interface.md) | Define `WorkspacePasswordProvider` interface | Small (0.5d) | Medium | — |
| [US-46.12](./US-46.12-fill-missingtests-md-gaps.md) | Add Auth Middleware RBAC + Rate Limit bursting tests | Medium (2d) | Medium | US-46.7 |
| [US-46.13](./US-46.13-add-funlen-gocyclo-lint-baseline.md) | Add `funlen`/`gocyclo` to golangci-lint with baseline | Small (0.5d) | Medium | US-46.6 |
| [US-46.14](./US-46.14-archive-v1-design-docs.md) | Move `design/0001`–`design/0020` to `design/archive/v1/` | Trivial (0.25d) | Medium | — |
| [US-46.15](./US-46.15-fix-readme-llm-stale-references.md) | Fix stale design-doc references in README-LLM.md | Trivial (0.25d) | Low-Medium | US-46.14 |

**Total effort:** ~21 engineering days. Parallelisable across 2 engineers in ~2.5 weeks given dependency order.

---

## Phased Execution

| Phase | Days | Stories | Theme |
|-------|------|---------|-------|
| 1 | 1–2 | US-46.1, US-46.2, US-46.14, US-46.15 | Debt clearance (zero risk) |
| 2 | 3–6 | US-46.3, US-46.4, US-46.5, US-46.9 | Type-safety foundations |
| 3 | 7–10 | US-46.7, US-46.8, US-46.11 | Interface extraction |
| 4 | 11–14 | US-46.6 | File decomposition |
| 5 | 15–18 | US-46.10 | Single-writer refactor (highest risk) |
| 6 | 19–21 | US-46.12, US-46.13 | Hygiene + lock-in |

---

## Scope Boundaries

### Covered by this epic
Findings from the production code audit (worklog 0337) that are **not already owned** by:
- **Epic 29** (Handler Decomposition) — owns SecretsHandler split, AgentClient extraction, constructor injection
- **Epic 38** (Architectural Remediation) — owns ProxyHandler decomposition, the 9 confirmed dead code locations, dual-pattern consolidation, services-out-of-handlers, credential triplication

### Cross-references (no duplication)
| This epic's story | Extends / complements |
|-------------------|----------------------|
| US-46.6 (split main.go) | Complements US-38.2 (proxy decomposition) |
| US-46.7 (Service interfaces) | Extends US-29.8 (constructor injection) |
| US-46.11 (WorkspacePasswordProvider) | Complements US-29.1 (AgentClient) |

### Explicitly out of scope
- Frontend changes
- New features
- Behaviour changes
- Documentation rewrites (only factual fixes in US-46.15)
- The 9 dead code locations from Epic 38 (US-46.2 only handles the GCPDriver stub, which Epic 38 did not list; the annotateModels guard was originally proposed for removal but is retained as intentional defense-in-depth — see US-46.2)

---

## Success Criteria

Tracked in the parent design doc ([§ Success Criteria](../../0044_2026-06-18_epic-46-codebase-debt-audit.md#success-criteria)). Each story's PR must show:

1. The grep count proving the targeted violation is gone
2. `make build && make test && make lint` passing
3. A worklog entry
4. A diff-size guard: stories exceeding estimate by >50% must be split

---

## Abstraction Tiers

| Tier | Rule | Examples |
|------|------|----------|
| **A — define now** | ≥2 current consumers | `AgentClient`, `settings.Repository` (typed), `WorkspacePasswordProvider` |
| **B — define when extending** | 1 consumer + imminent second | `metering.UsageExporter` (Epic 42), `database.WorkspaceMetadataStore` |
| **C — do NOT abstract** | Speculative / single-consumer | Generic `Handler` marker, `Repository[T]`, `EventBus`, duplicate `Logger` |

Full rationale in [parent design doc § Abstraction Opportunities](../../0044_2026-06-18_epic-46-codebase-debt-audit.md#abstraction-opportunities-where-interfaces-earn-their-keep).
