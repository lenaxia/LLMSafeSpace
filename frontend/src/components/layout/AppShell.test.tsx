import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AppShell } from "./AppShell";
import { AuthProvider } from "../../providers/AuthProvider";

vi.mock("../../api/auth", () => ({
  authApi: {
    me: vi.fn().mockResolvedValue({ id: "u1", username: "testuser", email: "t@t.com", role: "user", active: true }),
  },
}));

vi.mock("../../api/workspaces", () => ({
  workspacesApi: {
    list: vi.fn().mockResolvedValue({ items: [], pagination: { limit: 20, offset: 0, total: 0 } }),
  },
}));

describe("AppShell", () => {
  it("renders sidebar and outlet", async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <AuthProvider>
          <MemoryRouter initialEntries={["/chat"]}>
            <Routes>
              <Route element={<AppShell />}>
                <Route path="/chat" element={<div>Chat Content</div>} />
              </Route>
            </Routes>
          </MemoryRouter>
        </AuthProvider>
      </QueryClientProvider>,
    );

    // App title appears (may be in sidebar + mobile top bar)
    const titles = await screen.findAllByText("Safe Space");
    expect(titles.length).toBeGreaterThan(0);
    // Outlet renders child
    expect(screen.getByText("Chat Content")).toBeInTheDocument();
  });

  it("has skip-to-content link", async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <AuthProvider>
          <MemoryRouter initialEntries={["/chat"]}>
            <Routes>
              <Route element={<AppShell />}>
                <Route path="/chat" element={<div>Content</div>} />
              </Route>
            </Routes>
          </MemoryRouter>
        </AuthProvider>
      </QueryClientProvider>,
    );

    const skipLink = await screen.findByText("Skip to content");
    expect(skipLink).toHaveAttribute("href", "#main-content");
  });

  it("has main content landmark", async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <AuthProvider>
          <MemoryRouter initialEntries={["/chat"]}>
            <Routes>
              <Route element={<AppShell />}>
                <Route path="/chat" element={<div>Content</div>} />
              </Route>
            </Routes>
          </MemoryRouter>
        </AuthProvider>
      </QueryClientProvider>,
    );

    const main = await screen.findByRole("main");
    expect(main).toHaveAttribute("id", "main-content");
  });
});
