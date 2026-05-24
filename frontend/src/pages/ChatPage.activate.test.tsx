import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

function renderChat(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, refetchInterval: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/chat/:workspaceId" element={<ChatPage />} />
          <Route path="/chat/:workspaceId/:sessionId" element={<ChatPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("Workspace activate flow — state machine", () => {
  it("suspended workspace shows banner with resume button, composer disabled", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Suspended" });
    (workspacesApi.getSandboxes as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    renderChat("/chat/ws-1");

    await waitFor(() => expect(screen.getByText(/is suspended/)).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Resume to chat" })).toBeInTheDocument();
    expect(document.querySelector("textarea")).toBeDisabled();
  });

  it("clicking resume calls activate API", async () => {
    const user = userEvent.setup();
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Suspended" });
    (workspacesApi.getSandboxes as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    (workspacesApi.activate as ReturnType<typeof vi.fn>).mockResolvedValue({ resumed: "ws-1" });

    renderChat("/chat/ws-1");

    await waitFor(() => expect(screen.getByRole("button", { name: "Resume to chat" })).toBeInTheDocument());
    await user.click(screen.getByRole("button", { name: "Resume to chat" }));

    expect(workspacesApi.activate).toHaveBeenCalledWith("ws-1");
  });

  it("resuming state shows spinner with transitioning message", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Resuming" });
    (workspacesApi.getSandboxes as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    renderChat("/chat/ws-1");

    await waitFor(() => expect(screen.getByText(/resuming/i)).toBeInTheDocument());
  });

  it("active workspace with running sandbox enables composer", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    (workspacesApi.getSandboxes as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "sb-1", phase: "Running", podIP: "10.0.0.1" },
    ]);

    renderChat("/chat/ws-1/sess-1");

    await waitFor(() => {
      expect(document.querySelector("textarea")).not.toBeDisabled();
    });
  });

  it("active workspace with no sandbox keeps composer disabled", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    (workspacesApi.getSandboxes as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    renderChat("/chat/ws-1");

    await waitFor(() => {
      expect(document.querySelector("textarea")).toBeDisabled();
    });
  });

  it("active workspace with sandbox in Creating state keeps composer disabled", async () => {
    (workspacesApi.getStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ phase: "Active" });
    (workspacesApi.getSandboxes as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "sb-1", phase: "Creating" },
    ]);

    renderChat("/chat/ws-1");

    await waitFor(() => {
      expect(document.querySelector("textarea")).toBeDisabled();
    });
  });
});
