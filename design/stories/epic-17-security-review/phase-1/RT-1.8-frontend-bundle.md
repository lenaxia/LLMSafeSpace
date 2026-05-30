# RT-1.8 — Frontend Bundle Inventory

**Phase:** 1 (Reconnaissance)
**Method:** Static analysis. Every claim cites `file:line`. Cross-references RT-1.5/RT-1.6 (deps and images) and RT-1.7 (secret storage — auth-token in transit).
**Sources read:** `frontend/package.json`, `frontend/package-lock.json`, `frontend/vite.config.ts`, `frontend/index.html`, `frontend/dist/index.html`, `frontend/nginx.conf`, all `frontend/src/**/*.{ts,tsx}`, `frontend/node_modules/rehype-sanitize/lib/index.js`, `frontend/node_modules/react-diff-viewer-continued/package.json`, `frontend/node_modules/lucide-react/package.json`, `api/internal/server/router.go`, `api/internal/middleware/security.go`.

---

## 1. Third-party JS shipped to the browser

These are the 21 production dependencies declared in `frontend/package.json:16-38`. devDependencies (`tsc`, `eslint`, `vitest`, `@playwright/test`, `jsdom`, etc.) are excluded — they don't ship to the browser. Resolved versions taken from `frontend/package-lock.json`.

| Package | Resolved version | License | Role | Last touched / notes |
|---|---|---|---|---|
| `react` | 19.2.6 | MIT | Core view library | Modern (React 19 line). |
| `react-dom` | 19.2.6 | MIT | DOM renderer | Same line as react. |
| `react-router-dom` | 6.30.3 | MIT | Client routing | v6 line, current. |
| `@tanstack/react-query` | 5.100.14 | MIT | Server-state cache | Current (v5). |
| `@tanstack/react-virtual` | 3.13.25 | MIT | Virtualized message list | Current. |
| `@radix-ui/react-dialog` | 1.1.15 | MIT | Modal primitive | Current. |
| `@radix-ui/react-select` | 2.2.6 | MIT | Select primitive | Current. |
| `@radix-ui/react-switch` | 1.2.6 | MIT | Switch primitive | Current. |
| `@radix-ui/react-tabs` | 1.1.13 | MIT | Tabs primitive | Current. |
| `@radix-ui/react-toast` | 1.2.15 | MIT | Toast primitive | Current. |
| `@radix-ui/react-tooltip` | 1.2.8 | MIT | Tooltip primitive | Current. |
| `react-markdown` | 10.1.0 | MIT | Markdown renderer for assistant text. **The single largest XSS sink in the app.** | Current (v10 line). |
| `remark-gfm` | 4.0.1 | MIT | GFM extension (tables, task-lists) for `react-markdown` | Current. |
| `rehype-sanitize` | 6.0.0 | MIT | HAST sanitizer used in the markdown pipeline. **The single XSS defense in the app.** Wraps `hast-util-sanitize`'s `defaultSchema` (GitHub allowlist). | Current. See §3 for usage. |
| `react-diff-viewer-continued` | 4.2.2 | MIT | Diff renderer for `tool_use` Edit-tool blocks. Pulls in `@emotion/react`, `@emotion/css`, `js-yaml`, `diff`, `memoize-one`, `classnames` transitively. | Current; ships its own Web Worker (see §6). |
| `lucide-react` | **1.16.0** | ISC | Icon set. **Version is suspicious — see flag below.** | The current public lucide-react line is in the `0.4xx` range; a `1.16.0` stable does not match upstream history. May be a private fork / vendored mirror / publishing accident. Confirm before Phase 2. |
| `clsx` | 2.1.1 | MIT | Conditional className joiner | Tiny, current. |
| `tailwind-merge` | 3.6.0 | MIT | Tailwind class deduper | Current. |
| `tailwindcss` | 4.3.0 | MIT | CSS framework (v4 generates utilities at build time) | Current. |
| `@tailwindcss/vite` | 4.3.0 | MIT | Vite plugin for Tailwind v4 | Build-time only — does **not** ship to browser. (Listed under `dependencies` rather than `devDependencies`, but its runtime payload is zero.) |
| `vite-plugin-pwa` | 1.3.0 | MIT | PWA / Workbox integration. The runtime piece (`virtual:pwa-register/react`) ships ~5 KB; `workbox-window` is also bundled. | Current. |

### 1.1 Flagged versions

- **F1.8.1 — `lucide-react@1.16.0` is anomalous.** The public registry line for `lucide-react` is `0.x` (latest ≈ `0.460+` at time of write). A `1.16.0` resolution either (a) comes from a private/forked registry, (b) is a typosquat of `lucide` (the icon-only package, also `1.x`), or (c) is a real one-off republish that pre-dates the lucide split. **Phase 2 must verify the package's tarball integrity hash in `package-lock.json` against the upstream registry before promoting any code paths that depend on it.** Cite: `frontend/package.json:27`, `frontend/node_modules/lucide-react/package.json` (`"version": "1.16.0"`, `"name": "lucide-react"`).
- **F1.8.2 — No `npm audit` artifact in tree.** No CI step in `frontend/package.json:6-15` runs `npm audit` or `osv-scanner` against `package-lock.json`. RT-1.5/RT-1.6 already flagged the same gap for the Go side; this is the JS counterpart.

### 1.2 Known-CVE check

No package in the table above is currently known-vulnerable at its resolved version against the public advisory databases — but this report does **not** claim to be a CVE scan. F1.8.2 is the gap that needs closing in Phase 2.

---

## 2. Dangerous API surface

I grepped `frontend/src/` for every pattern in the brief. Results:

| Pattern | Hits | Notes |
|---|---|---|
| `dangerouslySetInnerHTML` | **0** | None. |
| `innerHTML =`, `outerHTML =`, `document.write` | **0** | None. |
| `eval(`, `new Function(` | **0** | None. |
| `setTimeout("…", …)` / `setInterval("…", …)` (string form) | **0** | All callers use the function-form (verified, see §2.1). |
| `window.open(...)` | **0** | The app never opens new windows. |
| `window.postMessage` consumer (no origin check) | **0 listeners**, **4 broadcast sends** | All are `BroadcastChannel.postMessage`, which is same-origin by definition. See §2.2. |
| `<a target="_blank">` without `rel="noopener noreferrer"` | **0** | No `target="_blank"` anywhere in `frontend/src/` or in the built `dist/assets/*.js`. Only one `<a>` element in the app shell, the in-page skip-link `frontend/src/components/layout/AppShell.tsx:76` (`href="#main-content"`). |
| User-controlled `href={…}` / `src={…}` interpolation | **0 in JSX** | No dynamic `href={…}` or `src={…}` in any `.tsx` source. |
| `localStorage.setItem(JWT)` / token persistence | **0** (token never touches `localStorage`) | See §4 — auth uses an HttpOnly cookie. |
| `document.cookie` reads of session token | **1 — and it is a bug.** | See F1.8.4 in §4. |

### 2.1 `setTimeout` / `setInterval` audit

```bash
$ grep -RnE 'setTimeout\s*\(\s*["\x27`]|setInterval\s*\(\s*["\x27`]' frontend/src/
# (no output)
```

All `setTimeout` / `setInterval` callers in the codebase pass a function reference (`frontend/src/api/events.ts:55, 82, 100, 106`, etc.), not a string. No `eval`-equivalent path here.

### 2.2 `postMessage` audit — `BroadcastChannel` only

The four `postMessage` calls all live on `BroadcastChannel` instances, not `window`:

`frontend/src/api/events.ts:42-58`:
```ts
eventSource.onmessage = (e) => {
  try {
    const parsed: SSEEvent = { type: e.type, data: JSON.parse(e.data) };
    onEvent(parsed);
    channel.postMessage({ type: "event", payload: parsed });
  } catch { /* ignore malformed */ }
};
// …
heartbeatInterval = setInterval(() => {
  channel.postMessage({ type: "heartbeat", tabId });
}, HEARTBEAT_MS);
channel.postMessage({ type: "heartbeat", tabId });
```

`BroadcastChannel` is same-origin and same-browser by spec, so origin-checking is not applicable. The receiver at `frontend/src/api/events.ts:61-76`:

```ts
function handleChannelMessage(e: MessageEvent) {
  const msg = e.data;
  if (msg.type === "event" && !isLeader) {
    onEvent(msg.payload as SSEEvent);
  } else if (msg.type === "heartbeat" && msg.tabId !== tabId) {
    lastLeaderHeartbeat = Date.now();
    if (electionTimeout) {
      clearTimeout(electionTimeout);
      electionTimeout = null;
    }
  } else if (msg.type === "leader-resign") {
    startElection();
  }
}
```

does **no schema validation** on `msg.payload` before re-dispatching it through `onEvent`. The follower tab trusts whatever the leader broadcasts. Within the same origin this is benign for confidentiality (any JS in that origin can already do anything), but it's a liveness/correctness concern: a buggy or compromised leader tab can DoS the followers (e.g. push `{type:"event", payload:null}` and crash a downstream reducer that expects shape). **Promotable: Phase 2 fuzz the BroadcastChannel input.**

### 2.3 `<script>` injection / dynamic `script` tag

No `document.createElement("script")` or string-built `<script>` patterns in `frontend/src/`. Vite injects the one `<script type="module" src="/src/main.tsx">` at build time (`frontend/index.html:12`), which becomes a static `<script type="module" crossorigin src="/assets/index-S26YHL--.js">` in the built bundle (`frontend/dist/index.html`).

### 2.4 `window.location` writes

Five reads, one write. The single write is the 401 redirect:

`frontend/src/api/client.ts:15-19`:
```ts
async function handleUnauthorized(status: number): Promise<void> {
  if (status === 401 && !window.location.pathname.startsWith("/login")) {
    window.location.href = "/login";
  }
}
```

Target is a hard-coded string literal — no user-controlled redirect. Safe.

`frontend/src/components/layout/Sidebar.tsx:291`, `Sidebar.tsx:464`, `frontend/src/pages/ChatPage.tsx:437` build `${window.location.origin}/chat/…` strings and pass them to `navigator.clipboard.writeText`. Origin is the document's own origin (read), not user input, and the result goes to the clipboard, not to a navigation. Safe.

### 2.5 Summary

The frontend has a **very small dangerous-API surface**. The only XSS sink that matters is the `react-markdown` pipeline (§3); the only auth-handling concern is the cookie check in §4.

---

## 3. Markdown / HTML rendering

The full assistant-message renderer lives in `frontend/src/components/chat/MessagePart.tsx`. It is the only place in the app that parses Markdown.

### 3.1 Imports (`MessagePart.tsx:1-10`)

```tsx
import { lazy, Suspense } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeSanitize from "rehype-sanitize";
import { Brain, Wrench, Server } from "lucide-react";
import { cn } from "../../lib/utils";
import { useUserSetting } from "../../hooks/useUserSettings";
import type { MessagePart as MessagePartType } from "../../api/types";

const ReactDiffViewer = lazy(() => import("react-diff-viewer-continued"));
```

### 3.2 ReactMarkdown call sites — exhaustive list

There are exactly **two** `ReactMarkdown` invocations in the codebase, both in `MessagePart.tsx`, both passed identical plugin lists:

**Call 1 — assistant text part** (`MessagePart.tsx:72-77`):
```tsx
return (
  <div className={cn("prose prose-sm dark:prose-invert max-w-none", wordWrap && "[&_pre]:whitespace-pre-wrap [&_pre]:break-words")}>
    <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeSanitize]}>
      {text}
    </ReactMarkdown>
  </div>
);
```

**Call 2 — `thinking`/`reasoning` part** (`MessagePart.tsx:81-87`):
```tsx
const content = (
  <div className="border-l-2 border-muted-foreground/30 pl-3 text-xs text-muted-foreground italic">
    <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeSanitize]}>
      {part.text}
    </ReactMarkdown>
  </div>
);
```

User-typed messages skip Markdown entirely and render through `<p className="whitespace-pre-wrap">{part.text}</p>` (`MessagePart.tsx:62`) — text is React-escaped, no XSS path.

### 3.3 Sanitization schema in use

`rehypeSanitize` is invoked **with no argument** in both call sites. Walking the implementation:

`frontend/node_modules/rehype-sanitize/lib/index.js:16-27`:
```js
export default function rehypeSanitize(options) {
  return function (tree) {
    const result = /** @type {Root} */ (sanitize(tree, options))
    return result
  }
}
```

`options === undefined` → `hast-util-sanitize` falls back to its built-in `defaultSchema`, which is the GitHub allowlist (it strips `<script>`, event-handler attributes like `onerror=`, `javascript:` URLs in `href`/`src`, MathML, foreignObject, etc., and only permits a fixed set of HTML tags + the `className`/`href`/`src`/`title`/`alt`/etc. attributes). This is the right default.

**Promotable observations for Phase 2:**

- **F1.8.3 — Sanitizer plugin order is correct.** `rehype-sanitize` is the **only** rehype plugin and runs after `remark-gfm` is converted to HAST. There is no `rehype-raw`, no `rehype-katex`, no `allowDangerousHtml` flag in the `ReactMarkdown` call. If anyone later adds `rehype-raw` or wraps `rehypeSanitize` with a custom schema that copies `defaultSchema` and adds `style` or `srcset` or relaxes the protocol allowlist, this file is the regression site. Worth a unit test (`MessagePart.test.tsx`) that pins the plugin list and verifies known XSS payloads (`<img src=x onerror=…>`, `<a href="javascript:…">`, `<iframe>`, `<object>`, `<svg><script>`) round-trip-strip.

- The `text` argument to ReactMarkdown comes from `part.text` (`MessagePart.tsx:60-65`) which is straight from the streaming-chat reducer state — i.e. agent output. The agent is itself fed by user prompts and tool outputs, so untrusted strings reach this renderer by design. Sanitizer must hold.

### 3.4 Diff viewer (also user-influenced HTML)

`react-diff-viewer-continued` is loaded via `React.lazy` and rendered when a tool call has Edit-shaped input (`MessagePart.tsx:36-46`):
```tsx
<ReactDiffViewer
  oldValue={oldStr}
  newValue={newStr}
  splitView={false}
  useDarkTheme
  hideLineNumbers={false}
  styles={{ contentText: { fontSize: "11px", lineHeight: "1.4" } }}
/>
```

`oldStr`/`newStr` are derived from `part.input.oldString` / `newString` (`MessagePart.tsx:152-153`). The diff viewer renders these as plain text inside its `@emotion/css`-styled DOM — it does **not** parse them as HTML. Promotable Phase 2 test: feed `<script>alert(1)</script>` and `</style><script>alert(1)</script>` into the diff and confirm they appear as text only.

---

## 4. Auth-token handling

### 4.1 What the frontend stores

Searched `frontend/src/` for `localStorage`, `sessionStorage`, `document.cookie`, `Authorization`, `Bearer`, `jwt`, `token`. Findings:

- `localStorage` is used for **theme preference** (`ThemeProvider.tsx:22, 40, 54`, key `"lsp-theme"`) and a **settings cache** (`useUserSettings.ts:15, 37`, key `"llmsafespace_user_settings"`). Neither contains tokens or PII.
- `sessionStorage` — **0 hits**.
- The string `token` appears in `frontend/src/api/types.ts:22` as a field on `AuthResponse` (`{ token: string; user: User; }`) and in test fixtures (`frontend/src/api/contract-fixtures.json:22`, `contract.test.ts:64`, `AuthProvider.test.tsx:56`). **It is never persisted on the client.** `AuthProvider.tsx:24-37` calls `authApi.login(...)`, captures `res.user`, and discards the rest. The `token` field is ignored.
- All API calls use `credentials: "include"` (`frontend/src/api/client.ts:29, 70`; `frontend/src/api/events.ts:39`; `frontend/src/hooks/useEventStream.ts:33`). The browser handles cookie attachment.

### 4.2 The actual session is a cookie set by the backend

`api/internal/server/router.go:264-267`:
```go
// setSessionCookie sets the HttpOnly session cookie on the response.
func setSessionCookie(c *gin.Context, token string) {
	c.SetCookie("lsp_session", token, 86400, "/", "", true, true)
}
```

`gin.Context.SetCookie` signature is `(name, value, maxAge, path, domain, secure, httpOnly)` → cookie is `Secure=true, HttpOnly=true, Path=/`, 24h lifetime, no `Domain` attribute (defaulted to the request host), no explicit `SameSite` flag. Cross-references RT-1.7 §1.2.

This is **the right design**: a JS bundle compromise (e.g. a tampered `lucide-react`) cannot exfiltrate the JWT because JS cannot read it. Note that `SameSite` is also worth checking — Gin's `SetCookie` uses the default which depends on the underlying `net/http`; recent Go defaults to `SameSiteLaxMode` if unset. **Phase 2 should enumerate the actual `Set-Cookie` header on the wire** and verify `SameSite=Strict` or `Lax` is being emitted.

### 4.3 The `document.cookie` bug — F1.8.4

There is exactly one `document.cookie` read in the entire frontend. It is broken:

`frontend/src/providers/ThemeProvider.tsx:28-51`:
```tsx
// Sync from API on mount — only if authenticated (cookie present)
useEffect(() => {
  const hasSession = document.cookie.includes("lsp_session");
  if (!hasSession) return;
  settingsApi.getUserSettings()
    .then((res) => {
      _updateSettingsCache(res.settings);
      const apiTheme = res.settings.theme as Theme | undefined;
      if (apiTheme && apiTheme !== theme) {
        localStorage.setItem("lsp-theme", apiTheme);
        setThemeState(apiTheme);
      }
      // …
    })
    .catch(() => {}); // Use localStorage value on failure
}, []);
```

**The bug:** `lsp_session` is set with `HttpOnly=true` (`router.go:266`, fifth-from-last argument is `true`). Browsers do **not** expose HttpOnly cookies via `document.cookie`. So `hasSession` is **always `false`**, and the entire "fetch settings from API on mount" branch is **dead code** for authenticated users.

**Impact:**
- A user who logs in, lands on the app, and has a remote `theme` preference set on the server, will not pick it up on first load — they'll see the localStorage theme until they navigate to Settings or toggle the theme manually (which calls `settingsApi.setUserSetting`, `ThemeProvider.tsx:57`).
- Not a confidentiality issue. Security-relevant only because (a) it indicates the dev who wrote this assumed the cookie was *not* HttpOnly — i.e. there is a latent assumption in the codebase that may have produced other broken auth-state checks elsewhere; and (b) the right pattern is to call `authApi.me()` and gate on the `200` response, which the codebase already does in `AuthProvider.tsx:21`. This component is duplicating auth-state inference instead of consuming `useAuth()`.

**Promotable Phase 2 test case:** assert that no production code reads `document.cookie` at all (lint rule). The auth state should be sourced exclusively from `AuthProvider`.

### 4.4 SSE / EventSource auth

`frontend/src/api/events.ts:38-40`:
```ts
eventSource = new EventSource(`${apiBaseUrl}/workspaces/${workspaceId}/events`, {
  withCredentials: true,
});
```

Uses cookie credentials. Same model. Note: the URL embeds `workspaceId` from the caller (`createEventStream(workspaceId, …)`) — no client-side validation of that string. Since it concatenates into a URL path, **a workspaceId containing `../` could traverse the API path**. Cross-reference RT-1.1 to confirm whether the API enforces workspace-id format on this route. (Quick mental model: backend should accept only `[a-z0-9-]{N}` UUIDs, but the **frontend should still URL-encode** before splicing.) Promotable: F1.8.5 — frontend does not `encodeURIComponent` path segments built from data.

---

## 5. CSP / security headers

### 5.1 Meta tag in HTML

Searched `frontend/index.html` and the built `frontend/dist/index.html` for `<meta http-equiv="Content-Security-Policy">`. **None exists.**

`frontend/index.html` (full file, all 14 lines):
```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no" />
    <meta name="theme-color" content="#0f172a" />
    <title>Safe Space</title>
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

### 5.2 nginx config (the SPA's HTTP server)

`frontend/nginx.conf:6-9`:
```
add_header X-Frame-Options "SAMEORIGIN" always;
add_header X-Content-Type-Options "nosniff" always;
add_header Referrer-Policy "strict-origin-when-cross-origin" always;
```

The SPA's nginx sends:
- `X-Frame-Options: SAMEORIGIN` ✓
- `X-Content-Type-Options: nosniff` ✓
- `Referrer-Policy: strict-origin-when-cross-origin` ✓
- **No `Content-Security-Policy`**
- **No `Permissions-Policy`**
- **No `Strict-Transport-Security`** (HSTS) — presumably delegated to the ingress

### 5.3 The API does send a CSP — but only on API responses

The Go API attaches a strict CSP at the middleware level (`api/internal/middleware/security.go:66`):
```go
ContentSecurityPolicy: "default-src 'self'; connect-src 'self' wss:; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; base-uri 'self'; block-all-mixed-content",
```

But this is on `/api/*` responses (`api/internal/server/router.go:100` — `router.Use(middleware.SecurityMiddleware(...))`). The SPA shell at `/index.html` is served by **nginx, not the API**, so this CSP **does not apply to the document that bootstraps the JS bundle**. CSP applies to the document that loaded the resource — fetches initiated by the SPA inherit the SPA's CSP, not the API's response CSP.

**Net effect:** the SPA runs **without any CSP at all**. F1.8.6.

**Promotable Phase 2 fix:** add CSP either as a meta tag in `frontend/index.html` (cheap, but can't set `frame-ancestors`) or as an nginx `add_header` in `frontend/nginx.conf` (correct location). A first-cut policy that matches the API's intent and allows what the SPA actually does:

```
default-src 'self';
connect-src 'self';
script-src 'self';
style-src 'self' 'unsafe-inline';            # Tailwind v4 inlines small style chunks; verify
img-src 'self' data:;
font-src 'self';
object-src 'none';
frame-ancestors 'none';
form-action 'self';
base-uri 'self';
worker-src 'self';                            # react-diff-viewer-continued ships a worker
manifest-src 'self';
```

Caveat: `react-diff-viewer-continued` uses `@emotion/css` which generates `<style>` tags at runtime — that needs `style-src 'self' 'unsafe-inline'` or a nonce-based scheme. Test before shipping.

### 5.4 Service worker / PWA

The build emits a Workbox service worker (`frontend/dist/sw.js`) registered via `vite-plugin-pwa` (`frontend/vite.config.ts:10-37`, `frontend/src/hooks/usePWA.ts:1`). The SW has its own caching scope: it pre-caches `**/*.{js,css,html,svg}` and runtime-caches `/env.json` with `NetworkFirst`. The SW is registered with `registerType: "prompt"` (`vite.config.ts:11`) so the user explicitly accepts updates — good. However, **the SW will happily serve a cached old `index.html` if the operator forgets to bump version**, and the SW itself runs without a CSP. Promotable: ensure the SW's `Service-Worker-Allowed` header / scope is `/`-only, and confirm no opaque cross-origin caching.

---

## 6. Bundle size

Built artifact sizes from `frontend/dist/assets/` (uncompressed):

| File | Size | Contents |
|---|---|---|
| `index-S26YHL--.js` | 371 KB | Main app chunk (Radix primitives, react-markdown, remark/rehype, app code) |
| `vendor-BuAALwxh.js` | 252 KB | React + ReactDOM + react-router (split via `vite.config.ts:51-53`) |
| `index-Gemr9jC6.js` | 113 KB | Lazy chunk — `react-diff-viewer-continued` + `@emotion/react` + `js-yaml` + `diff` |
| `workerBundle-DGWlUuev.js` | 67 KB | Diff worker shipped by `react-diff-viewer-continued` |
| `index-DFEKq_PN.css` | 42 KB | Tailwind output |
| `query-BANwsWtj.js` | 41 KB | `@tanstack/react-query` (split via `vite.config.ts:54-56`) |
| `workbox-window.prod.es5-BqEJf4Xk.js` | 5.6 KB | PWA bootstrap |

**Total JS shipped on first load:** ~669 KB uncompressed (main + vendor + query + workbox-window). Diff viewer (113 KB + 67 KB worker) is lazy-loaded only when an Edit-tool block is rendered.

### 6.1 Suspicious / oversized

- **F1.8.7 — `js-yaml` in the diff-viewer chunk.** A YAML parser is included transitively via `react-diff-viewer-continued@4.2.2` (`peerDependencies`/`dependencies` of that package). The diff viewer doesn't need to parse YAML for our use case (we feed it `oldString`/`newString` plain text). It's likely dead code in our paths, but `js-yaml` has had RCE-class CVEs historically — `js-yaml` ≤ 3.13.0 was vulnerable to prototype pollution. Phase 2: confirm the resolved `js-yaml` major and tree-shaking — `npm ls js-yaml --all` from `frontend/`.
- **F1.8.8 — `@emotion/react` is a 60+ KB CSS-in-JS runtime** that we use for exactly one component (the diff viewer). Tailwind already covers styling for the rest of the app. Architectural note for the team: if the diff feature is high-value, this is acceptable; if not, swap for a lighter diff renderer.
- **lucide-react@1.16.0** (see F1.8.1) — bundle impact is small because of icon-tree-shaking, but the version anomaly is a supply-chain question, not a size question.

### 6.2 No suspect old packages

Beyond F1.8.1, no production dep in §1 resolves to a years-old version. The Radix UI primitives, React 19, react-query 5, react-router 6, vite-plugin-pwa 1.x, and tailwindcss 4 are all current major lines.

---

## 7. Phase-1 findings (promotable to Phase 2 test cases)

| ID | Finding | Promotable test |
|---|---|---|
| **F1.8.1** | `lucide-react@1.16.0` resolved version does not match the public registry's `0.x` line. Possible private fork, typosquat, or vendored mirror. | Phase 2: verify the `package-lock.json` integrity hash (`integrity` field on the `node_modules/lucide-react` entry) against `registry.npmjs.org/lucide-react/1.16.0`. If mismatch, treat as a supply-chain incident. |
| **F1.8.2** | No CI step runs `npm audit` / `osv-scanner` on `package-lock.json`. Same gap RT-1.5/RT-1.6 flagged for Go. | Phase 2: add `npm audit --omit=dev --audit-level=moderate` (or `osv-scanner`) to CI; baseline current findings. |
| **F1.8.3** | The `react-markdown` pipeline at `MessagePart.tsx:74, 84` is the single XSS sink. It is correctly wired (`remarkGfm` + `rehypeSanitize` with `defaultSchema`), but there is no regression test pinning the plugin list. | Phase 2: add `MessagePart.test.tsx` cases that feed canonical XSS payloads (`<script>`, `<img onerror>`, `<a href="javascript:">`, `<iframe>`, `<object>`, `<svg><script>`, `<style>@import url(…)`, `<math>` MathML) and assert the rendered DOM contains none of `script`, `onerror`, `javascript:`, `<iframe>`. Also add a test that fails if the `rehypePlugins` list ever stops including `rehypeSanitize`. |
| **F1.8.4** | `ThemeProvider.tsx:30` reads `document.cookie.includes("lsp_session")` to gate API sync. The cookie is HttpOnly (`router.go:266`), so this check is **always false** — the `then`-branch is dead code. Indicates a latent misconception in the codebase about how the session is stored. | Phase 2: (a) replace the `document.cookie` check with a `useAuth()` consumer or unconditional fetch; (b) add an ESLint rule banning `document.cookie` reads in `frontend/src/**`. |
| **F1.8.5** | `frontend/src/api/events.ts:38` and similar splice user-data path segments (`workspaceId`) into URLs without `encodeURIComponent`. Backend may enforce format, but defense-in-depth at the client would catch malformed inputs early and prevent surprises if the format ever loosens. | Phase 2: wrap all URL-segment concatenation in a `path()` helper that calls `encodeURIComponent` on each segment; add unit tests with `../`, `%2F`, `?`, `#` payloads. |
| **F1.8.6** | The SPA HTML shell ships **without a CSP** — nginx sets `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy`, but no `Content-Security-Policy`. The API's CSP only applies to `/api/*` responses. | Phase 2: add a `Content-Security-Policy` header in `frontend/nginx.conf` matching the policy in §5.3 (with `worker-src 'self'` for the diff worker). E2E test: visit `/` in Playwright, assert the response header. Negative test: `<img src=x onerror="…">` injected via the markdown pipeline does not execute (already covered by F1.8.3, but CSP is the second line). |
| **F1.8.7** | `js-yaml` enters the bundle transitively through `react-diff-viewer-continued`. The dependency is not used by our code paths but historically has had RCE-class CVEs. | Phase 2: run `npm ls js-yaml --all` from `frontend/`, pin the resolved major in `package.json` overrides, confirm the chunk-graph tree-shakes it out (or at minimum the resolved version is ≥ 4.x). |
| **F1.8.8** | `@emotion/react` (60+ KB CSS-in-JS runtime) is shipped only for the diff viewer. Architectural debt, not a security finding per se. | Phase 2: out of security scope; raise as a separate perf ticket. |
| **F1.8.9** (architectural) | `BroadcastChannel` consumer in `events.ts:61-76` does no shape validation on `msg.payload` before re-dispatching. Same-origin so not a confidentiality risk, but a correctness/DoS concern. | Phase 2: fuzz the BroadcastChannel reducer with malformed `msg` shapes; confirm it cannot crash followers. |

---

## 8. Cross-references

- **RT-1.5 / RT-1.6** — same dependency-management gap (no SCA in CI) on the Go side.
- **RT-1.7 §1.2** — JWT in transit, `lsp_session` cookie attributes. F1.8.4 here is the frontend-side echo of that finding.
- **RT-1.1** (when promoted) — should confirm the API enforces workspace-id format on `/workspaces/:id/events` (relevant to F1.8.5).

---

## 9. What this report does **not** cover

- Runtime DOM inspection (no Playwright run was performed against a live build).
- Subresource Integrity — Vite's default build does not emit `integrity=` on the script tags. Worth raising in Phase 2 if the asset CDN/nginx model permits it.
- Any third-party code loaded at runtime *outside* the bundle (analytics, chat widgets, etc.) — there are none in this codebase, confirmed by absence of any external `<script>` tag in `frontend/index.html`.
- `env.json` content fetched at runtime (`frontend/src/env.ts` → `getEnv()` reads `/env.json`) — the runtime config endpoint. Worth a brief Phase 2 audit (what does it expose? is it cacheable cross-user?).
