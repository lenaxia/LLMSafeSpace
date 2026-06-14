/**
 * Tests for Epic 15: Streaming State Resilience & Mid-Stream Reconnect.
 * Covers US-15.1 through US-15.5.
 */
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, waitFor, act, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChatPage } from "./ChatPage";
import type { WorkspaceStreamEvent, SessionStatusEvent } from "../api/types";

// --- Mocks ---

vi.mock("../api/workspaces", () => ({
  workspacesApi: {
    getStatus: vi.fn(),
    activate: vi.fn(),
    list: vi.fn().mockResolvedValue({ items: [], pagination: { limit: 20, offset: 0, total: 0 } }),
    renameWorkspace: vi.fn().mockResolvedValue(undefined),
    renameSession: vi.fn().mockResolvedValue(undefined),
    markSessionSeen: vi.fn().mockResolvedValue(undefined),
    getSessions: vi.fn().mockResolvedValue([]),
  },
}));
vi.mock("../providers/SessionActivityProvider", () => ({
  useClearPendingUnread: () => () => {},
  useIsSessionBusy: () => false,
  useIsSessionUnread: () => false,
  useWorkspaceBusyCount: () => 0,
  useIsSessionPendingAction: () => false,
  useSessionPendingActions: () => new Set<string>(),
  useAddPendingAction: () => () => {},
  useRemovePendingAction: () => () => {},
  SessionActivityProvider: ({ children }: { children: any }) => <>{children}</>,
}));
vi.mock("../api/messages", () => {
  const gh = vi.fn().mockResolvedValue([]);
  return {
    messagesApi: {
      getHistory: gh,
      getHistoryPage: vi.fn().mockImplementation(async () => {
        const msgs = await gh();
        return { messages: msgs, nextCursor: undefined };
      }),
      sendAsync: vi.fn(), queueMessage: vi.fn().mockResolvedValue({ messageID: "msg_q_mock" }), getQueue: vi.fn().mockResolvedValue({ messages: [] }).mockResolvedValue(undefined),
    },
  };
});
vi.mock("../api/sessions", () => ({ sessionsApi: { create: vi.fn() } }));

// Capture the SSE handler and onReconnect callback
let capturedSSEHandler: ((data: unknown) => void) | null = null;
let capturedOnReconnect: (() => void) | null = null;
vi.mock("../hooks/useEventStream", () => ({
  useEventStream: vi.fn((_workspaceId: string | undefined, handler: (data: unknown) => void, options?: { onReconnect?: () => void }) => {
    capturedSSEHandler = handler;
    capturedOnReconnect = options?.onReconnect ?? null;
  }),
}));

// Mock ChatView to expose streaming state and parts
vi.mock("../components/chat/ChatView", () => ({
  ChatView: (props: Record<string, unknown>) => {
    return (
      <div
        data-testid="chat-view"
        data-stream-parts={JSON.stringify(props.streamParts ?? [])}
        data-streaming={String(props.streaming ?? false)}
        data-messages={JSON.stringify(props.messages ?? [])}
      >
        <textarea
          disabled={props.disabled as boolean}
          onChange={() => {}}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              (props.onSend as (t: string) => void)((e.target as HTMLTextAreaElement).value);
            }
          }}
        />
      </div>
    );
  },
}));

import { workspacesApi } from "../api/workspaces";
import { messagesApi } from "../api/messages";

// --- Helpers ---

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  });
}

function renderChat(qc: QueryClient, path: string) {
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/chat/:workspaceId/:sessionId" element={<ChatPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function sendSSEEvent(event: WorkspaceStreamEvent) {
  act(() => { capturedSSEHandler?.(event); });
}

function triggerReconnect() {
  act(() => { capturedOnReconnect?.(); });
}

function getStreamingState(): boolean {
  const el = screen.getByTestId("chat-view");
  return el.getAttribute("data-streaming") === "true";
}

function getStreamParts(): Array<{ type: string; text: string }> {
  const el = screen.getByTestId("chat-view");
  return JSON.parse(el.getAttribute("data-stream-parts") || "[]");
}

function getRenderedMessages(): Array<{ id: string; role: string; parts: Array<{ id?: string; type: string; text?: string }> }> {
  const el = screen.getByTestId("chat-view");
  return JSON.parse(el.getAttribute("data-messages") || "[]");
}

function makeSessionStatusEvent(sessionId: string, status: "idle" | "busy"): SessionStatusEvent {
  return { type: "session.status", session_id: sessionId, status };
}

function makePartUpdatedWithId(sessionId: string, partId: string, text: string): WorkspaceStreamEvent {
  return {
    type: "opencode.event",
    event_type: "message.part.updated",
    data: {
      type: "message.part.updated",
      properties: {
        sessionID: sessionId,
        part: { id: partId, type: "text", text },
      },
    },
  } as unknown as WorkspaceStreamEvent;
}

function makePartDeltaWithId(sessionId: string, partId: string, delta: string): WorkspaceStreamEvent {
  return {
    type: "opencode.event",
    event_type: "message.part.delta",
    data: {
      type: "message.part.delta",
      properties: {
        sessionID: sessionId,
        messageID: "msg-2",
        partID: partId,
        field: "text",
        delta,
      },
    },
  } as unknown as WorkspaceStreamEvent;
}

// --- Test Groups ---

describe("US-15.1 + US-15.2: Status-Driven Streaming Indicator", () => {
  let qc: QueryClient;

  beforeEach(() => {
    vi.clearAllMocks();
    capturedSSEHandler = null;
    capturedOnReconnect = null;
    qc = makeQueryClient();
  });

  it("shows streaming indicator when session is busy on mount", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "busy" }],
    });

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(getStreamingState()).toBe(true);
    });
  });

  it("no streaming indicator when session is idle on mount", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "idle" }],
    });

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(screen.getByTestId("chat-view")).toBeInTheDocument();
    });
    expect(getStreamingState()).toBe(false);
  });

  it("no streaming indicator when sessions array is empty", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [],
    });

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(screen.getByTestId("chat-view")).toBeInTheDocument();
    });
    expect(getStreamingState()).toBe(false);
  });

  it("SSE busy event shows streaming indicator", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "idle" }],
    });

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(screen.getByTestId("chat-view")).toBeInTheDocument();
    });
    expect(getStreamingState()).toBe(false);

    sendSSEEvent(makeSessionStatusEvent("sess-1", "busy"));

    await waitFor(() => {
      expect(getStreamingState()).toBe(true);
    });
  });

  it("SSE idle event hides streaming indicator", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "busy" }],
    });

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(getStreamingState()).toBe(true);
    });

    sendSSEEvent(makeSessionStatusEvent("sess-1", "idle"));

    await waitFor(() => {
      expect(getStreamingState()).toBe(false);
    });
  });

  it("SSE busy for different session is ignored", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "idle" }],
    });

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(screen.getByTestId("chat-view")).toBeInTheDocument();
    });

    sendSSEEvent(makeSessionStatusEvent("sess-other", "busy"));

    // Should still be false
    expect(getStreamingState()).toBe(false);
  });

  it("SSE reconnect triggers status re-poll to catch missed transitions", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce({
        phase: "Active",
        sessions: [{ id: "sess-1", status: "busy" }],
      })
      .mockResolvedValueOnce({
        phase: "Active",
        sessions: [{ id: "sess-1", status: "idle" }],
      });

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(getStreamingState()).toBe(true);
    });

    // Simulate SSE reconnect
    triggerReconnect();

    // After reconnect, status is re-polled and returns idle
    await waitFor(() => {
      expect(getStreamingState()).toBe(false);
    });
  });
});

describe("US-15.3: History Fetch on Busy Reconnect", () => {
  let qc: QueryClient;

  beforeEach(() => {
    vi.clearAllMocks();
    capturedSSEHandler = null;
    capturedOnReconnect = null;
    qc = makeQueryClient();
  });

  it("fetches and renders history when mounting into busy session", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "busy" }],
    });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "msg-2", role: "assistant", parts: [{ id: "p2", type: "text", text: "Hi there" }] },
      { id: "msg-1", role: "user", parts: [{ id: "p1", type: "text", text: "Hello" }] },
    ]);

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      const msgs = getRenderedMessages();
      expect(msgs).toHaveLength(2);
      expect(msgs[0]!.parts[0]!.text).toBe("Hello");
      expect(msgs[1]!.parts[0]!.text).toBe("Hi there");
    });
  });

  it("computes historyPartIds from fetched history", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "busy" }],
    });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "msg-1", role: "assistant", parts: [{ id: "p1", type: "text", text: "existing" }] },
    ]);

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(getRenderedMessages()).toHaveLength(1);
    });

    // Send an SSE event for a part already in history — should be ignored
    sendSSEEvent(makePartUpdatedWithId("sess-1", "p1", "existing updated"));

    // No new stream parts should appear
    expect(getStreamParts()).toHaveLength(0);
  });
});

describe("US-15.4: Boundary Detection", () => {
  let qc: QueryClient;

  beforeEach(() => {
    vi.clearAllMocks();
    capturedSSEHandler = null;
    capturedOnReconnect = null;
    qc = makeQueryClient();
  });

  it("ignores SSE event for part already in history", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "busy" }],
    });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "msg-1", role: "assistant", parts: [{ id: "p1", type: "text", text: "old text" }] },
    ]);

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(getRenderedMessages()).toHaveLength(1);
    });

    sendSSEEvent(makePartUpdatedWithId("sess-1", "p1", "old text updated"));
    expect(getStreamParts()).toHaveLength(0);
  });

  it("renders new part not in history", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "busy" }],
    });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "msg-1", role: "assistant", parts: [{ id: "p1", type: "text", text: "old" }] },
    ]);

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(getRenderedMessages()).toHaveLength(1);
    });

    sendSSEEvent(makePartUpdatedWithId("sess-1", "p2", "new content"));

    await waitFor(() => {
      const parts = getStreamParts();
      expect(parts).toHaveLength(1);
      expect(parts[0]!.text).toBe("new content");
    });
  });

  it("ignores delta for history part", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "busy" }],
    });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "msg-1", role: "assistant", parts: [{ id: "p1", type: "text", text: "old" }] },
    ]);

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(getRenderedMessages()).toHaveLength(1);
    });

    sendSSEEvent(makePartDeltaWithId("sess-1", "p1", " more text"));
    expect(getStreamParts()).toHaveLength(0);
  });

  it("appends delta for live part", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "busy" }],
    });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "msg-1", role: "assistant", parts: [{ id: "p1", type: "text", text: "old" }] },
    ]);

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(getRenderedMessages()).toHaveLength(1);
    });

    // First: new part arrives
    sendSSEEvent(makePartUpdatedWithId("sess-1", "p2", "start"));

    await waitFor(() => {
      expect(getStreamParts()).toHaveLength(1);
    });

    // Then: delta for that new part
    sendSSEEvent(makePartDeltaWithId("sess-1", "p2", " appended"));

    await waitFor(() => {
      const parts = getStreamParts();
      expect(parts[0]!.text).toBe("start appended");
    });
  });

  it("ignores orphan delta (unknown partID)", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "busy" }],
    });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(screen.getByTestId("chat-view")).toBeInTheDocument();
    });

    // Delta with no prior updated event — should be ignored
    sendSSEEvent(makePartDeltaWithId("sess-1", "unknown-part", "orphan"));
    expect(getStreamParts()).toHaveLength(0);
  });

  it("boundary gate inactive during normal send flow", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "idle" }],
    });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(screen.getByTestId("chat-view")).toBeInTheDocument();
    });

    // In normal (non-reconnect) mode, events should process normally
    // Send a text part event — should render (no gate active)
    sendSSEEvent({
      type: "opencode.event",
      event_type: "message.part.updated",
      data: {
        type: "message.part.updated",
        properties: {
          sessionID: "sess-1",
          part: { type: "text", text: "hello world" },
        },
      },
    } as unknown as WorkspaceStreamEvent);

    await waitFor(() => {
      const parts = getStreamParts();
      expect(parts.length).toBeGreaterThan(0);
    });
  });
});

describe("US-15.5: Idle Reconciliation", () => {
  let qc: QueryClient;

  beforeEach(() => {
    vi.clearAllMocks();
    capturedSSEHandler = null;
    capturedOnReconnect = null;
    qc = makeQueryClient();
  });

  it("idle event triggers history refetch and clears streaming parts", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "busy" }],
    });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce([
        { id: "msg-1", role: "assistant", parts: [{ id: "p1", type: "text", text: "partial" }] },
      ])
      .mockResolvedValueOnce([
        { id: "msg-1", role: "assistant", parts: [{ id: "p1", type: "text", text: "partial" }] },
        { id: "msg-2", role: "assistant", parts: [{ id: "p2", type: "text", text: "complete response" }] },
      ]);

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(getRenderedMessages()).toHaveLength(1);
    });

    // Simulate a new part streaming in
    sendSSEEvent(makePartUpdatedWithId("sess-1", "p2", "streaming..."));

    await waitFor(() => {
      expect(getStreamParts()).toHaveLength(1);
    });

    // Now idle arrives — should reconcile
    sendSSEEvent(makeSessionStatusEvent("sess-1", "idle"));

    await waitFor(() => {
      // Stream parts should be cleared
      expect(getStreamParts()).toHaveLength(0);
      // History should now have 2 messages (refetched)
      expect(getRenderedMessages()).toHaveLength(2);
    });
  });

  it("reconciliation resets reconnect mode — subsequent events not gated", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce({
        phase: "Active",
        sessions: [{ id: "sess-1", status: "busy" }],
      })
      .mockResolvedValue({
        phase: "Active",
        sessions: [{ id: "sess-1", status: "idle" }],
      });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce([
        { id: "msg-1", role: "assistant", parts: [{ id: "p1", type: "text", text: "done" }] },
      ])
      .mockResolvedValue([
        { id: "msg-1", role: "assistant", parts: [{ id: "p1", type: "text", text: "done" }] },
      ]);

    renderChat(qc, "/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(getStreamingState()).toBe(true);
    });

    // Idle arrives — reconcile
    sendSSEEvent(makeSessionStatusEvent("sess-1", "idle"));

    await waitFor(() => {
      expect(getStreamingState()).toBe(false);
    });

    // Now send a new event with part id "p1" (same as history) — after reconcile,
    // reconnect mode is off, so events with no part.id should still process normally
    sendSSEEvent({
      type: "opencode.event",
      event_type: "message.part.updated",
      data: {
        type: "message.part.updated",
        properties: {
          sessionID: "sess-1",
          part: { type: "text", text: "new after reconcile" },
        },
      },
    } as unknown as WorkspaceStreamEvent);

    await waitFor(() => {
      const parts = getStreamParts();
      expect(parts.length).toBeGreaterThan(0);
      expect(parts[0]!.text).toBe("new after reconcile");
    });
  });
});

describe("US-15.6: Full Flow Integration", () => {
  let qc: QueryClient;

  beforeEach(() => {
    vi.clearAllMocks();
    capturedSSEHandler = null;
    capturedOnReconnect = null;
    qc = makeQueryClient();
  });

  it("full reconnect flow: mount busy → history → new parts → idle → reconcile", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      phase: "Active",
      sessions: [{ id: "sess-1", status: "busy" }],
    });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce([
        { id: "msg-1", role: "user", parts: [{ id: "up1", type: "text", text: "question" }] },
        { id: "msg-2", role: "assistant", parts: [{ id: "ap1", type: "text", text: "partial answer" }] },
      ])
      .mockResolvedValueOnce([
        { id: "msg-1", role: "user", parts: [{ id: "up1", type: "text", text: "question" }] },
        { id: "msg-2", role: "assistant", parts: [{ id: "ap1", type: "text", text: "partial answer" }] },
        { id: "msg-3", role: "assistant", parts: [{ id: "ap2", type: "text", text: "full new response" }] },
      ]);

    renderChat(qc, "/chat/ws-1/sess-1");

    // 1. History renders
    await waitFor(() => {
      expect(getRenderedMessages()).toHaveLength(2);
    });

    // 2. Streaming indicator shows
    expect(getStreamingState()).toBe(true);

    // 3. New part arrives (not in history)
    sendSSEEvent(makePartUpdatedWithId("sess-1", "ap2", "streaming new"));

    await waitFor(() => {
      expect(getStreamParts()).toHaveLength(1);
      expect(getStreamParts()[0]!.text).toBe("streaming new");
    });

    // 4. Idle arrives — reconcile
    sendSSEEvent(makeSessionStatusEvent("sess-1", "idle"));

    await waitFor(() => {
      // Stream parts cleared
      expect(getStreamParts()).toHaveLength(0);
      // History now has 3 messages
      expect(getRenderedMessages()).toHaveLength(3);
      // Streaming indicator gone
      expect(getStreamingState()).toBe(false);
    });
  });
});

// ---------------------------------------------------------------------------
// Auto-abort stuck question/permission sessions on reconnect
// ---------------------------------------------------------------------------
describe("ChatPage auto-abort stuck input sessions", () => {
  beforeEach(() => {
    capturedSSEHandler = null;
    capturedOnReconnect = null;
    vi.clearAllMocks();
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    (workspacesApi.list as ReturnType<typeof vi.fn>).mockResolvedValue({
      items: [], pagination: { limit: 20, offset: 0, total: 0 },
    });
  });

  it("auto-aborts and shows interrupted banner when reconnecting to session stuck on question tool", async () => {
    // History has a question tool in "running" state — opencode restarted and
    // lost the question from its queue. On reconnect we should auto-abort and
    // show the interrupted banner without requiring user action.
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "msg-user", role: "user", parts: [{ type: "text", text: "push to github" }] },
      {
        id: "msg-asst",
        role: "assistant",
        parts: [
          { type: "tool_use", text: "question: GitHub auth required", toolState: "running" },
        ],
      },
    ]);
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValue({
      messages: [
        { id: "msg-user", role: "user", parts: [{ type: "text", text: "push to github" }] },
        {
          id: "msg-asst",
          role: "assistant",
          parts: [{ type: "tool_use", text: "question: GitHub auth required", toolState: "running" }],
        },
      ],
      nextCursor: undefined,
    });
    (workspacesApi as Record<string, unknown>).abortSession = vi.fn().mockResolvedValue(undefined);

    const qc = makeQueryClient();
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={["/chat/ws-1/sess-stuck"]}>
          <Routes>
            <Route path="/chat/:workspaceId/:sessionId" element={<ChatPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

    // Drive serverBusy=true (session was busy when we loaded)
    act(() => {
      capturedSSEHandler!({ type: "session.status", session_id: "sess-stuck", status: "busy" });
    });

    // After history loads with the stuck question tool, abort should be called
    await waitFor(
      () => expect((workspacesApi as Record<string, unknown>).abortSession)
        .toHaveBeenCalledWith("ws-1", "sess-stuck"),
      { timeout: 3000 },
    );

    // Interrupted banner should be visible
    await waitFor(() =>
      expect(screen.getByText(/session was interrupted/i)).toBeInTheDocument(),
    );
  });

  it("auto-aborts when last tool is permission (not just question)", async () => {
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "msg-user", role: "user", parts: [{ type: "text", text: "run deploy.sh" }] },
      {
        id: "msg-asst",
        role: "assistant",
        parts: [{ type: "tool_use", text: "permission: shell", toolState: "running" }],
      },
    ]);
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValue({
      messages: [
        { id: "msg-user", role: "user", parts: [{ type: "text", text: "run deploy.sh" }] },
        { id: "msg-asst", role: "assistant", parts: [{ type: "tool_use", text: "permission: shell", toolState: "running" }] },
      ],
      nextCursor: undefined,
    });
    (workspacesApi as Record<string, unknown>).abortSession = vi.fn().mockResolvedValue(undefined);

    const qc = makeQueryClient();
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={["/chat/ws-1/sess-perm"]}>
          <Routes>
            <Route path="/chat/:workspaceId/:sessionId" element={<ChatPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
    act(() => {
      capturedSSEHandler!({ type: "session.status", session_id: "sess-perm", status: "busy" });
    });

    await waitFor(
      () => expect((workspacesApi as Record<string, unknown>).abortSession)
        .toHaveBeenCalledWith("ws-1", "sess-perm"),
      { timeout: 3000 },
    );
    await waitFor(() =>
      expect(screen.getByText(/session was interrupted/i)).toBeInTheDocument(),
    );
  });

  it("abort failure still shows interrupted banner and reconciles history", async () => {
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "msg-user", role: "user", parts: [{ type: "text", text: "push code" }] },
      {
        id: "msg-asst",
        role: "assistant",
        parts: [{ type: "tool_use", text: "question: GitHub auth required", toolState: "running" }],
      },
    ]);
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValue({
      messages: [
        { id: "msg-user", role: "user", parts: [{ type: "text", text: "push code" }] },
        { id: "msg-asst", role: "assistant", parts: [{ type: "tool_use", text: "question: GitHub auth required", toolState: "running" }] },
      ],
      nextCursor: undefined,
    });
    // Abort fails (e.g. network error)
    (workspacesApi as Record<string, unknown>).abortSession = vi.fn().mockRejectedValue(new Error("network error"));

    const qc = makeQueryClient();
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={["/chat/ws-1/sess-abort-fail"]}>
          <Routes>
            <Route path="/chat/:workspaceId/:sessionId" element={<ChatPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await waitFor(() => expect(capturedSSEHandler).not.toBeNull());
    act(() => {
      capturedSSEHandler!({ type: "session.status", session_id: "sess-abort-fail", status: "busy" });
    });

    // Even when abort fails, banner must still appear
    await waitFor(() =>
      expect(screen.getByText(/session was interrupted/i)).toBeInTheDocument(),
      { timeout: 3000 },
    );
  });

  it("does NOT auto-abort when the question is still in the SSE queue (re-emitted on reconnect)", async () => {
    // If the agent.question SSE event arrives (emitPendingInputRequests replayed it),
    // the question is still live — don't abort, let the user answer.
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "msg-user", role: "user", parts: [{ type: "text", text: "push to github" }] },
      {
        id: "msg-asst",
        role: "assistant",
        parts: [{ type: "tool_use", text: "question: GitHub auth required", toolState: "running" }],
      },
    ]);
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValue({
      messages: [
        { id: "msg-user", role: "user", parts: [{ type: "text", text: "push to github" }] },
        { id: "msg-asst", role: "assistant", parts: [{ type: "tool_use", text: "question: GitHub auth required", toolState: "running" }] },
      ],
      nextCursor: undefined,
    });
    (workspacesApi as Record<string, unknown>).abortSession = vi.fn().mockResolvedValue(undefined);

    const qc = makeQueryClient();
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={["/chat/ws-1/sess-live-q"]}>
          <Routes>
            <Route path="/chat/:workspaceId/:sessionId" element={<ChatPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await waitFor(() => expect(capturedSSEHandler).not.toBeNull());

    act(() => {
      // Session is busy
      capturedSSEHandler!({ type: "session.status", session_id: "sess-live-q", status: "busy" });
      // Agent question was replayed via emitPendingInputRequests
      capturedSSEHandler!({
        type: "agent.question",
        data: {
          id: "que_123",
          session_id: "sess-live-q",
          root_session_id: "sess-live-q",
          questions: [{ question: "How to proceed?", header: "GitHub auth", options: [], multiple: false, custom: true }],
        },
      });
    });

    // Give time for any auto-abort to fire (it should not)
    await new Promise((r) => setTimeout(r, 200));

    expect((workspacesApi as Record<string, unknown>).abortSession).not.toHaveBeenCalled();
    expect(screen.queryByText(/session was interrupted/i)).toBeNull();
  });
});
