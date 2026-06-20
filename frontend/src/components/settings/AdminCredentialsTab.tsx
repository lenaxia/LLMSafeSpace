// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState, useRef } from "react";
import {
  credentialsApi,
  type CredentialSet,
  type RotateKeyResult,
  type CreateCredentialSetRequest,
} from "../../api/credentials";
import { Spinner } from "../ui/Spinner";
import { Shield, Trash2, Star, Plus, ChevronRight, X, Eye, EyeOff } from "lucide-react";

// ─── Main tab ────────────────────────────────────────────────────────────────

export function AdminCredentialsTab() {
  const [sets, setSets] = useState<CredentialSet[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [rotateResult, setRotateResult] = useState<RotateKeyResult | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [drawer, setDrawer] = useState<CredentialSet | null>(null);

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
      if (drawer?.id === id) setDrawer(null);
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : "Delete failed");
    }
  };

  const handleSetDefault = async (id: string) => {
    try {
      await credentialsApi.setDefault(id);
      setSets((prev) => prev.map((s) => ({ ...s, isDefault: s.id === id })));
      // Also update the drawer if it's open for a different set
      setDrawer((prev) => prev ? { ...prev, isDefault: prev.id === id } : null);
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

  const handleUpdated = (updated: CredentialSet) => {
    setSets((prev) => prev.map((s) => (s.id === updated.id ? updated : s)));
    setDrawer(updated);
  };

  if (loading) return <div className="flex justify-center p-8"><Spinner /></div>;
  if (error === "not-admin") return null;
  if (error) return <p className="text-destructive p-4">{error}</p>;

  return (
    <div className="max-w-3xl mx-auto space-y-6">
      {/* Header */}
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

      {/* Create form (inline, above list) */}
      {showCreate && (
        <CreateCredentialForm
          onCreated={(cs) => { setSets((prev) => [...prev, cs]); setShowCreate(false); }}
          onCancel={() => setShowCreate(false)}
        />
      )}

      {/* Rotate result banner */}
      {rotateResult && (
        <div className="rounded-md border border-border bg-muted/50 p-3 text-xs">
          Rotation complete: {rotateResult.rotated} rotated, {rotateResult.alreadyCurrent} already current, {rotateResult.errors} errors
        </div>
      )}

      {/* List */}
      {sets.length === 0 ? (
        <p className="text-sm text-muted-foreground">No credential sets configured.</p>
      ) : (
        <div className="space-y-2">
          {sets.map((cs) => (
            <div
              key={cs.id}
              onClick={() => setDrawer(cs)}
              className="flex items-center gap-3 rounded-md border border-border p-3 cursor-pointer hover:bg-accent/40 transition-colors group"
            >
              <Shield className="h-4 w-4 text-muted-foreground shrink-0" />
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">{cs.name}</span>
                  {cs.isDefault && (
                    <span className="rounded bg-primary/10 px-1.5 py-0.5 text-xs text-primary">default</span>
                  )}
                </div>
                <p className="text-xs text-muted-foreground">
                  {/* Defence-in-depth null guards — see worklog 0129 */}
                  Providers: {(cs.providers ?? []).join(", ") || "none"} ·{" "}
                  Models: {(cs.modelAllowlist ?? []).length || "all"}
                </p>
              </div>
              <div className="flex gap-1 shrink-0">
                {!cs.isDefault && (
                  <button
                    onClick={(e) => { e.stopPropagation(); handleSetDefault(cs.id); }}
                    className="rounded p-1.5 hover:bg-accent"
                    title="Set as default"
                  >
                    <Star className="h-3.5 w-3.5" />
                  </button>
                )}
                <button
                  onClick={(e) => { e.stopPropagation(); handleDelete(cs.id); }}
                  className="rounded p-1.5 hover:bg-destructive/10 text-destructive"
                  title="Delete"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
                <ChevronRight className="h-3.5 w-3.5 text-muted-foreground group-hover:text-foreground" />
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Detail drawer */}
      {drawer && (
        <CredentialSetDrawer
          cs={drawer}
          onClose={() => setDrawer(null)}
          onUpdated={handleUpdated}
          onDeleted={handleDelete}
          onSetDefault={handleSetDefault}
        />
      )}
    </div>
  );
}

// ─── Detail drawer ────────────────────────────────────────────────────────────

function CredentialSetDrawer({
  cs,
  onClose,
  onUpdated,
  onDeleted,
  onSetDefault,
}: {
  cs: CredentialSet;
  onClose: () => void;
  onUpdated: (updated: CredentialSet) => void;
  onDeleted: (id: string) => void;
  onSetDefault: (id: string) => void;
}) {
  const [tab, setTab] = useState<"details" | "providers" | "models">("details");
  const drawerRef = useRef<HTMLDivElement>(null);

  // Close on Escape
  useEffect(() => {
    const handler = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [onClose]);

  // Close on backdrop click
  const handleBackdropClick = (e: React.MouseEvent) => {
    if (drawerRef.current && !drawerRef.current.contains(e.target as Node)) {
      onClose();
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex justify-end"
      onClick={handleBackdropClick}
    >
      {/* Dim backdrop */}
      <div className="absolute inset-0 bg-background/50 backdrop-blur-sm" />

      {/* Drawer panel */}
      <div
        ref={drawerRef}
        className="relative z-10 w-full max-w-md h-full bg-background border-l border-border shadow-xl flex flex-col overflow-hidden"
      >
        {/* Drawer header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border shrink-0">
          <div className="flex items-center gap-2 min-w-0">
            <Shield className="h-4 w-4 text-muted-foreground shrink-0" />
            <span className="text-sm font-semibold truncate">{cs.name}</span>
            {cs.isDefault && (
              <span className="rounded bg-primary/10 px-1.5 py-0.5 text-xs text-primary shrink-0">default</span>
            )}
          </div>
          <button onClick={onClose} className="rounded p-1 hover:bg-accent">
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Tab bar */}
        <div className="flex border-b border-border shrink-0">
          {(["details", "providers", "models"] as const).map((t) => (
            <button
              key={t}
              onClick={() => setTab(t)}
              className={`px-4 py-2 text-xs font-medium capitalize border-b-2 transition-colors ${
                tab === t
                  ? "border-primary text-primary"
                  : "border-transparent text-muted-foreground hover:text-foreground"
              }`}
            >
              {t}
            </button>
          ))}
        </div>

        {/* Tab content */}
        <div className="flex-1 overflow-y-auto p-4">
          {tab === "details" && (
            <DetailsTab
              cs={cs}
              onUpdated={onUpdated}
              onDeleted={onDeleted}
              onSetDefault={onSetDefault}
            />
          )}
          {tab === "providers" && (
            <ProvidersTab cs={cs} onUpdated={onUpdated} />
          )}
          {tab === "models" && (
            <ModelsTab cs={cs} onUpdated={onUpdated} />
          )}
        </div>
      </div>
    </div>
  );
}

// ─── Details tab ─────────────────────────────────────────────────────────────

function DetailsTab({
  cs,
  onUpdated,
  onDeleted,
  onSetDefault,
}: {
  cs: CredentialSet;
  onUpdated: (updated: CredentialSet) => void;
  onDeleted: (id: string) => void;
  onSetDefault: (id: string) => void;
}) {
  const [name, setName] = useState(cs.name);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

  // Reset when credential changes
  useEffect(() => { setName(cs.name); setError(null); setSuccess(false); }, [cs.id, cs.name]);

  const handleSaveName = async () => {
    if (name === cs.name || !name.trim()) return;
    setSaving(true); setError(null); setSuccess(false);
    try {
      await credentialsApi.update(cs.id, { name: name.trim() });
      onUpdated({ ...cs, name: name.trim() });
      setSuccess(true);
      setTimeout(() => setSuccess(false), 2000);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-5">
      {/* Name */}
      <div>
        <label className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Name</label>
        <div className="flex gap-2 mt-1">
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter") handleSaveName(); }}
            className="flex-1 h-8 rounded-md border border-border bg-background px-2 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
          />
          <button
            onClick={handleSaveName}
            disabled={saving || name === cs.name}
            className="rounded-md bg-primary px-3 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-40"
          >
            {saving ? "…" : success ? "✓" : "Save"}
          </button>
        </div>
        {error && <p className="mt-1 text-xs text-destructive">{error}</p>}
      </div>

      {/* Metadata */}
      <div className="space-y-2 text-xs">
        <Row label="ID" value={cs.id} mono />
        <Row label="Key version" value={String(cs.keyVersion)} />
        <Row label="Assigned to" value={Array.isArray(cs.assignedTo) ? cs.assignedTo.join(", ") : cs.assignedTo} />
        <Row label="Created" value={cs.createdAt ? new Date(cs.createdAt).toLocaleString() : "—"} />
        <Row label="Updated" value={cs.updatedAt ? new Date(cs.updatedAt).toLocaleString() : "—"} />
      </div>

      {/* Actions */}
      <div className="pt-2 space-y-2">
        {!cs.isDefault && (
          <button
            onClick={() => onSetDefault(cs.id)}
            className="w-full rounded-md border border-border py-2 text-xs hover:bg-accent flex items-center justify-center gap-1.5"
          >
            <Star className="h-3.5 w-3.5" /> Set as default
          </button>
        )}
        <button
          onClick={() => {
            if (confirm(`Delete "${cs.name}"? This cannot be undone.`)) onDeleted(cs.id);
          }}
          className="w-full rounded-md border border-destructive/30 py-2 text-xs text-destructive hover:bg-destructive/10 flex items-center justify-center gap-1.5"
        >
          <Trash2 className="h-3.5 w-3.5" /> Delete credential set
        </button>
      </div>
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex gap-2 min-w-0">
      <span className="w-24 shrink-0 text-muted-foreground">{label}</span>
      <span className={`flex-1 min-w-0 truncate text-foreground ${mono ? "font-mono text-[11px]" : ""}`}>
        {value || "—"}
      </span>
    </div>
  );
}

// ─── Providers tab ────────────────────────────────────────────────────────────

function ProvidersTab({
  cs,
  onUpdated,
}: {
  cs: CredentialSet;
  onUpdated: (updated: CredentialSet) => void;
}) {
  // We only know provider names from the list (keys are encrypted). The
  // providers record here is the edit buffer — keys are blank until
  // the user types them. To UPDATE the set, the user must re-enter all
  // keys (or leave a field blank to keep it unchanged — the backend
  // only changes providers when the field is present in the request).
  //
  // UX contract: the form shows existing provider names with empty key
  // fields. The user fills in the ones they want to change. Clicking
  // "Save providers" re-posts the full providers object with whatever
  // keys were entered. Providers with empty apiKey are omitted (kept
  // as-is on the server).
  const [providerName, setProviderName] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  // { providerID → { apiKey, baseUrl } } — edit buffer
  const [editBuf, setEditBuf] = useState<Record<string, { apiKey: string; baseUrl: string }>>(() =>
    Object.fromEntries((cs.providers ?? []).map((p) => [p, { apiKey: "", baseUrl: "" }]))
  );
  const [showKey, setShowKey] = useState<Record<string, boolean>>({});
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

  useEffect(() => {
    setEditBuf(Object.fromEntries((cs.providers ?? []).map((p) => [p, { apiKey: "", baseUrl: "" }])));
    setError(null);
    // cs.providers is read to seed editBuf but intentionally omitted: the effect
    // must only reset the buffer when a different credential (cs.id) is selected,
    // not when the same credential's provider list refreshes (would discard edits).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cs.id]);

  const addProvider = () => {
    if (!providerName || !apiKey) return;
    setEditBuf((prev) => ({
      ...prev,
      [providerName]: { apiKey, baseUrl },
    }));
    setProviderName(""); setApiKey(""); setBaseUrl("");
  };

  const removeProvider = (key: string) => {
    setEditBuf((prev) => { const n = { ...prev }; delete n[key]; return n; });
  };

  const handleSave = async () => {
    // Only include providers that have a key specified — omit blanks.
    const providersToSend: Record<string, { apiKey: string; baseUrl?: string }> = {};
    for (const [k, v] of Object.entries(editBuf)) {
      if (!v.apiKey.trim()) continue;
      providersToSend[k] = { apiKey: v.apiKey.trim() };
      if (v.baseUrl.trim()) providersToSend[k].baseUrl = v.baseUrl.trim();
    }
    if (Object.keys(providersToSend).length === 0) {
      setError("Enter at least one API key to save provider changes.");
      return;
    }
    setSaving(true); setError(null); setSuccess(false);
    try {
      await credentialsApi.update(cs.id, { providers: providersToSend });
      // Update the providers list shown in the list view (names only)
      onUpdated({ ...cs, providers: Object.keys(providersToSend) });
      setSuccess(true);
      setTimeout(() => setSuccess(false), 2000);
      // Reset blanks
      setEditBuf(Object.fromEntries(Object.keys(providersToSend).map((p) => [p, { apiKey: "", baseUrl: "" }])));
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-4">
      <p className="text-xs text-muted-foreground">
        API keys are stored encrypted and never shown. Enter a new value to update a key; leave blank to keep the existing one.
      </p>

      {/* Existing providers */}
      {Object.entries(editBuf).map(([provider, val]) => (
        <div key={provider} className="space-y-1.5 rounded-md border border-border p-3">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium">{provider}</span>
            <button onClick={() => removeProvider(provider)} className="text-xs text-destructive hover:underline">
              Remove
            </button>
          </div>
          <div className="flex gap-1.5">
            <div className="relative flex-1">
              <input
                type={showKey[provider] ? "text" : "password"}
                value={val.apiKey}
                onChange={(e) => {
                  const v = e.target.value;
                  setEditBuf((prev) => ({ ...prev, [provider]: { apiKey: v, baseUrl: prev[provider]?.baseUrl ?? "" } }));
                }}
                placeholder="New API key (leave blank to keep)"
                className="h-7 w-full rounded border border-border bg-background px-2 pr-7 text-xs focus:outline-none focus:ring-1 focus:ring-ring"
              />
              <button
                type="button"
                tabIndex={-1}
                onClick={() => setShowKey((s) => ({ ...s, [provider]: !s[provider] }))}
                className="absolute right-1.5 top-1 text-muted-foreground hover:text-foreground"
              >
                {showKey[provider] ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
              </button>
            </div>
            <input
              type="text"
              value={val.baseUrl}
              onChange={(e) => {
                const v = e.target.value;
                setEditBuf((prev) => ({ ...prev, [provider]: { apiKey: prev[provider]?.apiKey ?? "", baseUrl: v } }));
              }}
              placeholder="Base URL (optional)"
              className="h-7 flex-1 rounded border border-border bg-background px-2 text-xs focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>
        </div>
      ))}

      {/* Add new provider */}
      <div className="rounded-md border border-dashed border-border p-3 space-y-2">
        <p className="text-xs text-muted-foreground">Add provider</p>
        <div className="flex flex-wrap gap-1.5">
          <input
            type="text"
            value={providerName}
            onChange={(e) => setProviderName(e.target.value)}
            placeholder="Provider name (e.g. openai)"
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
            className="h-7 rounded border border-border px-2 text-xs hover:bg-accent disabled:opacity-50"
          >
            Add
          </button>
        </div>
      </div>

      {error && <p className="text-xs text-destructive">{error}</p>}
      <button
        onClick={handleSave}
        disabled={saving}
        className="w-full rounded-md bg-primary py-2 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
      >
        {saving ? "Saving…" : success ? "✓ Saved" : "Save provider changes"}
      </button>
    </div>
  );
}

// ─── Models tab ───────────────────────────────────────────────────────────────

function ModelsTab({
  cs,
  onUpdated,
}: {
  cs: CredentialSet;
  onUpdated: (updated: CredentialSet) => void;
}) {
  const [allowlist, setAllowlist] = useState<string[]>((cs.modelAllowlist ?? []).slice());
  const [newModel, setNewModel] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

  useEffect(() => {
    setAllowlist((cs.modelAllowlist ?? []).slice());
    setError(null);
  }, [cs.id, cs.modelAllowlist]);

  const addModel = () => {
    const m = newModel.trim();
    if (!m || allowlist.includes(m)) return;
    setAllowlist((prev) => [...prev, m]);
    setNewModel("");
  };

  const removeModel = (m: string) => setAllowlist((prev) => prev.filter((x) => x !== m));

  const handleSave = async () => {
    setSaving(true); setError(null); setSuccess(false);
    try {
      await credentialsApi.update(cs.id, { modelAllowlist: allowlist });
      onUpdated({ ...cs, modelAllowlist: allowlist });
      setSuccess(true);
      setTimeout(() => setSuccess(false), 2000);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  };

  const empty = allowlist.length === 0;

  return (
    <div className="space-y-4">
      <p className="text-xs text-muted-foreground">
        {empty
          ? "No model restrictions — all models from configured providers are available."
          : "Only models in this list are available to users. Leave empty to allow all."}
      </p>

      {/* Model list */}
      {allowlist.length > 0 && (
        <ul className="space-y-1">
          {allowlist.map((m) => (
            <li key={m} className="flex items-center gap-2 rounded-md border border-border px-3 py-1.5">
              <span className="flex-1 text-xs font-mono">{m}</span>
              <button
                onClick={() => removeModel(m)}
                className="text-xs text-muted-foreground hover:text-destructive"
              >
                <X className="h-3 w-3" />
              </button>
            </li>
          ))}
        </ul>
      )}

      {/* Add model */}
      <div className="flex gap-2">
        <input
          type="text"
          value={newModel}
          onChange={(e) => setNewModel(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") addModel(); }}
          placeholder="e.g. openai/gpt-4o"
          className="flex-1 h-8 rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
        />
        <button
          onClick={addModel}
          disabled={!newModel.trim() || allowlist.includes(newModel.trim())}
          className="rounded-md border border-border px-3 text-xs hover:bg-accent disabled:opacity-50"
        >
          Add
        </button>
      </div>

      {error && <p className="text-xs text-destructive">{error}</p>}
      <button
        onClick={handleSave}
        disabled={saving}
        className="w-full rounded-md bg-primary py-2 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
      >
        {saving ? "Saving…" : success ? "✓ Saved" : "Save model allowlist"}
      </button>
    </div>
  );
}

// ─── Create form (unchanged, reuse) ───────────────────────────────────────────

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
    setProviderName(""); setApiKey(""); setBaseUrl("");
  };

  const removeProvider = (key: string) => {
    setProviders((prev) => { const n = { ...prev }; delete n[key]; return n; });
  };

  const handleSubmit = async () => {
    if (!name || Object.keys(providers).length === 0) {
      setError("Name and at least one provider are required");
      return;
    }
    setSaving(true); setError(null);
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
