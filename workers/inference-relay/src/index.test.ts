import { describe, it, expect, vi, beforeEach } from "vitest";

// We test the worker logic in isolation by importing the handler directly.
// The worker is restructured so the core logic is a pure function testable
// without the CF runtime.

import { handleRequest } from "./index";

const RELAY_SECRET = "test-secret-abc123";

function makeEnv(secret = RELAY_SECRET, upstream = "https://opencode.ai/zen/v1"): { RELAY_SECRET: string; UPSTREAM_URL: string } {
  return { RELAY_SECRET: secret, UPSTREAM_URL: upstream };
}

describe("inference-relay worker", () => {
  describe("secret validation", () => {
    it("returns 403 when no secret in path", async () => {
      const req = new Request("https://relay.safespaces.dev/responses");
      const res = await handleRequest(req, makeEnv(), mockFetch());
      expect(res.status).toBe(403);
      const body = await res.text();
      expect(body).toContain("Forbidden");
    });

    it("returns 403 when wrong secret in path", async () => {
      const req = new Request("https://relay.safespaces.dev/wrong-secret/responses");
      const res = await handleRequest(req, makeEnv(), mockFetch());
      expect(res.status).toBe(403);
    });

    it("returns 403 when RELAY_SECRET not configured", async () => {
      const req = new Request("https://relay.safespaces.dev/test-secret-abc123/responses");
      const env = { RELAY_SECRET: "", UPSTREAM_URL: "https://opencode.ai/zen/v1" };
      const res = await handleRequest(req, env, mockFetch());
      expect(res.status).toBe(403);
    });

    it("proxies when correct secret in path", async () => {
      const req = new Request("https://relay.safespaces.dev/test-secret-abc123/responses", {
        method: "POST",
        body: JSON.stringify({ model: "nemotron-3-ultra-free" }),
        headers: { "Content-Type": "application/json" },
      });
      const upstream = mockFetch(200, "ok");
      const res = await handleRequest(req, makeEnv(), upstream);
      expect(res.status).toBe(200);
    });
  });

  describe("path rewriting", () => {
    it("strips secret prefix and forwards remaining path to upstream", async () => {
      let capturedUrl = "";
      const upstream = vi.fn(async (req: Request) => {
        capturedUrl = req.url;
        return new Response("ok", { status: 200 });
      });

      const req = new Request("https://relay.safespaces.dev/test-secret-abc123/responses?stream=true");
      await handleRequest(req, makeEnv(), upstream);

      expect(capturedUrl).toBe("https://opencode.ai/zen/v1/responses?stream=true");
    });

    it("handles root path after secret (no trailing slash)", async () => {
      let capturedUrl = "";
      const upstream = vi.fn(async (req: Request) => {
        capturedUrl = req.url;
        return new Response("ok", { status: 200 });
      });

      const req = new Request("https://relay.safespaces.dev/test-secret-abc123");
      await handleRequest(req, makeEnv(), upstream);

      expect(capturedUrl).toBe("https://opencode.ai/zen/v1");
    });
  });

  describe("request forwarding", () => {
    it("forwards method, headers, and body to upstream", async () => {
      let capturedReq: Request | null = null;
      const upstream = vi.fn(async (req: Request) => {
        capturedReq = req;
        return new Response("ok", { status: 200 });
      });

      const req = new Request("https://relay.safespaces.dev/test-secret-abc123/responses", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Authorization": "Bearer public",
        },
        body: JSON.stringify({ model: "test" }),
      });
      await handleRequest(req, makeEnv(), upstream);

      expect(capturedReq!.method).toBe("POST");
      expect(capturedReq!.headers.get("Content-Type")).toBe("application/json");
      expect(capturedReq!.headers.get("Authorization")).toBe("Bearer public");
    });

    it("does NOT forward the secret in any header or the URL", async () => {
      let capturedReq: Request | null = null;
      const upstream = vi.fn(async (req: Request) => {
        capturedReq = req;
        return new Response("ok", { status: 200 });
      });

      const req = new Request("https://relay.safespaces.dev/test-secret-abc123/responses");
      await handleRequest(req, makeEnv(), upstream);

      // Secret must not appear in forwarded URL
      expect(capturedReq!.url).not.toContain("test-secret-abc123");
      // Secret must not appear in any header
      capturedReq!.headers.forEach((val) => {
        expect(val).not.toContain("test-secret-abc123");
      });
    });

    it("returns upstream status and body intact", async () => {
      const upstream = mockFetch(429, "rate limited");
      const req = new Request("https://relay.safespaces.dev/test-secret-abc123/responses");
      const res = await handleRequest(req, makeEnv(), upstream);
      expect(res.status).toBe(429);
      expect(await res.text()).toBe("rate limited");
    });
  });
});

// --- helpers ---

function mockFetch(status = 200, body = "upstream ok"): (req: Request) => Promise<Response> {
  return vi.fn(async (_req: Request) => new Response(body, { status }));
}
