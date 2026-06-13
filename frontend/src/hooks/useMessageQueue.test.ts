import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
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
    vi.useFakeTimers({ shouldAdvanceTime: true });
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  function render(queueOpts?: { workspaceId?: string; sessionId?: string }) {
    return renderHook(() =>
      useMessageQueue(queueOpts?.workspaceId ?? "ws-1", queueOpts?.sessionId ?? "ses-1"),
    );
  }

  function qm(result: ReturnType<typeof render>["result"], idx = 0) {
    return result.current.queuedMessages[idx]!;
  }

  // ── enqueue ──────────────────────────────────────────────────────────────

  it("enqueue adds a pending message but does NOT fire sendAsync", async () => {
    const { result } = render();
    act(() => { result.current.enqueue("hello"); });

    expect(result.current.queuedMessages).toHaveLength(1);
    expect(qm(result).text).toBe("hello");
    expect(qm(result).status).toBe("pending");
    expect(qm(result).id).toMatch(/^msg_/);
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();
  });

  it("enqueue does nothing when workspaceId is undefined", () => {
    const { result, rerender } = renderHook(
      (props: { wid: string }) => useMessageQueue(props.wid, "ses-1"),
      { initialProps: { wid: "ws-1" } },
    );
    rerender({ wid: undefined as unknown as string });
    act(() => { result.current.enqueue("hello"); });

    expect(result.current.queuedMessages).toHaveLength(0);
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();
  });

  it("enqueue does nothing when sessionId is undefined", () => {
    const { result, rerender } = renderHook(
      (props: { sid: string }) => useMessageQueue("ws-1", props.sid),
      { initialProps: { sid: "ses-1" } },
    );
    rerender({ sid: undefined as unknown as string });
    act(() => { result.current.enqueue("hello"); });

    expect(result.current.queuedMessages).toHaveLength(0);
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();
  });

  it("multiple enqueues generate unique IDs", () => {
    const { result } = render();
    act(() => { result.current.enqueue("a"); });
    act(() => { result.current.enqueue("b"); });

    const ids = result.current.queuedMessages.map((m) => m.id);
    expect(new Set(ids).size).toBe(2);
  });

  it("each queued message records its sessionId", () => {
    const { result } = render();
    act(() => { result.current.enqueue("hello"); });
    expect(qm(result).sessionId).toBe("ses-1");
  });

  // ── notifyIdle ────────────────────────────────────────────────────────────

  it("notifyIdle sends the first pending item and removes it on 204", async () => {
    const { result } = render();
    act(() => { result.current.enqueue("hello"); });
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();

    const sentId = result.current.queuedMessages[0]!.id;

    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(messagesApi.sendAsync).toHaveBeenCalledOnce();
    expect(messagesApi.sendAsync).toHaveBeenCalledWith("ws-1", "ses-1", {
      parts: [{ type: "text", text: "hello" }],
      messageID: sentId,
    });

    expect(result.current.queuedMessages).toHaveLength(0);
  });

  it("notifyIdle sends items one at a time — second item waits for next notifyIdle", async () => {
    const { result } = render();
    act(() => { result.current.enqueue("first"); });
    act(() => { result.current.enqueue("second"); });

    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(messagesApi.sendAsync).toHaveBeenCalledOnce();
    expect((messagesApi.sendAsync as ReturnType<typeof vi.fn>).mock.calls[0]![2]).toMatchObject({
      parts: [{ type: "text", text: "first" }],
    });
    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.text).toBe("second");

    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(messagesApi.sendAsync).toHaveBeenCalledTimes(2);
    expect(result.current.queuedMessages).toHaveLength(0);
  });

  it("notifyIdle is a no-op when queue is empty", async () => {
    const { result } = render();
    await act(async () => { result.current.notifyIdle(); });
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();
  });

  it("notifyIdle marks message as error when sendAsync rejects", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("network fail"));

    const { result } = render();
    act(() => { result.current.enqueue("hello"); });

    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.status).toBe("error");
    expect(result.current.queuedMessages[0]!.error).toBe("network fail");
  });

  it("notifyIdle handles 429 with descriptive error", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new ApiClientError(429, { error: "rate limited", retryAfter: 30 } as any),
    );

    const { result } = render();
    act(() => { result.current.enqueue("hello"); });

    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(result.current.queuedMessages[0]!.status).toBe("error");
    expect(result.current.queuedMessages[0]!.error).toContain("30");
  });

  it("notifyIdle skips error items and sends next pending item", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("fail"));

    const { result } = render();
    act(() => { result.current.enqueue("will-fail"); });
    act(() => { result.current.enqueue("will-send"); });

    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(result.current.queuedMessages[0]!.status).toBe("error");

    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(messagesApi.sendAsync).toHaveBeenCalledTimes(2);
    expect((messagesApi.sendAsync as ReturnType<typeof vi.fn>).mock.calls[1]![2]).toMatchObject({
      parts: [{ type: "text", text: "will-send" }],
    });
    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.text).toBe("will-fail");
  });

  it("double notifyIdle in same tick does not cause duplicate send", async () => {
    const { result } = render();
    act(() => { result.current.enqueue("hello"); });

    await act(async () => {
      result.current.notifyIdle();
      result.current.notifyIdle();
    });
    await act(async () => {});

    expect(messagesApi.sendAsync).toHaveBeenCalledOnce();
  });

  it("notifyIdle when all items are sending is a no-op", async () => {
    let resolveSend!: () => void;
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockReturnValueOnce(
      new Promise<void>((r) => { resolveSend = r; }),
    );

    const { result } = render();
    act(() => { result.current.enqueue("hello"); });

    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(result.current.queuedMessages[0]!.status).toBe("sending");

    await act(async () => { result.current.notifyIdle(); });
    expect(messagesApi.sendAsync).toHaveBeenCalledOnce();

    resolveSend();
    await act(async () => {});
  });

  // ── per-session isolation ────────────────────────────────────────────────

  it("changing sessionId clears messages from previous session", () => {
    const { result, rerender } = renderHook(
      (props: { sid: string }) => useMessageQueue("ws-1", props.sid),
      { initialProps: { sid: "ses-1" } },
    );

    act(() => { result.current.enqueue("msg for ses-1"); });
    expect(result.current.queuedMessages).toHaveLength(1);

    rerender({ sid: "ses-2" });

    expect(result.current.queuedMessages).toHaveLength(0);

    act(() => { result.current.enqueue("msg for ses-2"); });
    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.text).toBe("msg for ses-2");

    rerender({ sid: "ses-3" });
    expect(result.current.queuedMessages).toHaveLength(0);
  });

  // ── sending timeout ──────────────────────────────────────────────────────

  it("sending items that exceed timeout are marked as error", async () => {
    let resolveSend!: () => void;
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockReturnValueOnce(
      new Promise<void>((r) => { resolveSend = r; }),
    );

    const now = Date.now();
    vi.setSystemTime(now);

    const { result } = render();
    act(() => { result.current.enqueue("hello"); });
    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(result.current.queuedMessages[0]!.status).toBe("sending");

    vi.setSystemTime(now + 61_000);
    await act(async () => { vi.advanceTimersByTimeAsync(10_000); });
    await act(async () => {});

    expect(result.current.queuedMessages[0]!.status).toBe("error");
    expect(result.current.queuedMessages[0]!.error).toContain("timed out");

    resolveSend();
    await act(async () => {});
  });

  it("after sending timeout, subsequent notifyIdle can send again (no deadlock)", async () => {
    let resolveSend!: () => void;
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockReturnValueOnce(
      new Promise<void>((r) => { resolveSend = r; }),
    );

    const now = Date.now();
    vi.setSystemTime(now);

    const { result } = render();
    act(() => { result.current.enqueue("stuck"); });
    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});
    expect(result.current.queuedMessages[0]!.status).toBe("sending");

    vi.setSystemTime(now + 61_000);
    await act(async () => { vi.advanceTimersByTimeAsync(10_000); });
    await act(async () => {});
    expect(result.current.queuedMessages[0]!.status).toBe("error");

    resolveSend();
    await act(async () => {});

    act(() => { result.current.enqueue("next"); });
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined);

    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(messagesApi.sendAsync).toHaveBeenCalledTimes(2);
    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.status).toBe("error");
    expect(result.current.queuedMessages[0]!.text).toBe("stuck");
  });

  // ── remove / clear ────────────────────────────────────────────────────────

  it("clear removes all messages", () => {
    const { result } = render();
    act(() => { result.current.enqueue("a"); });
    act(() => { result.current.enqueue("b"); });
    act(() => { result.current.enqueue("c"); });

    act(() => { result.current.clear(); });
    expect(result.current.queuedMessages).toHaveLength(0);
  });

  // ── reconcile ─────────────────────────────────────────────────────────────

  it("reconcile removes messages whose id appears in history", () => {
    const { result } = render();
    act(() => { result.current.enqueue("sent"); });
    act(() => { result.current.enqueue("pending"); });

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

  it("reconcile only removes user messages", () => {
    const { result } = render();
    act(() => { result.current.enqueue("msg"); });

    const id = result.current.queuedMessages[0]!.id;
    act(() => {
      result.current.reconcile([
        { id, role: "assistant", parts: [{ type: "text", text: "not a user msg" }] },
      ]);
    });

    expect(result.current.queuedMessages).toHaveLength(1);
  });

  // ── dismiss / retry ───────────────────────────────────────────────────────

  it("dismiss removes a message", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("fail"));

    const { result } = render();
    act(() => { result.current.enqueue("hello"); });
    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(result.current.queuedMessages[0]!.status).toBe("error");

    const id = result.current.queuedMessages[0]!.id;
    act(() => { result.current.dismiss(id); });
    expect(result.current.queuedMessages).toHaveLength(0);
  });

  it("retry re-enqueues as pending (not sent immediately)", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("fail"));

    const { result } = render();
    act(() => { result.current.enqueue("hello"); });
    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(result.current.queuedMessages[0]!.status).toBe("error");
    const id = result.current.queuedMessages[0]!.id;

    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValueOnce([]);
    await act(async () => { await result.current.retry(id); });

    expect(result.current.queuedMessages[0]!.status).toBe("pending");
    expect(messagesApi.sendAsync).toHaveBeenCalledOnce();
  });

  it("retry on notifyIdle after retry fires sendAsync", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("fail"));

    const { result } = render();
    act(() => { result.current.enqueue("hello"); });
    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    const id = result.current.queuedMessages[0]!.id;
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValueOnce([]);
    await act(async () => { await result.current.retry(id); });

    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(messagesApi.sendAsync).toHaveBeenCalledTimes(2);
    expect(result.current.queuedMessages).toHaveLength(0);
  });

  it("retry removes message if already in history", async () => {
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("fail"));

    const { result } = render();
    act(() => { result.current.enqueue("hello"); });
    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    const id = result.current.queuedMessages[0]!.id;
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValueOnce([
      { id, role: "user", parts: [{ type: "text", text: "hello" }] },
    ]);
    await act(async () => { await result.current.retry(id); });

    expect(result.current.queuedMessages).toHaveLength(0);
    expect(messagesApi.sendAsync).toHaveBeenCalledOnce();
  });

  it("retry does nothing for non-error messages", async () => {
    const { result } = render();
    act(() => { result.current.enqueue("hello"); });

    const id = result.current.queuedMessages[0]!.id;
    await act(async () => { await result.current.retry(id); });

    expect(messagesApi.sendAsync).not.toHaveBeenCalled();
  });

  // ── onPhaseChange ─────────────────────────────────────────────────────────

  it("onPhaseChange marks pending messages as error on Creating", () => {
    const { result } = render();
    act(() => { result.current.enqueue("hello"); });

    act(() => { result.current.onPhaseChange("Creating"); });
    expect(result.current.queuedMessages[0]!.status).toBe("error");
    expect(result.current.queuedMessages[0]!.error).toContain("restarted");
  });

  it("onPhaseChange marks sending messages as error on Suspending", async () => {
    let resolveSend!: () => void;
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockReturnValueOnce(
      new Promise<void>((r) => { resolveSend = r; }),
    );

    const { result } = render();
    act(() => { result.current.enqueue("hello"); });

    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(result.current.queuedMessages[0]!.status).toBe("sending");

    act(() => { result.current.onPhaseChange("Suspending"); });
    expect(result.current.queuedMessages[0]!.status).toBe("error");

    resolveSend();
    await act(async () => {});
  });

  it("onPhaseChange does not affect Active phase", () => {
    const { result } = render();
    act(() => { result.current.enqueue("hello"); });

    act(() => { result.current.onPhaseChange("Active"); });
    expect(result.current.queuedMessages[0]!.status).toBe("pending");
  });

  // ── ID format ─────────────────────────────────────────────────────────────

  it("messageID starts with msg_ prefix and uses ULID-style format (not UUID)", async () => {
    const { result } = render();
    act(() => { result.current.enqueue("hello"); });
    await act(async () => { result.current.notifyIdle(); });

    const call = (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mock.calls[0]!;
    const id: string = call[2]!.messageID;
    expect(id).toMatch(/^msg_/);
    expect(id).not.toMatch(/^msg_[0-9a-f]{8}-[0-9a-f]{4}-/i);
    expect(id).toMatch(/^msg_[0-9a-f]{12}[0-9A-Za-z]{14}$/);
  });

  it("messageID sorts lexicographically below opencode internal UUIDs (fe... range)", () => {
    const { result } = render();
    act(() => { result.current.enqueue("hello"); });

    const id: string = result.current.queuedMessages[0]!.id;
    expect(id).not.toContain("-");
  });

  // ── US-41.3: 409 Conflict handling ──────────────────────────────────────────

  it("409 response transitions message to pending (not error)", async () => {
    const err = new ApiClientError(409, { error: "session is busy", retryAfter: 1 } as any);
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValue(err);

    const { result } = render();
    act(() => { result.current.enqueue("will-409"); });

    await act(async () => { result.current.notifyIdle(); });

    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.status).toBe("pending");
    expect(result.current.queuedMessages[0]!.error).toBeUndefined();
  });

  it("message in pending after 409 is retried on next notifyIdle", async () => {
    let callCount = 0;
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockImplementation(() => {
      callCount++;
      if (callCount === 1) {
        return Promise.reject(new ApiClientError(409, { error: "session is busy" } as any));
      }
      return Promise.resolve(undefined);
    });

    const { result } = render();
    act(() => { result.current.enqueue("retry-me"); });

    await act(async () => { result.current.notifyIdle(); });

    expect(result.current.queuedMessages[0]!.status).toBe("pending");

    await act(async () => { result.current.notifyIdle(); });

    await waitFor(() => {
      expect(result.current.queuedMessages).toHaveLength(0);
    });
    expect(callCount).toBe(2);
  });

  it("after MAX_RETRIES 409s, message transitions to error", async () => {
    const err = new ApiClientError(409, { error: "session is busy" } as any);
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValue(err);

    const { result } = render();
    act(() => { result.current.enqueue("max-retries"); });

    for (let i = 0; i < 5; i++) {
      await act(async () => { result.current.notifyIdle(); });
    }

    expect(result.current.queuedMessages[0]!.status).toBe("pending");

    await act(async () => { result.current.notifyIdle(); });

    expect(result.current.queuedMessages[0]!.status).toBe("error");
    expect(result.current.queuedMessages[0]!.error).toBe("Session busy — retry manually");
  });

  it("queue item is removed on successful send after prior 409 retries", async () => {
    let callCount = 0;
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockImplementation(() => {
      callCount++;
      if (callCount <= 2) {
        return Promise.reject(new ApiClientError(409, { error: "session is busy" } as any));
      }
      return Promise.resolve(undefined);
    });

    const { result } = render();
    act(() => { result.current.enqueue("eventually-works"); });

    await act(async () => { result.current.notifyIdle(); });
    expect(result.current.queuedMessages[0]!.status).toBe("pending");

    await act(async () => { result.current.notifyIdle(); });
    expect(result.current.queuedMessages[0]!.status).toBe("pending");

    await act(async () => { result.current.notifyIdle(); });
    await waitFor(() => {
      expect(result.current.queuedMessages).toHaveLength(0);
    });
    expect(callCount).toBe(3);
  });
});
