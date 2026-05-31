/**
 * Tests for ChatPage's agent input request handling (US-16.11, US-16.12).
 * Tests the SSE event → state → prompt render → resolve lifecycle.
 */
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, act, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChatPage } from "./ChatPage";
import type { WorkspaceStreamEvent } from "../api/types";

// --- Mocks ---

vi.mock("../api/workspaces", () => ({
  workspacesApi: {
    getStatus: vi.fn(),
    activate: vi.fn(),
    list: vi.fn().mockResolvedValue({ items: [], pagination: { limit: 20, offset: 0, total: 0 } }),
    renameSession: vi.fn(),
  },
}));
vi.mock("../api/messages", () => ({ messagesApi: { getHistory: vi.fn().mockResolvedValue([]), sendAsync: vi.fn() } }));
vi.mock("../api/sessions", () => ({ sessionsApi: { create: vi.fn() } }));
vi.mock("../api/input", () => ({
  inputApi: {
    questionReply: vi.fn().mockResolvedValue(true),
    questionReject: vi.fn().mockResolvedValue(true),
    permissionReply: vi.fn().mockResolvedValue(true),
    listQuestions: vi.fn().mockResolvedValue([]),
    listPermissions: vi.fn().mockResolvedValue([]),
  },
}));

// Capture the SSE handler
let capturedSSEHandler: ((data: unknown) => void) | null = null;
vi.mock("../hooks/useEventStream", () => ({
  useEventStream: vi.fn((_workspaceId: string | undefined, handler: (data: unknown) => void) => {
    capturedSSEHandler = handler;
  }),
}));

// Mock ChatView to render prompts prop (so we can test the full integration)
vi.mock("../components/chat/ChatView", () => ({
  ChatView: (props: Record<string, unknown>) => (
    <div data-testid="chat-view">
      {props.prompts as React.ReactNode}
    </div>
  ),
}));

import { workspacesApi } from "../api/workspaces";
import { messagesApi } from "../api/messages";

// --- Helpers ---

function makeQueryClient() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  return qc;
}

function renderChat(qc: QueryClient, path: string) {
  // Pre-seed queries so the component renders ChatView immediately
  const wsId = path.split("/")[2]; // e.g. "ws-1"
  const sesId = path.split("/")[3]; // e.g. "ses_1"
  qc.setQueryData(["workspace-status", wsId], { phase: "Active" });
  qc.setQueryData(["workspaces"], { items: [], pagination: { limit: 20, offset: 0, total: 0 } });
  qc.setQueryData(["messages", wsId, sesId], []);
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

function sendSSE(event: WorkspaceStreamEvent) {
  act(() => { capturedSSEHandler?.(event); });
}

const questionEvent: WorkspaceStreamEvent = {
  type: "agent.question",
  data: {
    id: "que_abc",
    session_id: "ses_1",
    questions: [{ header: "Language", question: "Pick one", options: [{ label: "Go", description: "fast" }] }],
  },
} as unknown as WorkspaceStreamEvent;

const questionResolvedEvent: WorkspaceStreamEvent = {
  type: "agent.question.resolved",
  data: { request_id: "que_abc", session_id: "ses_1" },
} as unknown as WorkspaceStreamEvent;

const permissionEvent: WorkspaceStreamEvent = {
  type: "agent.permission",
  data: {
    id: "per_xyz",
    session_id: "ses_1",
    permission: "shell",
    patterns: ["rm -rf /tmp"],
  },
} as unknown as WorkspaceStreamEvent;

const permissionResolvedEvent: WorkspaceStreamEvent = {
  type: "agent.permission.resolved",
  data: { request_id: "per_xyz", session_id: "ses_1", reply: "once" },
} as unknown as WorkspaceStreamEvent;

// --- Tests ---

describe("ChatPage agent input requests (US-16.11, US-16.12)", () => {
  beforeEach(() => {
    capturedSSEHandler = null;
    vi.clearAllMocks();
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    (workspacesApi.list as ReturnType<typeof vi.fn>).mockResolvedValue({ items: [], pagination: { limit: 20, offset: 0, total: 0 } });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);
  });

  it("agent.question event renders QuestionPrompt", async () => {
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(screen.getByTestId("chat-view")).toBeInTheDocument());
    sendSSE(questionEvent);
    expect(screen.getByText("Pick one")).toBeInTheDocument();
  });

  it("agent.question.resolved removes QuestionPrompt", async () => {
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(screen.getByTestId("chat-view")).toBeInTheDocument());
    sendSSE(questionEvent);
    expect(screen.getByText("Pick one")).toBeInTheDocument();
    sendSSE(questionResolvedEvent);
    expect(screen.queryByText("Pick one")).not.toBeInTheDocument();
  });

  it("agent.permission event renders PermissionPrompt", async () => {
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(screen.getByTestId("chat-view")).toBeInTheDocument());
    sendSSE(permissionEvent);
    expect(screen.getByText("Run shell command")).toBeInTheDocument();
  });

  it("agent.permission.resolved removes PermissionPrompt", async () => {
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(screen.getByTestId("chat-view")).toBeInTheDocument());
    sendSSE(permissionEvent);
    expect(screen.getByText("Run shell command")).toBeInTheDocument();
    sendSSE(permissionResolvedEvent);
    expect(screen.queryByText("Run shell command")).not.toBeInTheDocument();
  });

  it("event for different session is ignored", async () => {
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(screen.getByTestId("chat-view")).toBeInTheDocument());
    const wrongSession = { ...questionEvent, data: { ...(questionEvent as any).data, session_id: "ses_other" } } as unknown as WorkspaceStreamEvent;
    sendSSE(wrongSession);
    expect(screen.queryByText("Pick one")).not.toBeInTheDocument();
  });

  it("duplicate event (same id) does not double render", async () => {
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(screen.getByTestId("chat-view")).toBeInTheDocument());
    sendSSE(questionEvent);
    sendSSE(questionEvent);
    expect(screen.getAllByText("Pick one")).toHaveLength(1);
  });

  it("session idle clears all pending prompts (US-16.12)", async () => {
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(screen.getByTestId("chat-view")).toBeInTheDocument());
    sendSSE(questionEvent);
    sendSSE(permissionEvent);
    expect(screen.getByText("Pick one")).toBeInTheDocument();
    expect(screen.getByText("Run shell command")).toBeInTheDocument();
    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" } as unknown as WorkspaceStreamEvent);
    expect(screen.queryByText("Pick one")).not.toBeInTheDocument();
    expect(screen.queryByText("Run shell command")).not.toBeInTheDocument();
  });

  it("session error clears pending prompts (US-16.12)", async () => {
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(screen.getByTestId("chat-view")).toBeInTheDocument());
    sendSSE(questionEvent);
    expect(screen.getByText("Pick one")).toBeInTheDocument();
    sendSSE({
      type: "opencode.event",
      event_type: "session.error",
      data: { properties: { sessionID: "ses_1", error: { name: "timeout" } } },
    } as unknown as WorkspaceStreamEvent);
    expect(screen.queryByText("Pick one")).not.toBeInTheDocument();
  });

  it("session idle for different session does NOT clear prompts", async () => {
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(screen.getByTestId("chat-view")).toBeInTheDocument());
    sendSSE(questionEvent);
    sendSSE({ type: "session.status", session_id: "ses_other", status: "idle" } as unknown as WorkspaceStreamEvent);
    expect(screen.getByText("Pick one")).toBeInTheDocument();
  });

  it("no pending prompts + idle is a no-op", async () => {
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(screen.getByTestId("chat-view")).toBeInTheDocument());
    sendSSE({ type: "session.status", session_id: "ses_1", status: "idle" } as unknown as WorkspaceStreamEvent);
    expect(screen.getByTestId("chat-view")).toBeInTheDocument();
  });

  it("user answers question → onResolved removes prompt", async () => {
    const qc = makeQueryClient();
    renderChat(qc, "/chat/ws-1/ses_1");
    await waitFor(() => expect(screen.getByTestId("chat-view")).toBeInTheDocument());
    sendSSE(questionEvent);
    const goBtn = screen.getByRole("button", { name: "Go" });
    act(() => { goBtn.click(); });
    const submitBtn = screen.getByText("Submit answers");
    act(() => { submitBtn.click(); });
    await waitFor(() => expect(screen.queryByText("Pick one")).not.toBeInTheDocument());
  });
});
