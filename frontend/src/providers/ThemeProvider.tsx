import { createContext, useCallback, useContext, useEffect, useState } from "react";
import type { ReactNode } from "react";
import { settingsApi } from "../api/settings";

type Theme = "light" | "dark" | "system";

interface ThemeContextValue {
  theme: Theme;
  resolved: "light" | "dark";
  setTheme: (t: Theme) => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

function getSystemTheme(): "light" | "dark" {
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(
    () => (localStorage.getItem("lsp-theme") as Theme) || "system",
  );
  const [resolved, setResolved] = useState<"light" | "dark">(
    theme === "system" ? getSystemTheme() : theme,
  );

  // Sync from API on mount — only if authenticated (cookie present)
  useEffect(() => {
    const hasSession = document.cookie.includes("lsp_session");
    if (!hasSession) return;
    settingsApi.getUserSettings()
      .then((res) => {
        const apiTheme = res.settings.theme as Theme | undefined;
        if (apiTheme && apiTheme !== theme) {
          localStorage.setItem("lsp-theme", apiTheme);
          setThemeState(apiTheme);
        }
        const size = res.settings.fontSize as number | undefined;
        if (size && size >= 10 && size <= 24) {
          document.documentElement.style.fontSize = `${size}px`;
        }
        const compact = res.settings.compactMode as boolean | undefined;
        document.documentElement.setAttribute("data-compact", String(!!compact));
      })
      .catch(() => {}); // Use localStorage value on failure
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const setTheme = useCallback((t: Theme) => {
    localStorage.setItem("lsp-theme", t);
    setThemeState(t);
    // Persist to API (fire-and-forget)
    settingsApi.setUserSetting("theme", t).catch(() => {});
  }, []);

  useEffect(() => {
    const r = theme === "system" ? getSystemTheme() : theme;
    setResolved(r);
    document.documentElement.classList.toggle("dark", r === "dark");
  }, [theme]);

  useEffect(() => {
    if (theme !== "system") return;
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = () => {
      const r = getSystemTheme();
      setResolved(r);
      document.documentElement.classList.toggle("dark", r === "dark");
    };
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, [theme]);

  return (
    <ThemeContext.Provider value={{ theme, resolved, setTheme }}>
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error("useTheme must be used within ThemeProvider");
  return ctx;
}
