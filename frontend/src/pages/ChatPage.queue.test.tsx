/**
 * Integration tests for ChatPage message queue (v2).
 *
 * v2 design: when streaming, queue.enqueue(text) fires prompt_async immediately
 * (fire-and-forget). The server serializes behind the current turn. Pills are
 * shown in QueueSection and removed when reconcile() sees the message in history.
 * Abort calls queue.clear() — server drains its own queue.
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

describe("ChatPage message queue (v2)", () => {
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

  it("textarea stays enabled during streaming", async () => {
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    expect(document.querySelector("textarea")).not.toBeDisabled();
  });

  it("queues and fires prompt_async immediately when streaming", async () => {
    const user = userEvent.setup();
    renderChat(makeQueryClient(), "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    await user.type(document.querySelector("textarea")!, "queued msg");
    await user.keyboard("{Enter}");

    // v2: sendAsync fires immediately (prompt_async, server serializes)
    await waitFor(() => {
      expect(messagesApi.sendAsync).toHaveBeenCalledWith("ws-1", "ses_1", expect.objectContaining({
        parts: [{ type: "text", text: "queued msg" }],
      }));
    });

    // pill displayed
    expect(screen.getByText("queued msg")).toBeInTheDocument();
    expect(screen.getByText("1 message queued")).toBeInTheDocument();
  });

  it("multiple queued messages each fire prompt_async immediately", async () => {
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

    // All three fire immediately — server serializes via ensureRunning
    await waitFor(() => {
      expect(messagesApi.sendAsync).toHaveBeenCalledTimes(3);
    });

    expect(screen.getByText("3 messages queued")).toBeInTheDocument();
  });

  it("pills are removed after reconcile sees them in history", async () => {
    const user = userEvent.setup();
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    sendSSE({ type: "session.status", session_id: "ses_1", status: "busy" });

    await user.type(document.querySelector("textarea")!, "first");
    await user.keyboard("{Enter}");

    let sentId: string | undefined;
    await waitFor(() => {
      const calls = (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mock.calls;
      expect(calls.length).toBeGreaterThan(0);
      const call = calls[0]!;
      sentId = (call[2] as { messageID?: string }).messageID;
      expect(sentId).toMatch(/^msg_/);
    });

    expect(screen.getByText("1 message queued")).toBeInTheDocument();

    // Simulate server processing: history now contains our message, SSE fires idle
    (messagesApi.getHistoryPage as ReturnType<typeof vi.fn>).mockResolvedValue({
      messages: [{ id: sentId, role: "user", parts: [{ type: "text", text: "first" }] }],
      nextCursor: undefined,
    });

    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" });

    // After reconcile, pill should be gone
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
    // queue.clear() removes all pills
    await waitFor(() => {
      expect(screen.queryByText(/queued/)).not.toBeInTheDocument();
    });
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

    // Error pill with retry and dismiss buttons
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

    await waitFor(() => expect(screen.getByLabelText("Dismiss")).toBeInTheDocument());
    await user.click(screen.getByLabelText("Dismiss"));

    await waitFor(() => {
      expect(screen.queryByLabelText("Dismiss")).not.toBeInTheDocument();
    });
  });
});
