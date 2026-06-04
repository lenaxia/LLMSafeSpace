import { renderHook, act, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { useRelayClient } from "./useRelayClient";

// ReadyState numeric constants (mirrors WebSocket spec; avoids using
// the WebSocket global before vi.stubGlobal runs in beforeEach).
const WS_CONNECTING = 0;
const WS_OPEN = 1;
const WS_CLOSED = 3;

// Mock WebSocket
class MockWebSocket {
  // Static readyState constants — the hook reads WebSocket.OPEN, so these
  // must exist on the class that vi.stubGlobal("WebSocket", ...) installs.
  static CONNECTING = WS_CONNECTING;
  static OPEN = WS_OPEN;
  static CLOSING = 2;
  static CLOSED = WS_CLOSED;

  static instances: MockWebSocket[] = [];
  url: string;
  readyState = WS_CONNECTING;
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  sentMessages: string[] = [];

  constructor(url: string) {
    this.url = url;
    MockWebSocket.instances.push(this);
  }

  send(data: string) {
    this.sentMessages.push(data);
  }

  close() {
    this.readyState = WS_CLOSED;
    this.onclose?.();
  }

  // Test helpers
  simulateOpen() {
    this.readyState = WS_OPEN;
    this.onopen?.();
  }

  simulateMessage(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) });
  }

  simulateClose() {
    this.readyState = WS_CLOSED;
    this.onclose?.();
  }

  simulateError() {
    this.onerror?.();
  }
}

vi.mock("../env", () => ({
  getEnv: () => ({ apiBaseUrl: "http://localhost:8080/api/v1" }),
}));

describe("useRelayClient", () => {
  beforeEach(() => {
    MockWebSocket.instances = [];
    vi.stubGlobal("WebSocket", MockWebSocket);
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("starts disconnected when no workspaceId", () => {
    const { result } = renderHook(() =>
      useRelayClient({ workspaceId: null }),
    );
    expect(result.current.status).toBe("disconnected");
    expect(MockWebSocket.instances).toHaveLength(0);
  });

  it("connects when workspaceId is provided", () => {
    renderHook(() =>
      useRelayClient({ workspaceId: "ws1" }),
    );
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0]!.url).toContain("/workspaces/ws1/relay?role=client");
  });

  it("transitions to connected on WebSocket open", async () => {
    const { result } = renderHook(() =>
      useRelayClient({ workspaceId: "ws1" }),
    );

    act(() => {
      MockWebSocket.instances[0]!.simulateOpen();
    });

    expect(result.current.status).toBe("connected");
  });

  it("transitions to error on WebSocket error", () => {
    const { result } = renderHook(() =>
      useRelayClient({ workspaceId: "ws1" }),
    );

    act(() => {
      MockWebSocket.instances[0]!.simulateError();
    });

    expect(result.current.status).toBe("error");
  });

  it("handles proxy_request by calling fetch and streaming response", async () => {
    // Use real timers for this test: waitFor relies on setTimeout internally
    // and is incompatible with vi.useFakeTimers().
    vi.useRealTimers();

    const mockResponse = new Response("hello world", {
      status: 200,
      headers: { "content-type": "text/plain" },
    });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(mockResponse));

    renderHook(() =>
      useRelayClient({ workspaceId: "ws1" }),
    );

    const ws = MockWebSocket.instances[0]!;
    act(() => ws.simulateOpen());

    act(() => {
      ws.simulateMessage({
        type: "proxy_request",
        id: "req_1",
        method: "POST",
        url: "https://opencode.ai/v1/chat/completions",
        headers: { "content-type": "application/json" },
        body: '{"model":"test"}',
      });
    });

    // handleProxyRequest is fire-and-forget from onmessage; poll until
    // the async fetch chain completes and the messages are sent.
    await waitFor(() => {
      const types = ws.sentMessages
        .map((s) => JSON.parse(s) as { type: string })
        .map((m) => m.type);
      expect(types).toContain("proxy_response_start");
      expect(types).toContain("proxy_response_end");
    });
  });

  it("sends proxy_error on fetch failure (CORS)", async () => {
    // Use real timers — same reason as the test above.
    vi.useRealTimers();

    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new TypeError("Failed to fetch")),
    );

    renderHook(() =>
      useRelayClient({ workspaceId: "ws1" }),
    );

    const ws = MockWebSocket.instances[0]!;
    act(() => ws.simulateOpen());

    act(() => {
      ws.simulateMessage({
        type: "proxy_request",
        id: "req_2",
        method: "GET",
        url: "https://opencode.ai/v1/models",
        headers: {},
      });
    });

    await waitFor(() => {
      const sent = ws.sentMessages
        .map((s) => JSON.parse(s) as { type: string; error?: string });
      const errorMsg = sent.find((m) => m.type === "proxy_error");
      expect(errorMsg).toBeDefined();
      expect(errorMsg!.error).toContain("CORS");
    });
  });

  it("does not connect when enabled is false", () => {
    renderHook(() =>
      useRelayClient({ workspaceId: "ws1", enabled: false }),
    );
    expect(MockWebSocket.instances).toHaveLength(0);
  });

  it("reconnects with backoff on close", () => {
    const { result } = renderHook(() =>
      useRelayClient({ workspaceId: "ws1" }),
    );

    const ws = MockWebSocket.instances[0]!;
    act(() => ws.simulateOpen());
    act(() => ws.simulateClose());

    expect(result.current.status).toBe("disconnected");
    expect(MockWebSocket.instances).toHaveLength(1);

    // After backoff, should reconnect
    act(() => {
      vi.advanceTimersByTime(1000);
    });

    expect(MockWebSocket.instances).toHaveLength(2);
  });
});
