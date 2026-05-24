import { describe, expect, it, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WorkspaceSessionList } from "./WorkspaceSessionList";
import type { ReactNode } from "react";

vi.mock("../../api/workspaces", () => ({
  workspacesApi: {
    getSessions: vi.fn(),
  },
}));

import { workspacesApi } from "../../api/workspaces";

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={qc}>
      <MemoryRouter>{children}</MemoryRouter>
    </QueryClientProvider>
  );
}

describe("WorkspaceSessionList", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });
  it("fetches and renders sessions for the workspace", async () => {
    (workspacesApi.getSessions as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "s1", title: "Auth refactor", messageCount: 5, status: "idle" },
      { id: "s2", title: "Bug fix", messageCount: 2, status: "active" },
    ]);

    render(<WorkspaceSessionList workspaceId="ws-1" onSelectSession={vi.fn()} />, { wrapper });

    await waitFor(() => expect(screen.getByText("Auth refactor")).toBeInTheDocument());
    expect(screen.getByText("Bug fix")).toBeInTheDocument();
  });

  it("shows empty state when no sessions", async () => {
    (workspacesApi.getSessions as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    render(<WorkspaceSessionList workspaceId="ws-1" onSelectSession={vi.fn()} />, { wrapper });

    await waitFor(() => expect(screen.getByText("No sessions yet")).toBeInTheDocument());
  });

  it("calls onSelectSession when a session is clicked", async () => {
    const user = (await import("@testing-library/user-event")).default.setup();
    (workspacesApi.getSessions as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "s1", title: "Chat", messageCount: 1, status: "idle" },
    ]);

    const onSelect = vi.fn();
    render(<WorkspaceSessionList workspaceId="ws-1" onSelectSession={onSelect} />, { wrapper });

    await waitFor(() => expect(screen.getByText("Chat")).toBeInTheDocument());
    await user.click(screen.getByText("Chat"));
    expect(onSelect).toHaveBeenCalledWith("s1");
  });

  it("does not fetch when workspaceId is undefined", () => {
    render(<WorkspaceSessionList workspaceId={undefined} onSelectSession={vi.fn()} />, { wrapper });
    expect(workspacesApi.getSessions).not.toHaveBeenCalled();
  });
});
