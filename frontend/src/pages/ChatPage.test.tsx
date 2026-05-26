import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { render } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChatPage } from "./ChatPage";

vi.mock("../api/workspaces", () => ({
  workspacesApi: {
    getStatus: vi.fn(),
    getWorkspaceSessions: vi.fn(),
    activate: vi.fn(),
    list: vi.fn().mockResolvedValue({ items: [], pagination: { limit: 20, offset: 0, total: 0 } }),
    renameWorkspace: vi.fn(),
    deleteWorkspace: vi.fn(),
    suspend: vi.fn(),
  },
}));
vi.mock("../api/messages", () => ({ messagesApi: { getHistory: vi.fn(), send: vi.fn() } }));
vi.mock("../api/sessions", () => ({ sessionsApi: { create: vi.fn() } }));
vi.mock("../hooks/useEventStream", () => ({ useEventStream: vi.fn() }));

import { workspacesApi } from "../api/workspaces";

function renderChatPage(path = "/chat") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/chat" element={<ChatPage />} />
          <Route path="/chat/:workspaceId" element={<ChatPage />} />
          <Route path="/chat/:workspaceId/:sessionId" element={<ChatPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("ChatPage", () => {
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
    (workspacesApi.getWorkspaceSessions as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    renderChatPage("/chat/ws-1");
    await waitFor(() => expect(screen.getByText(/is suspended/)).toBeInTheDocument());
  });

  it("shows transitioning state", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Resuming" });
    (workspacesApi.getWorkspaceSessions as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    renderChatPage("/chat/ws-1");
    await waitFor(() => expect(screen.getByText(/resuming/i)).toBeInTheDocument());
  });

  it("disables composer when workspace is suspended", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Suspended" });
    (workspacesApi.getWorkspaceSessions as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    renderChatPage("/chat/ws-1/sess-1");
    await waitFor(() => expect(document.querySelector("textarea")).toBeDisabled());
  });

  it("enables composer when workspace is running and session is selected", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    (workspacesApi.getWorkspaceSessions as ReturnType<typeof vi.fn>).mockResolvedValue([{ id: "sb-1", phase: "Running" }]);
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
});
