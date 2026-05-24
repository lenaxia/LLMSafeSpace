# Worklog: Frontend Design Document

**Date:** 2026-05-23
**Session:** Scoping a web frontend ("Safe Space") for LLMSafeSpace V2; producing the design doc that locks in architectural decisions before any implementation begins.
**Status:** Complete

---

## Objective

Scope and document a chat-style web UI for LLMSafeSpace that lets users log in, browse their workspaces and sessions in a sidebar, and chat with long-running agents. The frontend must be optional (everything is also doable via API), responsive (mobile + desktop), themed (light + dark), GitOps-friendly (Helm-toggleable), and architected for the OIDC migration coming later.

The output of this session is `design/FRONTEND.md` — a 21-section design document covering goals, personas, locked architectural decisions, API surface additions, database additions, frontend repo layout, page-by-page UX, the active-session resource model, the workspace-activate atomic flow, the suspended-history resume-on-read strategy, theming/responsiveness/a11y, PWA + bundle budgets + Lighthouse CI, containerization + Helm, GitOps + CI, type sync, testing, phased implementation, risks, and deferred items.

---

## Work Completed

### Design exploration (interactive)

Walked the user through the key axes one at a time, with explicit trade-off framing on each:

- **Framework.** Chose **Vite + React 18 + TypeScript + Tailwind + shadcn/ui (SPA)** for maximum two-way-door reversibility. Rejected Next.js (SSR coupling, Node runtime in cluster) and SvelteKit (similar SSR coupling). When the user pushed back on SPA mobile-friendliness, defended SPA with the addition of a full PWA + bundle budgets + Lighthouse CI to address the underlying mobile-render concern directly rather than swapping frameworks.
- **Auth.** Chose **HttpOnly Secure cookie**. Acknowledged this requires API changes (cookie set on `/login`, new `/me` and `/logout` endpoints, cookie reader in middleware). Documented the OIDC migration path so the cookie shape survives the issuer swap.
- **Streaming transport.** Chose **HTTP streaming + SSE side channel** to mirror the verified opencode contract (V2 §7.1a). No WebSocket complexity.
- **Sidebar hierarchy.** After re-explaining the workspace ↔ sandbox ↔ session relationship from EVOLUTION-V2.md (the user asked to refresh), settled on **Workspace → Session(s)** with the sandbox layer hidden. Surface workspace-level Active/Suspended badge and per-workspace `N/M active` counter.
- **Activation flow.** Chose **server-side atomic** `POST /workspaces/{id}/activate` (suspend stalest active workspace if at cap, then resume target) so CLI/MCP clients also get the fairness guarantee.
- **Caps.** `maxActiveWorkspacesPerUser=5`, `maxActiveSessions=5`. User specifically wanted active-session cap to stay strict (resource pressure is real per opencode's in-memory context windows) and active-workspace cap to be more generous.
- **Suspended-workspace history.** User explicitly rejected caching message bodies in PostgreSQL ("a security breach waiting to happen"). Settled on **resume-on-read**, with a small `session_index` table caching only `(workspace_id, session_id, title, last_message_at, message_count)`. **Title is run through `pkg/redact` (the existing 16-rule pipeline used for log sanitization) before persistence; truncated to 200 chars.** No message bodies, no previews, ever.
- **Settings v1.** API Keys + Appearance only; Profile, MCP, Presets, Permissions stubbed as `<ComingSoonTab>` placeholders so the IA locks in early.
- **Registration.** Disabled by default; gated by Helm value + `GET /auth/config` feature flag served to the SPA.
- **PWA.** Full PWA: manifest + service worker + offline shell + add-to-home-screen.
- **Bundle/perf gates.** 200KB initial JS gzipped, 250KB chat chunk, 30KB CSS. Lighthouse CI mobile thresholds: Perf ≥ 85, A11y ≥ 95, Best Practices ≥ 95, PWA ≥ 90.
- **Type sync.** Generate TypeScript from API Swagger via `make openapi`; hand-written types disallowed.
- **E2E.** Playwright now (not deferred).
- **Repo layout.** `frontend/` at top level alongside `api/`, `controller/`, `runtimes/`.

### Codebase verification

Read enough of the existing codebase to ground every decision in real code:

- `api/internal/server/router.go` — confirmed existing auth/workspace/sandbox/proxy route shapes.
- `api/internal/handlers/proxy.go` — confirmed `defaultMaxActiveSessions = 5`, the 429 + `Retry-After` behavior, the per-session active-set tracking, the session_status SSE consumer.
- `api/internal/handlers/session_tracker.go` — confirmed session.status events from opencode at `idle`/`busy`.
- `pkg/types/types.go` — confirmed existing `RegisterRequest`, `LoginRequest`, `AuthResponse`, `APIKey`, `Workspace*` types.
- `controller/internal/resources/workspace_types.go` — confirmed `MaxActiveSessions` is per-workspace (`Workspace.Spec.MaxActiveSessions int32`), default 5.
- `charts/llmsafespace/values.yaml` — confirmed Helm chart structure for adding the new `frontend.*` block.
- `design/EVOLUTION-V2.md` §5 (workspaces), §6 (sessions, suspend/resume), §7.1a (verified opencode contract), §11 (API endpoints).

### Mobile-friendliness debate

User raised a valid concern that SPAs depend more heavily on mobile render capability. Rebutted with:
1. SSR's first-paint advantage doesn't apply to authenticated long-session apps where bounce rate is irrelevant.
2. The actual mobile-friendliness levers (bundle size, code splitting, virtualization, PWA caching, gzip/brotli) are orthogonal to SPA-vs-SSR.
3. Quantified them: PWA + 200KB JS budget + Lighthouse CI mobile thresholds + react-virtual for long histories + Tailwind JIT for tiny CSS.
4. The reversibility trade-off the user prioritized would be sacrificed by switching to Next.js/SvelteKit.
5. Migration path stays cheap (Vite → Astro+islands) if real metrics later demand it.

User accepted. Locked SPA + full PWA + budgets + Lighthouse CI.

### Active-session pressure model

Verified the existing implementation in `proxy.go:24-54, 240-247, 535-585`:
- `defaultMaxActiveSessions = 5` per workspace.
- `Workspace.Spec.MaxActiveSessions` (CRD) is the source of truth.
- `activeSess[sandboxID]` set bounded; over-cap returns 429 + `Retry-After` JSON body containing `maxActiveSessions`.
- `SSETracker` consumes opencode's `session.status` (`busy`/`idle`) events and admits/evicts sessions accordingly.

Concluded: the backend is already correct for the resource model the user is worried about. The frontend's job is to surface it (`N/M active` counter, per-session dot, `<AtCapBanner>` with countdown).

### History-while-suspended exploration

Mapped the option space:
1. Resume-on-read (no new components) — **chosen**.
2. In-pod read-only sidecar (incompatible with RWO PVC + suspend-deletes-pod).
3. PostgreSQL message cache — **rejected by user as security risk**.
4. On-demand history-reader Job (deferred to V2.1).
5. Hybrid (DB index of metadata only + resume for content) — **adopted in modified form**: index keeps only `(id, title, timestamp, count)`, NOT message bodies. Title is `pkg/redact`-sanitized before insert.

Reasoning recorded in §12 of the design doc.

### Document produced

Wrote `design/FRONTEND.md` (1100+ lines) covering:

- Goals/non-goals (10 each, explicit).
- Personas/anti-personas (3+3).
- 15 locked architectural decisions in a table for fast reference.
- Resource model refresher reproduced from EVOLUTION-V2.md.
- Auth: cookie shape, `/me`, `/logout`, `/config`, rate limiting, OIDC migration story.
- API surface additions (auth + workspaces) plus new types in `pkg/types/types.go`.
- DB schema migration `000003_session_index.up.sql` with the strict no-content-stored rule and a privacy-verification test requirement.
- Full frontend repo layout with locked dependencies.
- Page-by-page UX for `/login`, `/register`, `/chat`, `/chat/:wsId/:sessId`, `/settings/*`.
- Sidebar mockup + state matrix for the chat composer (Active/Suspending/Resuming/Suspended/AtCap).
- Workspace-activate algorithm (with Redis lock, edge cases).
- Suspended-history flow with full call trace appendix.
- Theming, responsiveness breakpoints, accessibility requirements.
- PWA configuration (manifest, caching strategy, update flow), bundle budgets, Lighthouse CI config.
- Dockerfile, nginx.conf, Helm chart additions (deployment, service, configmap, ingress unification, values block).
- New GHA workflow `build-frontend.yml` with all 13 CI steps.
- Type sync via `make openapi` → `openapi-typescript` → `frontend/src/api/types.gen.ts`.
- Testing strategy table (unit/hook/client/integration/e2e).
- 4-phase implementation plan (Phase A backend prereqs → B frontend foundation → C chat surface → D polish/ship), 10-13 days total estimate.
- Risk table with 10 risks + mitigations.
- 8 items deferred to V1.1+.
- 2 appendices: full call trace of "send first message in suspended workspace", and sample sidebar JSON shapes.

---

## Key Decisions

All recorded as AD1–AD15 in §3 of the design doc. Highlights and rationale:

- **AD1 (Vite SPA over SSR frameworks):** maximum reversibility was a stated user requirement. SSR's only meaningful advantage here would be a one-time ~300ms first-paint improvement on `/login`; everything after login is interactive and benefits from neither SSR nor Next.js conventions. Mobile concerns addressed via AD11 (PWA) + AD12 (budgets/Lighthouse).
- **AD3 (HttpOnly cookie):** best XSS posture, OIDC-compatible. Costs us four small backend additions (login sets cookie, new `/me`, new `/logout`, new `/config`) — modest in exchange for the security improvement.
- **AD5 (Workspace → Session in sidebar; sandbox hidden):** matches user mental model. Sandbox is an implementation detail of "is the workspace warm right now."
- **AD6 (server-side atomic activate):** ensures CLI/MCP clients get the same fairness as the UI. Single source of truth for the cap policy.
- **AD7 (caps 5/5):** generous enough for normal workflows, conservative enough for small clusters. Both Helm-configurable.
- **AD9 (registration disabled by default):** safer for cluster operators. They flip explicitly.
- **AD10 (no message-body caching in PostgreSQL):** user-stated security boundary. Index stores only redacted titles + timestamps + counts. No previews, ever.
- **AD11 (full PWA):** primary mitigation for the SPA-mobile concern.
- **AD12 (bundle budgets + Lighthouse CI thresholds):** quantitative guardrails so mobile regressions can't ship silently.
- **AD13 (generated types only):** prevents drift; treats Swagger as a contract.

---

## Blockers

None. Design is complete and approved. Ready to start Phase A (backend prerequisites) on next session.

One soft prerequisite to confirm at Phase A kickoff: current Swagger annotation coverage on `api/internal/handlers/*` — Phase A includes a sweep to backfill any gaps so `make openapi` produces a complete spec.

---

## Tests Run

None for this session — design-doc only, no code changes.

LSP reported pre-existing errors in `api/internal/services/database/database.go`, `api/internal/services/services.go`, `api/internal/mocks/database.go`, and two router test files relating to a missing `CountUsers` method on `DatabaseService`. These predate this session (unrelated to the new design doc) and will be tracked separately or fixed as part of Phase A's backend changes if they affect the new auth-config endpoint.

---

## Next Steps

Begin **Phase A (backend prerequisites)** as defined in `design/FRONTEND.md` §19:

1. Add types to `pkg/types/types.go`: `AuthConfig`, `ActivateWorkspaceResponse`, `SessionListItem`, `ActiveSessionsResponse`, `WorkspaceSuspendedError`. Tests first.
2. Write migration `api/migrations/000003_session_index.up.sql` and matching down migration.
3. New `api/internal/services/session_index/` service with `RecordMessage`, `ListByWorkspace`, `DeleteByWorkspace`. TDD: write tests asserting `pkg/redact` is invoked on title before insert; assert no message-body fields exist on the schema.
4. Wire `SessionIndexService` into `api/internal/handlers/proxy.go` `SendMessage` post-completion via fire-and-forget goroutine with bounded queue + dropped-event metric.
5. Extend `api/internal/services/auth/auth.go` to:
   - Set `lsp_session` HttpOnly Secure SameSite=Lax cookie on successful `Login`.
   - Add `Logout` method (clears cookie).
   - Add `Me` method.
   - Add `Config` method.
6. Extend `api/internal/middleware/auth.go` to read the JWT from `lsp_session` cookie in addition to `Authorization` header.
7. Extend `api/internal/services/workspace/workspace_service.go` with `ActivateWorkspace(ctx, userID, workspaceID)` implementing the algorithm in `design/FRONTEND.md` §11.2 with Redis lock per user.
8. Register routes in `api/internal/server/router.go`: `GET /auth/config`, `POST /auth/logout`, `GET /auth/me`, `POST /workspaces/{id}/activate`, `GET /workspaces/{id}/sessions`, `GET /workspaces/{id}/sessions/active`.
9. Update `api/config/config.yaml` and `charts/llmsafespace/values.yaml` with new config keys per §15.3.
10. Update `api/internal/middleware/cors.go` to enable credentialed CORS when `server.allowedOrigins` is configured.
11. Add Swagger annotations on every new/modified handler so `make openapi` produces a complete spec.
12. Run `make test` and `make lint`; fix the pre-existing `CountUsers`-missing errors as part of this work since they touch the same surface.
13. Worklog 0035 entry.

After Phase A merges, Phase B (frontend foundation) starts with `frontend/` scaffolding.

---

## Files Modified

- `design/FRONTEND.md` — created (new file; ~1100 lines).
- `worklogs/0034_2026-05-23_frontend-design-doc.md` — created (this file).

---

## Addendum: v2.0 Revalidation (same date)

After the v1.0 design was written, the user requested a full revalidation pass: "make sure it is consistent internally and with the existing code base, complete, reliable, robust, maintainable, PERFORMANT, secure, and scalable. it is SOLID and idiomatically best practices. state all assumptions up front and validate them. do not make assumptions you cannot validate."

Did the work in three steps:

### Step 1 — Enumerated 28 explicit assumptions

Pulled every assumption out of the v1.0 design into a numbered list. No design statement was left in "implicit" form. Each became a yes/no/needs-revision question to answer with code citations.

### Step 2 — Validated each assumption against the codebase

Read the actual implementation of:
- `api/internal/services/auth/auth.go` (all 532 lines)
- `api/internal/utilities/token_extractor.go`
- `api/internal/middleware/security.go` (CORS + secure headers wiring)
- `api/internal/middleware/cors.go` (verified unused — false lead from v1.0)
- `api/internal/handlers/proxy.go` (proxy lifecycle, connection cap, active session tracking)
- `api/internal/handlers/session_tracker.go` (SSE callback hook for index update)
- `api/internal/services/workspace/workspace_service.go` (verifyOwner, suspend/resume contract)
- `api/internal/interfaces/interfaces.go` (full surface)
- `controller/internal/resources/workspace_types.go` (CRD field validation bounds)
- `pkg/apis/llmsafespace/v1/types.go` (LastActivityAt, MaxActiveSessions)
- `pkg/types/types.go` (User, WorkspaceListItem, WorkspaceMetadata)
- `pkg/redact/redact.go` (public API verified: `Redact(s) (string, error)`)
- `api/internal/docs/swagger.go` (verified: NO annotations, paths empty)
- `charts/llmsafespace/templates/api-ingress.yaml` (path-based routing; reusable pattern)
- `api/migrations/` (numbering convention 6-digit)
- Ran `go build ./...` — **passes cleanly**. The LSP "CountUsers missing" errors that v1.0 noted were stale-cache artifacts; both interface and impl have the method.

Of the 28 assumptions, **8 were refuted or partially refuted**, each driving a concrete design correction:

| # | Refuted assumption | Correction |
|---|---|---|
| A1 | Login can set cookie inside the service | Move cookie issuance to the **router** (preserves SRP). Service stays HTTP-agnostic. |
| A3 | `/me` is JWT-only / no DB hit | `/me` calls `database.GetUser` with 15-minute Redis cache. |
| A6 | Proxy has a clean post-stream completion hook | Wire index update into the **existing** `SSETracker.onSessionIdle` callback. Works for both message and prompt_async paths; decouples from proxy hot path. |
| A8 | opencode auto-supplies session titles | Title becomes nullable, best-effort. UI fallback "Session at HH:MM". Add explicit `PUT /workspaces/{id}/sessions/{sessionId}/title` endpoint. |
| A9 | opencode supports message pagination | Treat history as fully-returned by opencode; client-side pagination only in V1. |
| A14 | Swagger annotations exist | **Major change.** No annotations in current code. V1 uses **hand-written TS types + Go-emitted JSON fixtures + Zod contract test**. Swagger codegen deferred. |
| A17 | `cacheService.Set` supports SETNX | Add `SetNX` to `CacheService` interface for the activate-lock. |
| A19 | `ListSandboxes` filters by workspace | New endpoint `GET /workspaces/{id}/sandboxes` (composable; no breaking change). |
| A22 | `WorkspaceListItem` has phase/active count | Extend the type and `WorkspaceService.ListWorkspaces` to merge CRD status. Avoids N+1 in sidebar. |

### Step 3 — Audit for SOLID, security, performance, scalability, consistency

Ran a focused review along each axis:

- **SOLID:** Cookie issuance moved to router (SRP). New `SessionIndexService` has its own interface (DIP). `CacheService.SetNX` extension preserves LSP. OCP for OIDC swap deferred but documented.
- **Security:** Identified that login response would carry the JWT in the body even after cookie auth. Resolved as AD18 (back-compat retained; SPA ignores). CSP `connect-src` clarified for cross-origin. SameSite=None requires Secure; documented as enforced. PWA never caches `/api/v1/*`.
- **Performance:** Identified N+1 in sidebar from `ListWorkspaces` returning incomplete data. Fixed by extending `WorkspaceListItem`.
- **Scalability:** Identified `maxConnectionsPerSandbox=10` cap interacts badly with multiple browser tabs. Added BroadcastChannel-multiplexed SSE (AD16) and client-side soft-throttle on Send button. Activate lock uses Redis SetNX (distributed-safe; multi-replica works).
- **Consistency (internal):** Several v1.0 references were corrected — e.g. `cors.go` named as the CORS site when it's actually `security.go`; `Login` named as cookie-setter when it's the router. v2.0 names the right files.
- **Maintainability:** Hand-written types + Go-emitted JSON fixtures + Zod schemas approach gives drift detection without the Swagger backfill blocker.

### Document v2.0 produced

Replaced `design/FRONTEND.md` v1.0 with v2.0 (1,216 lines vs 1,194). Differences:

- New §0 "Assumptions and Validation" with the 28-row table — every claim is now traceable to code.
- Architectural decisions table grew from 15 to 18 rows (added AD16 BroadcastChannel multiplex, AD17 SSE-driven index updates, AD18 Login response back-compat).
- §5 "Backend Changes" rewritten: cookie issuance moved to router; `SetNX` cache extension; `SessionIndexService` interface in `interfaces.go`; SSE-driven write path documented; new `GET /workspaces/{id}/sandboxes` endpoint; new `PUT .../sessions/{id}/title` endpoint; `WorkspaceListItem` extension.
- §7 "Workspace Activate Flow" hardened: explicit Redis SetNX algorithm; nil-LastActivityAt fallback; transition-state edge cases (Suspending/Pending/Failed/Terminating); audit log requirement.
- §10 "Active-Session Resource Pressure" gained §10.3 connection-budget management with BroadcastChannel.
- §14 "Security Considerations" added as a dedicated section: XSS, CSRF, secrets-in-titles, token-in-body trade-off, cookie attribute matrix, PWA SW scope, CORS.
- §17 "Type Synchronization" rewritten for hand-written + contract-test approach.
- §19 "SOLID Audit" added — explicit table of how each principle is honored.
- §21 "Risks" expanded from 10 to 14 rows.

### Phase A estimate revised

3-4 days (was 2-3) due to scope additions: `SetNX` cache method, `SessionIndexService` interface, `WorkspaceListItem` extension, new endpoints (`GET /workspaces/{id}/sandboxes`, `PUT .../sessions/{id}/title`, `GET /workspaces/{id}/sessions/active`), thread-safe accessor on `ProxyHandler` for active set introspection, and contract-test fixture emitters.

Total project estimate revised from 10-13 days to **11-15 working days**.

### Files modified in this revalidation

- `design/FRONTEND.md` — replaced v1.0 with v2.0.
- `worklogs/0034_2026-05-23_frontend-design-doc.md` — appended this addendum.

---

## Addendum: v3.0 Second Revalidation (same date)

User asked for a second revalidation pass with a higher bar: "only make changes if you can prove a meaningful benefit AND demonstrate that it does not negatively impact other parts of the project."

### Step 1 — Re-enumerated assumptions, including ones that v2.0 introduced implicitly

Pulled out 28 hidden assumptions (N1-N28) that v2.0 made but didn't list in §0. The most important were:
- N4: that `Workspace.Status.ActiveSessions` (controller-tracked) means what we want — **REFUTED**.
- N6: that one opencode busy/idle cycle = one message exchange — **NOT VERIFIED**.
- N11: that the proxy's connection cap is global — **REFUTED, it's per-replica**.
- N1, N2, N3, N7, N8, N9, N10 — all VALIDATED with code citations.

### Step 2 — Validated against the codebase

Read:
- `api/internal/services/cache/cache.go` (verified go-redis/v8 in use; SetNX is native).
- `api/internal/services/ratelimit/ratelimit.go` (verified Increment is **NOT** atomic; cannot substitute for SetNX — TOCTOU race at Get-then-Set).
- `api/internal/services/services.go` (lifecycle order; SessionIndexService placement).
- `api/internal/handlers/proxy.go` lines 24-99, 590-680 (verified connCount is per-replica; activate-callback signature; onSessionIdle pattern).
- `api/internal/handlers/session_tracker.go` lines 18, 207-218 (verified callback contract).
- `controller/internal/workspace/controller.go` line 266 — **the critical finding**: `workspace.Status.ActiveSessions = int32(len(sandboxes))`. The CRD status field counts attached sandboxes, NOT opencode active sessions. Under V2's RWO model that's always 0 or 1.

### Step 3 — Audit; triage 16 candidate changes against the bar

Each candidate was scored on (a) demonstrable benefit and (b) absence of negative impact:

| # | Candidate | Verdict | Reason |
|---|---|---|---|
| C1 | Drop `ActiveSessions` from `WorkspaceListItem` extension | APPLY | The controller field is sandbox count not session count; including it would surface a misleading number. Sidebar gets the real count from `/sessions/active`. |
| C2 | Drop `LastActivityAt` from `WorkspaceListItem` | APPLY (deferrable) | Not used in V1 frontend. |
| C3 | Document `message_count` is approximate | APPLY | Can't verify N6; honest UX expectations. Zero negative impact. |
| C4 | Clarify AD16 (BroadcastChannel) is for opencode pod budget, not API replica budget | APPLY | Correct rationale prevents future regressions. |
| C5 | Note that `Workspace.Status.ActiveSessions` is misleadingly named | APPLY | Prevents anyone re-introducing the bug C1 corrects. |
| C6 | Drop `/sessions/active` endpoint, use `WorkspaceListItem.ActiveSessions` instead | REJECT | Wrong source (per N4). |
| C7 | Use `RateLimiterService.Increment` for activate-lock instead of new `SetNX` | REJECT | Not atomic; TOCTOU race verified in code. |
| C8 | Replace SSE-based counting with proxy-completion-hook | REJECT | Doesn't work for prompt_async path; cosmetic-only field. |
| C9 | Drop BroadcastChannel multiplex | REJECT | Real opencode pod-side budget protection. |
| C10 | Drop title rename endpoint | REJECT | Sidebar would have empty titles forever. |
| C11 | Defer rename of `Workspace.Status.ActiveSessions` → `SandboxCount` to V1.1 | APPLY | Documents tech debt at the source. |
| C12-C13 | Add N1-N11 to §0 table | APPLY | Pulls hidden assumptions into the explicit table. |
| C14 | Document SessionIndexService lifecycle order | APPLY (no doc change needed; stating in N10) | |
| C15 | Note single-drainer-goroutine bottleneck risk in §21 | APPLY | Honest scalability expectation. |
| C16 | Document per-replica connection counter in §10.3 | APPLY | Operators need this to size capacity correctly. |

8 changes applied. 5 rejected with reason. 3 covered by table additions.

### What v3.0 did NOT change

Crucially, all of the following were *re-examined* and kept unchanged because no benefit could be proven over the cost:

- `SetNX` cache extension (vs reuse Increment): kept; Increment is not atomic.
- SSE-based session-index updates (vs hybrid post-stream hook): kept; cosmetic field; hybrid adds complexity.
- BroadcastChannel multiplex: kept with clarified rationale.
- `PUT .../title` endpoint: kept.
- `GET /workspaces/{id}/sessions/active` endpoint: kept (became more justified after N4 ruled out the alternative).
- `GET /workspaces/{id}/sandboxes` endpoint: kept.
- Hand-written TS types + contract test: kept.
- HttpOnly cookie + cookie-set-in-router: kept.
- All 18 architectural decisions AD1-AD18: kept.

### Diff summary

`design/FRONTEND.md` v2.0 → v3.0:
- §0: added N1-N11 (11 new validated assumption rows including 2 REFUTATIONS that drove design fixes).
- §5.2: removed `ActiveSessions` and `LastActivityAt` from the WorkspaceListItem extension. Kept `Phase` and `MaxActiveSessions`. Documented why omissions are intentional.
- §5.6: added "Approximation note (per N6)" subsection.
- §6.2: tagged `message_count` source as "Approximate".
- §10.3: rewrote with explicit per-replica clarification.
- §21: added 3 new risk rows (message_count deviation, single-drainer bottleneck, operator misread of cap).
- §22: added 4 new deferred items (rename `ActiveSessions` → `SandboxCount`; restore `LastActivityAt` on demand; multi-drainer pool; etc.).
- Header: change-log v2.0 → v3.0 added at top.
- Appendices: stale `activeSessions` and `lastActivityAt` JSON samples corrected.
- Phase A item list: removed reference to deleted fields.

Total: 1,260 lines (was 1,216 in v2.0; net +44 lines for the new assumption rows, the approximation discussion, the new risks, and the change log).

### Build status

`go build ./...` passes. No code was touched in this session (design-only).

### Next session

Phase A backend prerequisites can begin. Pick up from §20 of the v3.0 design.

### Files modified in v3.0 revalidation

- `design/FRONTEND.md` — v2.0 → v3.0 (targeted edits only; no rewrite).
- `worklogs/0034_2026-05-23_frontend-design-doc.md` — appended this v3.0 addendum.

---

## Addendum: v4.0 Third Revalidation (same date)

User asked for a third pass with the same higher bar: changes only land if a meaningful benefit can be proven AND no negative impact on the rest of the project can be demonstrated.

### Method

Re-read v3.0 §1-§22 carefully, looking for:
1. Claims that referenced code without a line-number citation.
2. Implementation details that "would happen" without specifying how.
3. Hidden assumptions not yet in §0.
4. Anything that contradicted what an actual code read would show.

Identified 25 candidate concerns to investigate. Read 7 additional source files / sections to validate or refute each one:

- `api/internal/services/sandbox/sandbox_service.go:362-388` — discovered the canonical sandbox-by-workspace label `llmsafespace.dev/workspace=<id>`, used by `controller.go:508,555`. v3.0 was hand-wavy about which label to use for `GET /workspaces/{id}/sandboxes`.
- `api/internal/services/workspace/workspace_service.go:478-512` — confirmed workspaces are labeled `user-id=<userID>`. Implies single label-selected list call avoids N+1 in both `ListWorkspaces` extension and the activate stalest-finder.
- `api/internal/handlers/proxy.go:53-54` — confirmed `activeSess` is also a per-replica in-process map (same pattern as `connCount`/N11). v3.0 only flagged `connCount`. The 429 cap is therefore also enforced per-replica — a pre-existing V2 behavior with capacity-planning implications operators need to know.
- `api/internal/handlers/proxy.go:148-153` — confirmed `CreateSession` is pure pass-through. Index updates flow only through busy/idle SSE events, so a freshly created session with no messages is invisible to the suspended-workspace path.
- `api/internal/app/app.go:95-98` — confirmed the API HTTP server has no `ReadTimeout`/`WriteTimeout`. Synchronous handlers can block; in production an Ingress timeout (typically 30-60s) would cut them off. Triggered §7.3 fix: activate handler no longer polls for 30s in-process; returns 503 + Retry-After immediately on transient phase.
- `api/internal/middleware/security.go:142-174` — confirmed `Origin`-check is informational (suppresses CORS reflect) but does NOT abort. No CSRF middleware exists. With `SameSite=Lax` (default), browser-side CSRF defense is sufficient; with `SameSite=None` (cross-origin), an explicit middleware is required and is deferred to V1.1.
- Verified `pkg/interfaces/kubernetes.go:37` exposes `List(opts metav1.ListOptions)` — accepts label selectors via the standard contract.

### Triage

16 candidate changes evaluated. 12 applied, 3 rejected, 1 already covered.

**Applied (proven benefit, demonstrable absence of negative impact):**

| # | Change | Reason |
|---|---|---|
| F1 | Pin `llmsafespace.dev/workspace` as the label for `GET /workspaces/{id}/sandboxes` | Validated against `sandbox_service.go:372` and `controller.go:508,555` — the canonical label is already used by the controller. Reusing it ensures the new endpoint integrates with existing reconciler behavior; no schema change. |
| F2 | Document per-replica `/sessions/active` view (per N12) | Honest UX expectation. Frontend SSE is authoritative once the workspace is open. |
| F3 | Document per-replica 429 cap (N13, pre-existing) | Operators need this for capacity planning. |
| F4 | Specify register handler also sets the cookie | Closes a UX gap (otherwise user must log in again post-register). |
| F5 | Replace 30s in-handler polling in activate with immediate 503 + Retry-After | Avoids long-held HTTP requests that risk Ingress write timeouts (per N17). Frontend already polls /status. |
| F6 | Activate stalest-finder uses single label-selected list | Eliminates N+1 to apiserver. Matches existing pattern. |
| F7 | ListWorkspaces uses single label-selected list to merge phase/maxActive | Same pattern; eliminates N+1 in sidebar render. |
| F8 | Pin Postgres UPSERT SQL in §5.5 | Removes implementation ambiguity for the implementer. |
| F9 | Add `react-markdown` + `rehype-sanitize` to locked deps | Resolves §14.1 ↔ §8.1 inconsistency in v3.0. |
| F10 | Specify SameSite=Lax as primary CSRF defense; cross-origin defense deferred | Concrete defense story instead of "middleware can verify". |
| F11 | Note freshly-created-empty session UX trade-off | Honest documentation. |
| F12 | Mark CRD field rename as breaking change in §22 | Prevents future ill-considered rename. |

**Rejected (no clear benefit OR negative impact):**

| # | Change | Reason for rejection |
|---|---|---|
| F13 | Document drop-oldest semantic of RecordMessage | Already in v3.0 §5.5 — no change needed. |
| F14 | Add batched `/workspaces/sessions/active` endpoint | Premature optimization. Sidebar typically <10 workspaces; 10 calls is fine. New endpoint = more surface area. |
| F15 | Move `activeSess` to Redis for cross-replica consistency | Significant change to existing proxy hot path; out of scope for a frontend epic. Documented as deferred follow-up (§22). |

### Document v4.0 produced

Targeted edits only; no rewrite. v3.0 → v4.0:

- **Header:** added v3.0 → v4.0 change log.
- **§0:** added N12-N18 (7 new validated assumption rows). 41 → 47 validation labels.
- **§5.2:** specified the implementation as a single label-selected list call, not N+1.
- **§5.3:** registration handler now also sets the cookie.
- **§5.4:** pinned canonical sandbox label; documented per-replica `/sessions/active`; flagged the empty-session trade-off.
- **§5.5:** pinned Postgres UPSERT SQL.
- **§7.3:** removed in-handler polling; specified single label-selected list.
- **§7.5:** updated edge-case table to reflect the new immediate-503 behavior.
- **§8.1:** added `react-markdown` + `rehype-sanitize`.
- **§9.5:** documented per-replica caveat for `N/M active`.
- **§10.1:** documented per-replica 429 cap (N13).
- **§14.2:** concrete CSRF defense story per cookie mode.
- **§21:** 4 new risk rows.
- **§22:** clarified breaking-change impact of CRD field rename; added Redis-backed `activeSess` and CSRF-middleware items.

Total: 1,314 lines (up from 1,260 in v3.0; +54 lines).

### Build status

`go build ./...` passes. No code touched in this session (design-only).

### Files modified in v4.0 revalidation

- `design/FRONTEND.md` — v3.0 → v4.0 (targeted edits only; no rewrite).
- `worklogs/0034_2026-05-23_frontend-design-doc.md` — appended this v4.0 addendum.
