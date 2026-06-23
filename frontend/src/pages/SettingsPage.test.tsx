import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "@testing-library/react";
import { ThemeProvider } from "../providers/ThemeProvider";
import { ToastProvider } from "../providers/ToastProvider";
import { SettingsPage } from "./SettingsPage";

// SettingsPage no longer renders platform-admin tabs (they moved to the
// /admin portal). The auth + provider mocks remain so any child component
// that reads auth or provider state has a valid context.
vi.mock("../providers/AuthProvider", () => ({
  useAuth: () => ({ user: { id: "1", role: "admin" }, loading: false }),
}));

vi.mock("../api/settings", () => ({
  settingsApi: {
    getUserSettings: () => Promise.resolve({ settings: {}, schemaVersion: 1 }),
    getUserSchema: () => Promise.resolve({ settings: [], schemaVersion: 1 }),
    getAdminSettings: () => Promise.resolve({ settings: { debug: false }, schemaVersion: 1 }),
    getAdminSchema: () => Promise.resolve({ settings: [], schemaVersion: 1 }),
    setUserSetting: vi.fn().mockResolvedValue({}),
    setAdminSetting: vi.fn().mockResolvedValue({}),
  },
}));

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

describe("SettingsPage", () => {
  it("renders settings heading", () => {
    renderSettings();
    expect(screen.getByText("Settings")).toBeInTheDocument();
  });

  it("renders personal tabs and not platform-admin tabs", () => {
    renderSettings();
    expect(screen.getByText("Preferences")).toBeInTheDocument();
    expect(screen.getByText("Provider Keys")).toBeInTheDocument();
    expect(screen.getByText("Secrets")).toBeInTheDocument();
    expect(screen.getByText("API Keys")).toBeInTheDocument();
    expect(screen.getByText("My Organisation")).toBeInTheDocument();
    // Platform-admin tabs migrated to the /admin portal
    expect(screen.queryByText("Platform Credentials")).not.toBeInTheDocument();
    expect(screen.queryByText("Platform Audit")).not.toBeInTheDocument();
    expect(screen.queryByText("Admin")).not.toBeInTheDocument();
  });

  it("shows Preferences tab by default", () => {
    renderSettings();
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
});
