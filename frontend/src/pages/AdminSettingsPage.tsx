import { useEffect, useState } from "react";
import { settingsApi, type SettingDef } from "../api/settings";
import { SettingsForm } from "../components/settings/SettingsForm";
import { Spinner } from "../components/ui/Spinner";

export function AdminSettingsPage() {
  const [schema, setSchema] = useState<SettingDef[]>([]);
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    async function load() {
      try {
        const [schemaRes, valuesRes] = await Promise.all([
          settingsApi.getAdminSchema(),
          settingsApi.getAdminSettings(),
        ]);
        setSchema(schemaRes.settings);
        setValues(valuesRes.settings);
      } catch (e: unknown) {
        if (e instanceof Error && e.message.includes("404")) {
          setError("not-admin");
        } else {
          setError(e instanceof Error ? e.message : "Failed to load settings");
        }
      } finally {
        setLoading(false);
      }
    }
    load();
  }, []);

  const handleSave = async (key: string, value: unknown) => {
    await settingsApi.setAdminSetting(key, value);
    setValues((prev) => ({ ...prev, [key]: value }));
  };

  if (loading) return <div className="flex justify-center p-8"><Spinner /></div>;
  if (error === "not-admin") return null; // Hidden from non-admins
  if (error) return <p className="text-destructive p-4">{error}</p>;

  return (
    <div className="max-w-3xl mx-auto">
      <h1 className="text-lg font-semibold mb-6">Instance Settings</h1>
      <SettingsForm schema={schema} values={values} onSave={handleSave} />
    </div>
  );
}
