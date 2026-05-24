import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render as rtlRender } from "@testing-library/react";
import { ThemeProvider } from "../../providers/ThemeProvider";
import { AppearanceTab } from "./AppearanceTab";

function renderWithTheme() {
  return rtlRender(
    <ThemeProvider>
      <AppearanceTab />
    </ThemeProvider>,
  );
}

describe("AppearanceTab", () => {
  it("renders heading", () => {
    renderWithTheme();
    expect(screen.getByText("Appearance")).toBeInTheDocument();
  });

  it("renders three theme options", () => {
    renderWithTheme();
    expect(screen.getByText("Light")).toBeInTheDocument();
    expect(screen.getByText("Dark")).toBeInTheDocument();
    expect(screen.getByText("System")).toBeInTheDocument();
  });

  it("highlights the selected theme", async () => {
    const user = userEvent.setup();
    renderWithTheme();
    await user.click(screen.getByText("Dark"));
    const darkBtn = screen.getByText("Dark").closest("button");
    expect(darkBtn?.className).toContain("border-primary");
  });
});
