import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { OrgMembersTab } from "./OrgMembersTab";
import type { OrgMember, OrgResponse } from "../../api/orgs";

const mockListMembers = vi.fn();
const mockVerifyMember = vi.fn();
const mockChangeMemberRole = vi.fn();
const mockRemoveMember = vi.fn();
const mockListInvitations = vi.fn();
const mockOutletContext = vi.fn();

vi.mock("../../api/orgs", () => ({
  orgsApi: {
    listMembers: (id: string) => mockListMembers(id),
    verifyMember: (id: string, userId: string) => mockVerifyMember(id, userId),
    changeMemberRole: (id: string, userId: string, role: string) =>
      mockChangeMemberRole(id, userId, role),
    removeMember: (id: string, userId: string) => mockRemoveMember(id, userId),
    listInvitations: (id: string) => mockListInvitations(id),
  },
}));

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>(
    "react-router-dom",
  );
  return {
    ...actual,
    useOutletContext: () => mockOutletContext(),
  };
});

const ORG: OrgResponse = {
  id: "org-1",
  name: "Acme",
  slug: "acme",
  createdBy: "u-1",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  status: "active",
  planId: "team",
  subscriptionStatus: "active",
  userRole: "admin",
  memberCount: 2,
};

const VERIFIED_ADMIN: OrgMember = {
  orgId: "org-1",
  userId: "admin-1",
  username: "alice",
  email: "alice@example.com",
  role: "admin",
  emailVerified: true,
  createdAt: "2026-01-01T00:00:00Z",
};

const UNVERIFIED_MEMBER: OrgMember = {
  orgId: "org-1",
  userId: "member-1",
  username: "bob",
  email: "bob@example.com",
  role: "member",
  emailVerified: false,
  createdAt: "2026-01-02T00:00:00Z",
};

function renderTab(isAdmin = true) {
  mockOutletContext.mockReturnValue({ org: ORG, isAdmin });
  return render(
    <MemoryRouter>
      <OrgMembersTab />
    </MemoryRouter>,
  );
}

describe("OrgMembersTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockListMembers.mockResolvedValue([VERIFIED_ADMIN, UNVERIFIED_MEMBER]);
    mockListInvitations.mockResolvedValue([]);
    mockVerifyMember.mockResolvedValue({ message: "Member verified" });
  });

  it("loads members via orgsApi.listMembers", async () => {
    renderTab();
    await waitFor(() => {
      expect(mockListMembers).toHaveBeenCalledWith("org-1");
    });
    expect(await screen.findByText("alice")).toBeInTheDocument();
    expect(await screen.findByText("bob")).toBeInTheDocument();
  });

  it("renders the Email Status column header", async () => {
    renderTab();
    expect(await screen.findByText("Email Status")).toBeInTheDocument();
  });

  it("shows Verified badge for emailVerified members and Pending for unverified", async () => {
    renderTab();
    await screen.findByText("bob");
    expect(screen.getByText("Verified")).toBeInTheDocument();
    expect(screen.getByText("Pending")).toBeInTheDocument();
  });

  it("shows a Verify button only for unverified members", async () => {
    renderTab();
    await screen.findByText("bob");
    const verifyButtons = screen.getAllByRole("button", { name: /^Verify$/i });
    expect(verifyButtons).toHaveLength(1);
  });

  it("calls orgsApi.verifyMember and refreshes when Verify is clicked", async () => {
    const user = userEvent.setup();
    renderTab();
    const verifyBtn = await screen.findByRole("button", { name: /^Verify$/i });
    await user.click(verifyBtn);
    await waitFor(() => {
      expect(mockVerifyMember).toHaveBeenCalledWith("org-1", "member-1");
    });
    // refresh() re-fetches members — listMembers is called once on mount and
    // once after onChanged fires.
    await waitFor(() => {
      expect(mockListMembers).toHaveBeenCalledTimes(2);
    });
  });

  it("hides action buttons when isAdmin is false (non-admin view)", async () => {
    renderTab(false);
    await screen.findByText("bob");
    expect(screen.queryByRole("button", { name: /^Verify$/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /remove/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /promote|demote/i })).toBeNull();
  });

  it("surfaces an error message when verifyMember fails (no swallowed errors)", async () => {
    const user = userEvent.setup();
    mockVerifyMember.mockRejectedValue(new Error("server is down"));
    renderTab();
    const verifyBtn = await screen.findByRole("button", { name: /^Verify$/i });
    await user.click(verifyBtn);
    // The error must be visible to the user — not swallowed.
    await waitFor(() => {
      expect(screen.getByText(/server is down/i)).toBeInTheDocument();
    });
  });

  it("surfaces a fallback message when verifyMember rejects with a non-Error", async () => {
    const user = userEvent.setup();
    mockVerifyMember.mockRejectedValue("network dropped");
    renderTab();
    const verifyBtn = await screen.findByRole("button", { name: /^Verify$/i });
    await user.click(verifyBtn);
    await waitFor(() => {
      expect(screen.getByText(/verify failed/i)).toBeInTheDocument();
    });
  });
});
