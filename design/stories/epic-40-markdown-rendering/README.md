# Epic 40: Markdown Rendering Overhaul

**Status:** Planning
**Created:** 2026-06-12
**Priority:** High (broken core UX — headings unstyled, dark mode non-functional, no syntax highlighting)
**GitHub Issue:** #127
**Depends on:** None (self-contained frontend changes)

---

## Problem Statement

Chat message bubbles have three compounding rendering failures:

1. **`@tailwindcss/typography` is not installed.** The `prose` classes applied in `MessagePart.tsx` produce no output. Headings (`#`, `##`, `###`), paragraph spacing, list styling, blockquote formatting, and inline code styling are all absent. The markdown parser (`react-markdown`) correctly emits `<h1>`, `<h2>`, `<ul>`, etc., but they render at body text size with no visual hierarchy.

2. **Tailwind v4 dark mode is mis-wired.** `@custom-variant dark` is missing from `index.css`. Every `dark:` utility across the entire frontend responds to `prefers-color-scheme` (OS setting) instead of the `dark` class toggled by `ThemeProvider`. Manual dark/light override is fully broken. The "System" option works by accident. Tracked separately as bug #125.

3. **No syntax highlighting.** `react-markdown` emits `language-*` classes on fenced code blocks but nothing reads them. No highlighter is installed. Code renders as unstyled monospace regardless of language.

Secondary issues identified during analysis:
- The streaming open-fence detection uses a line-count heuristic that fails on 4-backtick, tilde, and indented fences.
- The `codeBlockWordWrap` user setting will break silently once a custom code block renderer is introduced.
- The PWA service worker precaches all JS chunks including language grammar files, which will bloat install size once shiki is added.
- Dead dependencies `rehype-highlight` and `highlight.js` were installed during investigation and must be removed.

---

## Stories

| Story | Title | Effort | Depends On |
|-------|-------|--------|------------|
| US-40.1 | Fix prose/heading styles — install `@tailwindcss/typography` | Small (0.5d) | None |
| US-40.2 | Fix dark mode — `@custom-variant dark` + `.dark` CSS variable block | Small (0.5d) | None |
| US-40.3 | Add syntax highlighting — shiki singleton with JS engine (`createHighlighterCore`) | Medium (1d) | US-40.2 |
| US-40.4 | `CodeBlock` component — label, copy button, `not-prose`, `wordWrap` | Medium (1d) | US-40.3 |
| US-40.5 | No flicker during streaming — skip highlighting while streaming | Small (0.5d) | US-40.4 |
| US-40.6 | Fix streaming fence scanner — replace line-count heuristic | Small (0.5d) | None |
| US-40.7 | Fix PWA cache — exclude grammar chunks from precache, add eviction | Small (0.5d) | US-40.3 |
| US-40.8 | Fix diff viewer dark mode — remove hardcoded `useDarkTheme` | Trivial (< 0.25d) | US-40.2 |

---

## Dependency Graph

```
US-40.1 (typography)   ──┐
US-40.2 (dark mode)    ──┤── no dependencies, can start immediately
US-40.6 (fence fix)    ──┘

US-40.3 (shiki)        ──── US-40.2 (shiki dark CSS uses html.dark class; app must be correctly themed)

US-40.4 (CodeBlock)    ──── US-40.3 (needs shiki highlight() function)

US-40.5 (no flicker)   ──── US-40.4 (needs CodeBlock component)

US-40.7 (PWA cache)    ──── US-40.3 (shiki chunks exist in build output)

US-40.8 (diff viewer)  ──── US-40.2 (dark mode must work for this fix to be meaningful)
```

---

## Execution Order

All stories land in a single PR in this order:

1. US-40.2 — dark mode (`@custom-variant` + `.dark` variable block)
2. US-40.1 — typography (one line + one package install)
3. US-40.6 — fence scanner (isolated pure function, no dependencies)
4. US-40.8 — diff viewer dark mode (trivial, depends on US-40.2)
5. US-40.3 — shiki singleton (new file, no UI yet)
6. US-40.4 — CodeBlock component (wires shiki into ReactMarkdown)
7. US-40.5 — streaming skip (small guard in CodeBlock)
8. US-40.7 — PWA cache (vite.config.ts change)
9. Tests — update broken test, add CodeBlock suite, mock shiki

---

## Out of Scope

- User message markdown rendering — intentionally plain `whitespace-pre-wrap` (WYSIWYG parity)
- `tool_result` / `tool_output` markdown rendering — plain `<pre>` is correct for structured output
- `rehype-sanitize` schema customisation — default schema is safe and sufficient
- Thinking block syntax highlighting — plain `<pre>` fallback is acceptable; thinking blocks rarely contain code and the italic container conflicts with highlighted styling
- Any backend changes — this is entirely frontend

---

## Files Changing

| File | Change |
|---|---|
| `frontend/src/styles/index.css` | `@custom-variant dark`; `.dark {}` CSS variable block; `@plugin "@tailwindcss/typography"`; shiki `html.dark` override with `!important` |
| `frontend/package.json` | Remove `rehype-highlight`, `highlight.js`; add `shiki`, `@tailwindcss/typography` |
| `frontend/vite.config.ts` | Fix PWA `globPatterns`, add `lang-chunks` runtime cache with LRU eviction |
| `frontend/src/lib/shiki.ts` | **New** — `highlight()` singleton using `createHighlighterCore` + JS engine + on-demand language loading |
| `frontend/src/components/chat/MessagePart.tsx` | `CodeBlock` component; custom `ReactMarkdown` renderers (no `Children` import); `useTheme` for diff viewer; `ToolDiffView` `isDark` prop; fence scanner |
| `frontend/src/components/chat/MessagePart.test.tsx` | Mock shiki, fix broken `renders fenced code blocks` test, new `CodeBlock` suite |

---

## Success Criteria

1. `#`, `##`, `###` headings in assistant messages render with correct visual hierarchy in both light and dark mode
2. Manual dark/light app theme toggle changes backgrounds, borders, text colours, and `dark:` utilities regardless of OS `prefers-color-scheme`
3. Fenced code blocks show token-colored syntax highlighting for all 300+ shiki-supported languages
4. Language label visible on code blocks with a declared language
5. Per-block copy button present and functional (Copy → Check → revert after 2s)
6. `codeBlockWordWrap` setting applies to highlighted code blocks
7. No styling flash on streamed code blocks
8. All CommonMark fence variants (3+ backtick, tilde) handled correctly during streaming
9. Diff viewer (file edits) matches the current app theme — no more hardcoded dark
10. PWA service worker does not precache language grammar chunks; eviction configured
11. `npm test` passes with no failures
12. `npm run build` produces no oversized chunks (no single chunk > 500 KB gzipped)
