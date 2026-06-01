import { useEffect, useState } from "react";
import { credentialsApi, type CredentialSet, type RotateKeyResult, type CreateCredentialSetRequest } from "../../api/credentials";
import { Spinner } from "../ui/Spinner";
import { Shield, Trash2, Star, Plus } from "lucide-react";

export function AdminCredentialsTab() {
  const [sets, setSets] = useState<CredentialSet[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [rotateResult, setRotateResult] = useState<RotateKeyResult | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  const load = async () => {
    try {
      const data = await credentialsApi.list();
      setSets(data);
    } catch (e: unknown) {
      if (e instanceof Error && e.message.includes("404")) {
        setError("not-admin");
      } else {
        setError(e instanceof Error ? e.message : "Failed to load");
      }
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { load(); }, []);

  const handleDelete = async (id: string) => {
    if (!confirm("Delete this credential set?")) return;
    try {
      await credentialsApi.delete(id);
      setSets((prev) => prev.filter((s) => s.id !== id));
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : "Delete failed");
    }
  };

  const handleSetDefault = async (id: string) => {
    try {
      await credentialsApi.setDefault(id);
      setSets((prev) => prev.map((s) => ({ ...s, isDefault: s.id === id })));
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : "Failed to set default");
    }
  };

  const handleRotate = async () => {
    try {
      const result = await credentialsApi.rotateKey();
      setRotateResult(result);
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : "Rotation failed");
    }
  };

  if (loading) return <div className="flex justify-center p-8"><Spinner /></div>;
  if (error === "not-admin") return null;
  if (error) return <p className="text-destructive p-4">{error}</p>;

  return (
    <div className="max-w-3xl mx-auto space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">Credential Sets</h2>
        <div className="flex gap-2">
          <button
            onClick={() => setShowCreate(true)}
            className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent flex items-center gap-1"
          >
            <Plus className="h-3 w-3" /> Add
          </button>
          <button
            onClick={handleRotate}
            className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent"
          >
            Rotate Encryption Key
          </button>
        </div>
      </div>

      {showCreate && (
        <CreateCredentialForm
          onCreated={(cs) => { setSets((prev) => [...prev, cs]); setShowCreate(false); }}
          onCancel={() => setShowCreate(false)}
        />
      )}

      {rotateResult && (
        <div className="rounded-md border border-border bg-muted/50 p-3 text-xs">
          Rotation complete: {rotateResult.rotated} rotated, {rotateResult.alreadyCurrent} already current, {rotateResult.errors} errors
        </div>
      )}

      {sets.length === 0 ? (
        <p className="text-sm text-muted-foreground">No credential sets configured.</p>
      ) : (
        <div className="space-y-3">
          {sets.map((cs) => (
            <div key={cs.id} className="flex items-center gap-3 rounded-md border border-border p-3">
              <Shield className="h-4 w-4 text-muted-foreground shrink-0" />
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">{cs.name}</span>
                  {cs.isDefault && (
                    <span className="rounded bg-primary/10 px-1.5 py-0.5 text-xs text-primary">default</span>
                  )}
                </div>
                <p className="text-xs text-muted-foreground">
                  Providers: {cs.providers.join(", ") || "none"} · Models: {cs.modelAllowlist.length || "all"}
                </p>
              </div>
              <div className="flex gap-1 shrink-0">
                {!cs.isDefault && (
                  <button
                    onClick={() => handleSetDefault(cs.id)}
                    className="rounded p-1.5 hover:bg-accent"
                    title="Set as default"
                  >
                    <Star className="h-3.5 w-3.5" />
                  </button>
                )}
                <button
                  onClick={() => handleDelete(cs.id)}
                  className="rounded p-1.5 hover:bg-destructive/10 text-destructive"
                  title="Delete"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function CreateCredentialForm({ onCreated, onCancel }: {
  onCreated: (cs: CredentialSet) => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState("");
  const [providerName, setProviderName] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [providers, setProviders] = useState<Record<string, { apiKey: string; baseUrl?: string }>>({});
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const addProvider = () => {
    if (!providerName || !apiKey) return;
    setProviders((prev) => ({
      ...prev,
      [providerName]: { apiKey, ...(baseUrl ? { baseUrl } : {}) },
    }));
    setProviderName("");
    setApiKey("");
    setBaseUrl("");
  };

  const removeProvider = (key: string) => {
    setProviders((prev) => {
      const next = { ...prev };
      delete next[key];
      return next;
    });
  };

  const handleSubmit = async () => {
    if (!name || Object.keys(providers).length === 0) {
      setError("Name and at least one provider are required");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const req: CreateCredentialSetRequest = { name, providers };
      const cs = await credentialsApi.create(req);
      onCreated(cs);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Create failed");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="rounded-md border border-border p-4 space-y-3">
      <h3 className="text-sm font-semibold">New Credential Set</h3>
      {error && <p className="text-xs text-destructive">{error}</p>}
      <input
        type="text"
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder="Credential set name"
        className="h-8 w-full rounded-md border border-border bg-background px-2 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
      />
      <div className="space-y-2">
        <p className="text-xs text-muted-foreground font-medium">Providers</p>
        {Object.entries(providers).map(([key, val]) => (
          <div key={key} className="flex items-center gap-2 text-xs">
            <span className="font-medium">{key}</span>
            <span className="text-muted-foreground truncate">{val.apiKey.slice(0, 8)}…</span>
            <button onClick={() => removeProvider(key)} className="text-destructive hover:underline">remove</button>
          </div>
        ))}
        <div className="flex flex-wrap gap-2">
          <input
            type="text"
            value={providerName}
            onChange={(e) => setProviderName(e.target.value)}
            placeholder="Provider (e.g. openai)"
            className="h-7 flex-1 min-w-[8rem] rounded border border-border bg-background px-2 text-xs"
          />
          <input
            type="password"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder="API Key"
            className="h-7 flex-1 min-w-[8rem] rounded border border-border bg-background px-2 text-xs"
          />
          <input
            type="text"
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            placeholder="Base URL (optional)"
            className="h-7 flex-1 min-w-[8rem] rounded border border-border bg-background px-2 text-xs"
          />
          <button
            onClick={addProvider}
            disabled={!providerName || !apiKey}
            className="h-7 rounded border border-border px-2 text-xs hover:bg-accent disabled:opacity-50 shrink-0"
          >
            Add
          </button>
        </div>
      </div>
      <div className="flex gap-2 pt-2">
        <button
          onClick={handleSubmit}
          disabled={saving}
          className="rounded-md bg-primary px-3 py-1.5 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          {saving ? "Creating…" : "Create"}
        </button>
        <button
          onClick={onCancel}
          className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}
