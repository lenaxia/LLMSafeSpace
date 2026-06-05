/**
 * LLMSafeSpace Inference Relay Worker
 *
 * CORS-enabling proxy for free-tier LLM inference (Epic 26).
 * Relays browser requests to the upstream provider with CORS headers,
 * restricted to configured allowed origins.
 *
 * Configuration (via wrangler.toml [vars] or CF dashboard):
 *   ALLOWED_ORIGINS — comma-separated list of allowed origins
 *   UPSTREAM_URL    — provider base URL to proxy to
 */

interface Env {
  ALLOWED_ORIGINS: string; // e.g. "https://safespace.thekao.cloud,https://localhost:3000"
  UPSTREAM_URL: string; // e.g. "https://opencode.ai/zen/v1"
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const allowedOrigins = (env.ALLOWED_ORIGINS || "").split(",").map((s) => s.trim()).filter(Boolean);
    const upstream = env.UPSTREAM_URL || "https://opencode.ai/zen/v1";
    const origin = request.headers.get("Origin") || "";

    const isAllowed = allowedOrigins.length === 0 || allowedOrigins.includes(origin);

    // CORS preflight
    if (request.method === "OPTIONS") {
      if (!isAllowed) {
        return new Response(null, { status: 403 });
      }
      return new Response(null, {
        status: 204,
        headers: corsHeaders(origin, allowedOrigins),
      });
    }

    // Reject disallowed origins
    if (origin && !isAllowed) {
      return new Response("Forbidden", { status: 403 });
    }

    // Build upstream URL
    const url = new URL(request.url);
    const target = upstream + url.pathname + url.search;

    const resp = await fetch(
      new Request(target, {
        method: request.method,
        headers: request.headers,
        body: request.body,
      }),
    );

    // Return response with CORS headers
    const headers = new Headers(resp.headers);
    for (const [k, v] of Object.entries(corsHeaders(origin, allowedOrigins))) {
      headers.set(k, v);
    }

    return new Response(resp.body, { status: resp.status, headers });
  },
};

function corsHeaders(origin: string, allowedOrigins: string[]): Record<string, string> {
  // If no origins configured, allow the requesting origin (open mode for dev)
  const allowOrigin = allowedOrigins.length === 0 ? (origin || "*") : origin;
  return {
    "Access-Control-Allow-Origin": allowOrigin,
    "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
    "Access-Control-Allow-Headers": "content-type, authorization",
    "Access-Control-Max-Age": "86400",
  };
}
