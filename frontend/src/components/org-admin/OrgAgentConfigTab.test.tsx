import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { ToastProvider } from "../../providers/ToastProvider";
import { OrgAgentConfigTab } from "./OrgAgentConfigTab";
import type { OrgResponse } from "../../api/orgs";

const mockGetOrgPrompt = vi.fn();
const mockListOrgRoles = vi.fn();
const mockListPlatformRoles = vi.fn();
const mockOutletContext = vi.fn();

vi.mock("../../api/prompts", () => ({
  promptsApi: {
    getOrg: (id: string) => mockGetOrgPrompt(id),
    setOrg: vi.fn(),
  },
}));

vi.mock("../../api/agentRoles", () => ({
  agentRolesApi: {
    listOrg: (id: string) => mockListOrgRoles(id),
    listPlatform: () => mockListPlatformRoles(),
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

function renderTab() {
  mockOutletContext.mockReturnValue({ org: ORG, isAdmin: true });
  return render(
    <ToastProvider>
      <MemoryRouter>
        <OrgAgentConfigTab />
      </MemoryRouter>
    </ToastProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mockGetOrgPrompt.mockResolvedValue({ prompt: "", allowUserPrompt: true });
  mockListOrgRoles.mockResolvedValue([]);
  mockListPlatformRoles.mockResolvedValue([]);
});

// LLMSafeSpaces#480: the org-admin Agent Config tab and the workspace
// Custom Instructions drawer describe their fields using internal
// prompt-chain implementation terminology ("platform prompt", "role's
// system prompt", "Overlay", "appended"). Org admins and end users don't
// need to see this jargon — it leaks architectural detail into UX copy.
// These tests assert the rewritten copy renders AND the leaky terms are
// absent from the rendered DOM.
describe("OrgAgentConfigTab — copy hides implementation jargon (#480)", () => {
  it("renders the rewritten card title (no 'Overlay' jargon)", async () => {
    renderTab();
    await waitFor(() =>
      expect(
        screen.getByText("Organization Agent Prompt Customization"),
      ).toBeInTheDocument(),
    );
    // The old "Org Prompt Overlay" string must be gone.
    expect(screen.queryByText(/Overlay/)).not.toBeInTheDocument();
  });

  it("renders the rewritten helper text (no 'platform prompt' or 'append')", async () => {
    renderTab();
    await waitFor(() =>
      expect(
        screen.getByText(/Members will follow these instructions/),
      ).toBeInTheDocument(),
    );
    expect(screen.queryByText(/platform prompt/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Appended/i)).not.toBeInTheDocument();
  });

  it("renders the rewritten toggle-card description (no 'agent roles' jargon)", async () => {
    renderTab();
    await waitFor(() =>
      expect(
        screen.getByText(
          /When enabled, members can customize the agent's instructions/,
        ),
      ).toBeInTheDocument(),
    );
    // "agent roles" as a concept must not appear in the toggle-card prose.
    // (Note: the page may still have an Agent Roles section heading
    // elsewhere — that's a separate feature surface and intentionally
    // named. We're only scrubbing this prompt-toggle card here.)
    const toggleCardText = screen.getByText(
      /When enabled, members can customize the agent's instructions/,
    );
    expect(toggleCardText.textContent).not.toMatch(/agent roles/i);
  });

  it("renders the rewritten allow-disabled caption (no 'locked to org defaults')", async () => {
    mockGetOrgPrompt.mockResolvedValue({ prompt: "", allowUserPrompt: false });
    renderTab();
    await waitFor(() =>
      expect(
        screen.getByText("Members get the organization's default agent"),
      ).toBeInTheDocument(),
    );
    expect(
      screen.queryByText(/locked to org defaults/i),
    ).not.toBeInTheDocument();
  });
});
