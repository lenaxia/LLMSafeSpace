import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "@testing-library/react";
import { ThemeProvider } from "../providers/ThemeProvider";
import { SettingsPage } from "./SettingsPage";

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

  it("renders all tab labels", () => {
    renderSettings();
    expect(screen.getAllByText("API Keys").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Appearance").length).toBeGreaterThan(0);
    expect(screen.getByText("Profile")).toBeInTheDocument();
    expect(screen.getByText("MCP Servers")).toBeInTheDocument();
    expect(screen.getByText("Presets")).toBeInTheDocument();
  });

  it("shows API Keys tab content by default", () => {
    renderSettings();
    expect(screen.getByText(/no api keys yet/i)).toBeInTheDocument();
  });

  it("switches to Appearance tab", async () => {
    const user = userEvent.setup();
    renderSettings();
    await user.click(screen.getAllByText("Appearance")[0]!);
    expect(screen.getByText("Customize how Safe Space looks")).toBeInTheDocument();
  });

  it("shows Coming Soon for Profile tab", async () => {
    const user = userEvent.setup();
    renderSettings();
    await user.click(screen.getByText("Profile"));
    expect(screen.getByText("Coming soon")).toBeInTheDocument();
  });
});
