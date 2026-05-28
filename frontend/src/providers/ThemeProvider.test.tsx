import { describe, expect, it, beforeEach, vi } from "vitest";
import { screen, act, waitFor } from "@testing-library/react";
import { render } from "@testing-library/react";
import { ThemeProvider, useTheme } from "./ThemeProvider";

function TestConsumer() {
  const { theme, resolved, setTheme } = useTheme();
  return (
    <div>
      <span data-testid="theme">{theme}</span>
      <span data-testid="resolved">{resolved}</span>
      <button onClick={() => setTheme("dark")}>dark</button>
      <button onClick={() => setTheme("light")}>light</button>
      <button onClick={() => setTheme("system")}>system</button>
    </div>
  );
}

describe("ThemeProvider", () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.classList.remove("dark");
  });

  it("defaults to system theme", () => {
    render(<ThemeProvider><TestConsumer /></ThemeProvider>);
    expect(screen.getByTestId("theme").textContent).toBe("system");
  });

  it("resolves system theme to light when matchMedia returns false", () => {
    render(<ThemeProvider><TestConsumer /></ThemeProvider>);
    expect(screen.getByTestId("resolved").textContent).toBe("light");
  });

  it("switches to dark theme", () => {
    render(<ThemeProvider><TestConsumer /></ThemeProvider>);
    act(() => { screen.getByText("dark").click(); });
    expect(screen.getByTestId("theme").textContent).toBe("dark");
    expect(screen.getByTestId("resolved").textContent).toBe("dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
  });

  it("switches to light theme", () => {
    render(<ThemeProvider><TestConsumer /></ThemeProvider>);
    act(() => { screen.getByText("dark").click(); });
    act(() => { screen.getByText("light").click(); });
    expect(screen.getByTestId("resolved").textContent).toBe("light");
    expect(document.documentElement.classList.contains("dark")).toBe(false);
  });

  it("persists theme to localStorage", () => {
    render(<ThemeProvider><TestConsumer /></ThemeProvider>);
    act(() => { screen.getByText("dark").click(); });
    expect(localStorage.getItem("lsp-theme")).toBe("dark");
  });

  it("reads theme from localStorage on mount", () => {
    localStorage.setItem("lsp-theme", "dark");
    render(<ThemeProvider><TestConsumer /></ThemeProvider>);
    expect(screen.getByTestId("theme").textContent).toBe("dark");
    expect(screen.getByTestId("resolved").textContent).toBe("dark");
  });

  it("throws when useTheme is used outside provider", () => {
    expect(() => render(<TestConsumer />)).toThrow("useTheme must be used within ThemeProvider");
  });

  describe("compactMode", () => {
    beforeEach(() => {
      document.documentElement.removeAttribute("data-compact");
    });

    it("sets data-compact=true when API returns compactMode true", async () => {
      // Simulate authenticated session
      Object.defineProperty(document, "cookie", { value: "lsp_session=abc", writable: true });
      const { settingsApi } = await import("../api/settings");
      vi.spyOn(settingsApi, "getUserSettings").mockResolvedValueOnce({
        settings: { theme: "system", compactMode: true },
        schemaVersion: 1,
      });

      render(<ThemeProvider><TestConsumer /></ThemeProvider>);

      await waitFor(() => {
        expect(document.documentElement.getAttribute("data-compact")).toBe("true");
      });

      vi.restoreAllMocks();
      Object.defineProperty(document, "cookie", { value: "", writable: true });
    });

    it("sets data-compact=false when API returns compactMode false", async () => {
      Object.defineProperty(document, "cookie", { value: "lsp_session=abc", writable: true });
      const { settingsApi } = await import("../api/settings");
      vi.spyOn(settingsApi, "getUserSettings").mockResolvedValueOnce({
        settings: { theme: "system", compactMode: false },
        schemaVersion: 1,
      });

      render(<ThemeProvider><TestConsumer /></ThemeProvider>);

      await waitFor(() => {
        expect(document.documentElement.getAttribute("data-compact")).toBe("false");
      });

      vi.restoreAllMocks();
      Object.defineProperty(document, "cookie", { value: "", writable: true });
    });
  });
});
