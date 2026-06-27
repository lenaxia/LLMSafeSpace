import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { ToastProvider } from "../../providers/ToastProvider";
import { OrgCredentialsTab } from "./OrgCredentialsTab";
import type { OrgCredential, OrgResponse } from "../../api/orgs";

const mockList = vi.fn();
const mockCreate = vi.fn();
const mockUpdate = vi.fn();
const mockDelete = vi.fn();
const mockProbe = vi.fn();
const mockOutletContext = vi.fn();

vi.mock("../../api/orgs", () => ({
  orgsApi: {
    listCredentials: (id: string) => mockList(id),
    createCredential: (id: string, req: unknown) => mockCreate(id, req),
    updateCredential: (id: string, credId: string, req: unknown) =>
      mockUpdate(id, credId, req),
    deleteCredential: (id: string, credId: string) => mockDelete(id, credId),
    probeCredentialModels: (id: string, credId: string) => mockProbe(id, credId),
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
        <OrgCredentialsTab />
      </MemoryRouter>
    </ToastProvider>,
  );
}

const CRED: OrgCredential = {
  id: "cred-1",
  orgId: "org-1",
  name: "Team Key",
  kind: "openai",
  slug: "team-key",
  baseURL: "https://api.openai.com/v1",
  modelAllowlist: ["gpt-4o"],
  modelContextLimits: { "gpt-4o": 128000 },
  modelOutputLimits: { "gpt-4o": 16384 },
  createdAt: "2026-01-02T00:00:00Z",
  updatedAt: "2026-01-02T00:00:00Z",
};

beforeEach(() => {
  vi.clearAllMocks();
  mockList.mockResolvedValue([CRED]);
});

describe("OrgCredentialsTab", () => {
  it("lists credentials via orgsApi.listCredentials", async () => {
    renderTab();
    await waitFor(() => expect(screen.getByText("Team Key")).toBeInTheDocument());
    expect(mockList).toHaveBeenCalledWith("org-1");
  });

  it("shows provider and baseURL in collapsed row", async () => {
    renderTab();
    await waitFor(() => expect(screen.getByText("Team Key")).toBeInTheDocument());
    expect(
      screen.getByText(/openai · https:\/\/api\.openai\.com\/v1/),
    ).toBeInTheDocument();
  });

  it("expands a credential to show per-model limits", async () => {
    renderTab();
    await waitFor(() => expect(screen.getByText("Team Key")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Team Key"));
    expect(screen.getByText("Per-model limits")).toBeInTheDocument();
    // Format: "<model>: ctx 128,000 / out 16,384"
    expect(screen.getByText(/gpt-4o: ctx 128,000 \/ out 16,384/)).toBeInTheDocument();
  });

  it("shows empty state when no credentials exist", async () => {
    mockList.mockResolvedValue([]);
    renderTab();
    await waitFor(() =>
      expect(
        screen.getByText(/No org credentials configured/),
      ).toBeInTheDocument(),
    );
  });

  it("shows error message on list failure", async () => {
    mockList.mockRejectedValue(new Error("boom"));
    renderTab();
    await waitFor(() => expect(screen.getByText("boom")).toBeInTheDocument());
  });

  it("opens the create form on Add Credential", async () => {
    renderTab();
    await waitFor(() => expect(screen.getByText("Team Key")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Add Credential"));
    expect(screen.getByText("New Org Credential")).toBeInTheDocument();
    expect(screen.getByText("Create Credential")).toBeInTheDocument();
  });

  it("creates a credential and refreshes", async () => {
    mockCreate.mockResolvedValue({ ...CRED, id: "cred-2" });
    renderTab();
    await waitFor(() => expect(screen.getByText("Team Key")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Add Credential"));

    fireEvent.change(screen.getByPlaceholderText(/Name/), {
      target: { value: "New Key" },
    });
    // Epic 55: org tab now starts with kind="" (empty), forcing the user
    // to make an explicit SDK-class choice via the dropdown.
    fireEvent.change(screen.getByDisplayValue("— select SDK kind —"), {
      target: { value: "openai" },
    });
    // Slug auto-populates from name as "new-key".
    fireEvent.change(screen.getByPlaceholderText("API Key"), {
      target: { value: "sk-new" },
    });
    fireEvent.click(screen.getByText("Create Credential"));

    await waitFor(() => expect(mockCreate).toHaveBeenCalled());
    expect(mockCreate).toHaveBeenCalledWith(
      "org-1",
      expect.objectContaining({
        name: "New Key",
        kind: "openai",
        slug: "new-key",
        apiKey: "sk-new",
      }),
    );
    await waitFor(() => expect(mockList).toHaveBeenCalledTimes(2));
  });

  it("shows validation error when creating without name", async () => {
    renderTab();
    await waitFor(() => expect(screen.getByText("Team Key")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Add Credential"));
    fireEvent.click(screen.getByText("Create Credential"));
    expect(
      screen.getByText("Name and API key are required"),
    ).toBeInTheDocument();
    expect(mockCreate).not.toHaveBeenCalled();
  });

  it("shows validation error when creating without kind", async () => {
    renderTab();
    await waitFor(() => expect(screen.getByText("Team Key")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Add Credential"));
    fireEvent.change(screen.getByPlaceholderText(/Name/), { target: { value: "New" } });
    fireEvent.change(screen.getByPlaceholderText("API Key"), { target: { value: "sk-test" } });
    // Skip the kind dropdown — leaves it at "".
    fireEvent.click(screen.getByText("Create Credential"));
    expect(screen.getByText("Kind and slug are required")).toBeInTheDocument();
    expect(mockCreate).not.toHaveBeenCalled();
  });

  it("rejects invalid slug format client-side", async () => {
    renderTab();
    await waitFor(() => expect(screen.getByText("Team Key")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Add Credential"));
    fireEvent.change(screen.getByPlaceholderText(/Name/), { target: { value: "New" } });
    fireEvent.change(screen.getByDisplayValue("— select SDK kind —"), { target: { value: "openai" } });
    fireEvent.change(screen.getByPlaceholderText(/Slug/), { target: { value: "has space" } });
    fireEvent.change(screen.getByPlaceholderText("API Key"), { target: { value: "sk-test" } });
    fireEvent.click(screen.getByText("Create Credential"));
    expect(screen.getByText(/Slug must be 1–64 lowercase alphanumeric/)).toBeInTheDocument();
    expect(mockCreate).not.toHaveBeenCalled();
  });

  it("opens the edit form with existing values and updates", async () => {
    mockUpdate.mockResolvedValue({ ...CRED, name: "Renamed" });
    renderTab();
    await waitFor(() => expect(screen.getByText("Team Key")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Team Key"));
    fireEvent.click(screen.getByText("Edit"));

    expect(
      (screen.getByPlaceholderText(/Name/) as HTMLInputElement).value,
    ).toBe("Team Key");
    fireEvent.change(screen.getByPlaceholderText(/Name/), {
      target: { value: "Renamed" },
    });
    fireEvent.click(screen.getByText("Save Changes"));

    await waitFor(() => expect(mockUpdate).toHaveBeenCalled());
    expect(mockUpdate).toHaveBeenCalledWith(
      "org-1",
      "cred-1",
      expect.objectContaining({ name: "Renamed" }),
    );
  });

  it("deletes a credential after expand", async () => {
    mockDelete.mockResolvedValue(undefined);
    renderTab();
    await waitFor(() => expect(screen.getByText("Team Key")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Team Key"));
    fireEvent.click(screen.getByText("Delete"));
    await waitFor(() => expect(mockDelete).toHaveBeenCalledWith("org-1", "cred-1"));
  });

  it("probes models for an existing credential in edit mode", async () => {
    mockProbe.mockResolvedValue({
      models: [{ id: "gpt-4o-mini", contextLimit: 0, outputLimit: 0 }],
    });
    renderTab();
    await waitFor(() => expect(screen.getByText("Team Key")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Team Key"));
    fireEvent.click(screen.getByText("Edit"));
    fireEvent.click(screen.getByText("Fetch models from provider"));
    await waitFor(() => expect(mockProbe).toHaveBeenCalledWith("org-1", "cred-1"));
    await waitFor(() =>
      expect(screen.getByText("gpt-4o-mini")).toBeInTheDocument(),
    );
  });
});
