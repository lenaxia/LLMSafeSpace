// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { ToastProvider } from "../../providers/ToastProvider";
import { AdminProviderCredentialsTab } from "./AdminProviderCredentialsTab";

// ─── API mocks ───────────────────────────────────────────────────────────────

const mockList = vi.fn();
const mockCreate = vi.fn();
const mockUpdate = vi.fn();
const mockDelete = vi.fn();
const mockListAutoApply = vi.fn();
const mockCreateAutoApply = vi.fn();
const mockDeleteAutoApply = vi.fn();

vi.mock("../../api/providerCredentials", () => ({
  adminProviderCredentialsApi: {
    list: () => mockList(),
    create: (req: unknown) => mockCreate(req),
    update: (id: string, req: unknown) => mockUpdate(id, req),
    delete: (id: string) => mockDelete(id),
    listAutoApply: (id: string) => mockListAutoApply(id),
    createAutoApply: (id: string, req: unknown) => mockCreateAutoApply(id, req),
    deleteAutoApply: (id: string, targetType: string, targetId?: string) =>
      mockDeleteAutoApply(id, targetType, targetId),
  },
}));

const CRED = {
  id: "cred-1",
  name: "Platform OpenAI",
  provider: "openai",
  baseURL: "https://ai.example.com/v1",
  modelAllowlist: ["glm-5.1"],
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-02T00:00:00Z",
};

function renderTab() {
  return render(
    <ToastProvider>
      <AdminProviderCredentialsTab />
    </ToastProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mockListAutoApply.mockResolvedValue([]);
});

// ─── Tests ───────────────────────────────────────────────────────────────────

describe("AdminProviderCredentialsTab", () => {
  it("shows spinner while loading", () => {
    mockList.mockReturnValue(new Promise(() => {}));
    renderTab();
    expect(document.querySelector(".animate-spin")).toBeInTheDocument();
  });

  it("shows empty state when no credentials", async () => {
    mockList.mockResolvedValue([]);
    renderTab();
    await waitFor(() => {
      expect(screen.getByText(/No platform credentials configured/)).toBeInTheDocument();
    });
  });

  it("returns null for non-admin users (403)", async () => {
    mockList.mockRejectedValue(new Error("403 Forbidden"));
    renderTab();
    // The component returns null on 403 — no heading or spinner should appear
    await waitFor(() => {
      expect(screen.queryByText(/Platform LLM Provider/)).not.toBeInTheDocument();
      expect(document.querySelector(".animate-spin")).not.toBeInTheDocument();
    });
  });

  it("renders credential name and provider badge", async () => {
    mockList.mockResolvedValue([CRED]);
    renderTab();
    await waitFor(() => {
      expect(screen.getByText("Platform OpenAI")).toBeInTheDocument();
      expect(screen.getByText("openai")).toBeInTheDocument();
    });
  });

  it("expands row and loads auto-apply rules", async () => {
    mockList.mockResolvedValue([CRED]);
    mockListAutoApply.mockResolvedValue([
      { credentialId: "cred-1", targetType: "all", withinPriority: 0 },
    ]);
    renderTab();
    await waitFor(() => screen.getByText("Platform OpenAI"));

    fireEvent.click(screen.getByText("Platform OpenAI"));

    await waitFor(() => {
      expect(mockListAutoApply).toHaveBeenCalledWith("cred-1");
      expect(screen.getByText("all")).toBeInTheDocument();
      expect(screen.getByText(/Rotate API key/)).toBeInTheDocument();
    });
  });

  it("shows metadata in expanded panel (ID, dates, baseURL, allowlist)", async () => {
    mockList.mockResolvedValue([CRED]);
    renderTab();
    await waitFor(() => screen.getByText("Platform OpenAI"));
    fireEvent.click(screen.getByText("Platform OpenAI"));

    await waitFor(() => {
      expect(screen.getByText("cred-1")).toBeInTheDocument();
      expect(screen.getByText("glm-5.1")).toBeInTheDocument();
      // baseURL appears in both the row subtitle and the expanded metadata row
      expect(screen.getAllByText("https://ai.example.com/v1").length).toBeGreaterThanOrEqual(1);
    });
  });

  it("shows empty auto-apply state when no rules", async () => {
    mockList.mockResolvedValue([CRED]);
    mockListAutoApply.mockResolvedValue([]);
    renderTab();
    await waitFor(() => screen.getByText("Platform OpenAI"));
    fireEvent.click(screen.getByText("Platform OpenAI"));

    await waitFor(() => {
      expect(screen.getByText(/No auto-apply rules/)).toBeInTheDocument();
    });
  });

  it("inline delete confirm: shows Yes/No on trash click, calls delete on Yes", async () => {
    mockList.mockResolvedValue([CRED]);
    mockDelete.mockResolvedValue(undefined);
    renderTab();
    await waitFor(() => screen.getByText("Platform OpenAI"));

    // Click trash
    fireEvent.click(screen.getAllByTitle("Delete credential")[0]!);
    expect(screen.getByText("Delete?")).toBeInTheDocument();
    expect(screen.getByText("Yes")).toBeInTheDocument();
    expect(screen.getByText("No")).toBeInTheDocument();

    // Confirm
    fireEvent.click(screen.getByText("Yes"));
    await waitFor(() => {
      expect(mockDelete).toHaveBeenCalledWith("cred-1");
      expect(screen.queryByText("Platform OpenAI")).not.toBeInTheDocument();
    });
  });

  it("inline delete confirm: No cancels without deleting", async () => {
    mockList.mockResolvedValue([CRED]);
    renderTab();
    await waitFor(() => screen.getByText("Platform OpenAI"));

    fireEvent.click(screen.getAllByTitle("Delete credential")[0]!);
    fireEvent.click(screen.getByText("No"));

    expect(mockDelete).not.toHaveBeenCalled();
    expect(screen.getByText("Platform OpenAI")).toBeInTheDocument();
  });

  it("shows error banner on load failure", async () => {
    mockList.mockRejectedValue(new Error("network error"));
    renderTab();
    await waitFor(() => {
      expect(screen.getByText("network error")).toBeInTheDocument();
    });
  });

  it("error banner can be dismissed", async () => {
    mockList.mockRejectedValue(new Error("oops"));
    renderTab();
    await waitFor(() => screen.getByText("oops"));
    fireEvent.click(screen.getByText("✕"));
    expect(screen.queryByText("oops")).not.toBeInTheDocument();
  });

  it("create form appears on Add credential click and calls api.create", async () => {
    mockList.mockResolvedValue([]);
    mockCreate.mockResolvedValue({ ...CRED, id: "new-1", name: "New Cred" });
    renderTab();
    await waitFor(() => screen.getByText(/Add credential/));

    fireEvent.click(screen.getByText(/Add credential/));
    expect(screen.getByText("New Platform Credential")).toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText("e.g. OpenAI Production"), { target: { value: "New Cred" } });
    fireEvent.change(screen.getByPlaceholderText("e.g. openai"), { target: { value: "openai" } });
    fireEvent.change(screen.getByPlaceholderText("sk-…"), { target: { value: "sk-test-key" } });

    fireEvent.click(screen.getByText("Create"));
    await waitFor(() => {
      expect(mockCreate).toHaveBeenCalledWith(
        expect.objectContaining({ name: "New Cred", provider: "openai", apiKey: "sk-test-key" }),
      );
    });
  });

  it("create form validates required fields", async () => {
    mockList.mockResolvedValue([]);
    renderTab();
    await waitFor(() => screen.getByText(/Add credential/));
    fireEvent.click(screen.getByText(/Add credential/));
    fireEvent.click(screen.getByText("Create"));
    expect(screen.getByText(/Name, provider, and API key are required/)).toBeInTheDocument();
    expect(mockCreate).not.toHaveBeenCalled();
  });

  it("create form parses comma-separated model allowlist into array", async () => {
    mockList.mockResolvedValue([]);
    mockCreate.mockResolvedValue({ ...CRED, id: "c2", modelAllowlist: ["glm-5.1", "gpt-4o"] });
    renderTab();
    await waitFor(() => screen.getByText(/Add credential/));
    fireEvent.click(screen.getByText(/Add credential/));

    fireEvent.change(screen.getByPlaceholderText("e.g. OpenAI Production"), { target: { value: "X" } });
    fireEvent.change(screen.getByPlaceholderText("e.g. openai"), { target: { value: "openai" } });
    fireEvent.change(screen.getByPlaceholderText("sk-…"), { target: { value: "key" } });
    fireEvent.change(screen.getByPlaceholderText("e.g. glm-5.1, gpt-4o"), { target: { value: "glm-5.1, gpt-4o" } });

    fireEvent.click(screen.getByText("Create"));
    await waitFor(() => {
      expect(mockCreate).toHaveBeenCalledWith(
        expect.objectContaining({ modelAllowlist: ["glm-5.1", "gpt-4o"] }),
      );
    });
  });

  it("deleteAutoApply for 'all' rule sends sentinel targetId '_'", async () => {
    mockList.mockResolvedValue([CRED]);
    mockListAutoApply.mockResolvedValue([
      { credentialId: "cred-1", targetType: "all", targetId: undefined, withinPriority: 0 },
    ]);
    mockDeleteAutoApply.mockResolvedValue(undefined);
    renderTab();
    await waitFor(() => screen.getByText("Platform OpenAI"));
    fireEvent.click(screen.getByText("Platform OpenAI"));

    // Wait for auto-apply rules to load
    await waitFor(() => screen.getByText("all"));

    // Click the trash on the rule row (within the expanded panel)
    const trashButtons = screen.getAllByTitle("Remove rule");
    fireEvent.click(trashButtons[0]!);

    await waitFor(() => {
      // The component passes targetId=undefined to the API function.
      // The API function applies the sentinel "_" — verified in providerCredentials.test.ts.
      expect(mockDeleteAutoApply).toHaveBeenCalledWith("cred-1", "all", undefined);
    });
  });

  it("add auto-apply rule form for 'all' type omits targetId input", async () => {
    mockList.mockResolvedValue([CRED]);
    mockListAutoApply.mockResolvedValue([]);
    mockCreateAutoApply.mockResolvedValue({ credentialId: "cred-1", targetType: "all", withinPriority: 0 });
    renderTab();
    await waitFor(() => screen.getByText("Platform OpenAI"));
    fireEvent.click(screen.getByText("Platform OpenAI"));
    await waitFor(() => screen.getByText("Add rule"));

    fireEvent.click(screen.getByText("Add rule"));
    expect(screen.queryByPlaceholderText("User ID")).not.toBeInTheDocument();

    fireEvent.click(screen.getByText("Add"));
    await waitFor(() => {
      expect(mockCreateAutoApply).toHaveBeenCalledWith("cred-1", { targetType: "all" });
    });
  });

  it("add auto-apply rule form for 'user' type shows targetId input and requires it", async () => {
    mockList.mockResolvedValue([CRED]);
    mockListAutoApply.mockResolvedValue([]);
    renderTab();
    await waitFor(() => screen.getByText("Platform OpenAI"));
    fireEvent.click(screen.getByText("Platform OpenAI"));
    await waitFor(() => screen.getByText("Add rule"));

    fireEvent.click(screen.getByText("Add rule"));
    const select = screen.getByRole("combobox");
    fireEvent.change(select, { target: { value: "user" } });

    expect(screen.getByPlaceholderText("User ID")).toBeInTheDocument();
    // Disabled when targetId is empty
    expect(screen.getByText("Add")).toBeDisabled();
  });
});
