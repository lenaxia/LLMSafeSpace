import { renderHook, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { useUserSettings } from "./useUserSettings";

const mockGetUserSettings = vi.fn();
const mockSetUserSetting = vi.fn();

vi.mock("../api/settings", () => ({
  settingsApi: {
    getUserSettings: () => mockGetUserSettings(),
    setUserSetting: (key: string, value: unknown) => mockSetUserSetting(key, value),
    getUserSchema: vi.fn(),
  },
}));

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
});

describe("useUserSettings", () => {
  it("returns empty settings initially when no cache", () => {
    mockGetUserSettings.mockReturnValue(new Promise(() => {}));
    const { result } = renderHook(() => useUserSettings());
    expect(result.current.settings).toEqual({});
    expect(result.current.loading).toBe(true);
  });

  it("loads settings from API on mount", async () => {
    mockGetUserSettings.mockResolvedValue({ settings: { theme: "dark", fontSize: 16 }, schemaVersion: 1 });
    const { result } = renderHook(() => useUserSettings());

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });
    expect(result.current.settings.theme).toBe("dark");
    expect(result.current.settings.fontSize).toBe(16);
  });

  it("caches settings in localStorage", async () => {
    mockGetUserSettings.mockResolvedValue({ settings: { theme: "dark" }, schemaVersion: 1 });
    renderHook(() => useUserSettings());

    await waitFor(() => {
      const cached = JSON.parse(localStorage.getItem("llmsafespace_user_settings")!);
      expect(cached.theme).toBe("dark");
    });
  });

  it("reads from localStorage cache on mount", () => {
    localStorage.setItem("llmsafespace_user_settings", JSON.stringify({ theme: "light" }));
    mockGetUserSettings.mockReturnValue(new Promise(() => {}));

    const { result } = renderHook(() => useUserSettings());
    expect(result.current.settings.theme).toBe("light");
  });

  it("API overrides localStorage cache", async () => {
    localStorage.setItem("llmsafespace_user_settings", JSON.stringify({ theme: "light" }));
    mockGetUserSettings.mockResolvedValue({ settings: { theme: "dark" }, schemaVersion: 1 });

    const { result } = renderHook(() => useUserSettings());
    await waitFor(() => {
      expect(result.current.settings.theme).toBe("dark");
    });
  });

  it("setSetting updates optimistically and calls API", async () => {
    mockGetUserSettings.mockResolvedValue({ settings: { theme: "light" }, schemaVersion: 1 });
    mockSetUserSetting.mockResolvedValue({});

    const { result } = renderHook(() => useUserSettings());
    await waitFor(() => expect(result.current.loading).toBe(false));

    await result.current.setSetting("theme", "dark");

    await waitFor(() => {
      expect(result.current.settings.theme).toBe("dark");
    });
    expect(mockSetUserSetting).toHaveBeenCalledWith("theme", "dark");
  });

  it("falls back to cache when API fails", async () => {
    localStorage.setItem("llmsafespace_user_settings", JSON.stringify({ theme: "cached" }));
    mockGetUserSettings.mockRejectedValue(new Error("network"));

    const { result } = renderHook(() => useUserSettings());
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.settings.theme).toBe("cached");
  });
});
