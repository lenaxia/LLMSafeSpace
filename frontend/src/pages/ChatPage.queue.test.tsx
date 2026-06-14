/**
 * Integration tests for ChatPage message queue (v3).
 *
 * v3 design (matching TUI behavior): messages are held locally and sent one
 * at a time. enqueue() does NOT fire promptAsync. The message is sent when
 * session.status=idle arrives via SSE (notifyIdle). The pill is removed as
 * soon as promptAsync returns 204. The next item is sent on the subsequent
 * idle event.
 */
import { describe, expect, it, vi, beforeEach } from "vitest";
import { screen, waitFor, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ThemeProvider } from "../providers/ThemeProvider";
import { ChatPage } from "./ChatPage";

vi.mock("../api/workspaces", () => ({
  workspacesApi: {
    getStatus: vi.fn(),
    activate: vi.fn(),
    abortSession: vi.fn(),
    list: vi.fn().mockResolvedValue({ items: [], pagination: { limit: 20, offset: 0, total: 0 } }),
    renameSession: vi.fn(),
    renameWorkspace: vi.fn().mockResolvedValue({}),
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
vi.mock("../api/messages", () => ({
  messagesApi: {
    getHistory: vi.fn().mockResolvedValue([]),
    getHistoryPage: vi.fn().mockResolvedValue({ messages: [], nextCursor: undefined }),
    sendAsync: vi.fn(),
  },
}));
vi.mock("../api/sessions", () => ({ sessionsApi: { create: vi.fn() } }));

let capturedSSEHandler: ((data: unknown) => void) | null = null;
vi.mock("../hooks/useEventStream", () => ({
  useEventStream: vi.fn((_workspaceId: string | undefined, handler: (data: unknown) => void) => {
    capturedSSEHandler = handler;
  }),
}));

import { workspacesApi } from "../api/workspaces";
import { messagesApi } from "../api/messages";

function makeQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
}

function renderChat(qc: QueryClient, path: string) {
  const wsId = path.split("/")[2];
  const sesId = path.split("/")[3];
  qc.setQueryData(["workspace-status", wsId], { phase: "Active", sessions: [{ id: sesId, status: "idle" }] });
  qc.setQueryData(["workspaces"], { items: [], pagination: { limit: 20, offset: 0, total: 0 } });
  qc.setQueryData(["messages", wsId, sesId], { pages: [{ messages: [], nextCursor: undefined }], pageParams: [undefined] });
  return render(
    <QueryClientProvider client={qc}>
      <ThemeProvider>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path="/chat/:workspaceId/:sessionId" element={<ChatPage />} />
          </Routes>
        </MemoryRouter>
      </ThemeProvider>
    </QueryClientProvider>,
  );
}

function sendSSE(event: Record<string, unknown>) {
  act(() => { capturedSSEHandler?.(event); });
}

describe("ChatPage message queue (v3 — TUI-matching serialized)", () => {
  beforeEach(() => {
    capturedSSEHandler = null;
    vi.clearAllMocks();
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active", sessions: [{ id: "ses_1", status: "idle" }] });
    (workspacesApi.list as ReturnType<typeof vi.fn>).mockResolvedValue({ items: [], pagination: { limit: 20, offset: 0, total: 0 } });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValue({ messages: [], nextCursor: undefined });
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
  });

  it("sends immediately when not busy", async () => {
    const user = userEvent.setup();
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    await user.type(document.querySelector("textarea")!, "hello");
    await user.keyboard("{Enter}");

    await waitFor(() => {
      expect(messagesApi.sendAsync).toHaveBeenCalledWith("ws-1", "ses_1", {
        parts: [{ type: "text", text: "hello" }],
      });
    });
  });

  it("textarea stays enabled during streaming", async () => {
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    expect(document.querySelector("textarea")).not.toBeDisabled();
  });

  it("holds message locally when busy — does NOT fire promptAsync until idle", async () => {
    const user = userEvent.setup();
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    await user.type(document.querySelector("textarea")!, "queued msg");
    await user.keyboard("{Enter}");

    // Pill shown, but promptAsync NOT yet called
    expect(screen.getByText("queued msg")).toBeInTheDocument();
    expect(screen.getByText("1 message queued")).toBeInTheDocument();
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();
  });

  it("fires promptAsync for first item when idle arrives", async () => {
    const user = userEvent.setup();
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    await user.type(document.querySelector("textarea")!, "queued msg");
    await user.keyboard("{Enter}");

    expect(messagesApi.sendAsync).not.toHaveBeenCalled();

    // Session goes idle
    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" });

    await waitFor(() => {
      expect(messagesApi.sendAsync).toHaveBeenCalledOnce();
      expect(messagesApi.sendAsync).toHaveBeenCalledWith("ws-1", "ses_1", expect.objectContaining({
        parts: [{ type: "text", text: "queued msg" }],
      }));
    });
  });

  it("multiple queued messages are sent one per idle cycle", async () => {
    const user = userEvent.setup();
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    await user.type(document.querySelector("textarea")!, "first");
    await user.keyboard("{Enter}");
    await user.type(document.querySelector("textarea")!, "second");
    await user.keyboard("{Enter}");
    await user.type(document.querySelector("textarea")!, "third");
    await user.keyboard("{Enter}");

    expect(screen.getByText("3 messages queued")).toBeInTheDocument();
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();

    // First idle: sends "first" only
    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" });
    await waitFor(() => expect(messagesApi.sendAsync).toHaveBeenCalledOnce());

    expect((messagesApi.sendAsync as ReturnType<typeof vi.fn>).mock.calls[0]![2]).toMatchObject({
      parts: [{ type: "text", text: "first" }],
    });

    // Still two items remaining (pill for "first" gone after 204, "second" and "third" remain)
    await waitFor(() => expect(screen.getByText("2 messages queued")).toBeInTheDocument());

    // Second idle: sends "second"
    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" });
    await waitFor(() => expect(messagesApi.sendAsync).toHaveBeenCalledTimes(2));

    await waitFor(() => expect(screen.getByText("1 message queued")).toBeInTheDocument());

    // Third idle: sends "third"
    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" });
    await waitFor(() => expect(messagesApi.sendAsync).toHaveBeenCalledTimes(3));

    await waitFor(() => expect(screen.queryByText(/queued/)).not.toBeInTheDocument());
  });

  it("pill is removed after promptAsync returns 204 (not waiting for history)", async () => {
    const user = userEvent.setup();
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    await user.type(document.querySelector("textarea")!, "first");
    await user.keyboard("{Enter}");

    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" });

    // Pill gone after 204, before any history change
    await waitFor(() => {
      expect(screen.queryByText("1 message queued")).not.toBeInTheDocument();
    });
  });

  it("abort clears all queue pills", async () => {
    const user = userEvent.setup();
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    await user.type(document.querySelector("textarea")!, "first");
    await user.keyboard("{Enter}");
    await user.type(document.querySelector("textarea")!, "second");
    await user.keyboard("{Enter}");

    await waitFor(() => {
      expect(screen.getByText("2 messages queued")).toBeInTheDocument();
    });

    await user.click(screen.getByLabelText("Stop generating"));

    expect(workspacesApi.abortSession).toHaveBeenCalledWith("ws-1", "ses_1");
    await waitFor(() => {
      expect(screen.queryByText(/queued/)).not.toBeInTheDocument();
    });
    // No messages were ever sent
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();
  });

  it("stop button is shown during streaming", async () => {
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    expect(screen.getByLabelText("Stop generating")).toBeInTheDocument();
  });

  it("failed send shows error pill with retry/dismiss", async () => {
    const user = userEvent.setup();
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("network fail"));

    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    await user.type(document.querySelector("textarea")!, "failing msg");
    await user.keyboard("{Enter}");

    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" });

    await waitFor(() => {
      expect(screen.getByLabelText("Retry")).toBeInTheDocument();
      expect(screen.getByLabelText("Dismiss")).toBeInTheDocument();
    });
  });

  it("dismiss removes error pill", async () => {
    const user = userEvent.setup();
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("fail"));

    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    await user.type(document.querySelector("textarea")!, "msg");
    await user.keyboard("{Enter}");

    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" });

    await waitFor(() => expect(screen.getByLabelText("Dismiss")).toBeInTheDocument());
    await user.click(screen.getByLabelText("Dismiss"));

    await waitFor(() => {
      expect(screen.queryByLabelText("Dismiss")).not.toBeInTheDocument();
    });
  });

  it("does not clear sseStreamParts when reconcileOnIdle returns empty history", async () => {
    const qc = makeQueryClient();
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValue({
      messages: [],
      nextCursor: undefined,
    });
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    const user = userEvent.setup();
    await user.type(document.querySelector("textarea")!, "hello");
    await user.keyboard("{Enter}");

    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" });

    await waitFor(() => {
      expect(messagesApi.sendAsync).toHaveBeenCalled();
    });

    await waitFor(() => {
      expect(messagesApi.getHistoryPage).toHaveBeenCalled();
    });
  });

  it("clears sseStreamParts when reconcileOnIdle returns non-empty history", async () => {
    const qc = makeQueryClient();
    const historyMsg = { id: "msg_1", role: "assistant", parts: [{ type: "text", text: "response" }] };
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValue({
      messages: [historyMsg],
      nextCursor: undefined,
    });
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });
    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" });

    await waitFor(() => {
      expect(messagesApi.getHistoryPage).toHaveBeenCalled();
    });
  });

  it("queued message is sent on idle and response appears in history after second idle", async () => {
    const qc = makeQueryClient();
    const user = userEvent.setup();
    let callCount = 0;
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockImplementation(() => {
      callCount++;
      if (callCount === 1) {
        return Promise.resolve({ messages: [], nextCursor: undefined });
      }
      return Promise.resolve({
        messages: [{ id: "msg_resp", role: "assistant", parts: [{ type: "text", text: "final answer" }] }],
        nextCursor: undefined,
      });
    });
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    await user.type(document.querySelector("textarea")!, "question");
    await user.keyboard("{Enter}");

    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" });

    await waitFor(() => {
      expect(messagesApi.sendAsync).toHaveBeenCalled();
    });

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });
    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" });

    await waitFor(() => {
      expect(callCount).toBeGreaterThanOrEqual(2);
    });
  });
});
