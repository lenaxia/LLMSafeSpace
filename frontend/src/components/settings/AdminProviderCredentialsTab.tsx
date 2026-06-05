// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Epic 30 US-30.7: Admin LLM Provider Credentials UI
// Replaces the old AdminCredentialsTab (credential_sets) which was deleted
// in migration 000015.

import { useEffect, useState } from "react";
import {
  adminProviderCredentialsApi,
  type AdminProviderCredential,
  type CreateAdminCredentialRequest,
} from "../../api/providerCredentials";
import { Spinner } from "../ui/Spinner";
import { Shield, Trash2, Plus, ChevronDown, ChevronUp, Eye, EyeOff } from "lucide-react";

export function AdminProviderCredentialsTab() {
  const [creds, setCreds] = useState<AdminProviderCredential[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);

  const load = async () => {
    try {
      const data = await adminProviderCredentialsApi.list();
      setCreds(data);
    } catch (e: unknown) {
      if (e instanceof Error && e.message.includes("403")) {
        setError("not-admin");
      } else {
        setError(e instanceof Error ? e.message : "Failed to load credentials");
      }
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { load(); }, []);

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`Delete "${name}"? Active workspaces using this credential will lose provider access.`)) return;
    try {
      await adminProviderCredentialsApi.delete(id);
      setCreds((prev) => prev.filter((c) => c.id !== id));
      if (expanded === id) setExpanded(null);
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : "Delete failed");
    }
  };

  const handleCreated = (c: AdminProviderCredential) => {
    setCreds((prev) => [...prev, c]);
    setShowCreate(false);
  };

  if (loading) return <div className="flex justify-center p-8"><Spinner /></div>;
  if (error === "not-admin") return null;
  if (error) return <p className="text-destructive p-4 text-sm">{error}</p>;

  return (
    <div className="max-w-3xl mx-auto space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-base font-semibold">Platform LLM Provider Credentials</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Credentials auto-applied to all new workspaces unless overridden by users.
          </p>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent flex items-center gap-1.5"
        >
          <Plus className="h-3 w-3" /> Add credential
        </button>
      </div>

      {showCreate && (
        <CreateAdminCredentialForm
          onCreated={handleCreated}
          onCancel={() => setShowCreate(false)}
        />
      )}

      {creds.length === 0 && !showCreate ? (
        <div className="rounded-md border border-dashed border-border p-6 text-center">
          <Shield className="h-8 w-8 mx-auto text-muted-foreground mb-2" />
          <p className="text-sm text-muted-foreground">No platform credentials configured.</p>
          <p className="text-xs text-muted-foreground mt-1">
            Users can still use free-tier models. Add a credential to enable paid models for all users.
          </p>
        </div>
      ) : (
        <div className="divide-y divide-border rounded-md border border-border">
          {creds.map((c) => (
            <CredentialRow
              key={c.id}
              cred={c}
              expanded={expanded === c.id}
              onToggle={() => setExpanded(expanded === c.id ? null : c.id)}
              onDelete={() => handleDelete(c.id, c.name)}
              onUpdated={(updated) => setCreds((prev) => prev.map((x) => (x.id === updated.id ? updated : x)))}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ─── Credential row with inline expand ───────────────────────────────────────

function CredentialRow({
  cred,
  expanded,
  onToggle,
  onDelete,
  onUpdated,
}: {
  cred: AdminProviderCredential;
  expanded: boolean;
  onToggle: () => void;
  onDelete: () => void;
  onUpdated: (c: AdminProviderCredential) => void;
}) {
  const [editApiKey, setEditApiKey] = useState("");
  const [showKey, setShowKey] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveErr, setSaveErr] = useState<string | null>(null);
  const [saveOk, setSaveOk] = useState(false);

  const handleRotateKey = async () => {
    if (!editApiKey.trim()) return;
    setSaving(true); setSaveErr(null); setSaveOk(false);
    try {
      const updated = await adminProviderCredentialsApi.update(cred.id, { apiKey: editApiKey.trim() });
      onUpdated(updated);
      setEditApiKey("");
      setSaveOk(true);
      setTimeout(() => setSaveOk(false), 2000);
    } catch (e: unknown) {
      setSaveErr(e instanceof Error ? e.message : "Update failed");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div>
      {/* Row header */}
      <div
        className="flex items-center gap-3 px-4 py-3 hover:bg-accent/30 cursor-pointer transition-colors"
        onClick={onToggle}
      >
        <Shield className="h-4 w-4 text-muted-foreground shrink-0" />
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium">{cred.name}</span>
            <span className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">{cred.provider}</span>
          </div>
          {cred.baseURL && (
            <p className="text-xs text-muted-foreground truncate">{cred.baseURL}</p>
          )}
        </div>
        <div className="flex items-center gap-1 shrink-0" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={onDelete}
            className="rounded p-1.5 hover:bg-destructive/10 text-destructive"
            title="Delete"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
          {expanded ? <ChevronUp className="h-4 w-4 text-muted-foreground" /> : <ChevronDown className="h-4 w-4 text-muted-foreground" />}
        </div>
      </div>

      {/* Expanded detail */}
      {expanded && (
        <div className="border-t border-border bg-muted/20 px-4 py-3 space-y-3">
          {/* Metadata */}
          <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
            <MetaRow label="ID" value={cred.id} mono />
            <MetaRow label="Updated" value={new Date(cred.updatedAt).toLocaleString()} />
            {cred.modelAllowlist?.length > 0 && (
              <div className="col-span-2">
                <MetaRow label="Model allowlist" value={cred.modelAllowlist.join(", ")} />
              </div>
            )}
          </div>

          {/* Rotate key */}
          <div>
            <p className="text-xs font-medium text-muted-foreground mb-1">Rotate API key</p>
            <div className="flex gap-2">
              <div className="relative flex-1">
                <input
                  type={showKey ? "text" : "password"}
                  value={editApiKey}
                  onChange={(e) => setEditApiKey(e.target.value)}
                  placeholder="New API key"
                  className="h-7 w-full rounded border border-border bg-background px-2 pr-7 text-xs focus:outline-none focus:ring-1 focus:ring-ring"
                />
                <button
                  type="button"
                  tabIndex={-1}
                  onClick={() => setShowKey((s) => !s)}
                  className="absolute right-1.5 top-1 text-muted-foreground hover:text-foreground"
                >
                  {showKey ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
                </button>
              </div>
              <button
                onClick={handleRotateKey}
                disabled={saving || !editApiKey.trim()}
                className="rounded border border-border px-2 text-xs hover:bg-accent disabled:opacity-50 shrink-0"
              >
                {saving ? "…" : saveOk ? "✓" : "Update"}
              </button>
            </div>
            {saveErr && <p className="mt-1 text-xs text-destructive">{saveErr}</p>}
          </div>
        </div>
      )}
    </div>
  );
}

function MetaRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex gap-2 min-w-0">
      <span className="text-muted-foreground shrink-0">{label}:</span>
      <span className={`truncate ${mono ? "font-mono text-[10px]" : ""}`}>{value || "—"}</span>
    </div>
  );
}

// ─── Create form ──────────────────────────────────────────────────────────────

function CreateAdminCredentialForm({
  onCreated,
  onCancel,
}: {
  onCreated: (c: AdminProviderCredential) => void;
  onCancel: () => void;
}) {
  const [form, setForm] = useState<CreateAdminCredentialRequest>({
    name: "",
    provider: "",
    apiKey: "",
    baseURL: "",
    modelAllowlist: [],
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
      const req: CreateAdminCredentialRequest = {
        name: form.name.trim(),
        provider: form.provider.trim(),
        apiKey: form.apiKey.trim(),
        ...(form.baseURL?.trim() ? { baseURL: form.baseURL.trim() } : {}),
      };
      const c = await adminProviderCredentialsApi.create(req);
      onCreated(c);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Create failed");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="rounded-md border border-border bg-muted/20 p-4 space-y-3">
      <h3 className="text-sm font-semibold">New Platform Credential</h3>
      {error && <p className="text-xs text-destructive">{error}</p>}

      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="text-xs text-muted-foreground">Name</label>
          <input
            type="text"
            value={form.name}
            onChange={set("name")}
            placeholder="e.g. Anthropic Production"
            className="mt-0.5 h-8 w-full rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
        <div>
          <label className="text-xs text-muted-foreground">Provider</label>
          <input
            type="text"
            value={form.provider}
            onChange={set("provider")}
            placeholder="e.g. anthropic"
            className="mt-0.5 h-8 w-full rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
      </div>

      <div>
        <label className="text-xs text-muted-foreground">API Key</label>
        <div className="relative mt-0.5">
          <input
            type={showKey ? "text" : "password"}
            value={form.apiKey}
            onChange={set("apiKey")}
            placeholder="sk-…"
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
        <label className="text-xs text-muted-foreground">Base URL <span className="text-muted-foreground/50">(optional)</span></label>
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
