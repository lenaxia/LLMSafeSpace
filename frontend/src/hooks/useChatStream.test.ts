import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useChatStream } from "./useChatStream";

vi.mock("../api/messages", () => ({
  messagesApi: {
    send: vi.fn(),
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

  it("does nothing when sandboxId is undefined", async () => {
    const { result } = renderHook(() => useChatStream(undefined, "sess-1"));
    const onComplete = vi.fn();
    await act(async () => { result.current.send("hi", onComplete); });
    expect(messagesApi.send).not.toHaveBeenCalled();
    expect(onComplete).not.toHaveBeenCalled();
  });

  it("does nothing when sessionId is undefined", async () => {
    const { result } = renderHook(() => useChatStream("sb-1", undefined));
    const onComplete = vi.fn();
    await act(async () => { result.current.send("hi", onComplete); });
    expect(messagesApi.send).not.toHaveBeenCalled();
  });

  it("calls messagesApi.send with correct params", async () => {
    const mockReader = {
      read: vi.fn()
        .mockResolvedValueOnce({ done: false, value: new TextEncoder().encode("Hello") })
        .mockResolvedValueOnce({ done: true, value: undefined }),
    };
    const mockResponse = { body: { getReader: () => mockReader } };
    (messagesApi.send as ReturnType<typeof vi.fn>).mockResolvedValue(mockResponse);

    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));
    const onComplete = vi.fn();

    await act(async () => { await result.current.send("hi", onComplete); });

    expect(messagesApi.send).toHaveBeenCalledWith("sb-1", "sess-1", {
      parts: [{ type: "text", text: "hi" }],
    });
  });

  it("calls onComplete with assembled message after stream ends", async () => {
    const mockReader = {
      read: vi.fn()
        .mockResolvedValueOnce({ done: false, value: new TextEncoder().encode("Hel") })
        .mockResolvedValueOnce({ done: false, value: new TextEncoder().encode("lo") })
        .mockResolvedValueOnce({ done: true, value: undefined }),
    };
    const mockResponse = { body: { getReader: () => mockReader } };
    (messagesApi.send as ReturnType<typeof vi.fn>).mockResolvedValue(mockResponse);

    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));
    const onComplete = vi.fn();

    await act(async () => { await result.current.send("hi", onComplete); });

    expect(onComplete).toHaveBeenCalledWith(expect.objectContaining({
      role: "assistant",
      parts: [{ type: "text", text: "Hello" }],
    }));
  });

  it("sets streaming=true during send and false after", async () => {
    const mockReader = {
      read: vi.fn().mockResolvedValueOnce({ done: true, value: undefined }),
    };
    const mockResponse = { body: { getReader: () => mockReader } };
    (messagesApi.send as ReturnType<typeof vi.fn>).mockResolvedValue(mockResponse);

    const { result } = renderHook(() => useChatStream("sb-1", "sess-1"));

    await act(async () => { await result.current.send("hi", vi.fn()); });
    expect(result.current.streaming).toBe(false);
  });
});
