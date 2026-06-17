import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { OrgSettingsTab } from "./OrgSettingsTab";
import type { OrgResponse } from "../../api/orgs";

const mockList = vi.fn();
const mockCreate = vi.fn();
let mockUser: { role: string } | null = { role: "admin" };

vi.mock("../../api/orgs", () => ({
  orgsApi: {
    list: () => mockList(),
    create: (req: unknown) => mockCreate(req),
  },
}));

vi.mock("../../providers/AuthProvider", () => ({
  useAuth: () => ({ user: mockUser, loading: false }),
}));

const ORG: OrgResponse = {
  id: "org-1",
  name: "Acme",
  slug: "acme",
  createdBy: "admin-1",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  status: "active",
  planId: "enterprise",
  subscriptionStatus: "active",
  userRole: "admin",
  memberCount: 1,
};

function renderTab() {
  return render(
    <MemoryRouter>
      <OrgSettingsTab />
    </MemoryRouter>,
  );
}

describe("OrgSettingsTab", () => {
  beforeEach(() => {
    mockList.mockReset();
    mockCreate.mockReset();
    mockUser = { role: "admin" };
  });

  it("lists organisations", async () => {
    mockList.mockResolvedValue([ORG]);
    renderTab();
    await waitFor(() => expect(screen.getByText("Acme")).toBeInTheDocument());
  });

  it("shows the New Organisation button to a platform admin", async () => {
    mockList.mockResolvedValue([]);
    mockUser = { role: "admin" };
    renderTab();
    await waitFor(() =>
      expect(screen.getByText("New Organisation")).toBeInTheDocument(),
    );
  });

  it("hides the New Organisation button from non-admins", async () => {
    mockList.mockResolvedValue([]);
    mockUser = { role: "user" };
    renderTab();
    await waitFor(() =>
      expect(screen.queryByText("New Organisation")).not.toBeInTheDocument(),
    );
  });

  it("collects owner email + plan and posts them on create", async () => {
    mockList.mockResolvedValue([]);
    mockCreate.mockResolvedValue({});
    mockUser = { role: "admin" };
    const user = userEvent.setup();
    renderTab();

    await waitFor(() =>
      expect(screen.getByText("New Organisation")).toBeInTheDocument(),
    );
    await user.click(screen.getByText("New Organisation"));

    await user.type(
      screen.getByPlaceholderText(/owner email/i),
      "owner@example.com",
    );
    await user.type(
      screen.getByPlaceholderText(/organisation name/i),
      "Acme",
    );
    await user.click(screen.getByText("Create"));

    await waitFor(() => expect(mockCreate).toHaveBeenCalledTimes(1));
    const calls = mockCreate.mock.calls;
    expect(calls.length).toBe(1);
    const req = calls[0]![0] as {
      name: string;
      slug: string;
      ownerEmail: string;
      planId: string;
    };
    expect(req.ownerEmail).toBe("owner@example.com");
    expect(req.name).toBe("Acme");
    expect(req.slug).toBeTruthy();
    expect(req.planId).toBe("enterprise");
  });

  it("surfaces a 'no user' message when the backend returns 404", async () => {
    mockList.mockResolvedValue([]);
    const { ApiClientError } = await import("../../api/client");
    mockCreate.mockRejectedValue(new ApiClientError(404, { error: "owner not found" }));
    mockUser = { role: "admin" };
    const user = userEvent.setup();
    renderTab();

    await waitFor(() =>
      expect(screen.getByText("New Organisation")).toBeInTheDocument(),
    );
    await user.click(screen.getByText("New Organisation"));
    await user.type(
      screen.getByPlaceholderText(/owner email/i),
      "nobody@example.com",
    );
    await user.type(
      screen.getByPlaceholderText(/organisation name/i),
      "Acme",
    );
    await user.click(screen.getByText("Create"));

    await waitFor(() =>
      expect(screen.getByText(/no user found with that owner email/i)).toBeInTheDocument(),
    );
  });
});
