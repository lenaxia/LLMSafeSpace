import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "@testing-library/react";
import { ThemeProvider } from "../providers/ThemeProvider";
import { SettingsPage } from "./SettingsPage";

// Mock the settings API to avoid network calls
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

function renderSettings() {
  return render(
    <ThemeProvider>
      <SettingsPage />
    </ThemeProvider>,
  );
}

describe("SettingsPage", () => {
  it("renders settings heading", () => {
    renderSettings();
    expect(screen.getByText("Settings")).toBeInTheDocument();
  });

  it("renders tab labels", () => {
    renderSettings();
    expect(screen.getByText("Preferences")).toBeInTheDocument();
    expect(screen.getByText("API Keys")).toBeInTheDocument();
    expect(screen.getByText("Admin")).toBeInTheDocument();
  });

  it("shows Preferences tab by default", () => {
    renderSettings();
    // Preferences tab is active by default — the UserSettingsTab renders
    // (it will show a spinner while loading, then the form)
    expect(screen.getByText("Preferences")).toBeInTheDocument();
  });

  it("switches to API Keys tab", async () => {
    const user = userEvent.setup();
    renderSettings();
    await user.click(screen.getByText("API Keys"));
    expect(screen.getByText(/no api keys yet/i)).toBeInTheDocument();
  });
});
