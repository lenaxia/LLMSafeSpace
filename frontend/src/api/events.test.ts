import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { createEventStream } from "./events";

// --- Mock BroadcastChannel ---

type BCListener = (e: MessageEvent) => void;
const channels: Map<string, BCListener[]> = new Map();

class MockBroadcastChannel {
  name: string;
  onmessage: BCListener | null = null;

  constructor(name: string) {
    this.name = name;
    if (!channels.has(name)) channels.set(name, []);
    channels.get(name)!.push((e) => this.onmessage?.(e));
  }

  postMessage(data: unknown) {
    const listeners = channels.get(this.name) ?? [];
    for (const l of listeners) {
      l(new MessageEvent("message", { data }));
    }
  }

  close() {
    const listeners = channels.get(this.name);
    if (listeners) {
      const idx = listeners.indexOf(this.onmessage as any);
      if (idx >= 0) listeners.splice(idx, 1);
    }
  }
}

// --- Mock EventSource ---

let mockEventSources: MockEventSource[] = [];

class MockEventSource {
  url: string;
  withCredentials: boolean;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  onopen: (() => void) | null = null;
  readyState = 1;
  private _closed = false;

  constructor(url: string, opts?: { withCredentials?: boolean }) {
    this.url = url;
    this.withCredentials = opts?.withCredentials ?? false;
    mockEventSources.push(this);
    // Auto-fire onopen next tick
    setTimeout(() => this.onopen?.(), 0);
  }

  close() {
    this._closed = true;
    this.readyState = 2;
  }

  get closed() {
    return this._closed;
  }

  // Test helpers
  simulateError() {
    this.onerror?.();
  }

  simulateMessage(data: string) {
    this.onmessage?.(new MessageEvent("message", { data }));
  }

  simulateOpen() {
    this.onopen?.();
  }
}

describe("createEventStream — leader resignation", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockEventSources = [];
    channels.clear();
    (globalThis as any).BroadcastChannel = MockBroadcastChannel;
    (globalThis as any).EventSource = MockEventSource;
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    delete (globalThis as any).BroadcastChannel;
    delete (globalThis as any).EventSource;
  });

  it("leader resigns after EVENT_SOURCE_ERROR_TIMEOUT_MS of sustained error", async () => {
    const onEvent = vi.fn();
    const cleanup = createEventStream("ws-1", onEvent);

    // Wait for election timeout (max ~500ms)
    await vi.advanceTimersByTimeAsync(600);

    // Should have become leader
    expect(mockEventSources.length).toBe(1);
    const es = mockEventSources[0]!;

    // Simulate persistent error
    es.simulateError();

    // Advance past EVENT_SOURCE_ERROR_TIMEOUT_MS (15s) + checkLeaderAlive interval (5s)
    await vi.advanceTimersByTimeAsync(20_000);

    // Leader should have resigned — EventSource closed
    expect(es.closed).toBe(true);

    cleanup();
  });

  it("leader does NOT resign if onopen fires before timeout", async () => {
    const onEvent = vi.fn();
    const cleanup = createEventStream("ws-1", onEvent);

    await vi.advanceTimersByTimeAsync(600);

    const es = mockEventSources[0]!;

    // Error fires
    es.simulateError();

    // 10s later, connection recovers
    await vi.advanceTimersByTimeAsync(10_000);
    es.simulateOpen();

    // 10s more — past what would have been the timeout
    await vi.advanceTimersByTimeAsync(10_000);

    // Should NOT have resigned
    expect(es.closed).toBe(false);

    cleanup();
  });

  it("leader does NOT resign if onmessage fires before timeout", async () => {
    const onEvent = vi.fn();
    const cleanup = createEventStream("ws-1", onEvent);

    await vi.advanceTimersByTimeAsync(600);

    const es = mockEventSources[0]!;

    es.simulateError();

    await vi.advanceTimersByTimeAsync(10_000);
    es.simulateMessage(JSON.stringify({ type: "heartbeat" }));

    await vi.advanceTimersByTimeAsync(10_000);

    expect(es.closed).toBe(false);

    cleanup();
  });

  it("after resignation, applies backoff before re-election", async () => {
    const onEvent = vi.fn();
    const cleanup = createEventStream("ws-1", onEvent);

    await vi.advanceTimersByTimeAsync(600);
    expect(mockEventSources.length).toBe(1);

    const es1 = mockEventSources[0]!;
    es1.simulateError();

    // Trigger resignation
    await vi.advanceTimersByTimeAsync(20_000);
    expect(es1.closed).toBe(true);

    // A new EventSource should be created after backoff (>500ms election + possible backoff)
    await vi.advanceTimersByTimeAsync(5_000);

    // Should have re-elected (new EventSource created)
    expect(mockEventSources.length).toBe(2);

    cleanup();
  });

  it("single-tab resign/re-elect loop does not create rapid EventSource churn", async () => {
    const onEvent = vi.fn();
    const cleanup = createEventStream("ws-1", onEvent);

    await vi.advanceTimersByTimeAsync(600);

    // Simulate 3 resign cycles with increasing backoff
    for (let i = 0; i < 3; i++) {
      const es = mockEventSources[mockEventSources.length - 1]!;
      es.simulateError();
      // Need 15s error + 5s check interval to resign
      await vi.advanceTimersByTimeAsync(20_000);
      // Wait for re-election with increasing backoff (2s per resignation + 500ms jitter)
      await vi.advanceTimersByTimeAsync(10_000);
    }

    // Should have 4 EventSources total (1 initial + 3 re-elections)
    expect(mockEventSources.length).toBe(4);

    cleanup();
  });
});
