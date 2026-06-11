import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "@testing-library/react";
import { ThemeProvider } from "../providers/ThemeProvider";
import { ToastProvider } from "../providers/ToastProvider";
import { SettingsPage } from "./SettingsPage";
import { AdminSettingsPage } from "./AdminSettingsPage";
import { ApiClientError } from "../api/client";

// Mock auth provider
vi.mock("../providers/AuthProvider", () => ({
  useAuth: () => ({ user: { id: "1", role: "admin" }, loading: false }),
}));

// Mock settings API
vi.mock("../api/settings", () => ({
  settingsApi: {
    getUserSettings: () => Promise.resolve({ settings: {}, schemaVersion: 1 }),
    getUserSchema: () => Promise.resolve({ settings: [], schemaVersion: 1 }),
    getAdminSettings: () => Promise.resolve({ settings: { debug: false }, schemaVersion: 1 }),
    getAdminSchema: () =>
      Promise.resolve({
        settings: [
          {
            key: "debug",
            label: "Debug Mode",
            description: "Enable debug logging",
            type: "bool",
            category: "General",
            default: false,
          },
        ],
        schemaVersion: 1,
      }),
    setUserSetting: vi.fn().mockResolvedValue({}),
    setAdminSetting: vi.fn().mockResolvedValue({}),
  },
}));

// Mock new provider credentials APIs
vi.mock("../api/providerCredentials", () => ({
  adminProviderCredentialsApi: { list: () => Promise.resolve([]) },
  userProviderCredentialsApi: { list: () => Promise.resolve([]) },
}));

function renderSettings() {
  return render(
    <ThemeProvider>
      <ToastProvider>
        <SettingsPage />
      </ToastProvider>
    </ThemeProvider>,
  );
}

function renderAdminSettings() {
  return render(
    <ThemeProvider>
      <ToastProvider>
        <AdminSettingsPage />
      </ToastProvider>
    </ThemeProvider>,
  );
}

describe("SettingsPage", () => {
  it("renders settings heading", () => {
    renderSettings();
    expect(screen.getByText("Settings")).toBeInTheDocument();
  });

  it("renders all tabs for admin user", () => {
    renderSettings();
    expect(screen.getByText("Preferences")).toBeInTheDocument();
    expect(screen.getByText("Platform Credentials")).toBeInTheDocument();
    expect(screen.getByText("Provider Keys")).toBeInTheDocument();
    expect(screen.getByText("Admin")).toBeInTheDocument();
  });

  it("shows Preferences tab by default", () => {
    renderSettings();
    // Preferences is active — UserSettingsTab renders
    expect(screen.getByText("Preferences")).toBeInTheDocument();
  });

  it("switches to API Keys tab", async () => {
    const user = userEvent.setup();
    renderSettings();
    await user.click(screen.getByText("API Keys"));
    expect(screen.getByText(/no api keys yet/i)).toBeInTheDocument();
  });

  it("content area has min-w-0 to allow proper shrinking on narrow screens", () => {
    const { container } = renderSettings();
    const contentArea = container.querySelector(".flex-1.min-w-0.overflow-y-auto");
    expect(contentArea).not.toBeNull();
  });

  it("AdminSettingsPage shows 'Admin access required' message when backend returns 404", async () => {
    const user = userEvent.setup();
    // Override the admin settings API to reject with a 404 ApiClientError
    vi.mocked(
      (await import("../api/settings")).settingsApi.getAdminSchema,
    );
    // Re-mock for this test only using a spy approach via module mock override
    const { settingsApi } = await import("../api/settings");
    vi.spyOn(settingsApi, "getAdminSchema").mockRejectedValueOnce(
      new ApiClientError(404, { error: "Not Found" }),
    );
    vi.spyOn(settingsApi, "getAdminSettings").mockRejectedValueOnce(
      new ApiClientError(404, { error: "Not Found" }),
    );

    renderSettings();
    await user.click(screen.getByText("Admin"));

    await waitFor(() => {
      expect(screen.getByText(/Admin access required/i)).toBeInTheDocument();
    });
  });
});

describe("AdminSettingsPage save error handling", () => {
  it("shows an error toast when setAdminSetting throws", async () => {
    const { settingsApi } = await import("../api/settings");
    vi.mocked(settingsApi.setAdminSetting).mockRejectedValueOnce(
      new Error("Network error"),
    );

    const user = userEvent.setup();
    renderAdminSettings();

    // Wait for the schema to load and the toggle to appear
    const toggle = await screen.findByRole("switch", { name: /debug mode/i });

    // Trigger a save by toggling the checkbox
    await user.click(toggle);

    // Error toast should appear
    await waitFor(() => {
      expect(screen.getByText("Network error")).toBeInTheDocument();
    });
  });
});
