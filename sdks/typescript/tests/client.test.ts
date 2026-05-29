import { describe, it, expect, vi, beforeEach } from "vitest";
import { LLMSafeSpace } from "../src/client.js";
import { AuthError, NotFoundError, TimeoutError, LLMSafeSpaceError } from "../src/errors.js";

// Mock fetch globally
const mockFetch = vi.fn();
vi.stubGlobal("fetch", mockFetch);

function jsonResponse(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function errorResponse(error: string, status: number) {
  return new Response(JSON.stringify({ error }), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("LLMSafeSpace Client", () => {
  let client: LLMSafeSpace;

  beforeEach(() => {
    vi.clearAllMocks();
    client = new LLMSafeSpace({
      baseUrl: "http://localhost:8080",
      apiKey: "lsp_test123",
    });
  });

  describe("workspaces", () => {
    it("lists workspaces", async () => {
      const data = { items: [{ id: "ws-1", name: "test" }], pagination: null };
      mockFetch.mockResolvedValueOnce(jsonResponse(data));

      const result = await client.workspaces.list();
      expect(result.items).toHaveLength(1);
      expect(result.items[0].id).toBe("ws-1");
      expect(mockFetch).toHaveBeenCalledWith(
        "http://localhost:8080/api/v1/workspaces?limit=20&offset=0",
        expect.objectContaining({ method: "GET" }),
      );
    });

    it("creates a workspace", async () => {
      const ws = { id: "ws-new", name: "my-ws", runtime: "python:3.11" };
      mockFetch.mockResolvedValueOnce(jsonResponse(ws, 201));

      const result = await client.workspaces.create({ name: "my-ws", runtime: "python:3.11", storageSize: "10Gi" });
      expect(result.id).toBe("ws-new");
    });

    it("handles 404", async () => {
      mockFetch.mockResolvedValueOnce(errorResponse("workspace not found", 404));

      await expect(client.workspaces.get("nonexistent")).rejects.toThrow(NotFoundError);
    });

    it("suspends a workspace", async () => {
      mockFetch.mockResolvedValueOnce(new Response(null, { status: 204 }));

      await expect(client.workspaces.suspend("ws-1")).resolves.toBeUndefined();
    });
  });

  describe("sessions", () => {
    it("ensures a session", async () => {
      const data = { workspaceId: "ws-1", sessionId: "sess-1", resumed: false, workspacePhase: "Active" };
      mockFetch.mockResolvedValueOnce(jsonResponse(data));

      const result = await client.sessions.ensure("ws-1");
      expect(result.sessionId).toBe("sess-1");
    });

    it("sends a message and extracts content", async () => {
      const openCodeResp = {
        id: "msg-1",
        role: "assistant",
        parts: [
          { type: "text", text: "Hello " },
          { type: "text", text: "world!" },
          { type: "tool-invocation", toolName: "read_file" },
        ],
      };
      mockFetch.mockResolvedValueOnce(jsonResponse(openCodeResp));

      const result = await client.sessions.sendMessage("ws-1", "sess-1", "hi");
      expect(result.content).toBe("Hello world!");
      expect(result.raw).toEqual(openCodeResp);
    });
  });

  describe("auth", () => {
    it("sends API key in Authorization header", async () => {
      mockFetch.mockResolvedValueOnce(jsonResponse({ id: "u1", username: "test" }));

      await client.auth.me();
      const call = mockFetch.mock.calls[0];
      expect(call[1].headers["Authorization"]).toBe("Bearer lsp_test123");
    });

    it("auto-logins with credentials on first request", async () => {
      const credClient = new LLMSafeSpace({
        baseUrl: "http://localhost:8080",
        credentials: { email: "test@example.com", password: "pass123" },
        timeout: 5000,
      });

      // First call: login (direct fetch to /auth/login)
      mockFetch.mockResolvedValueOnce(jsonResponse({ token: "jwt-abc", user: { id: "u1" } }));
      // Second call: actual request with token
      mockFetch.mockResolvedValueOnce(jsonResponse({ id: "u1", username: "test" }));

      await credClient.auth.me();
      expect(mockFetch).toHaveBeenCalledTimes(2);
      // First call should be login
      expect(mockFetch.mock.calls[0][0]).toContain("/auth/login");
      // Second call should have the token
      expect(mockFetch.mock.calls[1][1].headers["Authorization"]).toBe("Bearer jwt-abc");
    });

    it("throws AuthError on 401", async () => {
      mockFetch.mockResolvedValueOnce(errorResponse("authentication required", 401));

      await expect(client.auth.me()).rejects.toThrow(AuthError);
    });
  });

  describe("error handling", () => {
    it("throws TimeoutError on abort", async () => {
      // Simulate fetch rejecting with AbortError (what happens when AbortController fires)
      mockFetch.mockImplementationOnce(() => {
        const err = new DOMException("The operation was aborted", "AbortError");
        return Promise.reject(err);
      });

      await expect(client.workspaces.list()).rejects.toThrow(TimeoutError);
    });

    it("throws LLMSafeSpaceError for 500", async () => {
      mockFetch.mockResolvedValueOnce(errorResponse("internal error", 500));

      await expect(client.workspaces.list()).rejects.toThrow(LLMSafeSpaceError);
    });
  });

  describe("terminal", () => {
    it("gets a ticket", async () => {
      const data = { ticket: "tkt_abc123", expiresAt: "2026-05-29T18:00:00Z" };
      mockFetch.mockImplementationOnce(() => Promise.resolve(jsonResponse(data)));

      const result = await client.terminal.getTicket("ws-1");
      expect(result.ticket).toBe("tkt_abc123");
    });
  });
});
