import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { createSSEConnection } from "./sseConnection";

// --- Helpers ---

function makeMockReader(opts?: { hangForever?: boolean; chunks?: string[] }) {
  const chunks = (opts?.chunks ?? []).map((c) => new TextEncoder().encode(c));
  let idx = 0;
  return {
    read: () => {
      if (opts?.hangForever || idx >= chunks.length) {
        return new Promise<{ done: boolean; value: Uint8Array }>(() => {});
      }
      const value = chunks[idx++]!;
      return Promise.resolve({ done: false, value });
    },
    cancel: vi.fn(() => Promise.resolve()),
  };
}

function makeMockFetch(reader: ReturnType<typeof makeMockReader>, status = 200) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    body: { getReader: () => reader },
  });
}

describe("createSSEConnection", () => {
  let fetchRestore: typeof globalThis.fetch;

  beforeEach(() => {
    fetchRestore = globalThis.fetch;
    vi.useFakeTimers();
  });

  afterEach(() => {
    globalThis.fetch = fetchRestore;
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("calls fetch with correct URL, headers, credentials, and signal", async () => {
    const reader = makeMockReader({ hangForever: true });
    const mock = makeMockFetch(reader);
    globalThis.fetch = mock;

    const conn = createSSEConnection({
      url: "/api/v1/events",
      onEvent: vi.fn(),
    });

    await vi.advanceTimersByTimeAsync(0);

    expect(mock).toHaveBeenCalledWith(
      "/api/v1/events",
      expect.objectContaining({
        credentials: "include",
        headers: expect.objectContaining({ Accept: "text/event-stream" }),
        signal: expect.any(AbortSignal),
      }),
    );

    conn.destroy();
  });

  it("passes custom headers to fetch", async () => {
    const reader = makeMockReader({ hangForever: true });
    const mock = makeMockFetch(reader);
    globalThis.fetch = mock;

    const conn = createSSEConnection({
      url: "/api/v1/events",
      headers: { "Last-Event-ID": "42" },
      onEvent: vi.fn(),
    });

    await vi.advanceTimersByTimeAsync(0);

    const headers = mock.mock.calls[0]?.[1]?.headers;
    expect(headers["Last-Event-ID"]).toBe("42");

    conn.destroy();
  });

  it("parses SSE data lines and calls onEvent", async () => {
    const reader = makeMockReader({
      chunks: [`data: {"type":"workspace.phase","phase":"Active"}\n\n`],
    });
    const mock = makeMockFetch(reader);
    globalThis.fetch = mock;
    const onEvent = vi.fn();

    const conn = createSSEConnection({ url: "/test", onEvent });

    await vi.advanceTimersByTimeAsync(0);
    // Let microtasks settle
    await vi.advanceTimersByTimeAsync(0);

    expect(onEvent).toHaveBeenCalledWith({ type: "workspace.phase", phase: "Active" });
    conn.destroy();
  });

  it("ignores malformed JSON data lines without throwing", async () => {
    const reader = makeMockReader({
      chunks: [`data: not-json\n\n`, `data: {"type":"ok"}\n\n`],
    });
    const mock = makeMockFetch(reader);
    globalThis.fetch = mock;
    const onEvent = vi.fn();

    const conn = createSSEConnection({ url: "/test", onEvent });

    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);

    expect(onEvent).toHaveBeenCalledTimes(1);
    expect(onEvent).toHaveBeenCalledWith({ type: "ok" });
    conn.destroy();
  });

  it("calls reader.cancel() and reconnects after READ_TIMEOUT_MS of silence", async () => {
    const reader = makeMockReader({ hangForever: true });
    let connectCount = 0;
    const mock = vi.fn().mockImplementation(() => {
      connectCount++;
      return Promise.resolve({
        ok: true,
        status: 200,
        body: { getReader: () => reader },
      });
    });
    globalThis.fetch = mock;

    const conn = createSSEConnection({
      url: "/test",
      onEvent: vi.fn(),
      readTimeoutMs: 35_000,
      minReconnectMs: 1_000,
    });

    await vi.advanceTimersByTimeAsync(0);
    expect(connectCount).toBe(1);

    // Advance past timeout
    await vi.advanceTimersByTimeAsync(35_000);

    // reader.cancel() should have been called
    expect(reader.cancel).toHaveBeenCalled();

    // Advance past reconnect delay (with jitter, max is minReconnect * 1.5)
    await vi.advanceTimersByTimeAsync(1_500);

    expect(connectCount).toBe(2);
    conn.destroy();
  });

  it("aborts the old AbortController on reconnect", async () => {
    const abortCalls: number[] = [];
    let connectCount = 0;

    const mock = vi.fn().mockImplementation((_url: string, opts: RequestInit) => {
      connectCount++;
      const n = connectCount;
      (opts.signal as AbortSignal).addEventListener("abort", () => abortCalls.push(n));
      return Promise.resolve({
        ok: true,
        status: 200,
        body: {
          getReader: () => ({
            read: () => new Promise(() => {}),
            cancel: vi.fn(() => Promise.resolve()),
          }),
        },
      });
    });
    globalThis.fetch = mock;

    const conn = createSSEConnection({
      url: "/test",
      onEvent: vi.fn(),
      readTimeoutMs: 100,
      minReconnectMs: 50,
    });

    await vi.advanceTimersByTimeAsync(0);
    expect(connectCount).toBe(1);

    // Timeout + reconnect
    await vi.advanceTimersByTimeAsync(100 + 75); // 75 to cover jitter

    expect(abortCalls).toContain(1);
    expect(connectCount).toBe(2);
    conn.destroy();
  });

  it("applies exponential backoff with jitter on repeated failures", async () => {
    let connectCount = 0;
    const connectTimes: number[] = [];

    const mock = vi.fn().mockImplementation(() => {
      connectCount++;
      connectTimes.push(Date.now());
      return Promise.resolve({ ok: false, status: 503, body: null });
    });
    globalThis.fetch = mock;

    const conn = createSSEConnection({
      url: "/test",
      onEvent: vi.fn(),
      minReconnectMs: 1_000,
      maxReconnectMs: 30_000,
    });

    // Initial connect
    await vi.advanceTimersByTimeAsync(0);
    expect(connectCount).toBe(1);

    // First retry: 1000 * [0.5, 1.5] jitter — advance 1500 to cover max
    await vi.advanceTimersByTimeAsync(1_500);
    expect(connectCount).toBe(2);

    // Second retry: 2000 * [0.5, 1.5] = up to 3000
    await vi.advanceTimersByTimeAsync(3_000);
    expect(connectCount).toBe(3);

    conn.destroy();
  });

  it("resets backoff after successful connection", async () => {
    let connectCount = 0;

    const mock = vi.fn().mockImplementation(() => {
      connectCount++;
      if (connectCount === 1) {
        // First attempt fails
        return Promise.resolve({ ok: false, status: 503, body: null });
      }
      // Second attempt succeeds but hangs
      return Promise.resolve({
        ok: true,
        status: 200,
        body: {
          getReader: () => ({
            read: () => new Promise(() => {}),
            cancel: vi.fn(() => Promise.resolve()),
          }),
        },
      });
    });
    globalThis.fetch = mock;

    const onConnect = vi.fn();
    const conn = createSSEConnection({
      url: "/test",
      onEvent: vi.fn(),
      onConnect,
      minReconnectMs: 1_000,
    });

    await vi.advanceTimersByTimeAsync(0); // fail
    await vi.advanceTimersByTimeAsync(1_500); // retry succeeds

    expect(onConnect).toHaveBeenCalledTimes(1);
    conn.destroy();
  });

  it("calls onDisconnect when stream ends", async () => {
    const reader = {
      read: vi.fn()
        .mockResolvedValueOnce({ done: false, value: new TextEncoder().encode("data: {}\n\n") })
        .mockResolvedValueOnce({ done: true, value: new Uint8Array(0) }),
      cancel: vi.fn(() => Promise.resolve()),
    };
    const mock = makeMockFetch(reader as any);
    globalThis.fetch = mock;

    const onDisconnect = vi.fn();
    const conn = createSSEConnection({
      url: "/test",
      onEvent: vi.fn(),
      onDisconnect,
    });

    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);

    expect(onDisconnect).toHaveBeenCalled();
    conn.destroy();
  });

  it("destroy() cancels everything and does not reconnect", async () => {
    const reader = makeMockReader({ hangForever: true });
    let connectCount = 0;
    const mock = vi.fn().mockImplementation(() => {
      connectCount++;
      return Promise.resolve({
        ok: true,
        status: 200,
        body: { getReader: () => reader },
      });
    });
    globalThis.fetch = mock;

    const conn = createSSEConnection({
      url: "/test",
      onEvent: vi.fn(),
      readTimeoutMs: 100,
      minReconnectMs: 50,
    });

    await vi.advanceTimersByTimeAsync(0);
    expect(connectCount).toBe(1);

    conn.destroy();

    // Advance well past timeout + reconnect
    await vi.advanceTimersByTimeAsync(10_000);

    expect(connectCount).toBe(1); // no reconnect after destroy
  });
});
