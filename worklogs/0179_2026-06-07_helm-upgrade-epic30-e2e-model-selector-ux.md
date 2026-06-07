# Worklog: Helm Upgrade → Epic 30 E2E Validation → Model Selector UX Session

**Date:** 2026-06-06 (evening PDT) → 2026-06-07 (early morning PDT)
**Agent:** agent-audit-0606
**Session scope:** Helm upgrade deployment, Epic 30 full e2e + UX validation, model selector UX bug fixes, json tag bug found and fixed. Session ended when Longhorn storage outage began (documented separately in worklog 0178).
**Status:** All work complete — 5 PRs merged, deployed to cluster

---

## What Was Done

### 1. Helm Upgrade to ts-1780783237 (revision 159)

Deployed the merged accumulation of fixes (PRs #46–#48 from prior session) plus the Epic 30 audit fixes (PR #45). This included:
- `fix(test): sseConnection cleanup` (1baac7d)
- `fix(credentials): address all 17 true-positive audit findings` (squash of fix/epic30-credential-audit)
- `test(epic28): goroutine leak + write-deadline tests` (f1af270)

Deployed as revision 159 (`ts-1780783237`).

---

### 2. Epic 30 End-to-End Validation

Full live validation of the Epic 30 credential pipeline:

**Admin credential path:**
- `POST /admin/provider-credentials` — create with `apiKey`, `baseURL`, `modelAllowlist` ✓
- `PUT /admin/provider-credentials/:id` — key rotation returns `baseURL` in response (M-8 fix confirmed) ✓
- `POST /admin/provider-credentials/:id/auto-apply` — `targetType: all` rule created ✓
- `DELETE /admin/provider-credentials/:id` — returns 404 for missing IDs (L-1 fix confirmed) ✓

**User credential path:**
- `POST /provider-credentials` — create, returns 201 with auto-bind to all workspaces ✓
- `GET /provider-credentials/:id/bindings` — returns `{workspaceIds, bindings}` with `sourceType` ✓
- `DELETE /provider-credentials/:id/bind/:wsId` — auto-bound returns 409 (H-1 fix confirmed) ✓
- `DELETE /provider-credentials/:id` — notifies bound workspaces (C-3 fix confirmed) ✓

**Workspace + model flow:**
- `POST /workspaces` → `SeedWorkspaceCredentials` auto-binds platform + user credentials ✓
- `GET /workspaces/:id/models` → 118 models, 98 available (user openai key injected), 20 free ✓
- `PUT /workspaces/:id/model` → SetModel with `glm-5.1` correctly resolves via `resolveModelIDFromCatalog` ✓
- Live LLM call → `providerID=openai modelID=gpt-5.5` reached `https://ai.thekao.cloud/v1` — 500 due to quota, not code issue ✓

**Bugs found during live validation:**

#### Bug A: `LLMSAFESPACE_MASTER_SECRET` not wired in Helm chart
The API pod was starting without `MASTER_SECRET` because the Helm chart template didn't inject it. The `opencode-free-tier` free-tier credential seeding was skipping on every pod start. Fixed in earlier session.

#### Bug B: json tags missing on `CredentialBindingInfo` (PR #49)
`GET /provider-credentials/:id/bindings` was returning:
```json
{"bindings": [{"WorkspaceID": "...", "SourceType": "auto"}]}
```
instead of camelCase `workspaceId`/`sourceType`. Go struct had no `json:` tags. The binding panel in the UI showed every workspace as "Bind" regardless of actual bound state. **Fixed: PR #49** (`3d3a5086` / `db8ba347`).

#### Bug C: `OPENCODE_AUTH_CONTENT` env var — old platform opencode credential had `apiKey: public`
The `opencode-free-tier` admin credential was configured with `apiKey: "public"` which fails authentication for paid models (`glm-5.1`, `deepseek-v4-flash`) on the `opencode.ai/zen/v1` relay. Updated to use `apiKey: $OPENAI_API_KEY` with `baseURL: https://ai.thekao.cloud/v1`. This makes `glm-5.1` and `deepseek-v4-flash` available via the proxy.

---

### 3. ModelSelector Disappear Bug (PR #51)

**Symptoms:** Model selector in the chat UI flashed briefly on workspace activation then disappeared permanently until page reload.

**Root causes identified:**

1. **`return null` on empty models during background refetch** (primary) — `invalidateQueries` fires on every `setModel` success. During the re-fetch window, `data?.models ?? []` is `[]`, hitting the unconditional `return null` guard. Fixed with `placeholderData: keepPreviousData` + tightened null guard to `!isLoading && models.length === 0`.

2. **Duplicate `useQuery` in ChatPage races with ModelSelector** — `ChatPage` had its own `useQuery(["models", workspaceId])` gated on `isReady && !!workspaceId`. When `isReady` flips true, this second query fires independently. Fixed to use same query key with `enabled: !!workspaceId` + `notifyOnChangeProps: ["data"]`.

3. **Redundant frontend `models.filter(m => m.enabled)`** — backend already hard-filters unavailable/disabled models. Removed.

**Regression test added:** `stays visible during background refetch` — uses `qc.removeQueries` to simulate the `invalidateQueries` window and asserts the button stays visible.

**PR #51** merged `cb763d72`.

---

### 4. Model Selector Shows Wrong Models / Free Models Broken (PR #53)

**Symptoms reported:** (1) Model selector showing all catalog models as "available" (paid). (2) Free models broken — messages failing with `ProviderModelNotFoundError`.

**Investigation:**

The workspace `5b573d58` (Mike's) showed `total=0 models` from the API even though opencode had 118 models loaded. Deep investigation revealed three bugs:

**Bug 1: `isZeroCostOpencode` missed `opencode-relay` providerID**

After Phase 2 relay injection, opencode uses `providerID="opencode-relay"` for free models. `isZeroCostOpencode()` only matched `providerID=="opencode"`, so all relay models were classified `ModelAvailable` (paid) instead of `ModelFreeTier`. Users saw free models as requiring an API key.

**Bug 2: `annotateModels` remap too broad**

The `opencode`→`opencode-relay` remap fired for any `ModelFreeTier` model, not just ones with `providerID=="opencode"`. In Phase 2 (catalog already has `opencode-relay`), models were being double-processed. Narrowed to only remap when `m.ProviderID == "opencode"`.

**Bug 3: Relay injector races with provider catalog initialization (primary)**

The relay injector fires 2s after opencode's HTTP server responds (`HealthCheck`), but opencode's provider catalog takes ~16s to fully initialize (`providers_connected` gate). When `fetchFreeModels` runs in the 14-second window:

```
ts=1780801078 "relay injector: no free opencode models found, skipping relay config"
ts=1780801087 "startup gate reached gate=providers_connected elapsed=16.4s"
```

The injector **permanently skips** relay injection for the pod lifetime. Consequence: `opencode-relay` is never registered but `annotateModels` still remaps free models to `opencode-relay` in the API response → `ProviderModelNotFoundError` on every message.

**Fix:** Retry `fetchFreeModels` for up to 30s on 0-model response (5s intervals).

**PR #53** merged `24e71f59`.

---

### 5. Stale `model_allowlist = {default}` Causing 0 Providers (PR #52)

**Symptom:** Mike's workspace showed `total=0 models` despite Longhorn and credentials being healthy.

**Root cause:** The OpenAI platform credential (`epic30-openai-1780700420`) had `model_allowlist = {default}` in the DB — a stale entry from the original e2e test create call that interpolated `${OPENAI_DEFAULT_MODEL}` = `"default"`. The injection code synthesized `LLMModelConfig{ID: "default"}`, `FormatOpenCodeConfig` wrote `"models": {"default": {}}` into the provider entry. opencode sees a provider with one model (`default`) that doesn't exist in its catalog → treats the provider as unconfigured → returns `[]` from `/api/provider`.

**Fix in injection.go:** Skip `""` and `"default"` as invalid allowlist IDs. If all IDs are invalid, leave `pd.Models` nil (provider registered with no model filtering = safe fallback).

**Additional DB fixes applied:**
- Cleared `model_allowlist` on the platform OpenAI credential
- Added `all` auto-apply rule for the OpenAI platform credential (was missing — only `opencode-free-tier` had an `all` rule)
- Backfilled OpenAI platform credential binding to all 162 existing workspaces

**PR #52** merged `c847761b`.

---

## Deployed Revisions

| Revision | Tag | What |
|----------|-----|------|
| 159 | ts-1780783237 | Epic 30 audit fixes + goroutine tests |
| 160 | ts-1780802868 | Injection invalid allowlist fix (PR #52) |
| 161 | ts-1780806706 | Free model classification + relay race fix (PR #53) |

---

## Cluster State at Session End (before Longhorn outage)

- `glm-5.1` and `deepseek-v4-flash`: `availability=available` ✓
- Model selector: correctly shows 118 models (20 free, 98 available) ✓
- Admin credentials: CRUD + auto-apply fully functional ✓
- User credentials: CRUD + bind/unbind + listBindings with sourceType ✓
- Credential injection: platform openai + opencode both injected correctly ✓
- Workspace `5b573d58` (Mike's): Active, 118 models, ready for chat ✓

**Then Longhorn went down** — see worklog 0178.

---

## Open Items at Session End

- Workspace `5b573d58` Suspended (controller gave up during Longhorn outage) — needs manual resume
- All other workspaces stuck in `Init:0/1` during outage — need health check after Longhorn recovery
- Flux HelmRelease for Longhorn: PR #1825 in talos-ops-prod needs merge
- Renovate package rule for Longhorn manual approval: not yet committed (talos-ops-prod)
- `glm-5.1` (non-free via opencode.ai relay) still requires an opencode.ai account key — the `ai.thekao.cloud` proxy serves it under the openai provider but not the opencode relay

---

## Files Changed (this session, by PR)

**PR #49** (`3d3a5086`, `db8ba347`) — `pkg/secrets/credential_store.go`

**PR #51** (`cb763d72`) — `frontend/src/components/chat/ModelSelector.tsx`, `ModelSelector.test.tsx`, `frontend/src/pages/ChatPage.tsx`

**PR #52** (`c847761b`) — `pkg/secrets/injection.go`, `pkg/secrets/credential_precedence_test.go`

**PR #53** (`24e71f59`) — `api/internal/handlers/models.go`, `models_test.go`, `cmd/workspace-agentd/relay_injector.go`, `relay_injector_test.go`
