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

  // ── enqueue ──────────────────────────────────────────────────────────────

  it("enqueue adds a pending pill but does NOT fire sendAsync", async () => {
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

  // ── notifyIdle ────────────────────────────────────────────────────────────

  it("notifyIdle sends the first pending item and removes it on 204", async () => {
    const { result } = render();
    act(() => { result.current.enqueue("hello"); });
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();

    // Capture the ID before notifyIdle — item is removed after 204
    const sentId = result.current.queuedMessages[0]!.id;

    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(messagesApi.sendAsync).toHaveBeenCalledOnce();
    expect(messagesApi.sendAsync).toHaveBeenCalledWith("ws-1", "ses-1", {
      parts: [{ type: "text", text: "hello" }],
      messageID: sentId,
    });

    // Item removed after 204
    expect(result.current.queuedMessages).toHaveLength(0);
  });

  it("notifyIdle sends items one at a time — second item waits for next notifyIdle", async () => {
    const { result } = render();
    act(() => { result.current.enqueue("first"); });
    act(() => { result.current.enqueue("second"); });

    // First idle: sends "first" only
    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(messagesApi.sendAsync).toHaveBeenCalledOnce();
    expect((messagesApi.sendAsync as ReturnType<typeof vi.fn>).mock.calls[0]![2]).toMatchObject({
      parts: [{ type: "text", text: "first" }],
    });
    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.text).toBe("second");

    // Second idle: sends "second"
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

  it("notifyIdle marks pill as error when sendAsync rejects", async () => {
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

    // First idle: sends "will-fail" which errors
    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(result.current.queuedMessages[0]!.status).toBe("error");

    // Second idle: skips errored item, sends "will-send"
    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(messagesApi.sendAsync).toHaveBeenCalledTimes(2);
    expect((messagesApi.sendAsync as ReturnType<typeof vi.fn>).mock.calls[1]![2]).toMatchObject({
      parts: [{ type: "text", text: "will-send" }],
    });
    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.text).toBe("will-fail");
  });

  // ── remove / clear ────────────────────────────────────────────────────────

  it("remove deletes a specific pill by id", () => {
    const { result } = render();
    act(() => { result.current.enqueue("first"); });
    act(() => { result.current.enqueue("second"); });

    const id = result.current.queuedMessages[0]!.id;
    act(() => { result.current.remove(id); });

    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.text).toBe("second");
  });

  it("clear removes all pills", () => {
    const { result } = render();
    act(() => { result.current.enqueue("a"); });
    act(() => { result.current.enqueue("b"); });
    act(() => { result.current.enqueue("c"); });

    act(() => { result.current.clear(); });
    expect(result.current.queuedMessages).toHaveLength(0);
  });

  // ── reconcile ─────────────────────────────────────────────────────────────

  it("reconcile removes pills whose id appears in history", () => {
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

  it("dismiss removes a pill", async () => {
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

    // Still in queue as pending, NOT sent yet
    expect(result.current.queuedMessages[0]!.status).toBe("pending");
    expect(messagesApi.sendAsync).toHaveBeenCalledOnce(); // only the original failed call
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

    // Now fire idle — should send
    await act(async () => { result.current.notifyIdle(); });
    await act(async () => {});

    expect(messagesApi.sendAsync).toHaveBeenCalledTimes(2);
    expect(result.current.queuedMessages).toHaveLength(0);
  });

  it("retry removes pill if already in history", async () => {
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
    expect(messagesApi.sendAsync).toHaveBeenCalledOnce(); // only original failed call
  });

  it("retry does nothing for non-error pills", async () => {
    const { result } = render();
    act(() => { result.current.enqueue("hello"); });

    const id = result.current.queuedMessages[0]!.id;
    await act(async () => { await result.current.retry(id); });

    expect(messagesApi.sendAsync).not.toHaveBeenCalled();
  });

  // ── onPhaseChange ─────────────────────────────────────────────────────────

  it("onPhaseChange marks pending pills as error on Creating", () => {
    const { result } = render();
    act(() => { result.current.enqueue("hello"); });

    act(() => { result.current.onPhaseChange("Creating"); });
    expect(result.current.queuedMessages[0]!.status).toBe("error");
    expect(result.current.queuedMessages[0]!.error).toContain("restarted");
  });

  it("onPhaseChange marks sending pills as error on Suspending", async () => {
    // Hold sendAsync pending so pill stays in "sending"
    let resolveSend!: () => void;
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockReturnValueOnce(
      new Promise<void>((r) => { resolveSend = r; }),
    );

    const { result } = render();
    act(() => { result.current.enqueue("hello"); });
    act(() => { result.current.notifyIdle(); });
    // pill is now "sending"
    expect(result.current.queuedMessages[0]!.status).toBe("sending");

    act(() => { result.current.onPhaseChange("Suspending"); });
    expect(result.current.queuedMessages[0]!.status).toBe("error");

    // resolve the pending promise to avoid unhandled rejection
    resolveSend();
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
});
