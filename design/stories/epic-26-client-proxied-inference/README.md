# Epic 26: Client-Proxied Inference for Free Models

**Status:** Design
**Priority:** Medium
**Depends On:** Epic 10 (Multi-Tenant Trust & Secret Management)
**Motivation:** Enable free-tier LLM models at scale without platform IP throttling/banning

---

## Problem Statement

### Current State

opencode ships with a built-in `opencode` provider that offers free models (zero-cost models from `models.dev` catalog). When no API key is configured, the opencode plugin sets `apiKey: "public"` and disables paid models, leaving only free ones available.

In the current architecture, ALL LLM API calls originate from the LLMSafeSpace server cluster:

```
User Browser → LLMSafeSpace API → Workspace Pod (opencode) → Provider API (opencode.ai)
                                                                    ↑
                                                            All traffic from
                                                            our server IPs
```

At scale this means:
- Every free-tier user's requests come from the same set of server IPs
- Rate limits and abuse detection are per-IP, not per-user
- 100 concurrent free-tier users all sharing the same IP pool = throttled/banned for everyone
- The platform bears the compute and bandwidth cost of proxying all LLM traffic

### Desired State

Free-model traffic is proxied through each user's own client (browser or SDK), so:
- Each user's requests appear to come from their own IP
- Rate limits apply per-user naturally (different source IPs)
- Platform server doesn't carry the bandwidth/compute for free model streaming
- Paid providers (user-supplied API keys) continue to go direct from server (lower latency, key security)

---

## Key Insight: opencode's Free Models

opencode's model catalog comes from `https://models.dev/api.json`. The `opencode` provider plugin (`packages/core/src/plugin/provider/opencode.ts`):

1. If no API key/env/account is set → sets `apiKey: "public"`
2. Disables all models with `cost.input > 0` (paid models hidden)
3. Free models (cost.input === 0) remain enabled

These free models route through opencode's inference gateway (`opencode.ai`). The `opencode` provider uses an `aisdk` endpoint type with an opencode-specific SDK package.

**Examples of free models** (from models.dev, cost=0):
- Models offered by the opencode provider at zero cost
- Typically community/open models proxied through opencode's gateway

---

## Architecture: Client-Proxied Inference

### Core Concept

Instead of the server making the HTTP call to the LLM provider, the server delegates the HTTP call to the client. The client makes the actual network request and streams the response back to the server, which feeds it to opencode.

### Protocol

```
┌─────────┐         ┌──────────────┐         ┌───────────┐
│  Client  │         │ LLMSafeSpace │         │  opencode  │
│(Browser/ │         │   Server     │         │  (in pod)  │
│   SDK)   │         │              │         │            │
└────┬─────┘         └──────┬───────┘         └─────┬──────┘
     │                      │                       │
     │  1. User sends prompt│                       │
     │─────────────────────>│  2. Forward to agent  │
     │                      │──────────────────────>│
     │                      │                       │
     │                      │  3. Agent needs to    │
     │                      │     call LLM API      │
     │                      │<──────────────────────│
     │                      │                       │
     │  4. Proxy request    │                       │
     │     (method, url,    │                       │
     │      headers, body)  │                       │
     │<─────────────────────│                       │
     │                      │                       │
     │  5. Client makes     │                       │
     │     HTTP request to  │                       │
     │     provider API     │                       │
     │─────────[network]────────────────────────────────> Provider API
     │                      │                       │
     │  6. Stream response  │                       │
     │     chunks back      │                       │
     │─────────────────────>│  7. Feed to agent     │
     │                      │──────────────────────>│
     │                      │                       │
     │  8. Agent processes, │                       │
     │     emits events     │                       │
     │<─────────────────────│<──────────────────────│
     │                      │                       │
```

### Transport: WebSocket Relay Channel

A dedicated WebSocket connection between client and server carries proxy requests:

```
Client ←──WebSocket──→ Server (relay endpoint)
```

Messages on this channel:

**Server → Client (proxy request):**
```json
{
  "type": "proxy_request",
  "id": "req_abc123",
  "method": "POST",
  "url": "https://opencode.ai/v1/chat/completions",
  "headers": {
    "content-type": "application/json",
    "authorization": "Bearer public"
  },
  "body": "{\"model\":\"...\",\"messages\":[...]}"
}
```

**Client → Server (proxy response, streamed):**
```json
{"type": "proxy_response_start", "id": "req_abc123", "status": 200, "headers": {"content-type": "text/event-stream"}}
{"type": "proxy_response_chunk", "id": "req_abc123", "data": "data: {\"choices\":[...]}\n\n"}
{"type": "proxy_response_chunk", "id": "req_abc123", "data": "data: {\"choices\":[...]}\n\n"}
{"type": "proxy_response_end", "id": "req_abc123"}
```

**Client → Server (proxy error):**
```json
{"type": "proxy_error", "id": "req_abc123", "error": "CORS blocked", "status": 0}
```

### Decision: Which Traffic Gets Proxied?

| Provider | API Key | Proxy Through Client? | Rationale |
|----------|---------|----------------------|-----------|
| `opencode` | `"public"` (free tier) | **Yes** | Avoid platform IP throttling |
| Any provider | User-supplied key | **No** (server-direct) | Lower latency, key never sent to client |
| `opencode` | User-supplied paid key | **No** (server-direct) | Paid user, no throttle risk |

The proxy decision is made by the server when it intercepts the outgoing HTTP request from opencode. If the target is the opencode provider with `apiKey: "public"`, route through client. Otherwise, make the call directly.

---

## Implementation Layers

### Layer 1: Custom HTTP Transport for opencode (in-pod)

A custom transport layer that intercepts opencode's outgoing HTTP calls and routes them to the relay channel instead of making them directly.

**Location:** `cmd/workspace-agentd/` or a new `pkg/agentd/proxy/` package

**Mechanism:** opencode uses the `ai-sdk` which uses Node.js `fetch`. We can intercept at the environment level:
- Option A: Custom `HTTP_PROXY`/`HTTPS_PROXY` env var pointing to a local proxy in the pod
- Option B: Patch opencode's fetch via `--experimental-fetch` or custom global
- Option C: Add a transport plugin to opencode (upstream contribution)

**Recommended: Option A (local proxy in pod).** The agentd already runs alongside opencode. Add an HTTP proxy mode that:
1. Receives the outgoing request from opencode (via standard HTTPS_PROXY)
2. Checks if it should be client-proxied (free tier detection)
3. If yes: holds the connection open, sends the request over the relay WebSocket to the client
4. Streams the response back from the WebSocket into the HTTP response to opencode
5. If no: makes the request directly (pass-through)

### Layer 2: WebSocket Relay Channel (API server)

A new WebSocket endpoint on the API server:

```
GET /api/v1/workspaces/:id/relay
Upgrade: websocket
```

This maintains a bidirectional channel between:
- The workspace pod's agentd (connects as "provider")
- The user's client (connects as "consumer")

Messages from agentd (proxy requests) are forwarded to the client. Messages from the client (proxy responses) are forwarded back to agentd.

### Layer 3: Client SDK / Browser Implementation

**Browser:**
- JavaScript/TypeScript SDK that connects to the relay WebSocket
- Receives `proxy_request` messages
- Uses browser `fetch()` to make the actual HTTP call to the provider
- Streams the response back as `proxy_response_chunk` messages

**SDK (Python/Node/Go):**
- Same protocol, just using the SDK's HTTP client instead of browser fetch

### Layer 4: Free Model UX Annotation

When `ListModels` returns the model catalog, annotate models from the `opencode` provider with free-tier:

```json
{
  "id": "opencode/some-model",
  "providerID": "opencode",
  "name": "Some Model",
  "tier": "free",
  "proxyRequired": true,
  "note": "Free model — requests proxied through your browser"
}
```

The frontend uses `proxyRequired: true` to:
1. Ensure the relay WebSocket is connected before allowing selection
2. Show a UI indicator that this model routes through the client
3. Warn if the client goes offline mid-conversation

---

## User Stories

### US-26.1: Local HTTP Proxy in agentd

**Goal:** Intercept outgoing HTTP from opencode and route free-tier requests to the relay channel.

**Scope:**
- HTTP CONNECT proxy running on localhost in the pod
- `HTTPS_PROXY=http://localhost:{port}` env var set for opencode
- Detection logic: if target host is `opencode.ai` (or the opencode provider gateway) AND no paid API key → proxy
- Otherwise: CONNECT pass-through (direct)
- Buffer: hold requests pending client connection for up to 5s, then fail with 503

### US-26.2: WebSocket Relay Endpoint (API server)

**Goal:** Bidirectional WebSocket relay between agentd and client.

**Scope:**
- `GET /api/v1/workspaces/:id/relay` endpoint
- Auth: same JWT/API key as other endpoints
- Two participants per workspace: agentd (pod) and client (browser/SDK)
- Message routing: agentd→client for requests, client→agentd for responses
- Heartbeat/keepalive (30s ping)
- Graceful degradation: if client disconnects, pending requests get 503
- Multiple concurrent requests supported (multiplexed by request ID)

### US-26.3: Browser Relay Client

**Goal:** JavaScript/TypeScript library that handles the client side of the relay.

**Scope:**
- Connect to relay WebSocket
- Receive proxy requests, execute via `fetch()`
- Stream responses back (support SSE/chunked transfer)
- Handle CORS: if provider blocks browser requests, report error
- Reconnection logic with exponential backoff
- npm package: `@llmsafespace/relay-client`

### US-26.4: SDK Relay Client

**Goal:** Go/Python/Node SDK support for relay proxying.

**Scope:**
- Same protocol as browser
- SDK makes HTTP calls using native HTTP client (no CORS issues)
- Integrated into existing LLMSafeSpace SDK
- Automatic detection: if workspace has free models, connect relay

### US-26.5: Model Tier Annotation

**Goal:** API returns tier/proxy metadata with model list.

**Scope:**
- `GET /api/v1/workspaces/:id/models` includes `tier` and `proxyRequired` fields
- Detection: models from `opencode` provider with public key → `tier: "free", proxyRequired: true`
- Frontend shows indicator for free/proxied models

### US-26.6: CORS Fallback

**Goal:** Handle the case where provider APIs block browser requests.

**Scope:**
- If browser `fetch()` fails due to CORS, report `proxy_error` with CORS reason
- Server falls back to making the request directly (accepting the rate-limit risk)
- Rate-limit the fallback per-user (e.g., 10 requests/minute server-side for free tier)
- UI shows: "Your browser couldn't reach the model provider directly. Using server proxy (rate limited)."

---

## Trade-offs

| Dimension | Client-Proxied | Server-Direct (current) |
|-----------|---------------|------------------------|
| Latency | +50-200ms (extra WebSocket hop) | Lowest |
| Server bandwidth | Zero (client carries LLM traffic) | Full streaming through server |
| Rate limiting | Per-user IP (natural) | Per-platform IP (throttled) |
| Scalability | Linear with users | Bottlenecked on server IPs |
| Client offline | Requests fail | Always works |
| CORS issues | Possible (fallback exists) | None |
| Complexity | High (relay protocol) | Low (direct HTTP) |

**Acceptable because:** This only applies to free models where users accept lower reliability for zero cost.

---

## Security Considerations

1. **No secrets in proxy requests.** Free-tier uses `apiKey: "public"` — nothing sensitive is sent to the client. If a user has a paid key, that traffic never touches the relay.

2. **Request validation.** The server must validate that proxy requests only target allowed hosts (the opencode provider gateway). A malicious agentd must not be able to use the client as an open proxy.

3. **Response integrity.** The server should validate that proxy responses are well-formed HTTP (status code, headers, body). A malicious client must not be able to inject arbitrary data into the opencode response stream.

4. **DoS protection.** Rate-limit the number of proxy requests per workspace (e.g., 60/minute). A runaway agent must not flood the client with proxy requests.

---

## Non-Requirements (Out of Scope)

1. **Paid provider proxying** — Never. Paid API keys stay server-side.
2. **Provider API key rotation** — Separate concern (Epic 10).
3. **Multiple simultaneous clients** — One relay client per workspace for now.
4. **Offline queue** — If client disconnects, requests fail immediately. No store-and-forward.
5. **WebRTC** — WebSocket is sufficient; no need for peer-to-peer.

---

## Dependency Graph

```
US-26.1 (Local Proxy in agentd) ──────┐
                                        │
US-26.2 (WebSocket Relay Endpoint) ────┼──→ US-26.3 (Browser Client)
                                        │         │
                                        │         ▼
                                        ├──→ US-26.4 (SDK Client)
                                        │
                                        ▼
                                  US-26.5 (Model Tier Annotation)
                                        │
                                        ▼
                                  US-26.6 (CORS Fallback)
```

**Critical path:** US-26.1 + US-26.2 + US-26.3 (minimum for browser-based free models)

---

## Open Questions

1. **Does opencode.ai's free API support CORS?** If yes, browser `fetch()` works directly. If no, the CORS fallback (US-26.6) becomes critical from day one rather than an edge case.

2. **Can we use `HTTPS_PROXY` with opencode?** opencode uses Node.js `fetch` via the AI SDK. Node's `fetch` respects `HTTPS_PROXY` via `node --experimental-global-fetch` but behavior varies. Needs validation.

3. **Alternative: opencode transport plugin.** Instead of an HTTP proxy, could we contribute a custom AI SDK transport to opencode that delegates to a Unix socket? This would be cleaner but requires upstream acceptance.

4. **Rate limit signals.** When the free provider rate-limits the client, how does that signal propagate back to opencode? The proxy response will be a 429 — opencode's retry logic should handle this, but needs testing.
