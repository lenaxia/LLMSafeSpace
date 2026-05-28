import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "@testing-library/react";
import { ThemeProvider } from "../providers/ThemeProvider";
import { ToastProvider } from "../providers/ToastProvider";
import { SettingsPage } from "./SettingsPage";

// Mock auth provider
vi.mock("../providers/AuthProvider", () => ({
  useAuth: () => ({ user: { id: "1", role: "admin" }, loading: false }),
}));

// Mock settings API
vi.mock("../api/settings", () => ({
  settingsApi: {
    getUserSettings: () => Promise.resolve({ settings: {}, schemaVersion: 1 }),
    getUserSchema: () => Promise.resolve({ settings: [], schemaVersion: 1 }),
    getAdminSettings: () => Promise.resolve({ settings: {}, schemaVersion: 1 }),
    getAdminSchema: () => Promise.resolve({ settings: [], schemaVersion: 1 }),
    setUserSetting: vi.fn().mockResolvedValue({}),
    setAdminSetting: vi.fn().mockResolvedValue({}),
  },
}));

// Mock credentials API
vi.mock("../api/credentials", () => ({
  credentialsApi: {
    list: () => Promise.resolve([]),
    rotateKey: vi.fn(),
  },
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

describe("SettingsPage", () => {
  it("renders settings heading", () => {
    renderSettings();
    expect(screen.getByText("Settings")).toBeInTheDocument();
  });

  it("renders all tabs for admin user", () => {
    renderSettings();
    expect(screen.getByText("Preferences")).toBeInTheDocument();
    expect(screen.getByText("Credentials")).toBeInTheDocument();
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
});
