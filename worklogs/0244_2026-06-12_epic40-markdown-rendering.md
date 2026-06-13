# Worklog: Epic 40 — Markdown Rendering Overhaul

**Date:** 2026-06-12
**Session:** Implement all 8 user stories for Epic 40 (markdown rendering, dark mode, syntax highlighting, code blocks, streaming, PWA cache)
**Status:** Complete

---

## Objective

Implement the full Epic 40: Markdown Rendering Overhaul — fix broken prose styles, dark mode, add syntax highlighting with shiki, code block component, streaming no-flicker, fence scanner fix, diff viewer dark mode, and PWA cache eviction. All stories in a single PR per the epic execution order.

---

## Work Completed

### US-40.2: Fix dark mode — `@custom-variant dark` + `.dark` CSS variable block

- Added `@custom-variant dark (&:where(.dark, .dark *))` to `index.css` after `@import "tailwindcss"`
- Added `.dark { ... }` CSS variable block mirroring the `@media (prefers-color-scheme: dark)` block exactly (19 CSS custom properties)
- No changes to `ThemeProvider.tsx` — it was already correct

### US-40.1: Install `@tailwindcss/typography`

- Added `@plugin "@tailwindcss/typography"` to `index.css`
- Removed hand-written `.prose table`, `.prose th`, `.prose td` CSS rules
- Installed `@tailwindcss/typography@0.5.20` via npm

### US-40.6: Fix streaming fence scanner

- Added exported `closeOpenFence()` pure function replacing the broken line-count heuristic
- Handles 3+ backtick fences, tilde fences, language info strings, and CommonMark closing rules (same char, length >= opening)
- Replaced inline heuristic in `MessagePart` with `closeOpenFence(text)` call
- Added 13 unit tests for `closeOpenFence` covering all CommonMark fence variants

### US-40.8: Fix diff viewer dark mode

- Added `useTheme()` import and `resolved` read in `MessagePart`
- Changed `ToolDiffView` to accept `isDark: boolean` prop
- Replaced hardcoded `useDarkTheme` with `useDarkTheme={isDark}`

### US-40.3: Add syntax highlighting — shiki singleton

- Installed `shiki@4.2.0`
- Created `src/lib/shiki.ts` with module-level singleton using `createHighlighter` from `shiki/bundle/full` + `createJavaScriptRegexEngine()`
- `highlight(code, lang)` returns dual-theme HTML with `defaultColor: false`
- Added `html.dark .shiki` CSS override block with `!important` in `index.css`

### US-40.4: CodeBlock component

- Added `CodeBlock` component to `MessagePart.tsx` with:
  - Language label in header bar (top-left)
  - Copy button (top-right) with 2s revert
  - `not-prose` escape hatch from Tailwind Typography
  - `wordWrap` support via `codeBlockWordWrap` setting
  - Plain `<pre>` fallback when `highlight()` returns null
- Added custom `components` prop to `ReactMarkdown` with `pre` and `code` renderers
- Removed redundant arbitrary-variant selectors from prose container (`[&_pre]:overflow-x-auto`, `[&_pre]:touch-manipulation`, `[&_:not(pre)>code]:break-all`)

### US-40.5: No flicker during streaming

- Added `isStreaming` prop to `CodeBlock`
- Guard in `useEffect`: `if (isStreaming || !lang) return;` — skips `highlight()` during streaming
- Reset effect: `if (isStreaming) setHighlightedHtml(null)` — defensive guard for restart scenarios
- When `isStreaming` flips `true → false`, `useEffect` fires once with final code content

### US-40.7: Fix PWA cache

- Updated `globPatterns` in `vite.config.ts` to explicitly list core chunks: `**/index*.js`, `**/vendor*.js`, `**/query*.js` (plus CSS, HTML, SVG)
- Added `async-chunks` runtime cache entry: `CacheFirst` with `maxEntries: 50`, `maxAgeSeconds: 2592000` (30 days)
- Verified at build time: precache has 10 entries (core chunks only), language grammar chunks are runtime-cached

### Test updates

- Added `vi.mock("../../lib/shiki")` to `MessagePart.test.tsx` with `highlight` mock returning `null` by default
- Fixed broken `renders fenced code blocks` test (shiki splits tokens; mock returns null for fallback path)
- Added `ThemeProvider` to shared test wrapper (`test/utils.tsx`) so all tests get `useTheme()` context
- Added `ThemeProvider` wrapping to `ChatPage.test.tsx` and `ChatPage.queue.test.tsx` render helpers
- Added 15 new `CodeBlock` tests: language label, copy button, highlight calls, streaming guard, shiki HTML rendering, plain fallback, word-wrap
- Added 13 new `closeOpenFence` tests covering all CommonMark fence variants

---

## Key Decisions

1. **`shiki/bundle/full` + `createHighlighter`** over `createHighlighterCore` + dynamic template literal imports — Vite 5 cannot statically analyze bare package specifier dynamic imports. `bundle/full` pre-chunks all 347 languages at build time.
2. **JavaScript regex engine** over Oniguruma WASM — eliminates WASM download entirely, 95%+ grammar compatibility.
3. **`useState` + `useEffect`** over React 19 `use()` — avoids Suspense boundary complexity for marginal gain.
4. **`defaultColor: false`** — shiki emits only CSS custom properties, no inline color/background. Dark mode switching is CSS-only, no re-render needed.
5. **Added `ThemeProvider` to test wrapper** rather than mocking in each test file — root cause fix for `useTheme()` context requirement.

---

## Blockers

None.

---

## Tests Run

```bash
cd frontend && npm test          # 955 passed, 0 failed (94 test files)
cd frontend && npm run typecheck # Clean — no errors
cd frontend && npm run lint      # 0 new errors in changed files
cd frontend && npm run build     # Clean build, PWA precache verified
```

---

## Next Steps

- Manual visual verification: headings render with hierarchy in both light and dark mode
- Manual visual verification: syntax highlighting appears on code blocks
- Manual visual verification: diff viewer matches app theme
- Consider future optimisation: Web Worker for shiki highlighting (noted in US-40.3 design doc)
- Consider future optimisation: inline `<script>` in `<head>` to prevent flash of wrong theme (noted in US-40.2 design doc)

---

## Files Modified

| File | Change |
|---|---|
| `frontend/package.json` | Added `@tailwindcss/typography`, `shiki` |
| `frontend/package-lock.json` | Updated lockfile |
| `frontend/src/styles/index.css` | `@custom-variant dark`, `@plugin "@tailwindcss/typography"`, `.dark {}` variable block, shiki dark override, removed hand-written table CSS |
| `frontend/src/components/chat/MessagePart.tsx` | `closeOpenFence()` function, `CodeBlock` component, custom `ReactMarkdown` `components` prop, `useTheme()` for diff viewer, removed old fence heuristic |
| `frontend/src/components/chat/MessagePart.test.tsx` | Shiki mock, `closeOpenFence` tests (13), `CodeBlock` tests (15), fixed existing tests for new component structure |
| `frontend/src/lib/shiki.ts` | New file — `highlight()` singleton with JS engine |
| `frontend/src/test/utils.tsx` | Added `ThemeProvider` to `AllProviders`, changed `render` export to wrapped version |
| `frontend/src/pages/ChatPage.test.tsx` | Added `ThemeProvider` wrapping in `renderChatPage` |
| `frontend/src/pages/ChatPage.queue.test.tsx` | Added `ThemeProvider` wrapping in `renderChat` |
| `frontend/vite.config.ts` | Updated PWA `globPatterns` and `runtimeCaching` |
