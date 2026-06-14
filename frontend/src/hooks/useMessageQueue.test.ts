import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";

vi.mock("../api/messages", () => ({
  messagesApi: {
    queueMessage: vi.fn(),
    getQueue: vi.fn().mockResolvedValue({ messages: [] }),
    getHistory: vi.fn().mockResolvedValue([]),
    sendAsync: vi.fn(),
  },
}));

import { messagesApi } from "../api/messages";
import { useMessageQueue } from "./useMessageQueue";

function render(workspaceId = "ws-1", sessionId = "ses-1") {
  return renderHook(() => useMessageQueue(workspaceId, sessionId));
}

describe("useMessageQueue (backend-backed)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (messagesApi.queueMessage as ReturnType<typeof vi.fn>).mockResolvedValue({ messageID: "msg_test_1" });
    (messagesApi.getQueue as ReturnType<typeof vi.fn>).mockResolvedValue({ messages: [] });
  });

  it("enqueue calls the backend queue API and adds to display state", async () => {
    const { result } = render();

    await act(async () => { await result.current.enqueue("hello"); });

    expect(messagesApi.queueMessage).toHaveBeenCalledWith("ws-1", "ses-1", "hello");
    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.text).toBe("hello");
    expect(result.current.queuedMessages[0]!.status).toBe("pending");
  });

  it("enqueue on API failure shows error pill", async () => {
    (messagesApi.queueMessage as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("network down"));
    const { result } = render();

    await act(async () => { await result.current.enqueue("will fail"); });

    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.status).toBe("error");
    expect(result.current.queuedMessages[0]!.error).toBe("Failed to queue");
  });

  it("markSent removes the message from display", async () => {
    const { result } = render();

    await act(async () => { await result.current.enqueue("hello"); });
    expect(result.current.queuedMessages).toHaveLength(1);

    act(() => { result.current.markSent("msg_test_1"); });
    expect(result.current.queuedMessages).toHaveLength(0);
  });

  it("markError marks the message as error", async () => {
    const { result } = render();

    await act(async () => { await result.current.enqueue("hello"); });

    act(() => { result.current.markError("msg_test_1", "send failed"); });

    expect(result.current.queuedMessages[0]!.status).toBe("error");
    expect(result.current.queuedMessages[0]!.error).toBe("send failed");
  });

  it("dismiss removes the message", async () => {
    const { result } = render();

    await act(async () => { await result.current.enqueue("hello"); });

    act(() => { result.current.dismiss("msg_test_1"); });
    expect(result.current.queuedMessages).toHaveLength(0);
  });

  it("retry re-enqueues the message text", async () => {
    const { result } = render();

    await act(async () => { await result.current.enqueue("retry me"); });
    act(() => { result.current.markError("msg_test_1", "failed"); });

    (messagesApi.queueMessage as ReturnType<typeof vi.fn>).mockResolvedValue({ messageID: "msg_test_2" });

    await act(async () => { await result.current.retry("msg_test_1"); });

    expect(messagesApi.queueMessage).toHaveBeenCalledTimes(2);
    expect(result.current.queuedMessages[0]!.id).toBe("msg_test_2");
    expect(result.current.queuedMessages[0]!.status).toBe("pending");
  });

  it("hydrates from backend queue on mount", async () => {
    (messagesApi.getQueue as ReturnType<typeof vi.fn>).mockResolvedValue({
      messages: [
        { id: "msg_existing", text: "persisted msg", session_id: "ses-1", workspace_id: "ws-1", enqueued_at: "", retry_count: 0 },
      ],
    });

    const { result } = render();

    await waitFor(() => {
      expect(result.current.queuedMessages).toHaveLength(1);
    });
    expect(result.current.queuedMessages[0]!.text).toBe("persisted msg");
    expect(result.current.queuedMessages[0]!.status).toBe("pending");
  });

  it("onPhaseChange clears queue on restart phases", async () => {
    const { result } = render();

    await act(async () => { await result.current.enqueue("hello"); });

    act(() => { result.current.onPhaseChange("Suspending"); });

    expect(result.current.queuedMessages).toHaveLength(0);
  });

  it("reconcile removes messages matching history IDs", async () => {
    const { result } = render();

    await act(async () => { await result.current.enqueue("hello"); });

    act(() => {
      result.current.reconcile([{ id: "msg_test_1", role: "user", parts: [{ type: "text", text: "hello" }] }] as any);
    });

    expect(result.current.queuedMessages).toHaveLength(0);
  });

  it("messages persist across session changes (display filters, state preserves)", async () => {
    const { result, rerender } = renderHook(
      (props: { sid: string }) => useMessageQueue("ws-1", props.sid),
      { initialProps: { sid: "ses-A" } },
    );

    await act(async () => { await result.current.enqueue("for A"); });
    expect(result.current.queuedMessages).toHaveLength(1);

    rerender({ sid: "ses-B" });
    expect(result.current.queuedMessages).toHaveLength(0);

    rerender({ sid: "ses-A" });
    expect(result.current.queuedMessages).toHaveLength(1);
    expect(result.current.queuedMessages[0]!.text).toBe("for A");
  });
});
