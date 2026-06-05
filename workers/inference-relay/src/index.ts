/**
 * LLMSafeSpace Inference Relay Worker
 *
 * Thin CORS-enabling proxy deployed to Cloudflare Workers.
 * Relays browser requests to opencode.ai/zen/v1 with proper CORS headers,
 * enabling client-proxied inference (Epic 26).
 *
 * Each user's request exits from the nearest Cloudflare POP, providing
 * natural IP distribution across 300+ global edge locations.
 */

const UPSTREAM = "https://opencode.ai/zen/v1";

const CORS_HEADERS = {
  "Access-Control-Allow-Origin": "*",
  "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
  "Access-Control-Allow-Headers": "content-type, authorization",
  "Access-Control-Max-Age": "86400",
};

export default {
  async fetch(request: Request): Promise<Response> {
    // Handle CORS preflight
    if (request.method === "OPTIONS") {
      return new Response(null, { status: 204, headers: CORS_HEADERS });
    }

    // Build upstream URL: worker.dev/chat/completions → opencode.ai/zen/v1/chat/completions
    const url = new URL(request.url);
    const target = UPSTREAM + url.pathname + url.search;

    // Forward the request to opencode.ai
    const upstreamReq = new Request(target, {
      method: request.method,
      headers: request.headers,
      body: request.body,
    });

    const resp = await fetch(upstreamReq);

    // Return response with CORS headers (streaming-compatible)
    const headers = new Headers(resp.headers);
    for (const [k, v] of Object.entries(CORS_HEADERS)) {
      headers.set(k, v);
    }

    return new Response(resp.body, {
      status: resp.status,
      headers,
    });
  },
};
