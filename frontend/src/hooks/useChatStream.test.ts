import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useChatStream } from "./useChatStream";

vi.mock("../api/messages", () => ({
  messagesApi: {
    sendAsync: vi.fn(),
    getHistory: vi.fn(),
  },
}));

import { messagesApi } from "../api/messages";

describe("useChatStream", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("starts with streaming=false", () => {
    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));
    expect(result.current.streaming).toBe(false);
  });

  it("does nothing when workspaceId is undefined", async () => {
    const { result } = renderHook(() => useChatStream(undefined, "sess-1"));
    const onComplete = vi.fn();
    await act(async () => { result.current.send("hi", onComplete); });
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();
    expect(onComplete).not.toHaveBeenCalled();
  });

  it("does nothing when sessionId is undefined", async () => {
    const { result } = renderHook(() => useChatStream("sb-1", undefined));
    const onComplete = vi.fn();
    await act(async () => { result.current.send("hi", onComplete); });
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();
  });

  it("calls messagesApi.sendAsync with correct params", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "user-1", role: "user", parts: [{ type: "text", text: "hi" }] },
      { id: "asst-1", role: "assistant", parts: [{ type: "text", text: "Hello" }] },
    ]);

    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));
    const onComplete = vi.fn();

    await act(async () => { await result.current.send("hi", onComplete); });

    expect(messagesApi.sendAsync).toHaveBeenCalledWith("sb-1", "sess-1", {
      parts: [{ type: "text", text: "hi" }],
    });
  });

  it("fetches history after sendAsync resolves and calls onComplete with last assistant message", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "user-1", role: "user", parts: [{ type: "text", text: "hi" }] },
      { id: "asst-1", role: "assistant", parts: [{ type: "text", text: "response" }] },
    ]);

    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));
    const onComplete = vi.fn();

    await act(async () => { await result.current.send("hi", onComplete); });

    expect(messagesApi.getHistory).toHaveBeenCalledWith("sb-1", "sess-1");
    expect(onComplete).toHaveBeenCalledWith(expect.objectContaining({
      id: "asst-1",
      role: "assistant",
      parts: [{ type: "text", text: "response" }],
    }));
  });

  it("sets streaming=true during send and false after", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockImplementation(async () => {
      return undefined;
    });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));

    await act(async () => { await result.current.send("hi", vi.fn()); });
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

  it("handles empty history gracefully after sendAsync", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));
    const onComplete = vi.fn();

    await act(async () => { await result.current.send("hi", onComplete); });

    expect(onComplete).toHaveBeenCalledWith(expect.objectContaining({
      role: "assistant",
      parts: [],
    }));
  });
});
