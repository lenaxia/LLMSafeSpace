import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { OrgCard } from "./OrgSettingsTab";
import type { OrgResponse } from "../../api/orgs";

const ORG_ADMIN: OrgResponse = {
  id: "aaa-bbb-ccc",
  name: "Acme Corp",
  slug: "acme-corp",
  createdBy: "user-1",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  status: "active",
  planId: "enterprise",
  subscriptionStatus: "active",
  userRole: "admin",
  memberCount: 3,
};

const ORG_MEMBER: OrgResponse = {
  ...ORG_ADMIN,
  userRole: "member",
};

function renderCard(org: OrgResponse) {
  return render(
    <MemoryRouter>
      <OrgCard org={org} onDeleted={vi.fn()} />
    </MemoryRouter>,
  );
}

describe("OrgCard", () => {
  it("renders Manage link targeting /orgs/:id for admin", () => {
    renderCard(ORG_ADMIN);
    const link = screen.getByText("Manage");
    expect(link).toBeInTheDocument();
    expect(link).toHaveAttribute("href", "/orgs/aaa-bbb-ccc");
  });

  it("renders Manage link targeting /orgs/:id for member", () => {
    renderCard(ORG_MEMBER);
    const link = screen.getByText("Manage");
    expect(link).toBeInTheDocument();
    expect(link).toHaveAttribute("href", "/orgs/aaa-bbb-ccc");
  });

  it("shows Delete button for admin", () => {
    renderCard(ORG_ADMIN);
    expect(screen.getByText("Delete")).toBeInTheDocument();
  });

  it("hides Delete button for member", () => {
    renderCard(ORG_MEMBER);
    expect(screen.queryByText("Delete")).not.toBeInTheDocument();
  });

  it("renders org name and slug", () => {
    renderCard(ORG_ADMIN);
    expect(screen.getByText("Acme Corp")).toBeInTheDocument();
    expect(screen.getByText("acme-corp")).toBeInTheDocument();
  });

  it("renders member count with singular label", () => {
    renderCard({ ...ORG_ADMIN, memberCount: 1 });
    expect(screen.getByText("1 member")).toBeInTheDocument();
  });

  it("renders member count with plural label", () => {
    renderCard(ORG_ADMIN);
    expect(screen.getByText("3 members")).toBeInTheDocument();
  });

  it("renders user role badge", () => {
    renderCard(ORG_ADMIN);
    expect(screen.getByText("admin")).toBeInTheDocument();
  });
});
