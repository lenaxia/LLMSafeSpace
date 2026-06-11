// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { ToastProvider } from "../../providers/ToastProvider";
import { ApiKeysTab } from "./ApiKeysTab";

// ─── API mocks ────────────────────────────────────────────────────────────────

const mockList = vi.fn();
const mockCreate = vi.fn();
const mockDelete = vi.fn();

vi.mock("../../api/apiKeys", () => ({
  apiKeysApi: {
    list: () => mockList(),
    create: (req: unknown) => mockCreate(req),
    delete: (id: string) => mockDelete(id),
  },
}));

// ─── Fixtures ─────────────────────────────────────────────────────────────────

const KEY1 = {
  id: "key-1",
  name: "CI Key",
  prefix: "llmsk_ab",
  createdAt: "2026-01-01T00:00:00Z",
  lastUsedAt: "2026-05-01T00:00:00Z",
};

const KEY2 = {
  id: "key-2",
  name: "Local Dev",
  prefix: "llmsk_cd",
  createdAt: "2026-02-01T00:00:00Z",
};

const CREATE_RESPONSE = {
  key: "llmsk_abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
  apiKey: {
    id: "key-new",
    name: "New Key",
    prefix: "llmsk_ab",
    createdAt: "2026-06-01T00:00:00Z",
  },
};

function renderTab() {
  return render(
    <ToastProvider>
      <ApiKeysTab />
    </ToastProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

// ─── Tests ────────────────────────────────────────────────────────────────────

describe("ApiKeysTab", () => {
  // ── Loading state ──────────────────────────────────────────────────────────

  it("shows spinner while loading", () => {
    mockList.mockReturnValue(new Promise(() => {}));
    renderTab();
    expect(document.querySelector(".animate-spin")).toBeInTheDocument();
  });

  // ── Empty state ────────────────────────────────────────────────────────────

  it("shows empty state when no keys exist", async () => {
    mockList.mockResolvedValue([]);
    renderTab();
    await waitFor(() => {
      expect(screen.getByText(/No API keys yet/)).toBeInTheDocument();
    });
  });

  // ── Key list ───────────────────────────────────────────────────────────────

  it("renders key list with name and prefix", async () => {
    mockList.mockResolvedValue([KEY1, KEY2]);
    renderTab();
    await waitFor(() => {
      expect(screen.getByText("CI Key")).toBeInTheDocument();
      expect(screen.getByText("Local Dev")).toBeInTheDocument();
    });
    expect(screen.getByText(/llmsk_ab/)).toBeInTheDocument();
  });

  it("shows last-used date when present", async () => {
    mockList.mockResolvedValue([KEY1]);
    renderTab();
    await waitFor(() => screen.getByText("CI Key"));
    expect(screen.getByText(/Last used/)).toBeInTheDocument();
  });

  it("shows 'Never used' when lastUsedAt is absent", async () => {
    mockList.mockResolvedValue([KEY2]);
    renderTab();
    await waitFor(() => screen.getByText("Local Dev"));
    expect(screen.getByText(/Never used/)).toBeInTheDocument();
  });

  // ── Error state ────────────────────────────────────────────────────────────

  it("shows error banner when list fails", async () => {
    mockList.mockRejectedValue(new Error("network error"));
    renderTab();
    await waitFor(() => {
      expect(screen.getByText("network error")).toBeInTheDocument();
    });
  });

  it("can dismiss the error banner", async () => {
    mockList.mockRejectedValue(new Error("network error"));
    renderTab();
    await waitFor(() => screen.getByText("network error"));
    fireEvent.click(screen.getByText("✕"));
    expect(screen.queryByText("network error")).not.toBeInTheDocument();
  });

  // ── Create key ─────────────────────────────────────────────────────────────

  it("opens create form when Create key is clicked", async () => {
    mockList.mockResolvedValue([]);
    renderTab();
    await waitFor(() => screen.getByRole("button", { name: /Create key/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create key/i }));
    expect(screen.getByText("New API Key")).toBeInTheDocument();
    expect(screen.getByPlaceholderText(/CI \/ Production/)).toBeInTheDocument();
  });

  it("validates that name is required", async () => {
    mockList.mockResolvedValue([]);
    renderTab();
    await waitFor(() => screen.getByRole("button", { name: /Create key/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create key/i }));
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    expect(screen.getByText("Name is required")).toBeInTheDocument();
    expect(mockCreate).not.toHaveBeenCalled();
  });

  it("submits form and adds new key to list", async () => {
    mockList.mockResolvedValue([]);
    mockCreate.mockResolvedValue(CREATE_RESPONSE);
    renderTab();
    await waitFor(() => screen.getByRole("button", { name: /Create key/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create key/i }));

    fireEvent.change(screen.getByPlaceholderText(/CI \/ Production/), {
      target: { value: "New Key" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => {
      expect(mockCreate).toHaveBeenCalledWith({ name: "New Key" });
      // "New Key" appears in both the one-time banner and the new key row.
      expect(screen.getAllByText("New Key").length).toBeGreaterThanOrEqual(1);
    });
  });

  it("shows one-time key banner after creation with the key value", async () => {
    mockList.mockResolvedValue([]);
    mockCreate.mockResolvedValue(CREATE_RESPONSE);
    renderTab();
    await waitFor(() => screen.getByRole("button", { name: /Create key/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create key/i }));
    fireEvent.change(screen.getByPlaceholderText(/CI \/ Production/), {
      target: { value: "New Key" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => {
      expect(screen.getByText(/Copy your key now/)).toBeInTheDocument();
      expect(screen.getByTestId("new-key-value")).toHaveTextContent(
        CREATE_RESPONSE.key,
      );
    });
  });

  it("one-time key banner has a copy button", async () => {
    mockList.mockResolvedValue([]);
    mockCreate.mockResolvedValue(CREATE_RESPONSE);
    Object.assign(navigator, {
      clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    renderTab();
    await waitFor(() => screen.getByRole("button", { name: /Create key/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create key/i }));
    fireEvent.change(screen.getByPlaceholderText(/CI \/ Production/), {
      target: { value: "New Key" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => screen.getByTitle("Copy key"));
    fireEvent.click(screen.getByTitle("Copy key"));
    await waitFor(() => {
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith(
        CREATE_RESPONSE.key,
      );
    });
  });

  it("can dismiss the one-time key banner", async () => {
    mockList.mockResolvedValue([]);
    mockCreate.mockResolvedValue(CREATE_RESPONSE);
    renderTab();
    await waitFor(() => screen.getByRole("button", { name: /Create key/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create key/i }));
    fireEvent.change(screen.getByPlaceholderText(/CI \/ Production/), {
      target: { value: "New Key" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => screen.getByText(/I've copied my key/));
    fireEvent.click(screen.getByText(/I've copied my key/));
    expect(screen.queryByText(/Copy your key now/)).not.toBeInTheDocument();
  });

  it("cancel closes the create form without submitting", async () => {
    mockList.mockResolvedValue([]);
    renderTab();
    await waitFor(() => screen.getByRole("button", { name: /Create key/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create key/i }));
    expect(screen.getByText("New API Key")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByText("New API Key")).not.toBeInTheDocument();
    expect(mockCreate).not.toHaveBeenCalled();
  });

  it("shows form error when create API call fails", async () => {
    mockList.mockResolvedValue([]);
    mockCreate.mockRejectedValue(new Error("server error"));
    renderTab();
    await waitFor(() => screen.getByRole("button", { name: /Create key/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create key/i }));
    fireEvent.change(screen.getByPlaceholderText(/CI \/ Production/), {
      target: { value: "Bad Key" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => {
      // "server error" appears in both the inline form error and the parent
      // error banner (both are set by the onError callback path).
      expect(screen.getAllByText("server error").length).toBeGreaterThanOrEqual(1);
    });
  });

  // ── Delete key ─────────────────────────────────────────────────────────────

  it("shows confirmation dialog when delete is clicked", async () => {
    mockList.mockResolvedValue([KEY1]);
    renderTab();
    await waitFor(() => screen.getByText("CI Key"));

    fireEvent.click(screen.getByTitle("Delete key"));
    expect(screen.getByText("Delete?")).toBeInTheDocument();
    expect(screen.getByText("Yes")).toBeInTheDocument();
    expect(screen.getByText("No")).toBeInTheDocument();
  });

  it("confirms delete: calls API and removes key from list", async () => {
    mockList.mockResolvedValue([KEY1, KEY2]);
    mockDelete.mockResolvedValue(undefined);
    renderTab();
    await waitFor(() => screen.getByText("CI Key"));

    // Two keys → two delete buttons; target the first one (KEY1).
    fireEvent.click(screen.getAllByTitle("Delete key")[0]!);
    fireEvent.click(screen.getByText("Yes"));

    await waitFor(() => {
      expect(mockDelete).toHaveBeenCalledWith("key-1");
      expect(screen.queryByText("CI Key")).not.toBeInTheDocument();
      expect(screen.getByText("Local Dev")).toBeInTheDocument();
    });
  });

  it("cancels delete: No keeps the key in the list", async () => {
    mockList.mockResolvedValue([KEY1]);
    renderTab();
    await waitFor(() => screen.getByText("CI Key"));

    fireEvent.click(screen.getByTitle("Delete key"));
    fireEvent.click(screen.getByText("No"));

    expect(mockDelete).not.toHaveBeenCalled();
    expect(screen.getByText("CI Key")).toBeInTheDocument();
  });

  // ── Expand row ─────────────────────────────────────────────────────────────

  it("expands row to show metadata on click", async () => {
    mockList.mockResolvedValue([KEY1]);
    renderTab();
    await waitFor(() => screen.getByText("CI Key"));

    fireEvent.click(screen.getByText("CI Key"));
    await waitFor(() => {
      expect(screen.getByText("key-1")).toBeInTheDocument();
    });
  });
});
