import { useEffect, useState } from "react";
import { ApiClientError } from "../api/client";
import { settingsApi, type SettingDef } from "../api/settings";
import { SettingsForm } from "../components/settings/SettingsForm";
import { Button } from "../components/ui/Button";
import { Spinner } from "../components/ui/Spinner";
import { useToast } from "../providers/ToastProvider";

export function AdminSettingsPage() {
  const [schema, setSchema] = useState<SettingDef[]>([]);
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const { toast } = useToast();

  const [testRecipient, setTestRecipient] = useState("");
  const [sendingTest, setSendingTest] = useState(false);

  const hasEmailSection = schema.some((s) => s.category === "Email");

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

  const handleTestSend = async () => {
    if (!testRecipient.trim()) {
      toast("Enter a recipient email address", "error");
      return;
    }
    setSendingTest(true);
    try {
      const res = await settingsApi.testEmailSend(testRecipient.trim());
      if (res.sent) {
        toast(`Test email sent to ${testRecipient.trim()} via ${res.provider}`, "success");
      } else {
        toast(`Email provider is ${res.provider} (noop) — no email was sent. Configure SES to send real email.`, "error");
      }
    } catch (e: unknown) {
      const msg = e instanceof ApiClientError ? e.body.error : e instanceof Error ? e.message : "Test send failed";
      toast(msg, "error");
    } finally {
      setSendingTest(false);
    }
  };

  if (loading) return <div className="flex justify-center p-8"><Spinner /></div>;
  if (error === "not-admin") return <p className="text-sm text-muted-foreground p-4">Admin access required. Contact your instance administrator.</p>;
  if (error) return <p className="text-destructive p-4">{error}</p>;

  return (
    <div className="max-w-3xl mx-auto">
      <h1 className="text-lg font-semibold mb-6">Instance Settings</h1>
      <SettingsForm schema={schema} values={values} onSave={handleSave} />

      {hasEmailSection && (
        <section className="mt-8">
          <h3 className="mb-3 text-sm font-semibold text-muted-foreground uppercase tracking-wide">
            Email Test
          </h3>
          <div className="rounded-md border border-border p-4">
            <p className="text-xs text-muted-foreground mb-3">
              Send a test email to verify the configured email provider (SES) is working end-to-end.
            </p>
            <div className="flex gap-2">
              <input
                type="email"
                placeholder="recipient@example.com"
                value={testRecipient}
                onChange={(e) => setTestRecipient(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter" && !sendingTest) handleTestSend(); }}
                disabled={sendingTest}
                className="h-8 flex-1 rounded-md border border-border bg-background px-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring disabled:opacity-50"
              />
              <Button size="sm" onClick={handleTestSend} disabled={sendingTest}>
                {sendingTest ? "Sending..." : "Send Test Email"}
              </Button>
            </div>
          </div>
        </section>
      )}
    </div>
  );
}
