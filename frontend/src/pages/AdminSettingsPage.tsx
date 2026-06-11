import { useEffect, useState } from "react";
import { ApiClientError } from "../api/client";
import { settingsApi, type SettingDef } from "../api/settings";
import { SettingsForm } from "../components/settings/SettingsForm";
import { Spinner } from "../components/ui/Spinner";
import { useToast } from "../providers/ToastProvider";

export function AdminSettingsPage() {
  const [schema, setSchema] = useState<SettingDef[]>([]);
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const { toast } = useToast();

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
        if (e instanceof ApiClientError && e.status === 404) {
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
    try {
      await settingsApi.setAdminSetting(key, value);
      setValues((prev) => ({ ...prev, [key]: value }));
    } catch (e: unknown) {
      toast(e instanceof Error ? e.message : "Failed to save setting", "error");
      throw e;
    }
  };

  if (loading) return <div className="flex justify-center p-8"><Spinner /></div>;
  if (error === "not-admin") return <p className="text-sm text-muted-foreground p-4">Admin access required. Contact your instance administrator.</p>;
  if (error) return <p className="text-destructive p-4">{error}</p>;

  return (
    <div className="max-w-3xl mx-auto">
      <h1 className="text-lg font-semibold mb-6">Instance Settings</h1>
      <SettingsForm schema={schema} values={values} onSave={handleSave} />
    </div>
  );
}
