# @llmsafespace/sdk

TypeScript SDK for the LLMSafeSpace API. Zero runtime dependencies — uses native `fetch`.

## Installation

```bash
npm install @llmsafespace/sdk
```

## Quick Start

```typescript
import { LLMSafeSpace } from '@llmsafespace/sdk';

const client = new LLMSafeSpace({
  baseUrl: 'https://llmsafespace.example.com',
  apiKey: 'lsp_your_api_key',
});

// Create a workspace and send a message
const workspace = await client.workspaces.create({ name: 'my-project', runtime: 'python:3.11', storageSize: '10Gi' });
const session = await client.sessions.ensure(workspace.id);
const response = await client.sessions.sendMessage(workspace.id, session.sessionId, 'Write hello world in Python');
console.log(response.content);
```

## Authentication

```typescript
// API key (recommended for programmatic use)
const client = new LLMSafeSpace({ baseUrl: '...', apiKey: 'lsp_...' });

// Email/password (auto-manages JWT, refreshes on 401)
const client = new LLMSafeSpace({ baseUrl: '...', credentials: { email: '...', password: '...' } });
```

## Important: `sendMessage` Timeout

`sendMessage` proxies to the LLM agent and blocks until it responds. This can take 30-120+ seconds. Default timeout is 120s. On timeout, a `TimeoutError` is thrown — the prompt may still be processing. Poll `getHistory` to check.

```typescript
import { TimeoutError } from '@llmsafespace/sdk';

try {
  const resp = await client.sessions.sendMessage(wsId, sessId, 'complex prompt...');
} catch (e) {
  if (e instanceof TimeoutError) {
    // Prompt may still be processing — check history later
    const history = await client.sessions.getHistory(wsId, sessId);
  }
}
```

## Error Handling

```typescript
import { NotFoundError, AuthError, ConflictError, LLMSafeSpaceError } from '@llmsafespace/sdk';

try {
  await client.workspaces.get('nonexistent');
} catch (e) {
  if (e instanceof NotFoundError) { /* 404 */ }
  if (e instanceof AuthError) { /* 401/403 */ }
  if (e instanceof ConflictError) { /* 409 - e.g. workspace not active */ }
  if (e instanceof LLMSafeSpaceError) { /* any API error: e.status, e.message */ }
}
```

## API Reference

### `client.workspaces`
- `list(limit?, offset?)` — List workspaces
- `create(req)` — Create workspace
- `get(id)` — Get workspace
- `rename(id, name)` — Rename workspace
- `delete(id)` — Delete workspace
- `getStatus(id)` — Get workspace status
- `activate(id)` — Activate (resume + ensure session)
- `suspend(id)` — Suspend workspace
- `resume(id)` — Resume workspace
- `setBindings(id, secretIds)` — Set secret bindings
- `setEnv(id, env)` — Set environment variables
- `getEnv(id)` — Get environment variables

### `client.sessions`
- `ensure(workspaceId)` — Create session (activates workspace if needed)
- `list(workspaceId)` — List sessions
- `getActive(workspaceId)` — Get active sessions
- `rename(workspaceId, sessionId, title)` — Rename session
- `sendMessage(workspaceId, sessionId, content)` — Send message (sync, blocks)
- `getHistory(workspaceId, sessionId)` — Get message history
- `abort(workspaceId, sessionId)` — Abort current operation

### `client.auth`
- `me()` — Get current user
- `listApiKeys()` — List API keys
- `createApiKey(name)` — Create API key
- `deleteApiKey(id)` — Delete API key

### `client.secrets`
- `create(req)` — Create secret
- `list()` — List secrets
- `get(id)` — Get secret
- `delete(id)` — Delete secret
- `reveal(id)` — Reveal secret value (audited)

### `client.terminal`
- `getTicket(workspaceId)` — Get one-time WebSocket terminal ticket

## Requirements

- Node.js 18+ or modern browser (native `fetch` required)
- No runtime dependencies
