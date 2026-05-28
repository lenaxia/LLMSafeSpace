import { useState, useEffect } from "react";
import { secretsApi, type SecretResponse } from "../../api/secrets";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";

const SECRET_TYPES = [
  { value: "llm-provider", label: "LLM Providers", icon: "🤖", metaFields: ["provider"] },
  { value: "ssh-key", label: "SSH Keys", icon: "🔑", metaFields: ["key_type", "host"] },
  { value: "git-credential", label: "Git Credentials", icon: "📦", metaFields: ["host"] },
  { value: "secret-file", label: "Secret Files", icon: "📄", metaFields: ["mount_path"] },
  { value: "env-secret", label: "Environment Variables", icon: "⚙️", metaFields: ["var_name"] },
] as const;

type SecretType = (typeof SECRET_TYPES)[number]["value"];

export function SecretsTab() {
  const [secrets, setSecrets] = useState<SecretResponse[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

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

  const toggleGroup = (type: string) => {
    setCollapsed((c) => ({ ...c, [type]: !c[type] }));
  };

  // Group secrets by type
  const grouped = SECRET_TYPES.map((t) => ({
    ...t,
    secrets: secrets.filter((s) => s.type === t.value),
  })).filter((g) => g.secrets.length > 0);

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

      {grouped.length === 0 && !showCreate ? (
        <p className="text-sm text-muted-foreground">No secrets yet. Create one to get started.</p>
      ) : (
        <div className="space-y-3">
          {grouped.map((group) => (
            <div key={group.value} className="rounded-md border border-border">
              <button
                onClick={() => toggleGroup(group.value)}
                className="flex w-full items-center justify-between px-4 py-2.5 text-left hover:bg-accent/30 transition-colors"
              >
                <span className="flex items-center gap-2 text-sm font-medium">
                  <span>{group.icon}</span>
                  <span>{group.label}</span>
                  <span className="rounded-full bg-accent px-2 py-0.5 text-xs">{group.secrets.length}</span>
                </span>
                <span className="text-xs text-muted-foreground">
                  {collapsed[group.value] ? "▶" : "▼"}
                </span>
              </button>
              {!collapsed[group.value] && (
                <div className="border-t border-border">
                  {group.secrets.map((s) => (
                    <div key={s.id} className="flex items-center justify-between px-4 py-2 hover:bg-accent/10">
                      <div className="flex items-center gap-3">
                        <span className="text-sm font-medium">{s.name}</span>
                        {s.metadata && Object.keys(s.metadata).length > 0 && (
                          <span className="text-xs text-muted-foreground">
                            {Object.entries(s.metadata).map(([k, v]) => `${k}: ${v}`).join(" · ")}
                          </span>
                        )}
                      </div>
                      <button
                        onClick={() => handleDelete(s.id, s.name)}
                        className="rounded px-2 py-1 text-xs text-red-500 hover:bg-red-50 hover:text-red-700 transition-colors"
                      >
                        Delete
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function CreateSecretForm({ onCreated, onError }: { onCreated: () => void; onError: (e: string) => void }) {
  const [name, setName] = useState("");
  const [type, setType] = useState<SecretType>("llm-provider");
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
    <form onSubmit={handleSubmit} className="space-y-4 rounded-md border border-border p-4 bg-accent/5">
      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="text-sm font-medium">Name</label>
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-api-key" required />
        </div>
        <div>
          <label className="text-sm font-medium">Type</label>
          <select
            value={type}
            onChange={(e) => { setType(e.target.value as SecretType); setMetadata({}); }}
            className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm"
          >
            {SECRET_TYPES.map((t) => (
              <option key={t.value} value={t.value}>{t.icon} {t.label}</option>
            ))}
          </select>
        </div>
      </div>

      <div>
        <label className="text-sm font-medium">Value</label>
        <textarea
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder={type === "ssh-key" ? "-----BEGIN OPENSSH PRIVATE KEY-----" : type === "env-secret" ? "postgres://user:pass@host/db" : "sk-..."}
          className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono min-h-[80px] resize-y"
          required
        />
        <p className="mt-1 text-xs text-muted-foreground">This value will be encrypted. You won't be able to view it after saving.</p>
      </div>

      {selectedType.metaFields.length > 0 && (
        <div className="grid grid-cols-2 gap-4">
          {selectedType.metaFields.map((field) => (
            <div key={field}>
              <label className="text-sm font-medium">{field.replace("_", " ")}</label>
              <Input
                value={metadata[field] || ""}
                onChange={(e) => setMetadata({ ...metadata, [field]: e.target.value })}
                placeholder={field === "key_type" ? "ed25519" : field === "mount_path" ? "/workspace/.secrets/cert.pem" : field === "var_name" ? "DATABASE_URL" : field === "provider" ? "anthropic" : "github.com"}
                required={field === "key_type" || field === "mount_path" || field === "var_name"}
              />
            </div>
          ))}
        </div>
      )}

      <Button type="submit" disabled={submitting || !name || !value}>
        {submitting ? "Encrypting..." : "Create Secret"}
      </Button>
    </form>
  );
}
