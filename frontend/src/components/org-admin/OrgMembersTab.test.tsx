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

// Pending invitation fixture used by the Pending Invitations table
// tests. Default shape: invitee already has a users row but their email
// is unverified — i.e. force-verify is actionable. Tests that need a
// different state (already verified, or no account) override the flags.
const PENDING_INVITATION: OrgInvitation = {
  id: "inv-1",
  orgId: "org-1",
  email: "invitee@example.com",
  role: "member",
  invitedBy: "admin-1",
  expiresAt: "2026-12-31T00:00:00Z",
  createdAt: "2026-01-03T00:00:00Z",
  inviteeUserExists: true,
  inviteeEmailVerified: false,
};

const PENDING_INVITATION_ALREADY_VERIFIED: OrgInvitation = {
  ...PENDING_INVITATION,
  id: "inv-already-verified",
  email: "verified-invitee@example.com",
  inviteeUserExists: true,
  inviteeEmailVerified: true,
};

const PENDING_INVITATION_NO_USER: OrgInvitation = {
  ...PENDING_INVITATION,
  id: "inv-no-user",
  email: "ghost@example.com",
  inviteeUserExists: false,
  inviteeEmailVerified: undefined,
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

  // ---------------------------------------------------------------------
  // Account Status column + conditional Verify button
  // (closes the UX gap reported after PR #352 deploy: the Verify button
  // persisted indefinitely after a successful verify because the
  // invitation row stays pending and the prior fixture had no concept
  // of the invitee's verification state).
  // ---------------------------------------------------------------------

  it("hides the Verify button when invitee email is already verified", async () => {
    // Members table has all-verified members so its own Verify button
    // is also absent — leaves the screen with NO Verify buttons total.
    mockListMembers.mockResolvedValue([VERIFIED_ADMIN]);
    mockListInvitations.mockResolvedValue([PENDING_INVITATION_ALREADY_VERIFIED]);
    renderTab();
    await screen.findByText("verified-invitee@example.com");
    expect(screen.queryByRole("button", { name: /^Verify$/i })).toBeNull();
  });

  it("renders a 'Verified' badge in the Account Status column for already-verified invitees", async () => {
    mockListMembers.mockResolvedValue([VERIFIED_ADMIN]);
    mockListInvitations.mockResolvedValue([PENDING_INVITATION_ALREADY_VERIFIED]);
    renderTab();
    await screen.findByText("verified-invitee@example.com");
    // 2 Verified badges are visible — one from the Members table for
    // VERIFIED_ADMIN, one from the Pending Invitations table for the
    // already-verified invitee. Confirms the new column renders.
    const badges = screen.getAllByText(/^Verified$/i);
    expect(badges.length).toBeGreaterThanOrEqual(2);
  });

  it("hides the Verify button when invitee has no account yet (button can't help, would 422)", async () => {
    mockListMembers.mockResolvedValue([VERIFIED_ADMIN]);
    mockListInvitations.mockResolvedValue([PENDING_INVITATION_NO_USER]);
    renderTab();
    await screen.findByText("ghost@example.com");
    // Verify button must be absent — clicking it would 422.
    expect(screen.queryByRole("button", { name: /^Verify$/i })).toBeNull();
    // A "No account" badge is shown so the admin understands why the
    // button is missing.
    expect(screen.getByText(/no account/i)).toBeInTheDocument();
  });

  it("shows a success notice after Verify succeeds (acknowledgement was missing pre-fix)", async () => {
    const user = userEvent.setup();
    mockListMembers.mockResolvedValue([VERIFIED_ADMIN]);
    mockListInvitations.mockResolvedValue([PENDING_INVITATION]);
    renderTab();
    const verifyBtn = await screen.findByRole("button", { name: /^Verify$/i });
    await user.click(verifyBtn);
    await waitFor(() => {
      expect(mockVerifyInvitee).toHaveBeenCalledWith("org-1", "inv-1");
    });
    // The user-visible notice must include the email so the admin
    // knows which row's verification succeeded — a generic "verified"
    // wouldn't be enough for tables with multiple pending invitations.
    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveTextContent(
        /Verified invitee@example\.com/i,
      );
    });
  });

  it("clears the success notice when a subsequent action errors", async () => {
    const user = userEvent.setup();
    mockListMembers.mockResolvedValue([VERIFIED_ADMIN]);
    mockListInvitations.mockResolvedValue([PENDING_INVITATION]);
    // First click succeeds, second click fails.
    mockVerifyInvitee
      .mockResolvedValueOnce({ message: "User verified" })
      .mockRejectedValueOnce(new Error("DB unreachable"));
    renderTab();
    const verifyBtn = await screen.findByRole("button", { name: /^Verify$/i });

    // First click: success notice appears.
    await user.click(verifyBtn);
    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveTextContent(
        /Verified invitee@example\.com/i,
      );
    });

    // Force a second click on the (still rendered, since refresh keeps
    // the same fixture) Verify button. The mock now rejects.
    await user.click(verifyBtn);
    await waitFor(() => {
      expect(screen.getByText(/db unreachable/i)).toBeInTheDocument();
    });
    // The success notice must be gone — the user should not see a stale
    // green "Verified" message after an error.
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("renders the em-dash fallback when invitee account state is unknown (older API response)", async () => {
    // Older API responses or stale browser caches won't include the new
    // inviteeUserExists / inviteeEmailVerified fields. The Account
    // Status column must render an em-dash placeholder rather than
    // crashing or rendering an empty cell.
    const PENDING_INVITATION_OLD_API: OrgInvitation = {
      id: "inv-old-api",
      orgId: "org-1",
      email: "old-api@example.com",
      role: "member",
      invitedBy: "admin-1",
      expiresAt: "2026-12-31T00:00:00Z",
      createdAt: "2026-01-03T00:00:00Z",
      // inviteeUserExists / inviteeEmailVerified intentionally absent
    };
    mockListMembers.mockResolvedValue([VERIFIED_ADMIN]);
    mockListInvitations.mockResolvedValue([PENDING_INVITATION_OLD_API]);
    renderTab();
    await screen.findByText("old-api@example.com");
    // The em-dash is rendered when both flags are undefined.
    expect(screen.getByText("—")).toBeInTheDocument();
    // The Verify button is also absent since `undefined === true` is
    // false — the conditional render correctly handles the unknown case.
    expect(screen.queryByRole("button", { name: /^Verify$/i })).toBeNull();
  });

  it("shows a success notice after Resend succeeds", async () => {
    const user = userEvent.setup();
    mockListMembers.mockResolvedValue([VERIFIED_ADMIN]);
    mockListInvitations.mockResolvedValue([PENDING_INVITATION]);
    renderTab();
    const resendBtn = await screen.findByRole("button", { name: /resend/i });
    await user.click(resendBtn);
    await waitFor(() => {
      expect(mockResendInvitation).toHaveBeenCalledWith("org-1", "inv-1");
    });
    // The notice must reference the invitee's email so the admin knows
    // which row's resend succeeded.
    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveTextContent(
        /Invitation resent to invitee@example\.com/i,
      );
    });
  });

  it("shows a success notice after Revoke succeeds", async () => {
    const user = userEvent.setup();
    mockListMembers.mockResolvedValue([VERIFIED_ADMIN]);
    mockListInvitations.mockResolvedValue([PENDING_INVITATION]);
    renderTab();
    const revokeBtn = await screen.findByRole("button", { name: /revoke/i });
    await user.click(revokeBtn);
    await waitFor(() => {
      expect(mockRevokeInvitation).toHaveBeenCalledWith("org-1", "inv-1");
    });
    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveTextContent(
        /Invitation revoked for invitee@example\.com/i,
      );
    });
  });

  it("clears the Resend success notice when a subsequent action errors", async () => {
    const user = userEvent.setup();
    mockListMembers.mockResolvedValue([VERIFIED_ADMIN]);
    mockListInvitations.mockResolvedValue([PENDING_INVITATION]);
    mockResendInvitation.mockResolvedValue(undefined);
    mockRevokeInvitation.mockRejectedValue(new Error("Revoke transient failure"));
    renderTab();

    // First: Resend succeeds → notice appears
    await user.click(await screen.findByRole("button", { name: /resend/i }));
    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveTextContent(/Invitation resent/i);
    });

    // Then: Revoke fails → error appears, notice clears
    await user.click(screen.getByRole("button", { name: /revoke/i }));
    await waitFor(() => {
      expect(
        screen.getByText(/revoke transient failure/i),
      ).toBeInTheDocument();
    });
    expect(screen.queryByRole("status")).toBeNull();
  });
});
