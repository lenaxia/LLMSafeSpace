import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ToastProvider } from "../providers/ToastProvider";
import { ApiClientError } from "../api/client";

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

function fillAndSend(recipient: string) {
  const input = screen.getByPlaceholderText("recipient@example.com");
  fireEvent.change(input, { target: { value: recipient } });
  fireEvent.click(screen.getByText("Send Test Email"));
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

  it("calls testEmailSend and shows success toast on ses success", async () => {
    vi.mocked(settingsApi.testEmailSend).mockResolvedValueOnce({ sent: true, provider: "ses" });
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("Send Test Email")).toBeInTheDocument();
    });

    fillAndSend("ops@test.com");

    await waitFor(() => {
      expect(settingsApi.testEmailSend).toHaveBeenCalledWith("ops@test.com");
    });
    await waitFor(() => {
      expect(screen.getByText(/Test email sent to ops@test.com via ses/)).toBeInTheDocument();
    });
  });

  it("shows noop toast when provider is noop", async () => {
    vi.mocked(settingsApi.testEmailSend).mockResolvedValueOnce({ sent: false, provider: "noop" });
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("Send Test Email")).toBeInTheDocument();
    });

    fillAndSend("ops@test.com");

    await waitFor(() => {
      expect(screen.getByText(/noop.*configure SES/i)).toBeInTheDocument();
    });
  });

  it("shows error toast from ApiClientError when testEmailSend fails", async () => {
    vi.mocked(settingsApi.testEmailSend).mockRejectedValueOnce(
      new ApiClientError(502, { error: "sender not verified in SES; verify the fromAddress domain" }),
    );
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("Send Test Email")).toBeInTheDocument();
    });

    fillAndSend("ops@test.com");

    await waitFor(() => {
      expect(screen.getByText(/sender not verified in SES/)).toBeInTheDocument();
    });
  });

  it("shows error toast when recipient is empty", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("Send Test Email")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("Send Test Email"));

    await waitFor(() => {
      expect(screen.getByText(/Enter a recipient email address/i)).toBeInTheDocument();
    });
    expect(settingsApi.testEmailSend).not.toHaveBeenCalled();
  });

  it("shows 'Admin access required' when the backend returns 404", async () => {
    vi.mocked(settingsApi.getAdminSchema).mockRejectedValueOnce(
      new ApiClientError(404, { error: "Not Found" }),
    );
    vi.mocked(settingsApi.getAdminSettings).mockRejectedValueOnce(
      new ApiClientError(404, { error: "Not Found" }),
    );

    renderPage();

    await waitFor(() => {
      expect(screen.getByText(/Admin access required/i)).toBeInTheDocument();
    });
  });

  it("shows an error toast when setAdminSetting throws", async () => {
    vi.mocked(settingsApi.setAdminSetting).mockRejectedValueOnce(new Error("Network error"));
    const user = userEvent.setup();

    renderPage();

    const toggle = await screen.findByRole("switch", { name: /registration/i });
    await user.click(toggle);

    await waitFor(() => {
      expect(screen.getByText("Network error")).toBeInTheDocument();
    });
  });
});
