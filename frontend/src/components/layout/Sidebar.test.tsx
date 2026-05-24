import { describe, expect, it, vi } from "vitest";
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
  },
}));

function renderSidebar() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <AuthProvider>
        <MemoryRouter>
          <Sidebar />
        </MemoryRouter>
      </AuthProvider>
    </QueryClientProvider>,
  );
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
});
