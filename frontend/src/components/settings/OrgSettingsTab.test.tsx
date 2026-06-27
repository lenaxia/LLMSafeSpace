import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { OrgSettingsTab } from "./OrgSettingsTab";
import type { OrgSummary } from "../../api/orgs";

const mockListOrgs = vi.fn();
const mockCreate = vi.fn();
const mockSuspendOrg = vi.fn();
const mockUnsuspendOrg = vi.fn();

vi.mock("../../api/orgs", () => ({
  orgsApi: {
    create: (req: unknown) => mockCreate(req),
    delete: vi.fn(),
  },
  adminPlatformApi: {
    listOrgs: (filters: unknown) => mockListOrgs(filters),
    suspendOrg: (id: string) => mockSuspendOrg(id),
    unsuspendOrg: (id: string) => mockUnsuspendOrg(id),
  },
}));

vi.mock("../../providers/AuthProvider", () => ({
  useAuth: () => ({ user: { role: "admin" }, loading: false }),
}));

const ORG_ACTIVE: OrgSummary = {
  id: "org-1",
  name: "Acme",
  slug: "acme",
  createdBy: "admin-1",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  status: "active",
  planId: "enterprise",
  subscriptionStatus: "active",
  memberCount: 3,
  workspaceCount: 5,
};

const ORG_SUSPENDED: OrgSummary = {
  ...ORG_ACTIVE,
  id: "org-2",
  name: "Globex",
  slug: "globex",
  status: "suspended",
  planId: "team",
  memberCount: 1,
  workspaceCount: 2,
};

function listResponse(items: OrgSummary[], total = items.length) {
  return {
    items,
    pagination: { total, start: 0, end: items.length, limit: 20, offset: 0 },
  };
}

function renderTab() {
  return render(
    <MemoryRouter>
      <OrgSettingsTab />
    </MemoryRouter>,
  );
}

describe("OrgSettingsTab", () => {
  beforeEach(() => {
    mockListOrgs.mockReset();
    mockCreate.mockReset();
    mockSuspendOrg.mockReset();
    mockUnsuspendOrg.mockReset();
  });

  it("lists organisations with member + workspace counts", async () => {
    mockListOrgs.mockResolvedValue(listResponse([ORG_ACTIVE, ORG_SUSPENDED]));
    renderTab();
    await waitFor(() => expect(screen.getByText("Acme")).toBeInTheDocument());
    expect(screen.getByText("Globex")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
    expect(screen.getByText("5")).toBeInTheDocument();
  });

  it("renders status badges for each org", async () => {
    mockListOrgs.mockResolvedValue(listResponse([ORG_ACTIVE, ORG_SUSPENDED]));
    renderTab();
    await waitFor(() => expect(screen.getByText("Acme")).toBeInTheDocument());
    expect(screen.getByText("active")).toBeInTheDocument();
    expect(screen.getByText("suspended")).toBeInTheDocument();
  });

  it("shows a Suspend action for active orgs and Unsuspend for suspended", async () => {
    mockListOrgs.mockResolvedValue(listResponse([ORG_ACTIVE, ORG_SUSPENDED]));
    renderTab();
    await waitFor(() => expect(screen.getByText("Acme")).toBeInTheDocument());
    const suspendButtons = screen.getAllByText("Suspend");
    const unsuspendButtons = screen.getAllByText("Unsuspend");
    expect(suspendButtons).toHaveLength(1);
    expect(unsuspendButtons).toHaveLength(1);
  });

  it("calls suspendOrg then refreshes on confirm", async () => {
    window.confirm = vi.fn(() => true);
    mockListOrgs
      .mockResolvedValueOnce(listResponse([ORG_ACTIVE]))
      .mockResolvedValueOnce(
        listResponse([{ ...ORG_ACTIVE, status: "suspended" }]),
      );
    mockSuspendOrg.mockResolvedValue({ status: "suspended" });
    const user = userEvent.setup();
    renderTab();
    await waitFor(() => expect(screen.getByText("Suspend")).toBeInTheDocument());
    await user.click(screen.getByText("Suspend"));
    await waitFor(() => expect(mockSuspendOrg).toHaveBeenCalledWith("org-1"));
    await waitFor(() => expect(mockListOrgs).toHaveBeenCalledTimes(2));
  });

  it("does not call suspendOrg when the confirm is cancelled", async () => {
    window.confirm = vi.fn(() => false);
    mockListOrgs.mockResolvedValue(listResponse([ORG_ACTIVE]));
    const user = userEvent.setup();
    renderTab();
    await waitFor(() => expect(screen.getByText("Suspend")).toBeInTheDocument());
    await user.click(screen.getByText("Suspend"));
    expect(mockSuspendOrg).not.toHaveBeenCalled();
  });

  it("calls unsuspendOrg for a suspended org", async () => {
    mockListOrgs
      .mockResolvedValueOnce(listResponse([ORG_SUSPENDED]))
      .mockResolvedValueOnce(listResponse([{ ...ORG_SUSPENDED, status: "active" }]));
    mockUnsuspendOrg.mockResolvedValue({ status: "active" });
    const user = userEvent.setup();
    renderTab();
    await waitFor(() => expect(screen.getByText("Unsuspend")).toBeInTheDocument());
    await user.click(screen.getByText("Unsuspend"));
    await waitFor(() => expect(mockUnsuspendOrg).toHaveBeenCalledWith("org-2"));
  });

  it("opens the create form and posts owner email + plan", async () => {
    mockListOrgs.mockResolvedValue(listResponse([]));
    mockCreate.mockResolvedValue({});
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
    const req = mockCreate.mock.calls[0]![0] as {
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
    mockListOrgs.mockResolvedValue(listResponse([]));
    const { ApiClientError } = await import("../../api/client");
    mockCreate.mockRejectedValue(new ApiClientError(404, { error: "owner not found" }));
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

  it("forwards the status filter to the list call", async () => {
    mockListOrgs.mockResolvedValue(listResponse([]));
    const user = userEvent.setup();
    renderTab();
    await waitFor(() => expect(mockListOrgs).toHaveBeenCalled());
    await user.selectOptions(screen.getByDisplayValue("All statuses"), "suspended");
    await waitFor(() => {
      const calls = mockListOrgs.mock.calls;
      const lastCall = calls[calls.length - 1]![0] as { status?: string };
      expect(lastCall.status).toBe("suspended");
    });
  });

  it("surfaces backend per-field validation details on 400 with a friendly label", async () => {
    mockListOrgs.mockResolvedValue(listResponse([]));
    const { ApiClientError } = await import("../../api/client");
    mockCreate.mockRejectedValue(
      new ApiClientError(400, {
        error: "validation failed",
        details: {
          slug: "Must be letters, digits, and single hyphens between segments (e.g. \"my-org\")",
        },
      }),
    );
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
    await waitFor(() =>
      expect(screen.getByText(/^Slug:.*letters, digits/i)).toBeInTheDocument(),
    );
  });
});
