/**
 * LLMSafeSpaces Inference Relay Worker
 *
 * Thin authenticated proxy to the LLM provider for IP distribution (Epic 26).
 * Workspace pods point their opencode provider baseURL here, with the relay
 * secret embedded as the first path segment:
 *
 *   https://relay.safespaces.dev/<RELAY_SECRET>/responses
 *
 * The Worker validates the secret, strips it from the path, then forwards
 * the request to UPSTREAM_URL. Requests without a valid secret get 403.
 *
 * Security:
 *   - RELAY_SECRET is a CF Worker secret (not in source, not in wrangler.toml)
 *   - The secret never appears in forwarded requests or response headers
 *   - Without the secret, the Worker is a 403 wall regardless of path
 *
 * Configuration (CF Worker secrets — set via `wrangler secret put`):
 *   RELAY_SECRET — required, random token embedded in inferenceRelayURL
 *   UPSTREAM_URL — provider base URL (default: https://opencode.ai/zen/v1)
 */

export interface Env {
  RELAY_SECRET?: string;
  UPSTREAM_URL?: string;
}

type FetchFn = (req: Request) => Promise<Response>;

/**
 * handleRequest is the core worker logic, extracted for testability.
 * fetchFn defaults to global fetch; tests inject a mock.
 */
export async function handleRequest(
  request: Request,
  env: Env,
  fetchFn: FetchFn = fetch,
): Promise<Response> {
  const secret = env.RELAY_SECRET ?? "";

  // Reject immediately if no secret is configured — misconfiguration guard.
  if (!secret) {
    return new Response("Forbidden: relay not configured", { status: 403 });
  }

  const url = new URL(request.url);
  const segments = url.pathname.split("/").filter(Boolean); // remove empty strings

  // First path segment must be the secret.
  if (segments[0] !== secret) {
    return new Response("Forbidden", { status: 403 });
  }

  // Strip the secret segment; rebuild the remaining path.
  // If no path remains after the secret, use bare "/" (no trailing slash added).
  const remaining = segments.slice(1).join("/");
  const upstreamPath = remaining ? "/" + remaining : "";
  const upstream = (env.UPSTREAM_URL ?? "https://opencode.ai/zen/v1").replace(/\/$/, "");
  const target = upstream + upstreamPath + url.search;

  const resp = await fetchFn(
    new Request(target, {
      method: request.method,
      headers: request.headers,
      body: request.body,
      // Required in Node.js environments for streaming request bodies
      // (no-op in the CF Workers runtime).
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      ...(request.body ? { duplex: "half" } as any : {}),
    }),
  );

  return new Response(resp.body, {
    status: resp.status,
    headers: resp.headers,
  });
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    return handleRequest(request, env);
  },
};
