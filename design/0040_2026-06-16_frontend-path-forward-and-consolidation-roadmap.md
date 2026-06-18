# Frontend — Path Forward & Consolidation Roadmap

**Date:** 2026-06-16
**Status:** Strategic synthesis — no code changes
**Synthesises:** `0037` (busy indicators), `0038` (broader findings), `0039` (quirks/dead code), `0040` (this doc — provider UX + roadmap)

---

## 1. The LLM-provider-UX question, precisely

The user asked: "we have three separate LLM provider UXes and we really should just have one." Validated against backend migrations, router registration, and the agent-config.json materialization path:

| UX surface | Verdict | Evidence |
|---|---|---|
| `UserProviderCredentialsTab` ("Provider Keys") | **Keep — the canonical user path** | Structured fields, `/v1/models` probe, explicit + auto workspace bindings. `provider_credentials` table. |
| `SecretsTab` type=`llm-provider` ("Secrets") | **Overlap — consolidate into Provider Keys** | Same `applyLLMProvider → FlushProviders` materializer (`secrets.go:581-589`), same wire format; weaker UX (free-text value, no probe, generic binding model). `user_secrets` table. |
| `AdminProviderCredentialsTab` ("Platform Credentials") | **Keep — distinct audience** | `owner_type=admin`, admin-guarded, auto-apply rules. Different lifecycle from user keys. |
| `AdminCredentialsTab` | **Delete — dead** | `credential_sets` table dropped (migration `000015:10`); `/admin/credentials` route not registered (`router.go`); never rendered in `SettingsPage.tsx`. |
| `RelayTab` / `RelaySetupWizard` / `RelayStatusDashboard` | **Keep — different concept entirely** | Cloud egress infra (AWS/OCI/GCP on WireGuard). "Provider" = cloud provider, not LLM. `InferenceRelay` CRD + K8s Secrets. |

**Real consolidation: two → one for the user-facing LLM-key path, plus delete one dead file.**

### 1.1 Why the SecretsTab overlap is non-trivial to remove

It is not a pure delete. The two paths write to **different Postgres tables** that both feed the *same* K8s Secret (`secrets.json`) on the pod via *different* decryption paths. Before folding:
- Existing `user_secrets` rows of type `llm-provider` must be migrated to `provider_credentials` (or grandfathered with a read-only view in the Provider Keys tab).
- The `SecretsTab` "type" picker needs `llm-provider` removed from the creatable set (it already does this for legacy `api-key` — `SecretsTab.tsx:14-19` — the same pattern applies).
- Workspace bindings differ in model: `provider_credentials` uses `workspace_credential_bindings`; `user_secrets` uses the generic secret-binding path. A bound `llm-provider` secret's binding must be preserved across migration.

This is a real story (~1–2 days), not a cleanup PR. But it is the right consolidation: it eliminates "which of these two do I use?" for the user and removes a divergent weaker path.

### 1.2 Recommendation

- **Now:** delete `AdminCredentialsTab.tsx` + test (pure removal, zero risk — backend already gone).
- **Story:** fold `SecretsTab`'s `llm-provider` into `UserProviderCredentialsTab` with a data migration. Decision needed from user: do existing `llm-provider` secrets need migration, or is the dataset small enough to ask users to re-enter via Provider Keys?

---

## 2. Consolidation opportunities (full menu across all four passes)

Grouped by theme. Each is a real "tie things together" candidate, validated.

### C1 — State-management: one fetch paradigm (from `0039` Q3)
**15 settings/org-admin components** hand-roll `useState(true)` + try/catch/finally; the rest of the app uses TanStack Query. Two paradigms for server state. **Highest-leverage consolidation for ongoing maintainability** — every future settings feature currently reinvents loading/error/refetch.

### C2 — Dead-code clusters: one cleanup sweep (from `0037` P3, `0039` Q4, §1.2 above)
All the same pathology (parallel implementations left behind by successive LLM sessions). Bundle into one PR:
- `AdminCredentialsTab.tsx` + test (backend dropped)
- `lib/stream.ts` + 2 tests (HTTP-streaming parser orphaned by SSE move)
- `SessionItem.tsx` / `SessionList.tsx` / `workspace/WorkspaceSessionList.tsx` (superseded by tree in Sidebar)
- `api/events.ts` + test (BroadcastChannel SSE never wired)
- `hooks/useSessions.ts`, `hooks/useWorkspaces` list export (unused)
- `api/workspaces.ts:55-56` `getActiveSessions`, `getWorkspaceSessions` (unused; latter also wrong-typed)
- `api/workspaces.ts:49-50` `ensureSession` + `EnsureSessionResponse` (duplicate of `sessions.ts`)

### C3 — Busy/loading mechanism: one source of truth (from `0037` P1, P2)
- Chat's `serverBusy`/`sseHasDrivenBusy` → consume `useIsSessionBusy()` from `SessionActivityProvider`.
- One `Spinner` (loading) + one `BusyIndicator` (agent working) primitive; replace 7 inline `Loader2`.

### C4 — LLM-provider UX: two→one (this doc, §1)
Covered above.

### C5 — Diagnostic logging: one gated logger (from `0038` P5)
`wsLog` always-on in prod (21 sites) + stray `console.log` in `useSessionTitle.ts:39`. Gate behind a flag; remove the stray.

### C6 — Per-message render cost (from `0038` P6)
`MessageBubble`'s `useNow()` interval is wasted (clock-time display doesn't need it) and defeats `memo`.

### C7 — Cache-subscribe hot path (from `0038` P7)
`SessionActivityProvider`'s `queryCache.getAll()` walk on every sessions event. Use `event.query`.

### C8 — Ref+counter workaround (from `0038` P8)
`contextBySessionRef` + `contextVersion` → scalar `useState`. Also revisit the undocumented 50% compaction threshold.

### C9 — Route code-splitting (from `0039` Q5)
Lazy-load org-admin + settings.

### NOT a consolidation (validated — recording so it's not re-litigated)
- **The two SSE connections cannot be merged** (`0038` §1). They carry genuinely different event types, confirmed from backend `PublishToUser` vs `broker.Publish` call sites. Only cross-tab *multiplexing per stream* would reduce connection count, and that's a larger separate decision.
- **Admin vs User provider credentials** are distinct audiences, not a split to merge.
- **The relay** is unrelated to LLM credentials.

---

## 3. The silent failure that should not wait (from `0039` Q1)

**Workspace Auto-Suspend settings are discarded** (`Sidebar.tsx:412` → `onSave={async () => {}}`). The drawer collects the toggle + idle timeout with a cost warning, the user clicks Save, nothing persists; the backend only honours the global instance setting. This is the only item actively misleading users *right now*. It needs a product decision before any of the consolidations:

- **Wire** (per-workspace autoSuspend) — requires backend PATCH for `spec.autoSuspend` + frontend `onSave` PUT. Real story.
- **Remove** the Auto-Suspend section from the drawer + direct users to the global admin setting. Trivial.

Either is defensible; the status quo is not.

---

## 4. Recommended sequencing

Ordered by risk-to-value ratio and dependency. Each phase is independently shippable.

### Phase 0 — Dead-code sweep (C2) + the stray log (C5 partial)
**Risk:** zero. **Value:** removes ~6 misleading parallel implementations that will confuse any future LLM or human working on the areas touched by Phases 1–3. Doing this *first* makes every later diff smaller and cleaner.
- One cleanup PR. No behaviour change. Run `npm run typecheck && test && lint` after.

### Phase 1 — Fix the silent failure (§3) + dead AbortController (C2/Q2)
**Risk:** low. **Value:** stops actively misleading users.
- **Blocker:** product decision on Auto-Suspend (wire vs remove).
- AbortController removal is trivial and independent.

### Phase 2 — Busy-indicator unification (C3)
**Risk:** medium (touches the most-tested chat area). **Value:** the original "tie things together" ask; removes dual state derivations.
- Follow `0037` P1's 5 validation gates (REST-seed parity, timeout-ref preservation, reconnect semantics, isReconnectMode derivation, test rework).
- Do P1 (mechanism) before P2 (primitives) so the concept has one source before unifying its rendering.

### Phase 3 — Performance hot paths (C6, C7, C8)
**Risk:** low–medium. **Value:** measurable; removes per-message and per-event cost.
- These are independent; can be three small PRs. C7 and C8 have strong existing test coverage to lean on.

### Phase 4 — The bigger consolidations (C1, C4) — parallelisable
**Risk:** medium. **Value:** the highest-leverage long-term maintainability wins.
- **C1 (Query migration)**: high volume, mechanical, per-tab. Run as an epic with a checklist; each tab is a clean PR. Tackle the org-admin tabs first (they're the most uniform pattern and ship in the lazy-loaded bundle from C9).
- **C4 (provider UX fold)**: needs the §1.2 migration decision. ~1–2 day story. Can proceed independently of C1.
- **C9 (code-splitting)**: trivial, do alongside C1 (lazy boundaries come for free once each tab is a clean module).

### Explicitly deferred
- Cross-tab SSE multiplexing (`0037`/`0038` P4) — only if connection pressure is observed. Update `design/0026` §10.3 to match reality now.
- Compaction-threshold redesign (C8 sub-item) — product decision.
- The 5 `eslint-disable exhaustive-deps` audits (`0039` §7.5) — opportunistic, when touching each file.

---

## 5. Decision points to unblock work

These are the questions only the user can answer; they gate specific phases:

1. **Auto-Suspend (gates Phase 1):** wire per-workspace, or remove the UI and use the global setting only?
2. **Provider UX fold (gates C4):** migrate existing `llm-provider` secrets to `provider_credentials`, or ask users to re-enter (if dataset is small)?
3. **Query migration scope (gates C1):** full settings+org-admin migration, or just the org-admin surface (highest uniform value) for now?
4. **wsLog gating (gates C5):** is always-on console logging intentional for support diagnostics, or should it default off in prod?

Everything else (C2, C3, C6, C7, C8, C9) needs no decision — they're clear improvements with validated justification.

---

## 6. One-paragraph summary

The frontend's issues are architectural drift, not syntactic decay: the code is cleanly typed and lint-passes, but successive LLM sessions left behind parallel implementations (two fetch paradigms, two LLM-key UXes, two busy-state derivations, dead orphan layers) and one live silent failure (Auto-Suspend). The path forward is: sweep the dead code first (it's zero-risk and shrinks every later diff), fix the silent failure (needs one product decision), then unify mechanisms in order of risk (busy indicators → perf hot paths → the two big consolidations: TanStack Query everywhere and one provider-key UX). The two SSE connections are genuinely necessary and should not be merged; the admin/user provider-credential split and the relay are distinct concepts, not redundancies.
