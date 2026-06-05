// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Epic 30 US-30.8: User LLM Provider Credentials UI
// Users can add their own API keys which take priority over admin credentials.

import { useEffect, useState } from "react";
import {
  userProviderCredentialsApi,
  type UserProviderCredential,
  type CreateUserCredentialRequest,
} from "../../api/providerCredentials";
import { Spinner } from "../ui/Spinner";
import { KeyRound, Trash2, Plus, Eye, EyeOff } from "lucide-react";

export function UserProviderCredentialsTab() {
  const [creds, setCreds] = useState<UserProviderCredential[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  const load = async () => {
    try {
      const data = await userProviderCredentialsApi.list();
      setCreds(data);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to load credentials");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { load(); }, []);

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`Remove "${name}"? Workspaces using this credential will fall back to platform defaults.`)) return;
    try {
      await userProviderCredentialsApi.delete(id);
      setCreds((prev) => prev.filter((c) => c.id !== id));
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : "Delete failed");
    }
  };

  const handleCreated = (c: UserProviderCredential) => {
    setCreds((prev) => [...prev, c]);
    setShowCreate(false);
  };

  if (loading) return <div className="flex justify-center p-8"><Spinner /></div>;
  if (error) return <p className="text-destructive p-4 text-sm">{error}</p>;

  return (
    <div className="max-w-2xl space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-base font-semibold">My LLM Provider Keys</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Your personal API keys. These take priority over platform defaults when bound to a workspace.
          </p>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent flex items-center gap-1.5"
        >
          <Plus className="h-3 w-3" /> Add key
        </button>
      </div>

      {showCreate && (
        <CreateUserCredentialForm
          onCreated={handleCreated}
          onCancel={() => setShowCreate(false)}
        />
      )}

      {creds.length === 0 && !showCreate ? (
        <div className="rounded-md border border-dashed border-border p-6 text-center">
          <KeyRound className="h-8 w-8 mx-auto text-muted-foreground mb-2" />
          <p className="text-sm text-muted-foreground">No personal provider keys added yet.</p>
          <p className="text-xs text-muted-foreground mt-1">
            You can use workspaces with platform-provided free-tier models without adding a key.
          </p>
        </div>
      ) : (
        <div className="divide-y divide-border rounded-md border border-border">
          {creds.map((c) => (
            <div key={c.id} className="flex items-center gap-3 px-4 py-3 hover:bg-accent/20">
              <KeyRound className="h-4 w-4 text-muted-foreground shrink-0" />
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">{c.name}</span>
                  <span className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">{c.provider}</span>
                </div>
                <p className="text-xs text-muted-foreground">
                  Added {new Date(c.createdAt).toLocaleDateString()}
                </p>
              </div>
              <button
                onClick={() => handleDelete(c.id, c.name)}
                className="rounded p-1.5 hover:bg-destructive/10 text-destructive shrink-0"
                title="Remove"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            </div>
          ))}
        </div>
      )}

      {creds.length > 0 && (
        <p className="text-xs text-muted-foreground">
          To use a key with a specific workspace, bind it from the workspace settings panel.
        </p>
      )}
    </div>
  );
}

// ─── Create form ──────────────────────────────────────────────────────────────

function CreateUserCredentialForm({
  onCreated,
  onCancel,
}: {
  onCreated: (c: UserProviderCredential) => void;
  onCancel: () => void;
}) {
  const [form, setForm] = useState<CreateUserCredentialRequest>({
    name: "",
    provider: "",
    apiKey: "",
    baseURL: "",
  });
  const [showKey, setShowKey] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((prev) => ({ ...prev, [k]: e.target.value }));

  const handleSubmit = async () => {
    if (!form.name || !form.provider || !form.apiKey) {
      setError("Name, provider, and API key are required");
      return;
    }
    setSaving(true); setError(null);
    try {
      const req: CreateUserCredentialRequest = {
        name: form.name.trim(),
        provider: form.provider.trim(),
        apiKey: form.apiKey.trim(),
        ...(form.baseURL?.trim() ? { baseURL: form.baseURL.trim() } : {}),
      };
      const c = await userProviderCredentialsApi.create(req);
      onCreated(c);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Create failed");
    } finally {
      setSaving(false);
    }
  };

  // Common providers for quick-pick
  const PROVIDERS = ["anthropic", "openai", "google", "openrouter", "groq", "mistral"] as const;

  return (
    <div className="rounded-md border border-border bg-muted/20 p-4 space-y-3">
      <h3 className="text-sm font-semibold">Add Provider Key</h3>
      {error && <p className="text-xs text-destructive">{error}</p>}

      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="text-xs text-muted-foreground">Display name</label>
          <input
            type="text"
            value={form.name}
            onChange={set("name")}
            placeholder="e.g. My Anthropic Key"
            className="mt-0.5 h-8 w-full rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
        <div>
          <label className="text-xs text-muted-foreground">Provider</label>
          <input
            list="provider-suggestions"
            type="text"
            value={form.provider}
            onChange={set("provider")}
            placeholder="e.g. anthropic"
            className="mt-0.5 h-8 w-full rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
          />
          <datalist id="provider-suggestions">
            {PROVIDERS.map((p) => <option key={p} value={p} />)}
          </datalist>
        </div>
      </div>

      <div>
        <label className="text-xs text-muted-foreground">API Key</label>
        <div className="relative mt-0.5">
          <input
            type={showKey ? "text" : "password"}
            value={form.apiKey}
            onChange={set("apiKey")}
            placeholder="sk-… or key-…"
            className="h-8 w-full rounded-md border border-border bg-background px-2 pr-8 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
          />
          <button
            type="button"
            tabIndex={-1}
            onClick={() => setShowKey((s) => !s)}
            className="absolute right-2 top-1.5 text-muted-foreground hover:text-foreground"
          >
            {showKey ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
          </button>
        </div>
      </div>

      <div>
        <label className="text-xs text-muted-foreground">
          Base URL <span className="text-muted-foreground/50">(optional, for custom endpoints)</span>
        </label>
        <input
          type="text"
          value={form.baseURL ?? ""}
          onChange={set("baseURL")}
          placeholder="https://api.example.com/v1"
          className="mt-0.5 h-8 w-full rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
        />
      </div>

      <div className="flex gap-2 pt-1">
        <button
          onClick={handleSubmit}
          disabled={saving}
          className="rounded-md bg-primary px-3 py-1.5 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          {saving ? "Saving…" : "Add key"}
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
