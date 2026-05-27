import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useChatStream } from "./useChatStream";

vi.mock("../api/messages", () => ({
  messagesApi: {
    sendAsync: vi.fn(),
    getHistory: vi.fn(),
  },
}));

vi.mock("../api/events", () => ({
  registerTabCloseAbort: vi.fn().mockReturnValue(vi.fn()),
}));

import { messagesApi } from "../api/messages";
import { registerTabCloseAbort } from "../api/events";

// Helper: sends a message and signals idle after sendAsync resolves
async function sendAndIdle(
  result: ReturnType<typeof renderHook<ReturnType<typeof useChatStream>, unknown>>["result"],
  text: string,
  onComplete = vi.fn(),
) {
  let sendPromise!: Promise<void>;
  act(() => { sendPromise = result.current.send(text, onComplete); });
  // Wait for sendAsync to have been called (means idle resolver is set)
  await vi.waitFor(() => expect(messagesApi.sendAsync).toHaveBeenCalled());
  act(() => { result.current.notifySessionIdle("sess-1"); });
  await act(async () => { await sendPromise; });
  return onComplete;
}

describe("useChatStream", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (registerTabCloseAbort as ReturnType<typeof vi.fn>).mockReturnValue(vi.fn());
  });

  it("starts with streaming=false and no error", () => {
    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));
    expect(result.current.streaming).toBe(false);
    expect(result.current.error).toBeNull();
  });

  it("does nothing when workspaceId is undefined", async () => {
    const { result } = renderHook(() => useChatStream(undefined, "sess-1"));
    await act(async () => { result.current.send("hi", vi.fn()); });
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();
  });

  it("does nothing when sessionId is undefined", async () => {
    const { result } = renderHook(() => useChatStream("sb-1", undefined));
    await act(async () => { result.current.send("hi", vi.fn()); });
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();
  });

  it("calls messagesApi.sendAsync with correct params", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));

    await sendAndIdle(result, "hi");

    expect(messagesApi.sendAsync).toHaveBeenCalledWith("sb-1", "sess-1", {
      parts: [{ type: "text", text: "hi" }],
    });
  });

  it("waits for notifySessionIdle before calling getHistory", async () => {
    let resolveSendAsync!: () => void;
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockReturnValue(
      new Promise<void>((r) => { resolveSendAsync = r; }),
    );
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));

    let sendPromise!: Promise<void>;
    act(() => { sendPromise = result.current.send("hi", vi.fn()); });

    // Resolve sendAsync
    act(() => { resolveSendAsync(); });

    // Give microtasks a tick to settle
    await vi.waitFor(() => expect(messagesApi.sendAsync).toHaveBeenCalled());
    await new Promise<void>((r) => setTimeout(r, 20));

    // getHistory must NOT be called yet — waiting for idle signal
    expect(messagesApi.getHistory).not.toHaveBeenCalled();

    // Now fire idle
    act(() => { result.current.notifySessionIdle("sess-1"); });
    await act(async () => { await sendPromise; });

    expect(messagesApi.getHistory).toHaveBeenCalledWith("sb-1", "sess-1");
  });

  it("ignores notifySessionIdle for a different sessionId", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));

    let sendPromise!: Promise<void>;
    act(() => { sendPromise = result.current.send("hi", vi.fn()); });
    await vi.waitFor(() => expect(messagesApi.sendAsync).toHaveBeenCalled());

    // Wrong session — should be ignored
    act(() => { result.current.notifySessionIdle("sess-OTHER"); });
    await new Promise<void>((r) => setTimeout(r, 30));
    expect(messagesApi.getHistory).not.toHaveBeenCalled();

    // Correct session
    act(() => { result.current.notifySessionIdle("sess-1"); });
    await act(async () => { await sendPromise; });
    expect(messagesApi.getHistory).toHaveBeenCalled();
  });

  it("calls onComplete with last assistant message after idle signal", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "user-1", role: "user", parts: [{ type: "text", text: "hi" }] },
      { id: "asst-1", role: "assistant", parts: [{ type: "text", text: "response" }] },
    ]);
    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));
    const onComplete = vi.fn();

    await sendAndIdle(result, "hi", onComplete);

    expect(onComplete).toHaveBeenCalledWith(expect.objectContaining({
      id: "asst-1",
      role: "assistant",
    }));
  });

  it("sets streaming=true during send and false after", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));

    let sendPromise!: Promise<void>;
    act(() => { sendPromise = result.current.send("hi", vi.fn()); });

    await vi.waitFor(() => expect(result.current.streaming).toBe(true));

    act(() => { result.current.notifySessionIdle("sess-1"); });
    await act(async () => { await sendPromise; });

    expect(result.current.streaming).toBe(false);
  });

  it("sets error when sendAsync rejects", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("network error"));

    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));
    const onComplete = vi.fn();

    await act(async () => { await result.current.send("hi", onComplete); });

    expect(result.current.error).toBe("network error");
    expect(result.current.streaming).toBe(false);
    expect(onComplete).not.toHaveBeenCalled();
  });

  it("sets error when getHistory rejects after idle signal", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("history failed"));
    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));
    const onComplete = vi.fn();

    await sendAndIdle(result, "hi", onComplete);

    expect(result.current.error).toBe("history failed");
    expect(result.current.streaming).toBe(false);
    expect(onComplete).not.toHaveBeenCalled();
  });

  it("handles empty history gracefully after idle signal", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));
    const onComplete = vi.fn();

    await sendAndIdle(result, "hi", onComplete);

    expect(onComplete).toHaveBeenCalledWith(expect.objectContaining({
      role: "assistant",
      parts: [],
    }));
  });

  it("clearError resets error to null", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("oops"));
    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));

    await act(async () => { await result.current.send("hi", vi.fn()); });
    expect(result.current.error).toBe("oops");

    act(() => { result.current.clearError(); });
    expect(result.current.error).toBeNull();
  });

  it("abort() is a no-op when not streaming", () => {
    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));
    expect(() => result.current.abort()).not.toThrow();
  });

  it("registerTabCloseAbort is called on send and cleanup runs on completion", async () => {
    const mockCleanup = vi.fn();
    (registerTabCloseAbort as ReturnType<typeof vi.fn>).mockReturnValue(mockCleanup);
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));
    await sendAndIdle(result, "hi");

    expect(registerTabCloseAbort).toHaveBeenCalledWith("sb-1", "sess-1");
    expect(mockCleanup).toHaveBeenCalled();
  });
});
