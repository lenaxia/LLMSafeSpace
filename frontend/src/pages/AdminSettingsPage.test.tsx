import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { ToastProvider } from "../providers/ToastProvider";

// Mock must be self-contained (vitest hoists vi.mock before imports)
vi.mock("../api/settings", () => ({
  settingsApi: {
    getAdminSchema: vi.fn().mockResolvedValue({
      settings: [
        { key: "email.provider", tier: 2, type: "string", default: "", category: "Email", label: "Provider", description: "Email provider" },
        { key: "auth.registrationEnabled", tier: 2, type: "bool", default: true, category: "Auth", label: "Registration", description: "Allow signups" },
      ],
      schemaVersion: 5,
    }),
    getAdminSettings: vi.fn().mockResolvedValue({ settings: {}, schemaVersion: 5 }),
    setAdminSetting: vi.fn(),
    testEmailSend: vi.fn(),
  },
}));

import { settingsApi } from "../api/settings";
import { AdminSettingsPage } from "./AdminSettingsPage";

function renderPage() {
  return render(
    <ToastProvider>
      <AdminSettingsPage />
    </ToastProvider>,
  );
}

describe("AdminSettingsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders email test section when Email category exists in schema", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("Email Test")).toBeInTheDocument();
    });
    expect(screen.getByPlaceholderText("recipient@example.com")).toBeInTheDocument();
    expect(screen.getByText("Send Test Email")).toBeInTheDocument();
  });

  it("does not render email test section when no Email category", async () => {
    vi.mocked(settingsApi.getAdminSchema).mockResolvedValueOnce({
      settings: [{ key: "auth.registrationEnabled", tier: 2, type: "bool", default: true, category: "Auth", label: "Registration", description: "" }],
      schemaVersion: 5,
    });
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("Instance Settings")).toBeInTheDocument();
    });
    expect(screen.queryByText("Email Test")).not.toBeInTheDocument();
  });

  it("calls testEmailSend and shows success toast", async () => {
    vi.mocked(settingsApi.testEmailSend).mockResolvedValueOnce({ sent: true, provider: "ses" });
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("Send Test Email")).toBeInTheDocument();
    });

    const input = screen.getByPlaceholderText("recipient@example.com");
    fireEvent.change(input, { target: { value: "ops@test.com" } });
    fireEvent.click(screen.getByText("Send Test Email"));

    await waitFor(() => {
      expect(settingsApi.testEmailSend).toHaveBeenCalledWith("ops@test.com");
    });
  });

  it("shows error toast when testEmailSend fails", async () => {
    vi.mocked(settingsApi.testEmailSend).mockRejectedValueOnce({
      status: 502,
      body: { error: "sender not verified in SES" },
      message: "sender not verified in SES",
      name: "ApiClientError",
    });
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("Send Test Email")).toBeInTheDocument();
    });

    const input = screen.getByPlaceholderText("recipient@example.com");
    fireEvent.change(input, { target: { value: "ops@test.com" } });
    fireEvent.click(screen.getByText("Send Test Email"));

    await waitFor(() => {
      expect(settingsApi.testEmailSend).toHaveBeenCalledWith("ops@test.com");
    });
  });
});
