/**
 * LLMSafeSpace Inference Relay Worker
 *
 * Thin proxy to the LLM provider for IP distribution (Epic 26).
 * Workspace pods point their opencode provider baseURL here.
 * Requests exit from Cloudflare's 300+ edge POPs, providing
 * natural IP diversity without any cluster infrastructure.
 *
 * Security: URL is not published. Discovery = access to free-tier
 * opencode.ai models (same access anyone has with Bearer public).
 *
 * Configuration:
 *   UPSTREAM_URL — provider base URL (default: https://opencode.ai/zen/v1)
 */

interface Env {
  UPSTREAM_URL?: string;
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const upstream = env.UPSTREAM_URL || "https://opencode.ai/zen/v1";
    const url = new URL(request.url);
    const target = upstream + url.pathname + url.search;

    const resp = await fetch(new Request(target, {
      method: request.method,
      headers: request.headers,
      body: request.body,
    }));

    return new Response(resp.body, {
      status: resp.status,
      headers: resp.headers,
    });
  },
};
