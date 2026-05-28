import { useCallback, useEffect, useState } from "react";
import { settingsApi } from "../api/settings";

const STORAGE_KEY = "llmsafespace_user_settings";

/** Reads user settings with localStorage-first strategy (instant render, API sync on mount). */
export function useUserSettings() {
  const [settings, setSettings] = useState<Record<string, unknown>>(() => {
    try {
      const cached = localStorage.getItem(STORAGE_KEY);
      return cached ? JSON.parse(cached) : {};
    } catch {
      return {};
    }
  });
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    settingsApi.getUserSettings()
      .then((res) => {
        setSettings(res.settings);
        localStorage.setItem(STORAGE_KEY, JSON.stringify(res.settings));
      })
      .catch(() => {}) // Use cached values on failure
      .finally(() => setLoading(false));
  }, []);

  const setSetting = useCallback(async (key: string, value: unknown) => {
    // Optimistic local update
    setSettings((prev) => {
      const next = { ...prev, [key]: value };
      localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
      return next;
    });
    // Persist to API
    await settingsApi.setUserSetting(key, value);
  }, []);

  return { settings, loading, setSetting };
}

/** Convenience: get a single typed setting with a default fallback. */
export function useUserSetting<T>(key: string, defaultValue: T): T {
  const { settings } = useUserSettings();
  return (settings[key] as T) ?? defaultValue;
}
