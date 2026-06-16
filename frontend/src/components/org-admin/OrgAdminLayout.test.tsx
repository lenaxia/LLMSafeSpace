import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { OrgAdminLayout } from "./OrgAdminLayout";

const mockGet = vi.fn();

vi.mock("../../api/orgs", () => ({
  orgsApi: {
    get: (id: string) => mockGet(id),
  },
}));

const ORG_ADMIN = {
  id: "aaa-bbb-ccc",
  name: "Acme Corp",
  slug: "acme-corp",
  createdBy: "user-1",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  status: "active" as const,
  planId: "enterprise" as const,
  subscriptionStatus: "active" as const,
  userRole: "admin" as const,
  memberCount: 3,
};

const ORG_MEMBER = {
  ...ORG_ADMIN,
  userRole: "member" as const,
};

function renderLayout(orgId: string) {
  return render(
    <MemoryRouter initialEntries={[`/orgs/${orgId}`]}>
      <Routes>
        <Route path="/orgs/:id" element={<OrgAdminLayout />}>
          <Route index element={<div>overview</div>} />
          <Route path="overview" element={<div>overview</div>} />
          <Route path="members" element={<div>members</div>} />
          <Route path="credentials" element={<div>credentials</div>} />
          <Route path="workspaces" element={<div>workspaces</div>} />
          <Route path="audit" element={<div>audit</div>} />
          <Route path="billing" element={<div>billing</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("OrgAdminLayout", () => {
  it("resolves org via orgsApi.get with the route id param", async () => {
    mockGet.mockResolvedValue(ORG_ADMIN);
    renderLayout("aaa-bbb-ccc");
    await waitFor(() => screen.getByText("Acme Corp"));
    expect(mockGet).toHaveBeenCalledWith("aaa-bbb-ccc");
  });

  it("renders the org name in the header", async () => {
    mockGet.mockResolvedValue(ORG_ADMIN);
    renderLayout("aaa-bbb-ccc");
    await waitFor(() => screen.getByText("Acme Corp"));
    expect(screen.getByText("Acme Corp")).toBeInTheDocument();
  });

  it("renders member count", async () => {
    mockGet.mockResolvedValue(ORG_ADMIN);
    renderLayout("aaa-bbb-ccc");
    await waitFor(() => screen.getByText("Acme Corp"));
    expect(screen.getByText(/3 members/)).toBeInTheDocument();
  });

  it("shows admin-only nav items when user is admin", async () => {
    mockGet.mockResolvedValue(ORG_ADMIN);
    renderLayout("aaa-bbb-ccc");
    await waitFor(() => screen.getByText("Acme Corp"));
    expect(screen.getByText("Members")).toBeInTheDocument();
    expect(screen.getByText("Credentials")).toBeInTheDocument();
    expect(screen.getByText("Audit")).toBeInTheDocument();
    expect(screen.getByText("Billing")).toBeInTheDocument();
  });

  it("hides admin-only nav items when user is member", async () => {
    mockGet.mockResolvedValue(ORG_MEMBER);
    renderLayout("aaa-bbb-ccc");
    await waitFor(() => screen.getByText("Acme Corp"));
    expect(screen.queryByText("Members")).not.toBeInTheDocument();
    expect(screen.queryByText("Credentials")).not.toBeInTheDocument();
    expect(screen.queryByText("Audit")).not.toBeInTheDocument();
    expect(screen.queryByText("Billing")).not.toBeInTheDocument();
  });

  it("shows member-shared nav items for all roles", async () => {
    mockGet.mockResolvedValue(ORG_MEMBER);
    renderLayout("aaa-bbb-ccc");
    await waitFor(() => screen.getByText("Acme Corp"));
    expect(screen.getByText("Overview")).toBeInTheDocument();
    expect(screen.getByText("Workspaces")).toBeInTheDocument();
  });

  it("shows error message on API failure", async () => {
    mockGet.mockRejectedValue(new Error("server explosion"));
    renderLayout("aaa-bbb-ccc");
    await waitFor(() => {
      expect(screen.getByText("server explosion")).toBeInTheDocument();
    });
  });

  it("renders Back to Chat link in header", async () => {
    mockGet.mockResolvedValue(ORG_ADMIN);
    renderLayout("aaa-bbb-ccc");
    await waitFor(() => screen.getByText("Acme Corp"));
    expect(screen.getByText("← Back to Chat")).toHaveAttribute("href", "/chat");
  });
});
