// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { ToastProvider } from "../../providers/ToastProvider";
import { UserProviderCredentialsTab } from "./UserProviderCredentialsTab";

// ─── API mocks ───────────────────────────────────────────────────────────────

const mockList = vi.fn();
const mockCreate = vi.fn();
const mockDelete = vi.fn();

vi.mock("../../api/providerCredentials", () => ({
  userProviderCredentialsApi: {
    list: () => mockList(),
    create: (req: unknown) => mockCreate(req),
    delete: (id: string) => mockDelete(id),
    // Bind/unbind endpoints still exist server-side, but the UI no longer calls
    // them (keys are auto-bound to all of a user's workspaces). Provided here as
    // no-ops so any incidental access does not throw.
    listBindings: () => Promise.resolve({ workspaceIds: [], bindings: [] }),
    bindToWorkspace: () => Promise.resolve(),
    unbindFromWorkspace: () => Promise.resolve(),
    probeModelsAnon: () => Promise.resolve({ models: [], warning: undefined }),
  },
}));

const CRED = {
  id: "cred-1",
  name: "My OpenAI Key",
  provider: "openai",
  baseURL: "https://ai.example.com/v1",
  modelAllowlist: ["glm-5.1", "gpt-4o"],
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-02T00:00:00Z",
};

function renderTab() {
  return render(
    <ToastProvider>
      <UserProviderCredentialsTab />
    </ToastProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

// ─── Tests ───────────────────────────────────────────────────────────────────

describe("UserProviderCredentialsTab", () => {
  it("shows spinner while loading", () => {
    mockList.mockReturnValue(new Promise(() => {}));
    renderTab();
    expect(document.querySelector(".animate-spin")).toBeInTheDocument();
  });

  it("shows empty state when no credentials", async () => {
    mockList.mockResolvedValue([]);
    renderTab();
    await waitFor(() => {
      expect(screen.getByText(/No personal provider keys added yet/)).toBeInTheDocument();
    });
  });

  it("renders credential row with name, provider badge, allowlist count", async () => {
    mockList.mockResolvedValue([CRED]);
    renderTab();
    await waitFor(() => {
      expect(screen.getByText("My OpenAI Key")).toBeInTheDocument();
      expect(screen.getByText("openai")).toBeInTheDocument();
      expect(screen.getByText("2 models")).toBeInTheDocument();
    });
  });

  it("renders the zero-knowledge security explainer", async () => {
    mockList.mockResolvedValue([]);
    renderTab();
    await waitFor(() => {
      expect(screen.getByText("How your keys are protected")).toBeInTheDocument();
      // "Zero Knowledge encryption" appears in both explainer paragraphs
      expect(screen.getAllByText(/Zero Knowledge encryption/)).toHaveLength(2);
      // Layperson warning about password-reset consequence
      expect(screen.getByText(/reset it by email/)).toBeInTheDocument();
      expect(screen.getByText(/They will be deleted/)).toBeInTheDocument();
      expect(screen.getByText(/attacker would not be able to steal your secrets/)).toBeInTheDocument();
    });
  });

  it("states that keys are automatically available across all workspaces", async () => {
    mockList.mockResolvedValue([CRED]);
    renderTab();
    await waitFor(() => screen.getByText("My OpenAI Key"));
    expect(
      screen.getByText(/Your keys are automatically available across all of your workspaces/),
    ).toBeInTheDocument();
  });

  it("shows error banner on load failure and can dismiss it", async () => {
    mockList.mockRejectedValue(new Error("load failed"));
    renderTab();
    await waitFor(() => screen.getByText("load failed"));
    fireEvent.click(screen.getByText("✕"));
    expect(screen.queryByText("load failed")).not.toBeInTheDocument();
  });

  it("expanded row shows read-only details: ID, updated, baseURL, model allowlist", async () => {
    mockList.mockResolvedValue([CRED]);
    renderTab();
    await waitFor(() => screen.getByText("My OpenAI Key"));
    fireEvent.click(screen.getByText("My OpenAI Key"));

    await waitFor(() => {
      expect(screen.getByText("cred-1")).toBeInTheDocument();
      expect(screen.getByText("https://ai.example.com/v1")).toBeInTheDocument();
      expect(screen.getByText("glm-5.1, gpt-4o")).toBeInTheDocument();
    });
  });

  it("expanded row does not render per-workspace bind/unbind controls", async () => {
    mockList.mockResolvedValue([CRED]);
    renderTab();
    await waitFor(() => screen.getByText("My OpenAI Key"));
    fireEvent.click(screen.getByText("My OpenAI Key"));

    await waitFor(() => screen.getByText("cred-1"));
    expect(screen.queryByText("Workspace bindings")).not.toBeInTheDocument();
    expect(screen.queryByText("Bind")).not.toBeInTheDocument();
    expect(screen.queryByText("Unbind")).not.toBeInTheDocument();
  });

  it("inline delete confirm shows Yes/No, Yes calls delete", async () => {
    mockList.mockResolvedValue([CRED]);
    mockDelete.mockResolvedValue(undefined);
    renderTab();
    await waitFor(() => screen.getByText("My OpenAI Key"));

    fireEvent.click(screen.getByTitle("Remove key"));
    expect(screen.getByText("Remove?")).toBeInTheDocument();

    fireEvent.click(screen.getByText("Yes"));
    await waitFor(() => {
      expect(mockDelete).toHaveBeenCalledWith("cred-1");
      expect(screen.queryByText("My OpenAI Key")).not.toBeInTheDocument();
    });
  });

  it("inline delete confirm No cancels without deleting", async () => {
    mockList.mockResolvedValue([CRED]);
    renderTab();
    await waitFor(() => screen.getByText("My OpenAI Key"));

    fireEvent.click(screen.getByTitle("Remove key"));
    fireEvent.click(screen.getByText("No"));

    expect(mockDelete).not.toHaveBeenCalled();
    expect(screen.getByText("My OpenAI Key")).toBeInTheDocument();
  });

  it("create form adds a credential and calls API", async () => {
    mockList.mockResolvedValue([]);
    mockCreate.mockResolvedValue({ ...CRED, id: "new-1", name: "Work Key" });
    renderTab();
    await waitFor(() => screen.getByText(/Add key/));

    // Open the form — click the nav button (first "Add key" button)
    const [navBtn] = screen.getAllByRole("button", { name: /Add key/ });
    fireEvent.click(navBtn!);
    expect(screen.getByText("Add Provider Key")).toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText("e.g. My OpenAI Key"), { target: { value: "Work Key" } });
    fireEvent.change(screen.getByPlaceholderText("e.g. openai"), { target: { value: "openai" } });
    fireEvent.change(screen.getByPlaceholderText("sk-… or key-…"), { target: { value: "sk-work-key" } });

    // Submit — the form's own button is the second "Add key" button
    const addBtns = screen.getAllByRole("button", { name: "Add key" });
    fireEvent.click(addBtns[addBtns.length - 1]!);
    await waitFor(() => {
      expect(mockCreate).toHaveBeenCalledWith(
        expect.objectContaining({ name: "Work Key", provider: "openai", apiKey: "sk-work-key" }),
      );
      expect(screen.getByText("Work Key")).toBeInTheDocument();
    });
  });

  it("create form validates required fields", async () => {
    mockList.mockResolvedValue([]);
    renderTab();
    await waitFor(() => screen.getByText(/Add key/));
    // Open the form
    const [navBtn] = screen.getAllByRole("button", { name: /Add key/ });
    fireEvent.click(navBtn!);
    // Submit empty — the form button is the last "Add key"
    const addBtns = screen.getAllByRole("button", { name: "Add key" });
    fireEvent.click(addBtns[addBtns.length - 1]!);
    expect(screen.getByText(/Name, provider, and API key are required/)).toBeInTheDocument();
    expect(mockCreate).not.toHaveBeenCalled();
  });
});
