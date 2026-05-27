import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import { useEventStream } from "./useEventStream";

// --- Fetch mock helpers ---

type MockStreamController = {
  send: (line: string) => void;
  close: () => void;
};

function makeMockFetch(): {
  mock: ReturnType<typeof vi.fn>;
  controllers: MockStreamController[];
} {
  const controllers: MockStreamController[] = [];

  const mock = vi.fn().mockImplementation(() => {
    let closed = false;
    const chunks: Uint8Array[] = [];
    const listeners: Array<(chunk: Uint8Array) => void> = [];

    const ctrl: MockStreamController = {
      send(line: string) {
        if (closed) return;
        const chunk = new TextEncoder().encode(line);
        if (listeners.length > 0) {
          listeners.splice(0).forEach((l) => l(chunk));
        } else {
          chunks.push(chunk);
        }
      },
      close() {
        closed = true;
        listeners.splice(0).forEach((l) => l(new Uint8Array(0)));
      },
    };
    controllers.push(ctrl);

    const body = {
      getReader: () => ({
        read: () =>
          new Promise<{ done: boolean; value: Uint8Array }>((resolve) => {
            if (closed) return resolve({ done: true, value: new Uint8Array(0) });
            const next = chunks.shift();
            if (next) return resolve({ done: false, value: next });
            listeners.push((chunk) => {
              if (chunk.length === 0) resolve({ done: true, value: chunk });
              else resolve({ done: false, value: chunk });
            });
          }),
      }),
    };

    return Promise.resolve({ ok: true, body, status: 200 });
  });

  return { mock, controllers };
}

describe("useEventStream", () => {
  let fetchRestore: typeof globalThis.fetch;

  beforeEach(() => {
    fetchRestore = globalThis.fetch;
  });

  afterEach(() => {
    globalThis.fetch = fetchRestore;
    vi.restoreAllMocks();
  });

  it("does not connect when workspaceId is undefined", () => {
    const fetchMock = vi.fn();
    globalThis.fetch = fetchMock;
    renderHook(() => useEventStream(undefined, vi.fn()));
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("connects to the correct SSE endpoint", async () => {
    const { mock, controllers } = makeMockFetch();
    globalThis.fetch = mock;

    renderHook(() => useEventStream("sb-123", vi.fn()));

    await waitFor(() => expect(mock).toHaveBeenCalled());
    const [url, opts] = mock.mock.calls[0] as [string, RequestInit];
    expect(url).toContain("/workspaces/sb-123/events");
    expect((opts.headers as Record<string, string>)?.Accept).toBe("text/event-stream");
    expect(opts.credentials).toBe("include");

    controllers[0]!.close();
  });

  it("calls onEvent when a data event is received", async () => {
    const { mock, controllers } = makeMockFetch();
    globalThis.fetch = mock;

    const onEvent = vi.fn();
    renderHook(() => useEventStream("sb-123", onEvent));

    await waitFor(() => expect(mock).toHaveBeenCalled());

    act(() => {
      controllers[0]!.send(`data: ${JSON.stringify({ type: "session.status" })}\n\n`);
    });

    await waitFor(() => expect(onEvent).toHaveBeenCalledWith({ type: "session.status" }));
    controllers[0]!.close();
  });

  it("ignores malformed data lines", async () => {
    const { mock, controllers } = makeMockFetch();
    globalThis.fetch = mock;

    const onEvent = vi.fn();
    renderHook(() => useEventStream("sb-123", onEvent));

    await waitFor(() => expect(mock).toHaveBeenCalled());

    act(() => { controllers[0]!.send("data: not-json\n\n"); });
    await new Promise((r) => setTimeout(r, 20));

    expect(onEvent).not.toHaveBeenCalled();
    controllers[0]!.close();
  });

  it("aborts fetch on unmount", async () => {
    const abortSpy = vi.spyOn(AbortController.prototype, "abort");
    const { mock, controllers } = makeMockFetch();
    globalThis.fetch = mock;

    const { unmount } = renderHook(() => useEventStream("sb-123", vi.fn()));
    await waitFor(() => expect(mock).toHaveBeenCalled());

    unmount();
    expect(abortSpy).toHaveBeenCalled();
    controllers[0]?.close();
  });

  it("reconnects when workspaceId changes", async () => {
    const { mock, controllers } = makeMockFetch();
    globalThis.fetch = mock;

    const { rerender } = renderHook(
      ({ id }) => useEventStream(id, vi.fn()),
      { initialProps: { id: "sb-1" as string | undefined } },
    );

    await waitFor(() => expect(mock).toHaveBeenCalledTimes(1));
    expect((mock.mock.calls[0]![0] as string)).toContain("sb-1");

    // Close first stream and change workspaceId
    controllers[0]!.close();
    rerender({ id: "sb-2" });

    // Should connect to new workspace (after reconnect delay — use fake timers)
    await waitFor(() => expect(mock).toHaveBeenCalledTimes(2), { timeout: 5000 });
    expect((mock.mock.calls[1]![0] as string)).toContain("sb-2");
    controllers[1]?.close();
  });
});
