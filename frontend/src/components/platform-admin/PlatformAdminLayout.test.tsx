import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

const authState = vi.hoisted(() => ({
  user: { id: "1", role: "admin" } as { id: string; role: string } | null,
}));

vi.mock("../../providers/AuthProvider", () => ({
  useAuth: () => ({ user: authState.user, loading: false }),
}));

import { PlatformAdminLayout } from "./PlatformAdminLayout";

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/admin" element={<PlatformAdminLayout />}>
          <Route path="users" element={<div>users content</div>} />
          <Route path="audit" element={<div>audit content</div>} />
        </Route>
        <Route path="/chat" element={<div>chat content</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("PlatformAdminLayout", () => {
  beforeEach(() => {
    authState.user = { id: "1", role: "admin" };
  });

  it("renders the portal title in the header", () => {
    renderAt("/admin/users");
    expect(screen.getByText("Platform Administration")).toBeInTheDocument();
  });

  it("renders a back link to chat", () => {
    renderAt("/admin/users");
    expect(screen.getByRole("link", { name: /Back to Chat/ })).toHaveAttribute("href", "/chat");
  });

  it("renders all admin section nav items", () => {
    renderAt("/admin/users");
    expect(screen.getByRole("link", { name: "Users" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Organisations" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Credentials" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Relay" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Settings" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Audit" })).toBeInTheDocument();
  });

  it("renders the active section content", () => {
    renderAt("/admin/users");
    expect(screen.getByText("users content")).toBeInTheDocument();
  });

  it("denies access for non-admin users and hides the portal", () => {
    authState.user = { id: "2", role: "user" };
    renderAt("/admin/users");
    expect(screen.getByText(/Platform administrator access required/i)).toBeInTheDocument();
    expect(screen.queryByText("Platform Administration")).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Users" })).not.toBeInTheDocument();
  });

  it("denies access when user is null", () => {
    authState.user = null;
    renderAt("/admin/users");
    expect(screen.getByText(/Platform administrator access required/i)).toBeInTheDocument();
  });
});
