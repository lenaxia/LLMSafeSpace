import { useEffect, useState } from "react";
import { settingsApi, type SettingDef } from "../../api/settings";
import { SettingsForm } from "./SettingsForm";
import { Spinner } from "../ui/Spinner";
import { useTheme } from "../../providers/ThemeProvider";
import { useToast } from "../../providers/ToastProvider";

export function UserSettingsTab() {
  const [schema, setSchema] = useState<SettingDef[]>([]);
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const { setTheme } = useTheme();
  const { toast } = useToast();

  useEffect(() => {
    async function load() {
      try {
        const [schemaRes, valuesRes] = await Promise.all([
          settingsApi.getUserSchema(),
          settingsApi.getUserSettings(),
        ]);
        setSchema(schemaRes.settings);
        setValues(valuesRes.settings);
      } catch (e: unknown) {
        setError(e instanceof Error ? e.message : "Failed to load settings");
      } finally {
        setLoading(false);
      }
    }
    load();
  }, []);

  const handleSave = async (key: string, value: unknown) => {
    try {
      await settingsApi.setUserSetting(key, value);
      setValues((prev) => ({ ...prev, [key]: value }));

      // Apply side effects after successful persist
      applySideEffect(key, value);
    } catch (e: unknown) {
      toast(e instanceof Error ? e.message : "Failed to save setting", "error");
      throw e; // Re-throw so SettingRow shows inline error too
    }
  };

  const applySideEffect = (key: string, value: unknown) => {
    switch (key) {
      case "theme":
        setTheme(value as "light" | "dark" | "system");
        break;
      case "fontSize":
        if (typeof value === "number") {
          document.documentElement.style.fontSize = `${value}px`;
        }
        break;
      case "compactMode":
        document.documentElement.classList.toggle("compact", value === true);
        break;
    }
  };

  if (loading) return <div className="flex justify-center p-8"><Spinner /></div>;
  if (error) return <p className="text-destructive p-4">{error}</p>;

  return <SettingsForm schema={schema} values={values} onSave={handleSave} />;
}
