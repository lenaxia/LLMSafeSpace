import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { api, ApiClientError, streamRequest } from "./client";

describe("api client", () => {
  const mockFetch = vi.fn();

  beforeEach(() => {
    vi.stubGlobal("fetch", mockFetch);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("makes GET request with credentials include", async () => {
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: () => Promise.resolve({ data: "ok" }) });
    await api.get("/test");
    expect(mockFetch).toHaveBeenCalledWith(
      "/api/v1/test",
      expect.objectContaining({ credentials: "include" }),
    );
  });

  it("makes POST request with JSON body", async () => {
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: () => Promise.resolve({}) });
    await api.post("/test", { key: "value" });
    expect(mockFetch).toHaveBeenCalledWith(
      "/api/v1/test",
      expect.objectContaining({
        method: "POST",
        body: '{"key":"value"}',
      }),
    );
  });

  it("makes DELETE request", async () => {
    mockFetch.mockResolvedValue({ ok: true, status: 200, json: () => Promise.resolve({}) });
    await api.delete("/test/1");
    expect(mockFetch).toHaveBeenCalledWith(
      "/api/v1/test/1",
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("returns undefined for 204 responses", async () => {
    mockFetch.mockResolvedValue({ ok: true, status: 204 });
    const result = await api.post("/test");
    expect(result).toBeUndefined();
  });

  it("throws ApiClientError on non-ok response", async () => {
    mockFetch.mockResolvedValue({
      ok: false,
      status: 401,
      statusText: "Unauthorized",
      json: () => Promise.resolve({ error: "unauthorized" }),
    });
    await expect(api.get("/protected")).rejects.toThrow(ApiClientError);
  });

  it("ApiClientError contains status and body", async () => {
    mockFetch.mockResolvedValue({
      ok: false,
      status: 404,
      statusText: "Not Found",
      json: () => Promise.resolve({ error: "not found" }),
    });
    try {
      await api.get("/missing");
    } catch (e) {
      expect(e).toBeInstanceOf(ApiClientError);
      expect((e as ApiClientError).status).toBe(404);
      expect((e as ApiClientError).body.error).toBe("not found");
    }
  });

  it("handles non-JSON error responses gracefully", async () => {
    mockFetch.mockResolvedValue({
      ok: false,
      status: 500,
      statusText: "Internal Server Error",
      json: () => Promise.reject(new Error("not json")),
    });
    await expect(api.get("/broken")).rejects.toThrow("Internal Server Error");
  });

  it("streamRequest returns raw response for streaming", async () => {
    const mockResponse = { ok: true, status: 200, body: "stream" };
    mockFetch.mockResolvedValue(mockResponse);
    const res = await streamRequest("/chat", { text: "hi" });
    expect(res.body).toBe("stream");
  });

  it("streamRequest throws on error", async () => {
    mockFetch.mockResolvedValue({
      ok: false,
      status: 429,
      statusText: "Too Many Requests",
      json: () => Promise.resolve({ error: "rate limited" }),
    });
    await expect(streamRequest("/chat", {})).rejects.toThrow(ApiClientError);
  });
});
