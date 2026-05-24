import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useEventStream } from "./useEventStream";

// Mock EventSource
class MockEventSource {
  static instances: MockEventSource[] = [];
  url: string;
  withCredentials: boolean;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  readyState = 0;
  close = vi.fn();

  constructor(url: string, opts?: { withCredentials?: boolean }) {
    this.url = url;
    this.withCredentials = opts?.withCredentials ?? false;
    MockEventSource.instances.push(this);
  }

  simulateMessage(data: unknown) {
    this.onmessage?.(new MessageEvent("message", { data: JSON.stringify(data) }));
  }
}

describe("useEventStream", () => {
  beforeEach(() => {
    MockEventSource.instances = [];
    vi.stubGlobal("EventSource", MockEventSource);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("does not connect when workspaceId is undefined", () => {
    renderHook(() => useEventStream(undefined, vi.fn()));
    expect(MockEventSource.instances).toHaveLength(0);
  });

  it("connects to the correct SSE endpoint", () => {
    renderHook(() => useEventStream("sb-123", vi.fn()));
    expect(MockEventSource.instances).toHaveLength(1);
    expect(MockEventSource.instances[0]!.url).toContain("/workspaces/sb-123/events");
    expect(MockEventSource.instances[0]!.withCredentials).toBe(true);
  });

  it("calls onEvent when a message is received", () => {
    const onEvent = vi.fn();
    renderHook(() => useEventStream("sb-123", onEvent));

    const es = MockEventSource.instances[0]!;
    act(() => {
      es.simulateMessage({ session: { id: "s1", status: "active" } });
    });

    expect(onEvent).toHaveBeenCalledWith({ session: { id: "s1", status: "active" } });
  });

  it("closes connection on unmount", () => {
    const { unmount } = renderHook(() => useEventStream("sb-123", vi.fn()));
    const es = MockEventSource.instances[0]!;
    unmount();
    expect(es.close).toHaveBeenCalled();
  });

  it("reconnects when workspaceId changes", () => {
    const { rerender } = renderHook(
      ({ id }) => useEventStream(id, vi.fn()),
      { initialProps: { id: "sb-1" as string | undefined } },
    );

    expect(MockEventSource.instances).toHaveLength(1);
    const first = MockEventSource.instances[0]!;

    rerender({ id: "sb-2" });

    expect(first.close).toHaveBeenCalled();
    expect(MockEventSource.instances).toHaveLength(2);
    expect(MockEventSource.instances[1]!.url).toContain("/workspaces/sb-2/events");
  });

  it("ignores malformed messages", () => {
    const onEvent = vi.fn();
    renderHook(() => useEventStream("sb-123", onEvent));

    const es = MockEventSource.instances[0]!;
    // Simulate a message with invalid JSON
    es.onmessage?.(new MessageEvent("message", { data: "not json" }));

    expect(onEvent).not.toHaveBeenCalled();
  });
});
