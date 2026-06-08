/**
 * Tests for ChatPage message queue behavior.
 * Messages sent while streaming are queued and flushed on idle.
 */
import { describe, expect, it, vi, beforeEach } from "vitest";
import { screen, waitFor, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChatPage } from "./ChatPage";

vi.mock("../api/workspaces", () => ({
  workspacesApi: {
    getStatus: vi.fn(),
    activate: vi.fn(),
    abortSession: vi.fn(),
    list: vi.fn().mockResolvedValue({ items: [], pagination: { limit: 20, offset: 0, total: 0 } }),
    renameSession: vi.fn(),
    renameWorkspace: vi.fn().mockResolvedValue({}),
  },
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
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/chat/:workspaceId/:sessionId" element={<ChatPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function sendSSE(event: Record<string, unknown>) {
  act(() => { capturedSSEHandler?.(event); });
}

describe("ChatPage message queue", () => {
  beforeEach(() => {
    capturedSSEHandler = null;
    vi.clearAllMocks();
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active", sessions: [{ id: "ses_1", status: "idle" }] });
    (workspacesApi.list as ReturnType<typeof vi.fn>).mockResolvedValue({ items: [], pagination: { limit: 20, offset: 0, total: 0 } });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValue({ messages: [], nextCursor: undefined });
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
  });

  it("sends immediately when not streaming", async () => {
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

  it("queues message when streaming is true (server busy)", async () => {
    const user = userEvent.setup();
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    // Set server to busy via SSE
    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    // Send a message — should be queued, not sent
    await user.type(document.querySelector("textarea")!, "queued msg");
    await user.keyboard("{Enter}");

    // sendAsync should NOT have been called (message is queued)
    expect(messagesApi.sendAsync).not.toHaveBeenCalled();

    // Queue indicator should show 1
    expect(screen.getByText("1 message queued")).toBeInTheDocument();
  });

  it("queued message shows as optimistic user message", async () => {
    const user = userEvent.setup();
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    // Set server to busy via SSE
    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    // Send a message
    await user.type(document.querySelector("textarea")!, "queued msg");
    await user.keyboard("{Enter}");

    // Optimistic message should appear
    expect(screen.getByText("queued msg")).toBeInTheDocument();
  });

  it("flushes queue when session returns to idle", async () => {
    const user = userEvent.setup();
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    // Set server to busy
    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    // Queue two messages
    await user.type(document.querySelector("textarea")!, "first");
    await user.keyboard("{Enter}");
    await user.type(document.querySelector("textarea")!, "second");
    await user.keyboard("{Enter}");

    expect(screen.getByText("2 messages queued")).toBeInTheDocument();

    // Flush queue by sending idle
    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" });

    // First queued message should be sent
    await waitFor(() => {
      expect(messagesApi.sendAsync).toHaveBeenCalledTimes(1);
      expect(messagesApi.sendAsync).toHaveBeenCalledWith("ws-1", "ses_1", {
        parts: [{ type: "text", text: "first" }],
      });
    });
  });

  it("clears queue on session change", async () => {
    const user = userEvent.setup();
    const qc = makeQueryClient();
    const { unmount } = renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    // Set server to busy
    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    // Queue a message
    await user.type(document.querySelector("textarea")!, "queued msg");
    await user.keyboard("{Enter}");
    expect(screen.getByText("1 message queued")).toBeInTheDocument();

    // Navigate to different session
    unmount();
    const qc2 = makeQueryClient();
    qc2.setQueryData(["workspace-status", "ws-1"], { phase: "Active", sessions: [{ id: "ses_2", status: "idle" }] });
    qc2.setQueryData(["messages", "ws-1", "ses_2"], { pages: [{ messages: [], nextCursor: undefined }], pageParams: [undefined] });
    render(
      <QueryClientProvider client={qc2}>
        <MemoryRouter initialEntries={["/chat/ws-1/ses_2"]}>
          <Routes>
            <Route path="/chat/:workspaceId/:sessionId" element={<ChatPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());
    expect(screen.queryByText(/queued/)).not.toBeInTheDocument();
  });

  it("textarea stays enabled during streaming", async () => {
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    // Set server to busy
    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    // Textarea should still be enabled
    expect(document.querySelector("textarea")).not.toBeDisabled();
  });

  it("multiple messages can be queued", async () => {
    const user = userEvent.setup();
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    await user.type(document.querySelector("textarea")!, "msg1");
    await user.keyboard("{Enter}");
    await user.type(document.querySelector("textarea")!, "msg2");
    await user.keyboard("{Enter}");
    await user.type(document.querySelector("textarea")!, "msg3");
    await user.keyboard("{Enter}");

    expect(screen.getByText("3 messages queued")).toBeInTheDocument();
  });

  it("stop button is shown during streaming", async () => {
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    // Set server to busy
    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    // Stop button should be visible
    expect(screen.getByLabelText("Stop generating")).toBeInTheDocument();
  });

  it("stop button calls abortSession", async () => {
    const user = userEvent.setup();
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    await user.type(document.querySelector("textarea")!, "queued msg");
    await user.keyboard("{Enter}");
    expect(screen.getByText("1 message queued")).toBeInTheDocument();

    // Click stop
    await user.click(screen.getByLabelText("Stop generating"));

    // Abort session should be called
    expect(workspacesApi.abortSession).toHaveBeenCalledWith("ws-1", "ses_1");
  });
});
