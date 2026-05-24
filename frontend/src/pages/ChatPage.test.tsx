import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { render } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChatPage } from "./ChatPage";

vi.mock("../api/workspaces", () => ({
  workspacesApi: {
    getStatus: vi.fn(),
    getSandboxes: vi.fn(),
    activate: vi.fn(),
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

  it("shows suspended banner for suspended workspace", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Suspended" });
    (workspacesApi.getSandboxes as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    renderChatPage("/chat/ws-1");
    await waitFor(() => expect(screen.getByText(/is suspended/)).toBeInTheDocument());
  });

  it("shows transitioning state", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Resuming" });
    (workspacesApi.getSandboxes as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    renderChatPage("/chat/ws-1");
    await waitFor(() => expect(screen.getByText(/resuming/i)).toBeInTheDocument());
  });

  it("disables composer when workspace is suspended", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Suspended" });
    (workspacesApi.getSandboxes as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    renderChatPage("/chat/ws-1/sess-1");
    await waitFor(() => expect(document.querySelector("textarea")).toBeDisabled());
  });

  it("enables composer when sandbox is running and session is selected", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    (workspacesApi.getSandboxes as ReturnType<typeof vi.fn>).mockResolvedValue([{ id: "sb-1", phase: "Running" }]);
    renderChatPage("/chat/ws-1/sess-1");
    await waitFor(() => expect(document.querySelector("textarea")).not.toBeDisabled());
  });
});
