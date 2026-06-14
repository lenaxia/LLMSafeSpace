// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Epic 30 US-30.7: Admin LLM Provider Credentials UI
// Allows admins to manage platform-wide LLM provider credentials and
// auto-apply rules. These credentials are seeded into all new workspaces
// unless overridden by the user.

import { useEffect, useState } from "react";
import { ApiClientError } from "../../api/client";
import {
  adminProviderCredentialsApi,
  type AdminProviderCredential,
  type AutoApplyRule,
  type CreateAdminCredentialRequest,
  type ProbeModelEntry,
} from "../../api/providerCredentials";
import { useToast } from "../../providers/ToastProvider";
import { Spinner } from "../ui/Spinner";
import { MetaRow } from "./MetaRow";
import {
  Shield,
  Trash2,
  Plus,
  ChevronDown,
  ChevronUp,
  Eye,
  EyeOff,
  Zap,
  RefreshCw,
  Search,
} from "lucide-react";

// ─── Main tab ────────────────────────────────────────────────────────────────

export function AdminProviderCredentialsTab() {
  const [creds, setCreds] = useState<AdminProviderCredential[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
  const { toast } = useToast();

  const load = async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await adminProviderCredentialsApi.list();
      setCreds(data);
    } catch (e: unknown) {
      if (e instanceof ApiClientError && e.status === 404) {
        setError("not-admin");
      } else {
        setError(e instanceof Error ? e.message : "Failed to load credentials");
      }
    } finally {
      setLoading(false);
    }
  };

  // silentLoad refreshes the list without showing a spinner — used after key
  // rotation where the PUT response omits the decrypted baseURL.
  const silentLoad = async () => {
    try {
      const data = await adminProviderCredentialsApi.list();
      setCreds(data);
    } catch {
      // Ignore background refresh failures — the user already got a success toast.
    }
  };

  useEffect(() => { load(); }, []);

  const handleDelete = async (id: string, name: string) => {
    try {
      await adminProviderCredentialsApi.delete(id);
      setCreds((prev) => prev.filter((c) => c.id !== id));
      setExpanded((prev) => (prev === id ? null : prev));
      toast(`Deleted "${name}"`);
    } catch (e: unknown) {
      toast(e instanceof Error ? e.message : "Delete failed", "error");
    }
  };

  const handleCreated = (c: AdminProviderCredential) => {
    setCreds((prev) => [...prev, c]);
    setShowCreate(false);
    toast(`Created "${c.name}"`);
  };

  const handleUpdated = (updated: AdminProviderCredential) => {
    // Apply the updatedAt timestamp from the PUT response immediately so the
    // row reflects the rotation time, then silently re-fetch to get the
    // decrypted baseURL (omitted from PUT responses for security).
    setCreds((prev) => prev.map((x) => (x.id === updated.id ? { ...x, updatedAt: updated.updatedAt } : x)));
    toast("API key updated");
    silentLoad();
  };

  if (loading) return <div className="flex justify-center p-8"><Spinner /></div>;
  if (error === "not-admin") return null;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-lg font-medium">Platform LLM Provider Credentials</h3>
          <p className="text-sm text-muted-foreground">
            Credentials auto-applied to all new workspaces. Users can override with their own keys.
          </p>
        </div>
        <button
          onClick={() => { setShowCreate(true); setExpanded(null); }}
          disabled={showCreate}
          className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent disabled:opacity-50"
        >
          <Plus className="h-3 w-3" /> Add credential
        </button>
      </div>

      {/* Error banner */}
      {error && (
        <div className="flex items-center justify-between rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <span>{error}</span>
          <button onClick={() => setError(null)} className="ml-2 shrink-0 text-destructive hover:opacity-70">✕</button>
        </div>
      )}

      {/* Inline create form */}
      {showCreate && (
        <CreateAdminCredentialForm
          onCreated={handleCreated}
          onCancel={() => setShowCreate(false)}
          onError={(msg) => setError(msg)}
        />
      )}

      {/* Empty state */}
      {creds.length === 0 && !showCreate ? (
        <div className="rounded-md border border-dashed border-border p-8 text-center">
          <Shield className="mx-auto mb-2 h-8 w-8 text-muted-foreground" />
          <p className="text-sm text-muted-foreground">No platform credentials configured.</p>
          <p className="text-xs text-muted-foreground mt-1">
            Users can still use free-tier opencode models. Add a credential to enable paid providers for everyone.
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
              onUpdated={handleUpdated}
              onError={(msg) => setError(msg)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ─── Credential row ───────────────────────────────────────────────────────────

function CredentialRow({
  cred,
  expanded,
  onToggle,
  onDelete,
  onUpdated,
  onError,
}: {
  cred: AdminProviderCredential;
  expanded: boolean;
  onToggle: () => void;
  onDelete: () => void;
  onUpdated: (c: AdminProviderCredential) => void;
  onError: (msg: string) => void;
}) {
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [editApiKey, setEditApiKey] = useState("");
  const [showKey, setShowKey] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveOk, setSaveOk] = useState(false);
  const [autoApplyRules, setAutoApplyRules] = useState<AutoApplyRule[] | null>(null);
  const [loadingRules, setLoadingRules] = useState(false);
  const [showAddRule, setShowAddRule] = useState(false);

  // Load auto-apply rules when row is expanded
  useEffect(() => {
    if (!expanded || autoApplyRules !== null) return;
    setLoadingRules(true);
    adminProviderCredentialsApi.listAutoApply(cred.id)
      .then(setAutoApplyRules)
      .catch(() => setAutoApplyRules([]))
      .finally(() => setLoadingRules(false));
  }, [expanded, cred.id, autoApplyRules]);

  const handleRotateKey = async () => {
    if (!editApiKey.trim()) return;
    setSaving(true);
    try {
      const updated = await adminProviderCredentialsApi.update(cred.id, { apiKey: editApiKey.trim() });
      onUpdated(updated);
      setEditApiKey("");
      setSaveOk(true);
      setTimeout(() => setSaveOk(false), 2000);
    } catch (e: unknown) {
      onError(e instanceof Error ? e.message : "Update failed");
    } finally {
      setSaving(false);
    }
  };

  const handleDeleteRule = async (rule: AutoApplyRule) => {
    try {
      await adminProviderCredentialsApi.deleteAutoApply(cred.id, rule.targetType, rule.targetId);
      setAutoApplyRules((prev) => prev?.filter((r) => !(r.targetType === rule.targetType && r.targetId === rule.targetId)) ?? null);
    } catch (e: unknown) {
      onError(e instanceof Error ? e.message : "Failed to delete auto-apply rule");
    }
  };

  const handleRuleCreated = (rule: AutoApplyRule) => {
    setAutoApplyRules((prev) => [...(prev ?? []), rule]);
    setShowAddRule(false);
  };

  return (
    <div>
      {/* Row header */}
      <div
        className="flex cursor-pointer items-center gap-3 px-4 py-3 hover:bg-accent/30 transition-colors"
        onClick={onToggle}
      >
        <Shield className="h-4 w-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium">{cred.name}</span>
            <span className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
              {cred.provider}
            </span>
          </div>
          {cred.baseURL && (
            <p className="truncate text-xs text-muted-foreground">{cred.baseURL}</p>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-1" onClick={(e) => e.stopPropagation()}>
          {confirmDelete ? (
            <span className="flex items-center gap-1 text-xs">
              <span className="text-muted-foreground">Delete?</span>
              <button
                onClick={() => { setConfirmDelete(false); onDelete(); }}
                className="rounded px-2 py-0.5 text-xs text-destructive hover:bg-destructive/10"
              >
                Yes
              </button>
              <button
                onClick={() => setConfirmDelete(false)}
                className="rounded px-2 py-0.5 text-xs hover:bg-accent"
              >
                No
              </button>
            </span>
          ) : (
            <button
              onClick={() => setConfirmDelete(true)}
              className="rounded p-1.5 text-destructive hover:bg-destructive/10"
              title="Delete credential"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          )}
          {expanded
            ? <ChevronUp className="h-4 w-4 text-muted-foreground" />
            : <ChevronDown className="h-4 w-4 text-muted-foreground" />}
        </div>
      </div>

      {/* Expanded panel */}
      {expanded && (
        <div className="space-y-4 border-t border-border bg-muted/20 px-4 py-4">
          {/* Metadata grid */}
          <div className="grid grid-cols-2 gap-x-6 gap-y-1 text-xs">
            <MetaRow label="ID" value={cred.id} mono />
            <MetaRow label="Updated" value={new Date(cred.updatedAt).toLocaleString()} />
            <MetaRow label="Created" value={new Date(cred.createdAt).toLocaleString()} />
            {cred.baseURL && <MetaRow label="Base URL" value={cred.baseURL} />}
            {(cred.modelAllowlist?.length ?? 0) > 0 && (
              <div className="col-span-2">
                <MetaRow label="Model allowlist" value={cred.modelAllowlist.join(", ")} />
              </div>
            )}
          </div>

          {/* Rotate key */}
          <div>
            <p className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
              <RefreshCw className="h-3 w-3" /> Rotate API key
            </p>
            <div className="flex gap-2">
              <div className="relative flex-1">
                <input
                  type={showKey ? "text" : "password"}
                  value={editApiKey}
                  onChange={(e) => setEditApiKey(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && handleRotateKey()}
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
                className="shrink-0 rounded border border-border px-2 text-xs hover:bg-accent disabled:opacity-50"
              >
                {saving ? "…" : saveOk ? "✓" : "Update"}
              </button>
            </div>
          </div>

          {/* Auto-apply rules */}
          <div>
            <div className="mb-1.5 flex items-center justify-between">
              <p className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
                <Zap className="h-3 w-3" /> Auto-apply rules
              </p>
              {!showAddRule && (
                <button
                  onClick={() => setShowAddRule(true)}
                  className="flex items-center gap-1 text-xs text-primary hover:text-primary/80"
                >
                  <Plus className="h-3 w-3" /> Add rule
                </button>
              )}
            </div>

            {loadingRules && <p className="text-xs text-muted-foreground">Loading…</p>}

            {!loadingRules && autoApplyRules !== null && (
              <>
                {autoApplyRules.length === 0 && !showAddRule && (
                  <p className="text-xs text-muted-foreground">
                    No auto-apply rules. This credential will not be automatically seeded.
                  </p>
                )}
                {autoApplyRules.length > 0 && (
                  <div className="divide-y divide-border rounded border border-border">
                    {autoApplyRules.map((rule) => (
                      <div key={`${rule.credentialId}:${rule.targetType}:${rule.targetId ?? "_"}`} className="flex items-center justify-between px-3 py-2">
                        <div className="text-xs">
                          <span className="rounded bg-muted px-1.5 py-0.5 text-muted-foreground">
                            {rule.targetType}
                          </span>
                          {rule.targetId && (
                            <span className="ml-2 font-mono text-[10px] text-muted-foreground">
                              {rule.targetId}
                            </span>
                          )}
                        </div>
                        <button
                          onClick={() => handleDeleteRule(rule)}
                          className="rounded p-1 text-destructive hover:bg-destructive/10"
                          title="Remove rule"
                        >
                          <Trash2 className="h-3 w-3" />
                        </button>
                      </div>
                    ))}
                  </div>
                )}
                {showAddRule && (
                  <AddAutoApplyRuleForm
                    credentialId={cred.id}
                    onCreated={handleRuleCreated}
                    onCancel={() => setShowAddRule(false)}
                    onError={onError}
                  />
                )}
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

// ─── Add auto-apply rule form ─────────────────────────────────────────────────

function AddAutoApplyRuleForm({
  credentialId,
  onCreated,
  onCancel,
  onError,
}: {
  credentialId: string;
  onCreated: (rule: AutoApplyRule) => void;
  onCancel: () => void;
  onError: (msg: string) => void;
}) {
  const [targetType, setTargetType] = useState<"all" | "user" | "org">("all");
  const [targetId, setTargetId] = useState("");
  const [saving, setSaving] = useState(false);

  const handleSubmit = async () => {
    setSaving(true);
    try {
      const rule = await adminProviderCredentialsApi.createAutoApply(credentialId, {
        targetType,
        ...(targetType !== "all" && targetId.trim() ? { targetId: targetId.trim() } : {}),
      });
      onCreated(rule);
    } catch (e: unknown) {
      onError(e instanceof Error ? e.message : "Failed to create auto-apply rule");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="mt-2 rounded border border-border bg-background p-3 space-y-2">
      <p className="text-xs font-medium">New auto-apply rule</p>
      <div className="flex gap-2">
        <select
          value={targetType}
          onChange={(e) => { setTargetType(e.target.value as "all" | "user" | "org"); setTargetId(""); }}
          className="h-7 rounded border border-border bg-background px-2 text-xs focus:outline-none focus:ring-1 focus:ring-ring"
        >
          <option value="all">All workspaces</option>
          <option value="user">Specific user</option>
          <option value="org">Organisation</option>
        </select>
        {targetType !== "all" && (
          <input
            type="text"
            value={targetId}
            onChange={(e) => setTargetId(e.target.value)}
            placeholder={targetType === "user" ? "User ID" : "Org ID"}
            className="h-7 flex-1 rounded border border-border bg-background px-2 text-xs focus:outline-none focus:ring-1 focus:ring-ring"
          />
        )}
      </div>
      <div className="flex gap-2">
        <button
          onClick={handleSubmit}
          disabled={saving || (targetType !== "all" && !targetId.trim())}
          className="rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          {saving ? "Adding…" : "Add"}
        </button>
        <button
          onClick={onCancel}
          className="rounded-md border border-border px-2.5 py-1 text-xs hover:bg-accent"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}

// ─── Shared model config table ────────────────────────────────────────────────
// Used by both admin and user create forms. Shows models fetched from the
// provider's /v1/models endpoint and lets the user enable/disable each one
// and optionally enter a context window size.

interface ModelRow {
  id: string;
  enabled: boolean;
  contextLimit: string; // stored as string for controlled input
}

function ModelConfigTable({
  rows,
  onChange,
}: {
  rows: ModelRow[];
  onChange: (rows: ModelRow[]) => void;
}) {
  const update = (idx: number, patch: Partial<ModelRow>) =>
    onChange(rows.map((r, i) => (i === idx ? { ...r, ...patch } : r)));

  if (rows.length === 0) {
    return (
      <p className="text-xs text-muted-foreground italic">
        No models found. Check your API key and base URL, or add models manually below.
      </p>
    );
  }

  return (
    <div className="max-h-48 overflow-y-auto rounded-md border border-border">
      <table className="w-full text-xs">
        <thead className="sticky top-0 bg-muted/80 backdrop-blur-sm">
          <tr>
            <th className="px-2 py-1.5 text-left font-medium text-muted-foreground w-8">On</th>
            <th className="px-2 py-1.5 text-left font-medium text-muted-foreground">Model ID</th>
            <th className="px-2 py-1.5 text-left font-medium text-muted-foreground w-40">
              Context window <span className="text-muted-foreground/50">(tokens)</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row, idx) => (
            <tr key={row.id} className="border-t border-border/50 hover:bg-muted/30">
              <td className="px-2 py-1">
                <input
                  type="checkbox"
                  checked={row.enabled}
                  onChange={(e) => update(idx, { enabled: e.target.checked })}
                  className="h-3.5 w-3.5"
                />
              </td>
              <td className="px-2 py-1 font-mono">{row.id}</td>
              <td className="px-2 py-1">
                <input
                  type="number"
                  min={0}
                  value={row.contextLimit}
                  onChange={(e) => update(idx, { contextLimit: e.target.value })}
                  placeholder="e.g. 200000"
                  disabled={!row.enabled}
                  className="h-6 w-full rounded border border-border bg-background px-1.5 text-xs focus:outline-none focus:ring-1 focus:ring-ring disabled:opacity-40"
                />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ─── Create form ──────────────────────────────────────────────────────────────

function CreateAdminCredentialForm({
  onCreated,
  onCancel,
  onError,
}: {
  onCreated: (c: AdminProviderCredential) => void;
  onCancel: () => void;
  onError: (msg: string) => void;
}) {
  const [form, setForm] = useState<CreateAdminCredentialRequest>({
    name: "",
    provider: "",
    apiKey: "",
    baseURL: "",
    modelAllowlist: [],
    modelContextLimits: {},
  });
  const [showKey, setShowKey] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Model fetch state
  const [modelRows, setModelRows] = useState<ModelRow[]>([]);
  const [fetchState, setFetchState] = useState<"idle" | "loading" | "done" | "error">("idle");
  const [fetchWarning, setFetchWarning] = useState<string | null>(null);
  const [fetchedCredId, setFetchedCredId] = useState<string | null>(null);

  const setField = (k: keyof CreateAdminCredentialRequest) =>
    (e: React.ChangeEvent<HTMLInputElement>) =>
      setForm((prev) => ({ ...prev, [k]: e.target.value }));

  // After creating the credential, probe its models automatically.
  // This is done after creation (not before) because we need the credential
  // ID to call the probe endpoint — which decrypts using the server-side KEK.
  const handleFetchModels = async (credId: string) => {
    setFetchState("loading");
    setFetchWarning(null);
    try {
      const result = await adminProviderCredentialsApi.probeModels(credId);
      if (result.warning) setFetchWarning(result.warning);
      const rows: ModelRow[] = (result.models ?? []).map((m: ProbeModelEntry) => ({
        id: m.id,
        enabled: true,
        contextLimit: m.contextLimit > 0 ? String(m.contextLimit) : "",
      }));
      setModelRows(rows);
      setFetchedCredId(credId);
      setFetchState("done");
    } catch {
      setFetchState("error");
      setFetchWarning("Failed to fetch model list. You can save without configuring models.");
    }
  };

  // Phase 1: create the credential shell (no models yet).
  const handleCreate = async () => {
    if (!form.name.trim() || !form.provider.trim() || !form.apiKey.trim()) {
      setError("Name, provider, and API key are required");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const req: CreateAdminCredentialRequest = {
        name: form.name.trim(),
        provider: form.provider.trim(),
        apiKey: form.apiKey.trim(),
        ...(form.baseURL?.trim() ? { baseURL: form.baseURL.trim() } : {}),
      };
      const cred = await adminProviderCredentialsApi.create(req);
      // Auto-probe models after credential is created
      await handleFetchModels(cred.id);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "Create failed";
      setError(msg);
      onError(msg);
    } finally {
      setSaving(false);
    }
  };

  // Phase 2: save model configuration (allowlist + context limits).
  const handleSaveModels = async () => {
    if (!fetchedCredId) return;
    setSaving(true);
    setError(null);
    try {
      const enabled = modelRows.filter((r) => r.enabled);
      const modelAllowlist = enabled.map((r) => r.id);
      const modelContextLimits: Record<string, number> = {};
      for (const r of enabled) {
        const v = parseInt(r.contextLimit, 10);
        if (v > 0) modelContextLimits[r.id] = v;
      }
      const updated = await adminProviderCredentialsApi.update(fetchedCredId, {
        modelAllowlist,
        modelContextLimits,
      });
      onCreated(updated);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "Failed to save model configuration";
      setError(msg);
      onError(msg);
    } finally {
      setSaving(false);
    }
  };

  const PROVIDERS = ["openai", "anthropic", "google", "openrouter", "groq", "mistral", "zhipuai"];

  // Phase 2: model configuration screen
  if (fetchState === "done" || fetchState === "error") {
    return (
      <div className="rounded-md border border-border bg-muted/20 p-4 space-y-3">
        <h3 className="text-sm font-semibold">Configure Models</h3>
        <p className="text-xs text-muted-foreground">
          Toggle which models to expose and set their context window sizes (in tokens).
          Context window size is shown in the usage bar while chatting.
          Leave a field empty if unknown.
        </p>

        {fetchWarning && (
          <p className="text-xs text-amber-600 bg-amber-50 dark:bg-amber-950/30 rounded px-2 py-1.5">
            {fetchWarning}
          </p>
        )}
        {error && <p className="text-xs text-destructive">{error}</p>}

        <ModelConfigTable rows={modelRows} onChange={setModelRows} />

        <div className="flex gap-2 pt-1">
          <button
            onClick={handleSaveModels}
            disabled={saving}
            className="rounded-md bg-primary px-3 py-1.5 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          >
            {saving ? "Saving…" : "Save configuration"}
          </button>
          <button
            onClick={() => fetchedCredId && handleFetchModels(fetchedCredId)}
            disabled={saving || (fetchState as string) === "loading"}
            className="flex items-center gap-1 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent disabled:opacity-50"
          >
            <RefreshCw className="h-3 w-3" /> Re-fetch
          </button>
          <button
            onClick={onCancel}
            className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent"
          >
            Skip
          </button>
        </div>
      </div>
    );
  }

  // Phase 1: credential entry screen
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
            onChange={setField("name")}
            placeholder="e.g. OpenAI Production"
            className="mt-0.5 h-8 w-full rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
        <div>
          <label className="text-xs text-muted-foreground">Provider</label>
          <input
            list="admin-provider-suggestions"
            type="text"
            value={form.provider}
            onChange={setField("provider")}
            placeholder="e.g. openai"
            className="mt-0.5 h-8 w-full rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
          />
          <datalist id="admin-provider-suggestions">
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
            onChange={setField("apiKey")}
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
        <label className="text-xs text-muted-foreground">
          Base URL <span className="text-muted-foreground/50">(optional)</span>
        </label>
        <input
          type="text"
          value={form.baseURL ?? ""}
          onChange={setField("baseURL")}
          placeholder="https://api.example.com/v1"
          className="mt-0.5 h-8 w-full rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
        />
        <p className="mt-0.5 text-[10px] text-muted-foreground">
          Required for custom endpoints (LiteLLM, vLLM, etc.). Leave empty for first-party providers.
        </p>
      </div>

      <div className="flex gap-2 pt-1">
        <button
          onClick={handleCreate}
          disabled={saving || fetchState === "loading"}
          className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          {saving || fetchState === "loading" ? (
            <><Spinner className="h-3 w-3" /> {saving ? "Creating…" : "Fetching models…"}</>
          ) : (
            <><Search className="h-3 w-3" /> Create &amp; fetch models</>
          )}
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
