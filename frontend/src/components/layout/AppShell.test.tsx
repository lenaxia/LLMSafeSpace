import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
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

vi.mock("../../providers/SessionActivityProvider", () => ({
  useClearPendingUnread: () => () => {},
  useIsSessionBusy: () => false,
  useIsSessionUnread: () => false,
  useWorkspaceBusyCount: () => 0,
  SessionActivityProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

function renderWithDataRouter(initialPath: string, childElement: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter(
    [{ path: "/", element: <AppShell />, children: [{ path: "chat", element: childElement }] }],
    { initialEntries: [initialPath] },
  );
  return render(
    <QueryClientProvider client={qc}>
      <AuthProvider>
        <RouterProvider router={router} />
      </AuthProvider>
    </QueryClientProvider>,
  );
}

describe("AppShell", () => {
  it("renders sidebar and outlet", async () => {
    renderWithDataRouter("/chat", <div>Chat Content</div>);
    const titles = await screen.findAllByText("Safe Space");
    expect(titles.length).toBeGreaterThan(0);
    expect(screen.getByText("Chat Content")).toBeInTheDocument();
  });

  it("has skip-to-content link", async () => {
    renderWithDataRouter("/chat", <div>Content</div>);
    const skipLink = await screen.findByText("Skip to content");
    expect(skipLink).toHaveAttribute("href", "#main-content");
  });

  it("has main content landmark", async () => {
    renderWithDataRouter("/chat", <div>Content</div>);
    const main = await screen.findByRole("main");
    expect(main).toHaveAttribute("id", "main-content");
  });
});
