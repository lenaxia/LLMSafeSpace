import { useState, useEffect } from "react";
import { secretsApi, type SecretResponse, type CreateSecretRequest } from "../../api/secrets";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";

const SECRET_TYPES = [
  { value: "llm-provider", label: "LLM Provider", metaFields: ["provider"] },
  { value: "ssh-key", label: "SSH Key", metaFields: ["key_type", "host"] },
  { value: "git-credential", label: "Git Credential", metaFields: ["host"] },
  { value: "secret-file", label: "Secret File", metaFields: ["mount_path"] },
  { value: "env-secret", label: "Environment Variable", metaFields: ["var_name"] },
] as const;

export function SecretsTab() {
  const [secrets, setSecrets] = useState<SecretResponse[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [showCreate, setShowCreate] = useState(false);

  const fetchSecrets = async () => {
    try {
      const res = await secretsApi.list();
      setSecrets(res.secrets || []);
    } catch (e: any) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchSecrets(); }, []);

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`Delete secret "${name}"? This cannot be undone.`)) return;
    try {
      await secretsApi.delete(id);
      setSecrets((s) => s.filter((x) => x.id !== id));
    } catch (e: any) {
      setError(e.message);
    }
  };

  if (loading) return <p className="text-sm text-muted-foreground">Loading secrets...</p>;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-lg font-medium">Secrets</h3>
          <p className="text-sm text-muted-foreground">
            Encrypted secrets injected into your workspaces. Values are never stored in plaintext.
          </p>
        </div>
        <Button onClick={() => setShowCreate(!showCreate)}>
          {showCreate ? "Cancel" : "+ New Secret"}
        </Button>
      </div>

      {error && <p className="text-sm text-red-500">{error}</p>}

      {showCreate && (
        <CreateSecretForm
          onCreated={() => { setShowCreate(false); fetchSecrets(); }}
          onError={setError}
        />
      )}

      {secrets.length === 0 && !showCreate ? (
        <p className="text-sm text-muted-foreground">No secrets yet. Create one to get started.</p>
      ) : (
        <div className="space-y-2">
          {secrets.map((s) => (
            <div key={s.id} className="flex items-center justify-between rounded-md border border-border p-3">
              <div>
                <span className="font-medium text-sm">{s.name}</span>
                <span className="ml-2 rounded bg-accent px-1.5 py-0.5 text-xs">{s.type}</span>
                {s.metadata && Object.keys(s.metadata).length > 0 && (
                  <span className="ml-2 text-xs text-muted-foreground">
                    {Object.entries(s.metadata).map(([k, v]) => `${k}=${v}`).join(", ")}
                  </span>
                )}
              </div>
              <button
                onClick={() => handleDelete(s.id, s.name)}
                className="text-xs text-red-500 hover:text-red-700"
              >
                Delete
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function CreateSecretForm({ onCreated, onError }: { onCreated: () => void; onError: (e: string) => void }) {
  const [name, setName] = useState("");
  const [type, setType] = useState<CreateSecretRequest["type"]>("llm-provider");
  const [value, setValue] = useState("");
  const [metadata, setMetadata] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);

  const selectedType = SECRET_TYPES.find((t) => t.value === type)!;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    try {
      await secretsApi.create({ name, type, value, metadata: Object.keys(metadata).length > 0 ? metadata : undefined });
      onCreated();
    } catch (err: any) {
      onError(err.message);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-4 rounded-md border border-border p-4">
      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="text-sm font-medium">Name</label>
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-api-key" required />
        </div>
        <div>
          <label className="text-sm font-medium">Type</label>
          <select
            value={type}
            onChange={(e) => { setType(e.target.value as any); setMetadata({}); }}
            className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm"
          >
            {SECRET_TYPES.map((t) => (
              <option key={t.value} value={t.value}>{t.label}</option>
            ))}
          </select>
        </div>
      </div>

      <div>
        <label className="text-sm font-medium">Value</label>
        <textarea
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder={type === "ssh-key" ? "-----BEGIN OPENSSH PRIVATE KEY-----" : "sk-..."}
          className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono min-h-[80px]"
          required
        />
      </div>

      {selectedType.metaFields.length > 0 && (
        <div className="grid grid-cols-2 gap-4">
          {selectedType.metaFields.map((field) => (
            <div key={field}>
              <label className="text-sm font-medium">{field}</label>
              <Input
                value={metadata[field] || ""}
                onChange={(e) => setMetadata({ ...metadata, [field]: e.target.value })}
                placeholder={field}
                required={field === "key_type" || field === "mount_path" || field === "var_name"}
              />
            </div>
          ))}
        </div>
      )}

      <Button type="submit" disabled={submitting || !name || !value}>
        {submitting ? "Creating..." : "Create Secret"}
      </Button>
    </form>
  );
}
