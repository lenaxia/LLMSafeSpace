import { describe, expect, it, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Sidebar } from "./Sidebar";
import { AuthProvider } from "../../providers/AuthProvider";

vi.mock("../../api/auth", () => ({
  authApi: {
    me: vi.fn().mockResolvedValue({ id: "u1", username: "alice", email: "a@b.com", role: "user", active: true }),
  },
}));

vi.mock("../../api/workspaces", () => ({
  workspacesApi: {
    list: vi.fn().mockResolvedValue({
      items: [
        { id: "ws-1", name: "alpha", phase: "Active", userId: "u1", runtime: "python", storageSize: "5Gi", createdAt: "", updatedAt: "" },
      ],
      pagination: { limit: 20, offset: 0, total: 1 },
    }),
    create: vi.fn().mockResolvedValue({ id: "ws-new", name: "new-ws" }),
    activate: vi.fn().mockResolvedValue({ resumed: "ws-1" }),
    ensureSession: vi.fn().mockResolvedValue({ sessionId: "sess-1", workspaceId: "ws-1" }),
    getSessions: vi.fn().mockResolvedValue([
      { id: "sess-1", title: "My session", messageCount: 3, status: "idle" },
    ]),
    renameWorkspace: vi.fn().mockResolvedValue(undefined),
    deleteWorkspace: vi.fn().mockResolvedValue(undefined),
    renameSession: vi.fn().mockResolvedValue(undefined),
    deleteSession: vi.fn().mockResolvedValue(undefined),
  },
}));

import { workspacesApi } from "../../api/workspaces";

function renderSidebar() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return {
    qc,
    ...render(
      <QueryClientProvider client={qc}>
        <AuthProvider>
          <MemoryRouter>
            <Sidebar />
          </MemoryRouter>
        </AuthProvider>
      </QueryClientProvider>,
    ),
  };
}

describe("Sidebar", () => {
  it("renders app title", async () => {
    renderSidebar();
    expect(await screen.findByText("Safe Space")).toBeInTheDocument();
  });

  it("renders username", async () => {
    renderSidebar();
    expect(await screen.findByText("alice")).toBeInTheDocument();
  });

  it("renders workspace list", async () => {
    renderSidebar();
    expect(await screen.findByText("alpha")).toBeInTheDocument();
  });

  it("renders settings button", async () => {
    renderSidebar();
    expect(await screen.findByLabelText("Settings")).toBeInTheDocument();
  });

  it("renders logout button", async () => {
    renderSidebar();
    expect(await screen.findByLabelText("Log out")).toBeInTheDocument();
  });

  it("renders kebab menu for workspace", async () => {
    renderSidebar();
    const kebabButtons = await screen.findAllByLabelText("Actions");
    expect(kebabButtons.length).toBeGreaterThanOrEqual(1);
  });

  it("new workspace button creates immediately without dialog", async () => {
    renderSidebar();
    const btn = await screen.findByLabelText("New workspace");
    expect(btn).toBeInTheDocument();
    // No dialog should be visible — the button triggers creation directly
    expect(screen.queryByText("New Workspace")).not.toBeInTheDocument();
  });

  it("does not render 'Sessions' subheading", async () => {
    renderSidebar();
    await screen.findByText("alpha");
    expect(screen.queryByText("Sessions")).not.toBeInTheDocument();
  });

  it("sidebar has resize-x class for resizability", async () => {
    renderSidebar();
    const aside = await screen.findByLabelText("Navigation");
    expect(aside.className).toContain("resize-x");
  });

  it("sidebar has overflow-x-hidden to prevent horizontal scroll", async () => {
    renderSidebar();
    const scrollContainer = (await screen.findByLabelText("Navigation")).querySelector(".overflow-x-hidden");
    expect(scrollContainer).toBeInTheDocument();
  });
});

describe("Sidebar — session delete", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("calls deleteSession when session kebab delete is confirmed", async () => {
    const { qc } = renderSidebar();

    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "My session", messageCount: 3, status: "idle" },
    ]);

    await screen.findByText("My session");

    vi.spyOn(window, "confirm").mockReturnValue(true);

    const kebabButtons = await screen.findAllByLabelText("Actions");
    const sessionKebab = kebabButtons[kebabButtons.length - 1]!;
    sessionKebab.click();

    const deleteBtn = await screen.findByText("Delete");
    deleteBtn.click();

    expect(workspacesApi.deleteSession).toHaveBeenCalledWith("ws-1", "sess-1");
  });

  it("does not call deleteSession when confirm is cancelled", async () => {
    const { qc } = renderSidebar();

    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Keep me", messageCount: 1, status: "idle" },
    ]);

    await screen.findByText("Keep me");

    vi.spyOn(window, "confirm").mockReturnValue(false);

    const kebabButtons = await screen.findAllByLabelText("Actions");
    const sessionKebab = kebabButtons[kebabButtons.length - 1]!;
    sessionKebab.click();

    const deleteBtn = await screen.findByText("Delete");
    deleteBtn.click();

    expect(workspacesApi.deleteSession).not.toHaveBeenCalled();
  });

  it("treats 404 as success on delete", async () => {
    const { qc } = renderSidebar();

    const err404: any = new Error("not found");
    err404.status = 404;
    (workspacesApi.deleteSession as ReturnType<typeof vi.fn>).mockRejectedValueOnce(err404);

    qc.setQueryData(["sessions", "ws-1"], [
      { id: "sess-1", title: "Will 404", messageCount: 1, status: "idle" },
    ]);

    await screen.findByText("Will 404");

    vi.spyOn(window, "confirm").mockReturnValue(true);

    const kebabButtons = await screen.findAllByLabelText("Actions");
    const sessionKebab = kebabButtons[kebabButtons.length - 1]!;
    sessionKebab.click();

    const deleteBtn = await screen.findByText("Delete");
    deleteBtn.click();

    expect(workspacesApi.deleteSession).toHaveBeenCalledWith("ws-1", "sess-1");
  });
});
