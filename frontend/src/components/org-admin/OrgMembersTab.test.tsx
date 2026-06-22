import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { OrgMembersTab } from "./OrgMembersTab";
import type { OrgMember, OrgResponse, OrgInvitation } from "../../api/orgs";
import { ApiClientError } from "../../api/client";

const mockListMembers = vi.fn();
const mockVerifyMember = vi.fn();
const mockChangeMemberRole = vi.fn();
const mockRemoveMember = vi.fn();
const mockListInvitations = vi.fn();
const mockVerifyInvitee = vi.fn();
const mockResendInvitation = vi.fn();
const mockRevokeInvitation = vi.fn();
const mockOutletContext = vi.fn();

vi.mock("../../api/orgs", () => ({
  orgsApi: {
    listMembers: (id: string) => mockListMembers(id),
    verifyMember: (id: string, userId: string) => mockVerifyMember(id, userId),
    changeMemberRole: (id: string, userId: string, role: string) =>
      mockChangeMemberRole(id, userId, role),
    removeMember: (id: string, userId: string) => mockRemoveMember(id, userId),
    listInvitations: (id: string) => mockListInvitations(id),
    verifyInvitee: (id: string, invId: string) => mockVerifyInvitee(id, invId),
    resendInvitation: (id: string, invId: string) =>
      mockResendInvitation(id, invId),
    revokeInvitation: (id: string, invId: string) =>
      mockRevokeInvitation(id, invId),
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

// Pending invitation fixture used by the Pending Invitations table tests.
// emailVerified does not apply at this stage — there may not even be a
// users row yet.
const PENDING_INVITATION: OrgInvitation = {
  id: "inv-1",
  orgId: "org-1",
  email: "invitee@example.com",
  role: "member",
  invitedBy: "admin-1",
  expiresAt: "2026-12-31T00:00:00Z",
  createdAt: "2026-01-03T00:00:00Z",
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
    mockVerifyInvitee.mockResolvedValue({ message: "User verified" });
    mockResendInvitation.mockResolvedValue(undefined);
    mockRevokeInvitation.mockResolvedValue(undefined);
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

  // ---------------------------------------------------------------------
  // Pending Invitations: force-verify the invitee's account
  // (epic-43 follow-up — closes the gap from PR #343 which only handled
  // already-accepted members)
  // ---------------------------------------------------------------------

  it("renders a Verify button on each pending invitation row (admin)", async () => {
    mockListInvitations.mockResolvedValue([PENDING_INVITATION]);
    renderTab();
    // Disambiguate from the Members-table Verify button (UNVERIFIED_MEMBER's row
    // also has one). The pending-invitation Verify lives in the same DOM but
    // adjacent to Resend/Revoke; checking we have two Verify buttons total
    // confirms both surfaces are wired.
    await screen.findByText("invitee@example.com");
    const verifyButtons = screen.getAllByRole("button", { name: /^Verify$/i });
    expect(verifyButtons.length).toBe(2);
    // Resend + Revoke must still be present.
    expect(screen.getByRole("button", { name: /resend/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /revoke/i })).toBeInTheDocument();
  });

  it("calls orgsApi.verifyInvitee(orgId, invId) when Verify is clicked on a pending invitation", async () => {
    const user = userEvent.setup();
    // Hide the Members-table Verify button by making everyone verified —
    // leaves only the pending-invitation Verify on screen.
    mockListMembers.mockResolvedValue([VERIFIED_ADMIN]);
    mockListInvitations.mockResolvedValue([PENDING_INVITATION]);
    renderTab();
    await screen.findByText("invitee@example.com");
    const verifyBtn = screen.getByRole("button", { name: /^Verify$/i });
    await user.click(verifyBtn);
    await waitFor(() => {
      expect(mockVerifyInvitee).toHaveBeenCalledWith("org-1", "inv-1");
    });
    // Refresh re-fetches both members and invitations.
    await waitFor(() => {
      expect(mockListMembers).toHaveBeenCalledTimes(2);
    });
  });

  it("renders a clear 'must sign up first' message on 422 no_account_for_email", async () => {
    const user = userEvent.setup();
    mockListMembers.mockResolvedValue([VERIFIED_ADMIN]);
    mockListInvitations.mockResolvedValue([PENDING_INVITATION]);
    mockVerifyInvitee.mockRejectedValue(
      new ApiClientError(422, { error: "no_account_for_email" }),
    );
    renderTab();
    const verifyBtn = await screen.findByRole("button", { name: /^Verify$/i });
    await user.click(verifyBtn);
    // The user-visible message must be the friendly explanation, not the
    // raw 'no_account_for_email' code — the frontend's job is to translate
    // machine codes into human guidance.
    await waitFor(() => {
      expect(
        screen.getByText(/no account exists for this email yet/i),
      ).toBeInTheDocument();
      expect(
        screen.getByText(/the invitee must sign up before/i),
      ).toBeInTheDocument();
    });
  });

  it("falls through to a generic error message on non-422 failures", async () => {
    const user = userEvent.setup();
    mockListMembers.mockResolvedValue([VERIFIED_ADMIN]);
    mockListInvitations.mockResolvedValue([PENDING_INVITATION]);
    mockVerifyInvitee.mockRejectedValue(new Error("DB unreachable"));
    renderTab();
    const verifyBtn = await screen.findByRole("button", { name: /^Verify$/i });
    await user.click(verifyBtn);
    await waitFor(() => {
      expect(screen.getByText(/db unreachable/i)).toBeInTheDocument();
    });
  });

  it("hides the pending-invitation Verify button for non-admins", async () => {
    mockListInvitations.mockResolvedValue([PENDING_INVITATION]);
    renderTab(false);
    // Non-admin: the Pending Invitations section is not rendered at all,
    // so the invitee email must not appear.
    await waitFor(() => {
      expect(screen.queryByText("invitee@example.com")).toBeNull();
    });
  });
});
