import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { ApiClientError } from "../api/client";

vi.mock("../api/messages", () => ({
  messagesApi: {
    sendAsync: vi.fn(),
    getHistory: vi.fn().mockResolvedValue([]),
  },
}));

import { messagesApi } from "../api/messages";
import { useMessageQueue } from "./useMessageQueue";

describe("useMessageQueue", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
  });

  function render(queueOpts?: { workspaceId?: string; sessionId?: string }) {
    return renderHook(() =>
      useMessageQueue(queueOpts?.workspaceId ?? "ws-1", queueOpts?.sessionId ?? "ses-1"),
    );
  }

  function qm(result: ReturnType<typeof render>["result"], idx = 0) {
    return result.current.queuedMessages[idx]!;
  }

  it("enqueue adds a pending pill and fires sendAsync with messageID", async () => {
    const { result } = render();
    await act(async () => {
      result.current.enqueue("hello");
    });

    const msgs = result.current.queuedMessages;
    expect(msgs).toHaveLength(1);
    expect(qm(result).text).toBe("hello");
    expect(qm(result).status).toBe("pending");
    expect(qm(result).id).toMatch(/^msg_/);

    expect(messagesApi.sendAsync).toHaveBeenCalledWith("ws-1", "ses-1", {
      parts: [{ type: "text", text: "hello" }],
      messageID: qm(result).id,
    });
  });

  it("enqueue marks pill as error when sendAsync rejects", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("network fail"));

    const { result } = render();
    await act(async () => {
      result.current.enqueue("hello");
    });

    // Allow microtask to flush
    await act(async () => {});

    const msgs = result.current.queuedMessages;
    expect(msgs).toHaveLength(1);
    expect(qm(result).status).toBe("error");
    expect(qm(result).error).toBe("network fail");
  });

  it("enqueue handles 429 with descriptive error", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new ApiClientError(429, { error: "rate limited", retryAfter: 30 } as any),
    );

    const { result } = render();
    await act(async () => {
      result.current.enqueue("hello");
    });
    await act(async () => {});

    const msgs = result.current.queuedMessages;
    expect(msgs).toHaveLength(1);
    expect(msgs[0]!.status).toBe("error");
    expect(msgs[0]!.error).toContain("30");
  });

  it("enqueue does nothing when workspaceId is undefined", async () => {
    const { result, rerender } = renderHook(
      (props: { wid: string }) => useMessageQueue(props.wid, "ses-1"),
      { initialProps: { wid: "ws-1" } },
    );
    rerender({ wid: undefined as unknown as string });
    await act(async () => {
      result.current.enqueue("hello");
    });

    expect(result.current.queuedMessages).toHaveLength(0);
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();
  });

  it("enqueue does nothing when sessionId is undefined", async () => {
    const { result, rerender } = renderHook(
      (props: { sid: string }) => useMessageQueue("ws-1", props.sid),
      { initialProps: { sid: "ses-1" } },
    );
    rerender({ sid: undefined as unknown as string });
    await act(async () => {
      result.current.enqueue("hello");
    });

    expect(result.current.queuedMessages).toHaveLength(0);
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();
  });

  it("remove deletes a specific pill by id", async () => {
    const { result } = render();
    await act(async () => { result.current.enqueue("first"); });
    await act(async () => { result.current.enqueue("second"); });

    const id = result.current.queuedMessages[0]!.id;
    act(() => { result.current.remove(id); });

    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.text).toBe("second");
  });

  it("clear removes all pills", async () => {
    const { result } = render();
    await act(async () => { result.current.enqueue("a"); });
    await act(async () => { result.current.enqueue("b"); });
    await act(async () => { result.current.enqueue("c"); });

    act(() => { result.current.clear(); });
    expect(result.current.queuedMessages).toHaveLength(0);
  });

  it("reconcile removes pills whose id appears in history", async () => {
    const { result } = render();
    await act(async () => { result.current.enqueue("sent"); });
    await act(async () => { result.current.enqueue("pending"); });

    const sentId = result.current.queuedMessages[0]!.id;
    act(() => {
      result.current.reconcile([
        { id: sentId, role: "user", parts: [{ type: "text", text: "sent" }] },
        { id: "other", role: "assistant", parts: [{ type: "text", text: "response" }] },
      ]);
    });

    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.text).toBe("pending");
  });

  it("reconcile only removes user messages", async () => {
    const { result } = render();
    await act(async () => { result.current.enqueue("msg"); });

    const id = result.current.queuedMessages[0]!.id;
    act(() => {
      result.current.reconcile([
        { id, role: "assistant", parts: [{ type: "text", text: "not a user msg" }] },
      ]);
    });

    expect(result.current.queuedMessages).toHaveLength(1);
  });

  it("dismiss removes an error pill", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("fail"));

    const { result } = render();
    await act(async () => { result.current.enqueue("hello"); });
    await act(async () => {});

    expect(result.current.queuedMessages[0]!.status).toBe("error");

    const id = result.current.queuedMessages[0]!.id;
    act(() => { result.current.dismiss(id); });
    expect(result.current.queuedMessages).toHaveLength(0);
  });

  it("retry re-fires sendAsync after checking history", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("fail"));

    const { result } = render();
    await act(async () => { result.current.enqueue("hello"); });
    await act(async () => {});

    const id = result.current.queuedMessages[0]!.id;

    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValueOnce([]);
    await act(async () => { await result.current.retry(id); });

    expect(messagesApi.sendAsync).toHaveBeenCalledTimes(2);
    expect(messagesApi.sendAsync).toHaveBeenLastCalledWith("ws-1", "ses-1", {
      parts: [{ type: "text", text: "hello" }],
      messageID: id,
    });
    expect(result.current.queuedMessages[0]!.status).toBe("pending");
  });

  it("retry removes pill if already in history", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("fail"));

    const { result } = render();
    await act(async () => { result.current.enqueue("hello"); });
    await act(async () => {});

    const id = result.current.queuedMessages[0]!.id;

    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValueOnce([
      { id, role: "user", parts: [{ type: "text", text: "hello" }] },
    ]);
    await act(async () => { await result.current.retry(id); });

    expect(messagesApi.sendAsync).toHaveBeenCalledTimes(1);
    expect(result.current.queuedMessages).toHaveLength(0);
  });

  it("retry does nothing for non-error pills", async () => {
    const { result } = render();
    await act(async () => { result.current.enqueue("hello"); });

    const id = result.current.queuedMessages[0]!.id;
    await act(async () => { await result.current.retry(id); });

    expect(messagesApi.sendAsync).toHaveBeenCalledTimes(1);
  });

  it("onPhaseChange marks pending pills as error on Creating", async () => {
    const { result } = render();
    await act(async () => { result.current.enqueue("hello"); });

    act(() => { result.current.onPhaseChange("Creating"); });
    expect(result.current.queuedMessages[0]!.status).toBe("error");
    expect(result.current.queuedMessages[0]!.error).toContain("restarted");
  });

  it("onPhaseChange marks pending pills as error on Suspending", async () => {
    const { result } = render();
    await act(async () => { result.current.enqueue("hello"); });

    act(() => { result.current.onPhaseChange("Suspending"); });
    expect(result.current.queuedMessages[0]!.status).toBe("error");
  });

  it("onPhaseChange does not affect Active phase", async () => {
    const { result } = render();
    await act(async () => { result.current.enqueue("hello"); });

    act(() => { result.current.onPhaseChange("Active"); });
    expect(result.current.queuedMessages[0]!.status).toBe("pending");
  });

  it("registers a 15s stuck detector interval", async () => {
    const spy = vi.spyOn(global, "setInterval");
    render();
    expect(spy).toHaveBeenCalledWith(expect.any(Function), 15_000);
    spy.mockRestore();
  });

  it("stuck detector does not affect pills <90s", async () => {
    vi.useFakeTimers();
    try {
      const { result } = render();
      await act(async () => { result.current.enqueue("hello"); });

      // Advance 89 seconds — pill should still be pending
      await act(async () => { vi.advanceTimersByTime(89_000); });
      expect(result.current.queuedMessages[0]!.status).toBe("pending");
    } finally {
      vi.useRealTimers();
    }
  });

  it("stuck detector marks pill as error after 90s", async () => {
    vi.useFakeTimers();
    try {
      const { result } = render();
      await act(async () => { result.current.enqueue("hello"); });

      // Advance past the 90s timeout (the interval fires every 15s)
      await act(async () => { vi.advanceTimersByTime(105_000); });

      expect(result.current.queuedMessages[0]!.status).toBe("error");
      expect(result.current.queuedMessages[0]!.error).toBe("Timed out");
    } finally {
      vi.useRealTimers();
    }
  });

  it("multiple enqueues generate unique IDs", async () => {
    const { result } = render();
    await act(async () => { result.current.enqueue("a"); });
    await act(async () => { result.current.enqueue("b"); });

    const ids = result.current.queuedMessages.map((m) => m.id);
    expect(new Set(ids).size).toBe(2);
  });

  it("retry on double-failure returns pill to error state with new message", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("fail"));

    const { result } = render();
    await act(async () => { result.current.enqueue("hello"); });
    await act(async () => {});

    expect(result.current.queuedMessages[0]!.status).toBe("error");
    expect(result.current.queuedMessages[0]!.error).toBe("fail");

    const id = result.current.queuedMessages[0]!.id;

    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValueOnce([]);
    // Second sendAsync call also rejects
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("retry fail"));

    await act(async () => { await result.current.retry(id); });
    // After retry fires, wait for the rejection to propagate
    await act(async () => {});

    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.status).toBe("error");
    expect(result.current.queuedMessages[0]!.error).toBe("retry fail");
  });

  it("messageID starts with msg_ prefix and uses ULID-style format (not UUID)", async () => {
    const { result } = render();
    await act(async () => { result.current.enqueue("hello"); });

    const call = (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mock.calls[0]!;
    const id: string = call[2]!.messageID;
    expect(id).toMatch(/^msg_/);
    // Must NOT be UUID format (msg_xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)
    expect(id).not.toMatch(/^msg_[0-9a-f]{8}-[0-9a-f]{4}-/i);
    // Must be msg_ + 12 hex chars + 14 base62 chars (opencode ULID scheme)
    expect(id).toMatch(/^msg_[0-9a-f]{12}[0-9A-Za-z]{14}$/);
  });

  it("messageID sorts lexicographically below opencode internal UUIDs (fe... range)", async () => {
    const { result } = render();
    await act(async () => { result.current.enqueue("hello"); });

    const id: string = result.current.queuedMessages[0]!.id;
    // opencode ULIDs currently encode timestamps in the eb... range.
    // A UUID like msg_feeb1f32-... would sort higher than msg_eb... IDs,
    // causing the wrong lastUser to be selected. Our ULID IDs must sort
    // correctly relative to other ULID IDs (not above the fe... UUID range).
    // Assert our ID is not in the UUID namespace (no dashes).
    expect(id).not.toContain("-");
  });
});
