import { describe, expect, it, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChatPage } from "./ChatPage";

vi.mock("../api/workspaces", () => ({
  workspacesApi: {
    getStatus: vi.fn(),
    activate: vi.fn(),
    list: vi.fn().mockResolvedValue({ items: [], pagination: { limit: 20, offset: 0, total: 0 } }),
    renameWorkspace: vi.fn().mockResolvedValue({}),
    deleteWorkspace: vi.fn().mockResolvedValue({}),
    suspend: vi.fn().mockResolvedValue({}),
  },
}));
vi.mock("../api/messages", () => ({ messagesApi: { getHistory: vi.fn().mockResolvedValue([]), sendAsync: vi.fn() } }));
vi.mock("../api/sessions", () => ({ sessionsApi: { create: vi.fn() } }));
vi.mock("../hooks/useEventStream", () => ({ useEventStream: vi.fn() }));

import { workspacesApi } from "../api/workspaces";
import { messagesApi } from "../api/messages";
import { sessionsApi } from "../api/sessions";

function renderChatPage(path = "/chat") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return {
    qc,
    ...render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path="/chat" element={<ChatPage />} />
            <Route path="/chat/:workspaceId" element={<ChatPage />} />
            <Route path="/chat/:workspaceId/:sessionId" element={<ChatPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    ),
  };
}

describe("ChatPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (workspacesApi.list as ReturnType<typeof vi.fn>).mockResolvedValue({ items: [], pagination: { limit: 20, offset: 0, total: 0 } });
    (messagesApi.getHistory as ReturnType<typeof vi.fn>).mockResolvedValue([]);
  });
  it("shows empty state when no workspace selected", () => {
    renderChatPage("/chat");
    expect(screen.getByText("Select a workspace to start chatting")).toBeInTheDocument();
  });

  it("shows workspace name in header", async () => {
    (workspacesApi.list as ReturnType<typeof vi.fn>).mockResolvedValue({
      items: [{ id: "ws-1", name: "My Workspace", phase: "Active" }],
      pagination: { limit: 20, offset: 0, total: 1 },
    });
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Suspended" });
    renderChatPage("/chat/ws-1");
    await waitFor(() => expect(screen.getByText("My Workspace")).toBeInTheDocument());
  });

  it("shows suspended banner for suspended workspace", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Suspended" });
    renderChatPage("/chat/ws-1");
    await waitFor(() => expect(screen.getByText(/is suspended/)).toBeInTheDocument());
  });

  it("shows transitioning state", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Resuming" });
    renderChatPage("/chat/ws-1");
    await waitFor(() => expect(screen.getByText(/resuming/i)).toBeInTheDocument());
  });

  it("disables composer when workspace is suspended", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Suspended" });
    renderChatPage("/chat/ws-1/sess-1");
    await waitFor(() => expect(document.querySelector("textarea")).toBeDisabled());
  });

  it("enables composer when workspace is running and session is selected", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    renderChatPage("/chat/ws-1/sess-1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());
  });

  it("shows kebab menu in header", async () => {
    (workspacesApi.list as ReturnType<typeof vi.fn>).mockResolvedValue({
      items: [{ id: "ws-1", name: "My Workspace", phase: "Active" }],
      pagination: { limit: 20, offset: 0, total: 1 },
    });
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    renderChatPage("/chat/ws-1");
    await waitFor(() => expect(screen.getByLabelText("Actions")).toBeInTheDocument());
  });

  it("auto-creates session when workspace Active and no sessionId", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    (sessionsApi.create as ReturnType<typeof vi.fn>).mockResolvedValue({ sessionId: "new-sess" });
    renderChatPage("/chat/ws-1");
    await waitFor(() => expect(sessionsApi.create).toHaveBeenCalledWith("ws-1", "New chat"));
  });

  it("shows chatError banner when send fails", async () => {
    const user = userEvent.setup();
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("LLM error"));

    renderChatPage("/chat/ws-1/sess-1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    await user.click(document.querySelector("textarea")!);
    await user.type(document.querySelector("textarea")!, "hello");
    await user.keyboard("{Enter}");

    await waitFor(() => expect(screen.getByText("LLM error")).toBeInTheDocument());
  });

  it("Dismiss button clears chatError", async () => {
    const user = userEvent.setup();
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    (messagesApi.sendAsync as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("boom"));

    renderChatPage("/chat/ws-1/sess-1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());

    await user.click(document.querySelector("textarea")!);
    await user.type(document.querySelector("textarea")!, "hello");
    await user.keyboard("{Enter}");

    await waitFor(() => expect(screen.getByText("boom")).toBeInTheDocument());
    await user.click(screen.getByRole("button", { name: "Dismiss" }));
    await waitFor(() => expect(screen.queryByText("boom")).not.toBeInTheDocument());
  });
});
