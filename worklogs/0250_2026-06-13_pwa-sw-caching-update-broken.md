# Worklog: PWA SW Caching — "Update available [Reload]" does nothing

**Date:** 2026-06-13
**Session:** Root-cause analysis and fix for broken PWA update flow after Epic 40 deploy
**Status:** Complete

---

## Objective

Investigate why "Update available [Reload]" toast appears in the frontend after `helm upgrade` to image `ts-1781332002` but clicking Reload does nothing.

---

## Work Completed

### Root cause investigation

Confirmed the running image via `kubectl get pod llmsafespace-frontend-6bc74b8d-dsvcn -o jsonpath='{.spec.containers[0].image}'` → `ghcr.io/lenaxia/llmsafespace/frontend:ts-1781332002`, which is HEAD (`bf61310d`), not the `values-cluster.yaml` tag `ts-1781285219`. The cluster had already been upgraded out-of-band.

Identified the correct "previous deploy" as `ts-1781285219` and "new deploy" as `ts-1781332002`. The commits between them include `c318bbcc` (Epic 40 — Markdown rendering overhaul), which was the only commit that touched `vite.config.ts`.

Ran `kubectl exec` on both images to extract precache manifests and routing strategies from the live `sw.js` files:

- **Old (`ts-1781285219`):** `workbox-window` and `workerBundle` in precache. No `CacheFirst` route.
- **New (`ts-1781332002`):** `workbox-window` and `workerBundle` absent from precache. `CacheFirst` route for all `/assets/*.js` → cache name `async-chunks`.

Epic 40's `globPatterns` change narrowed from `**/*.{js,...}` to only `index*`, `vendor*`, `query*` — accidentally dropping `workbox-window` and `workerBundle`. The broad `CacheFirst` route then caught them and served them stale forever.

Traced the `updateServiceWorker(true)` call path through `vite-plugin-pwa/dist/client/build/react.js` to confirm `window.location.reload()` is only reached if `event.isUpdate` is true on the `controlling` event, which requires `mn = !!navigator.serviceWorker.controller` to be truthy at registration time (it is). The reload fires, but the reloaded page serves `workbox-window` from the stale `CacheFirst` cache — so on future updates, `workbox-window` itself is the stale library handling the update flow.

Also confirmed `sw.js` has no `no-store` header in `nginx.conf` — a pre-existing gap that can prevent update detection at the HTTP cache layer.

### Fix

**`frontend/vite.config.ts`:**
- Added `workbox-window*.js` and `workerBundle*.js` to `globPatterns`.
- Scoped `CacheFirst` `urlPattern` to exclude core chunks via an explicit exclusion regex.
- Renamed cache `async-chunks` → `shiki-chunks` (matches purpose; orphans stale entries on next SW activation).
- Raised `maxEntries` 50 → 350 to cover all ~301 Shiki grammar/theme chunks.
- Added detailed comments documenting the two-category caching contract and the manual-sync requirement.

**`frontend/nginx.conf`:**
- Added `Cache-Control: no-store` for `sw.js` and `manifest.webmanifest`.

**`frontend/src/lib/shiki-chunks.test.ts`:**
- Added unit test for the exclusion regex: asserts core chunk paths are excluded from `CacheFirst` and Shiki chunk paths are included.

### PR

Opened PR #139. CI failed on first push due to `package-lock.json` containing harmony-registry-generated lockfile entries (`@emnapi/wasi-threads` version mismatch). Fixed by reverting the lockfile to `origin/main` and re-committing without it. Second push: all CI checks pass. AI reviewer approved (twice — two review runs triggered).

Addressed reviewer feedback: fixed regex redundancy in `workbox-window[^/]*` alternation, added exclusion regex unit test, added this worklog.

---

## Key Decisions

- **Exclusion regex over allowlist.** The `urlPattern` uses a negative regex (exclude known core chunks) rather than a positive match for Shiki chunks (e.g., matching by filename pattern). The exclusion approach is more robust: new Shiki grammars added by `shiki` updates are automatically handled without any config change. The risk (a new core chunk being silently `CacheFirst`-cached) is mitigated by the explicit comment requiring both lists to be updated in parallel.

- **`maxEntries: 350`** over an exact count. Shiki currently ships 301 grammar/theme chunks; 350 gives ~15% headroom for future additions without LRU thrashing.

- **Cache rename `async-chunks` → `shiki-chunks`.** Serves dual purpose: accurately names the cache, and causes existing browsers to abandon the stale `async-chunks` entries on next SW activation (Workbox does not migrate between differently-named caches).

---

## Blockers

None.

---

## Tests Run

- `npm run build` — passes locally after `npm install` with public registry.
- CI: all checks green on second push (Build Frontend amd64/arm64, Frontend unit+typecheck+e2e, Lint, Trivy, govulncheck, Gitleaks, pkg/secrets integration).

---

## Next Steps

Deploy the fix via `helm upgrade` with a new image tag built from this branch after merge.

---

## Files Modified

- `frontend/vite.config.ts`
- `frontend/nginx.conf`
- `frontend/src/lib/shiki-chunks.ts` (new)
- `frontend/src/lib/shiki-chunks.test.ts` (new)
- `worklogs/0250_2026-06-13_pwa-sw-caching-update-broken.md` (this file)
