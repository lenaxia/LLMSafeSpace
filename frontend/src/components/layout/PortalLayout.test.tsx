import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useOutletContext } from "react-router-dom";
import { PortalLayout, type NavItem } from "./PortalLayout";

const NAV_ITEMS: NavItem[] = [
  { to: "overview", label: "Overview" },
  { to: "settings", label: "Settings" },
];

function renderPortal(initialPath = "/portal/overview") {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route path="/portal" element={<PortalLayout title="Test Portal" backLink="/chat" navItems={NAV_ITEMS} context={null} />}>
          <Route path="overview" element={<div>overview content</div>} />
          <Route path="settings" element={<div>settings content</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe("PortalLayout", () => {
  it("renders the title in the header", () => {
    renderPortal();
    expect(screen.getByText("Test Portal")).toBeInTheDocument();
  });

  it("renders a back link", () => {
    renderPortal();
    const link = screen.getByText(/Back to Chat/);
    expect(link).toHaveAttribute("href", "/chat");
  });

  it("renders all nav items", () => {
    renderPortal();
    expect(screen.getByText("Overview")).toBeInTheDocument();
    expect(screen.getByText("Settings")).toBeInTheDocument();
  });

  it("renders the outlet content for the active route", () => {
    renderPortal("/portal/overview");
    expect(screen.getByText("overview content")).toBeInTheDocument();
  });

  it("renders custom back label", () => {
    render(
      <MemoryRouter initialEntries={["/portal/overview"]}>
        <Routes>
          <Route path="/portal" element={<PortalLayout title="T" backLink="/home" backLabel="Home" navItems={[]} context={null} />}>
            <Route path="overview" element={<div>content</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.getByText("← Home")).toBeInTheDocument();
  });

  it("renders badges in the header when provided", () => {
    render(
      <MemoryRouter initialEntries={["/portal/overview"]}>
        <Routes>
          <Route path="/portal" element={<PortalLayout title="T" backLink="/chat" navItems={[]} context={null} badges={<span data-testid="badge">PRO</span>} />}>
            <Route path="overview" element={<div>content</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.getByTestId("badge")).toBeInTheDocument();
  });

  it("renders meta in the header when provided", () => {
    render(
      <MemoryRouter initialEntries={["/portal/overview"]}>
        <Routes>
          <Route path="/portal" element={<PortalLayout title="T" backLink="/chat" navItems={[]} context={null} meta={<span data-testid="meta">5 users</span>} />}>
            <Route path="overview" element={<div>content</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.getByTestId("meta")).toBeInTheDocument();
  });

  it("renders as full-screen without AppShell", () => {
    const { container } = renderPortal();
    const root = container.firstElementChild;
    expect(root).toHaveClass("h-screen");
  });

  it("propagates the context prop to child routes via useOutletContext", () => {
    const ctx = { org: { id: "org-1" }, isAdmin: true };

    function ContextReader() {
      const c = useOutletContext<typeof ctx>();
      return <span data-testid="ctx">{JSON.stringify(c)}</span>;
    }

    render(
      <MemoryRouter initialEntries={["/portal/overview"]}>
        <Routes>
          <Route path="/portal" element={<PortalLayout title="T" backLink="/chat" navItems={[]} context={ctx} />}>
            <Route path="overview" element={<ContextReader />} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );

    const ctxEl = screen.getByTestId("ctx");
    expect(ctxEl.textContent).toContain("org-1");
    expect(ctxEl.textContent).toContain('"isAdmin":true');
  });
});
