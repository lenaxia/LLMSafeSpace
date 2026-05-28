import { useEffect, useState } from "react";
import { settingsApi, type SettingDef } from "../../api/settings";
import { SettingsForm } from "./SettingsForm";
import { Spinner } from "../ui/Spinner";

export function UserSettingsTab() {
  const [schema, setSchema] = useState<SettingDef[]>([]);
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

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
    await settingsApi.setUserSetting(key, value);
    setValues((prev) => ({ ...prev, [key]: value }));
  };

  if (loading) return <div className="flex justify-center p-8"><Spinner /></div>;
  if (error) return <p className="text-destructive p-4">{error}</p>;

  return <SettingsForm schema={schema} values={values} onSave={handleSave} />;
}
