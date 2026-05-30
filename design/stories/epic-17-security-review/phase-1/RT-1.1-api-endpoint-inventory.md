# RT-1.1 — API Endpoint Inventory

**Phase:** 1 (Reconnaissance)
**Source:** `api/internal/server/router.go` and registered handler files. No route registrations were found in any other `api/internal/server/*.go` file.
**Method:** static enumeration. No live traffic generated.

All routes go through global middleware: `Recovery → Tracing → Security → Logging → Metrics → RateLimit → ErrorHandler` (`router.go:98-104`). Below this is abbreviated as **GLOBAL**.

---

## 1. Open / no auth

| Method | Path | Handler | Description | Risk | Source |
|---|---|---|---|---|---|
| GET | `/metrics` | `promhttp.Handler` | Prometheus metrics. **Unauthenticated**; cardinality may reveal route templates, user counts, internal state. | medium | router.go:200 |
| GET | `/livez` | anonymous | Liveness probe — always 200. | low | router.go:207 |
| GET | `/health` | anonymous | Legacy alias for `/livez`. | low | router.go:211 |
| GET | `/readyz` | anonymous | Probes Postgres + Redis; **returns 503 with raw error strings on failure** (`router.go:227,234,241`). May leak driver / connection string fragments. | medium | router.go:217 |
| GET | `/api/v1/auth/config` | anonymous | Returns instance feature flags (`registrationEnabled`, instance name, MOTD). | low | router.go:274 |
| POST | `/api/v1/auth/register` | anonymous | Registers user; sets session cookie; returns JWT + user. | high | router.go:297 |
| POST | `/api/v1/auth/login` | anonymous | Logs in; sets session cookie; returns JWT. **Error messages from `authSvc.Login` are passed through verbatim** (`router.go:322`) — verify they don't enable user enumeration. | high | router.go:313 |
| POST | `/api/v1/auth/logout` | anonymous | Clears `lsp_session` cookie. | low | router.go:330 |
| POST | `/api/v1/account/recover` | `RotateKeyHandler.RecoverAccount` | **Registered on root router with no auth middleware** (router.go:196). Operates on a specific user's account; rate limiting via global limiter only. | high | router.go:196 |

---

## 2. Authenticated (JWT or API key)

All routes have **GLOBAL + Auth** via `services.GetAuth().AuthMiddleware()`.

### `/api/v1/auth/*` (auth-required subgroup)

| Method | Path | Description | Risk | Source |
|---|---|---|---|---|
| GET | `/api/v1/auth/me` | Returns full user record. `PasswordHash` is `json:"-"` so it is NOT serialized (verified in `pkg/types/types.go:308`). | medium | router.go:338 |
| POST | `/api/v1/auth/api-keys` | Creates API key. **Response includes the new key value** (one-time only). | high | router.go:354 |
| GET | `/api/v1/auth/api-keys` | Lists API keys (metadata only — key bodies redacted). | medium | router.go:373 |
| DELETE | `/api/v1/auth/api-keys/:id` | Deletes own API key. | medium | router.go:389 |

### `/api/v1/workspaces/*` (CRUD)

| Method | Path | Description | Risk | Source |
|---|---|---|---|---|
| GET | `/api/v1/workspaces` | List user's workspaces (paginated). | low | router.go:409 |
| POST | `/api/v1/workspaces` | Create workspace; provisions sandbox pod. | medium | router.go:435 |
| GET | `/api/v1/workspaces/:id` | Fetch workspace (ownership-checked in handler). | low | router.go:454 |
| PUT | `/api/v1/workspaces/:id` | Rename. | low | router.go:468 |
| DELETE | `/api/v1/workspaces/:id` | Delete. | medium | router.go:488 |
| POST | `/api/v1/workspaces/:id/suspend` | Suspend. | low | router.go:501 |
| POST | `/api/v1/workspaces/:id/resume` | Resume. | low | router.go:514 |
| GET | `/api/v1/workspaces/:id/status` | Get phase. | low | router.go:527 |
| POST | `/api/v1/workspaces/:id/activate` | Boot sandbox pod. | medium | router.go:541 |
| GET | `/api/v1/workspaces/:id/sessions` | List opencode sessions for workspace. | low | router.go:559 |
| POST | `/api/v1/workspaces/:id/sessions/new` | Create / ensure session. | low | router.go:573 |
| PUT | `/api/v1/workspaces/:id/sessions/:sessionId/title` | Rename session. | low | router.go:587 |
| GET | `/api/v1/workspaces/:id/sessions/active` | List active sessions (registered only if `proxyHandler != nil`). | low | router.go:122 |

### Proxy routes (forwarded to sandbox pod)

| Method | Path | Description | Risk | Source |
|---|---|---|---|---|
| POST | `/api/v1/workspaces/:id/sessions/:sessionId/message` | Send chat message. **`:sessionId` interpolated directly into upstream URL with no validation** at API layer (`proxy.go:171`). Upstream BasicAuth protects but path surface inside that auth is reachable. | high | router.go:611 |
| POST | `/api/v1/workspaces/:id/sessions/:sessionId/prompt` | Async prompt. Same `:sessionId` concern. | high | router.go:612 |
| GET | `/api/v1/workspaces/:id/sessions/:sessionId/message` | Fetch history. | medium | router.go:613 |
| GET | `/api/v1/workspaces/:id/sessions/:sessionId` | Get session metadata. | low | router.go:614 |
| POST | `/api/v1/workspaces/:id/sessions/:sessionId/abort` | Abort in-flight session. | low | router.go:615 |
| GET | `/api/v1/workspaces/:id/events` | **Long-lived SSE stream**, rate-limit-exempt (`router.go:67`). Authenticated client can hold connection without rate-limit cost. | medium | router.go:616 |
| GET | `/api/v1/workspaces/:id/question` | List pending questions. | low | router.go:619 |
| POST | `/api/v1/workspaces/:id/question/:requestID/reply` | Reply. `:requestID` interpolation worth checking. | low | router.go:620 |
| POST | `/api/v1/workspaces/:id/question/:requestID/reject` | Reject. | low | router.go:621 |
| GET | `/api/v1/workspaces/:id/permission` | List permission requests. | low | router.go:622 |
| POST | `/api/v1/workspaces/:id/permission/:requestID/reply` | Reply to permission request. | medium | router.go:623 |

### Terminal ticket

| Method | Path | Description | Risk | Source |
|---|---|---|---|---|
| POST | `/api/v1/workspaces/:id/terminal/ticket` | Mints one-time ticket for terminal WS (30 s TTL, Redis-backed). | medium | router.go:154 |

### Secrets

| Method | Path | Description | Risk | Source |
|---|---|---|---|---|
| POST | `/api/v1/secrets` | Create secret. | high | router.go:173 |
| GET | `/api/v1/secrets` | List own secrets (metadata). | medium | router.go:174 |
| GET | `/api/v1/secrets/audit` | Audit log of secret access. | medium | router.go:175 |
| GET | `/api/v1/secrets/:id` | Get secret metadata. | medium | router.go:176 |
| PUT | `/api/v1/secrets/:id` | Update secret. | high | router.go:177 |
| DELETE | `/api/v1/secrets/:id` | Delete. | medium | router.go:178 |
| POST | `/api/v1/secrets/:id/reveal` | **Returns plaintext secret value.** | high | router.go:179 |
| GET | `/api/v1/secrets/:id/bindings` | List workspaces secret is bound to. | low | router.go:180 |

### Workspace bindings + env

| Method | Path | Description | Risk | Source |
|---|---|---|---|---|
| PUT | `/api/v1/workspaces/:id/bindings` | Bind secrets to workspace. | medium | router.go:182 |
| GET | `/api/v1/workspaces/:id/bindings` | List bindings. | low | router.go:183 |
| POST | `/api/v1/workspaces/:id/reload-secrets` | Reload secrets into pod. | medium | router.go:184 |
| PUT | `/api/v1/workspaces/:id/env` | Set plaintext env vars. | medium | router.go:185 |
| GET | `/api/v1/workspaces/:id/env` | List env vars. | medium | router.go:186 |
| DELETE | `/api/v1/workspaces/:id/env/:name` | Delete env var. | low | router.go:187 |

### Account

| Method | Path | Description | Risk | Source |
|---|---|---|---|---|
| POST | `/api/v1/account/rotate-key` | Rotate user's encryption key. | high | router.go:194 |
| POST | `/api/v1/account/change-password` | Change password. | high | router.go:195 |

### User settings

| Method | Path | Description | Risk | Source |
|---|---|---|---|---|
| GET | `/api/v1/users/me/settings` | Get user settings. | low | router.go:654 |
| GET | `/api/v1/users/me/settings/schema` | JSON schema. | low | router.go:655 |
| PUT | `/api/v1/users/me/settings/:key` | Update one setting. | low | router.go:656 |

---

## 3. Admin-only

All routes have **GLOBAL + Auth + AdminGuard**. Both admin groups explicitly call `middleware.AdminGuard()` (router.go:646, 665) — no route bypass found.

### Admin settings

| Method | Path | Description | Risk | Source |
|---|---|---|---|---|
| GET | `/api/v1/admin/settings` | Read instance settings. | medium | router.go:647 |
| GET | `/api/v1/admin/settings/schema` | Schema. | low | router.go:648 |
| PUT | `/api/v1/admin/settings/:key` | Update instance setting. | high | router.go:649 |

### Admin credential sets

| Method | Path | Description | Risk | Source |
|---|---|---|---|---|
| POST | `/api/v1/admin/credentials` | Create. | high | router.go:666 |
| GET | `/api/v1/admin/credentials` | List. | medium | router.go:667 |
| GET | `/api/v1/admin/credentials/:id` | Read. | high | router.go:668 |
| PUT | `/api/v1/admin/credentials/:id` | Update. | high | router.go:669 |
| DELETE | `/api/v1/admin/credentials/:id` | Delete. | medium | router.go:670 |
| PUT | `/api/v1/admin/credentials/:id/default` | Mark default. | medium | router.go:671 |
| POST | `/api/v1/admin/credentials/rotate-key` | Rotate credential-encryption key. | high | router.go:672 |

---

## 4. Special

| Method | Path | Auth | Description | Risk | Source |
|---|---|---|---|---|---|
| GET | `/api/v1/workspaces/:id/terminal` | **ws-only** — `?ticket=` validated against Redis (`terminal.go:185-213`). Registered on root router, **NO `AuthMiddleware`**. | WebSocket terminal proxy into sandbox pod. | high | router.go:156 |
| (group only) | `/api/v1/workspaces/:id/stream` | WS security + WS metrics middleware applied (router.go:107-109) but **no routes registered on this group anywhere in the codebase**. Dead code or forgotten route. | n/a | low | router.go:107 |

---

## Phase-1 derived findings (promote to Phase 2+ test plan)

These are static-analysis findings worth adding to the pentest test plan as new RT-x.y entries, or worth investigating during Phase 2:

### F1.1.1 — `/readyz` leaks driver error strings (medium)

`router.go:227, 234, 241` — `readyz` handler embeds raw `db.Ping()` and `cache.Ping()` error strings directly into the response body. On a probe failure, this could leak Postgres or Redis driver-level diagnostics (server version, connection-string fragments, hostname). Phase-1 finding; reproduce in Phase 2 by causing a Postgres outage during a `/readyz` probe and inspecting the body.

### F1.1.2 — `:sessionId` path traversal upstream (high)

`api/internal/handlers/proxy.go:171, 181, 186, 191, 196` — the user-supplied `:sessionId` is interpolated directly into the upstream URL path (`"/session/" + sid + "/message"`) with no validation at the API layer. The upstream opencode server is BasicAuth-protected with a per-workspace password, but inside that auth the entire path surface is reachable. A `sessionId` containing `..` or `/` could traverse to other endpoints on the upstream. Promote to **RT-2.16** for Phase 2 testing: send `sessionId="../admin"` and verify the upstream rejects rather than honours.

### F1.1.3 — `/metrics` unauthenticated (medium)

`router.go:200` — Prometheus `/metrics` endpoint has no auth gate. Default cardinality may include user counts, route templates, and request-distribution data. Promote to **RT-1.10**: enumerate metric names and assess what's leaked.

### F1.1.4 — `/api/v1/workspaces/:id/stream` group has middleware but no handler (low)

`router.go:107-109` — group is created with WS security middleware but no `GET`/`POST` registers on it. Either dead code (delete) or a forgotten route registration. Phase-1 finding; not a vulnerability but a code-quality red flag — investigate before Phase 2.

### F1.1.5 — `/api/v1/account/recover` outside the `/account` auth group (medium)

`router.go:196` — registered on root router, NOT inside `accountGroup` which has the auth middleware (`router.go:192-193`). This is intentional for recovery (user can't authenticate before they recover), but it places a high-value endpoint behind only the global rate limiter. Promote to **RT-2.17**: brute-force attempt against recovery flow.

### F1.1.6 — `/events` SSE rate-limit-exempt (low/medium)

`router.go:67` — SSE event stream is exempt from rate limiting. The per-workspace connection cap inside `ProxyHandler.acquireConnection` should bound it, but a misbehaving authenticated client can hold a long-lived connection without per-request rate-limit cost. Phase-2 RT-5.5 (connection exhaustion) already covers this; cross-reference there.

### F1.1.7 — Login error messages may enable user enumeration (medium)

`router.go:322` — `authSvc.Login` errors are passed through verbatim. If "user not found" and "wrong password" produce different error strings or different timing, account enumeration is possible. Phase-2 RT-2.4 should cover; cross-reference.
