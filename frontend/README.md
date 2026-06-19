# LLMSafeSpaces Frontend

React SPA for the LLMSafeSpaces platform — a chat interface for driving AI agents running in Kubernetes-backed workspaces.

## Stack

- **React 19** + TypeScript 6
- **Vite 5** (dev server + build)
- **Tailwind CSS 4** (via `@tailwindcss/vite` plugin)
- **Radix UI** — Dialog, Select, Switch, Tabs, Toast, Tooltip
- **TanStack Query** — server state + caching
- **TanStack Virtual** — virtualized message lists
- **React Router 6** — client-side routing
- **react-markdown** + remark-gfm + rehype-sanitize — message rendering
- **react-diff-viewer-continued** — diff display in tool output
- **lucide-react** — icons
- **vite-plugin-pwa** — offline support + install prompt

## Project Structure

```
src/
  api/            # Typed API client (auth, workspaces, sessions, messages, events, settings, secrets, credentials)
  components/
    auth/         # LoginForm, RegisterForm, AuthCard
    chat/         # ChatView, MessageList, MessageBubble, MessagePart, Composer, StreamingIndicator, HealthBanner
    layout/       # AppShell, Sidebar, ErrorBoundary, UpdateAvailableToast
    session/      # SessionItem, SessionList, RenameSessionDialog
    settings/     # SettingsForm, AdminCredentialsTab, SecretsTab, UserSettingsTab, AppearanceTab, ApiKeysTab
    ui/           # Button, Input, Card, Badge, Spinner, KebabMenu, Toggle, Select, NumberInput, TagInput
    workspace/    # WorkspaceItem, WorkspaceList, WorkspaceSessionList, WorkspaceSettingsDrawer, RenameWorkspaceDialog, NewWorkspaceDialog
  hooks/          # useChatStream, useEventStream, useSessions, useWorkspaces, useActivateWorkspace, useSessionTitle, useUserSettings, useMediaQuery, usePWA
  lib/            # stream parser, time utils, name generator, general utils
  pages/          # ChatPage, LoginPage, RegisterPage, SettingsPage, AdminSettingsPage, NotFoundPage
  providers/      # AuthProvider, QueryClientProvider, ThemeProvider, ToastProvider
  test/           # test setup + render utils
```

## Key Features

- **SSE streaming** — real-time assistant responses via Server-Sent Events
- **Workspace lifecycle** — create, activate (auto-resume), suspend from the sidebar
- **Session management** — create, rename, switch sessions; auto-title from first message
- **Message parts** — renders text, tool calls, tool results, thinking blocks, diffs, code fences
- **Settings** — user preferences (theme, font size, streaming toggle) + admin instance settings (schema-driven forms)
- **Secrets management** — CRUD for encrypted user secrets (LLM keys, SSH, Git, env vars, files) with workspace bindings
- **Credential sets** — admin-managed provider credential sets with model allowlists
- **Dark/light/system theme** — persisted in user settings, synced to API

## Development

```bash
npm install
npm run dev          # Vite dev server (port 5173)
npm run build        # TypeScript check + production build
npm run test         # Vitest unit tests
npm run test:e2e     # Playwright e2e tests
npm run typecheck    # tsc --noEmit
npm run lint         # ESLint
```

## Testing

- **Unit tests**: Vitest + Testing Library (`*.test.ts{x}` co-located with source)
- **E2E tests**: Playwright (`tests/` directory)
- **Coverage**: `@vitest/coverage-v8`

## Docker

Multi-stage build producing an nginx container that serves the SPA and proxies `/api` to the API service. See `nginx.conf` for routing.

## Environment

Configuration via `src/env.ts`:
- `VITE_API_URL` — API base URL (default: `/api/v1` in production, `http://localhost:8080/api/v1` in dev)
