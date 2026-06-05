# Inference Relay Worker

Thin Cloudflare Worker proxy for free-tier LLM inference IP distribution (Epic 26).

## Architecture

```
opencode (in pod) → https://relay.safespaces.dev/responses → CF Worker → https://opencode.ai/zen/v1/responses
```

Workspace pods have `OPENCODE_AUTH_CONTENT` with `metadata.baseURL: "https://relay.safespaces.dev"`. opencode sends free-tier requests to this URL. The Worker proxies to `opencode.ai/zen/v1`. Requests exit from Cloudflare's 300+ edge POPs, distributing IPs globally.

## Deployment

```bash
cd workers/inference-relay
npm install
npx wrangler deploy
```

After first deploy, add a custom domain route in CF dashboard:
- Domain: `safespaces.dev`
- Route: `relay.safespaces.dev/*` → this Worker

## URL Rotation

For additional obscurity, rotate the underlying Worker name periodically:

1. Deploy a new Worker with a fresh name:
   ```bash
   sed -i 's/name = .*/name = "relay-'$(date +%Y%m%d)'"/' wrangler.toml
   npx wrangler deploy
   ```

2. Update the `relay.safespaces.dev` route to point to the new Worker (CF dashboard or API).

3. Delete the old Worker after DNS TTL expires (~1 hour):
   ```bash
   npx wrangler delete --name relay-<old-date>
   ```

Pods never restart — they always call `relay.safespaces.dev`.

## Configuration

| Variable | Where | Default |
|----------|-------|---------|
| `UPSTREAM_URL` | `wrangler.toml` [vars] | `https://opencode.ai/zen/v1` |
| `inferenceRelayURL` | Helm `values.yaml` | `https://relay.safespaces.dev` |

## Security

URL obscurity only. Worst-case if discovered: attacker gets free-tier opencode.ai access (same as calling opencode.ai directly with `Bearer public`). CF rate limiting available via dashboard if needed.
