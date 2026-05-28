import { useEffect, useState } from "react";
import { settingsApi, type SettingDef } from "../../api/settings";
import { SettingsForm } from "./SettingsForm";
import { Spinner } from "../ui/Spinner";
import { useTheme } from "../../providers/ThemeProvider";
import { useToast } from "../../providers/ToastProvider";
import { useUserSettings } from "../../hooks/useUserSettings";

export function UserSettingsTab() {
  const [schema, setSchema] = useState<SettingDef[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const { setTheme } = useTheme();
  const { toast } = useToast();
  const { settings: values, setSetting } = useUserSettings();

  useEffect(() => {
    settingsApi.getUserSchema()
      .then((res) => setSchema(res.settings))
      .catch((e: unknown) => setError(e instanceof Error ? e.message : "Failed to load schema"))
      .finally(() => setLoading(false));
  }, []);

  const handleSave = async (key: string, value: unknown) => {
    try {
      // Update shared reactive store + persist to API
      await setSetting(key, value);
      // Apply immediate side effects
      applySideEffect(key, value);
    } catch (e: unknown) {
      toast(e instanceof Error ? e.message : "Failed to save setting", "error");
      throw e;
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
        document.documentElement.setAttribute("data-compact", String(!!value));
        break;
    }
  };

  if (loading) return <div className="flex justify-center p-8"><Spinner /></div>;
  if (error) return <p className="text-destructive p-4">{error}</p>;

  return <SettingsForm schema={schema} values={values} onSave={handleSave} />;
}
