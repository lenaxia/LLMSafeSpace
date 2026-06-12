import { createHighlighter } from "shiki/bundle/full";
import { createJavaScriptRegexEngine } from "shiki/engine/javascript";

// Module-level singleton. Created once on first import, reused for the
// lifetime of the tab. Never disposed — the highlighter must outlive any
// individual component.
//
// shiki/bundle/full includes all 347 languages as lazy async chunks.
// Themes are pre-loaded at init time (small, always needed).
// Languages are loaded on demand via h.loadLanguage(string) — shiki resolves
// the string against its bundled language map and loads the corresponding
// async chunk. Each language chunk is downloaded at most once per session;
// Vite/Rollup handles the chunk splitting at build time.
//
// Using createHighlighter (not codeToHtml shorthand) so that we can pass
// engine: createJavaScriptRegexEngine() — eliminating the WASM download.
// Validated: createHighlighter forwards engine to createHighlighterCore via
// `engine: options.engine ?? engine()` in @shikijs/core source.
const highlighterPromise = createHighlighter({
  themes: ["github-light", "github-dark"],
  langs: [],
  engine: createJavaScriptRegexEngine(),
});

/**
 * Highlight a code string for the given language using shiki.
 *
 * Language grammars are loaded on demand from shiki's bundled language set
 * (shiki/bundle/full, 347 languages) and cached in the highlighter instance.
 * The first call for a new language downloads one async chunk; all subsequent
 * calls are synchronous.
 *
 * Returns dual-theme HTML: the <pre> element carries --shiki-light-bg /
 * --shiki-dark-bg inline style vars; each <span> token carries
 * --shiki-light / --shiki-dark. The active theme is selected by CSS rules
 * in index.css using the html.dark class. No re-render is needed when the
 * user switches themes.
 *
 * Returns null if the language is unknown or loading/highlighting fails
 * for any reason. Callers must render a plain <pre><code> fallback on null.
 *
 * @param code - Raw code string (not HTML-encoded)
 * @param lang - Language identifier (e.g. "go", "python", "yaml")
 */
export async function highlight(code: string, lang: string): Promise<string | null> {
  try {
    const h = await highlighterPromise;
    if (!h.getLoadedLanguages().includes(lang)) {
      await h.loadLanguage(lang as Parameters<typeof h.loadLanguage>[0]);
    }
    return h.codeToHtml(code, {
      lang,
      themes: { light: "github-light", dark: "github-dark" },
      defaultColor: false,
    });
  } catch {
    // Unknown language, network failure, or grammar parse error.
    // Caller renders plain <pre><code> as fallback.
    return null;
  }
}
