// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Epic 30 US-30.7: Admin LLM Provider Credentials UI
// Allows admins to manage platform-wide LLM provider credentials and
// auto-apply rules. These credentials are seeded into all new workspaces
// unless overridden by the user.

import { useEffect, useState } from "react";
import {
  adminProviderCredentialsApi,
  type AdminProviderCredential,
  type AutoApplyRule,
  type CreateAdminCredentialRequest,
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
      if (e instanceof Error && e.message.includes("403")) {
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
  });
  const [showKey, setShowKey] = useState(false);
  const [allowlistInput, setAllowlistInput] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const setField = (k: keyof CreateAdminCredentialRequest) =>
    (e: React.ChangeEvent<HTMLInputElement>) =>
      setForm((prev) => ({ ...prev, [k]: e.target.value }));

  const handleSubmit = async () => {
    if (!form.name.trim() || !form.provider.trim() || !form.apiKey.trim()) {
      setError("Name, provider, and API key are required");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const modelAllowlist = allowlistInput
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      const req: CreateAdminCredentialRequest = {
        name: form.name.trim(),
        provider: form.provider.trim(),
        apiKey: form.apiKey.trim(),
        ...(form.baseURL?.trim() ? { baseURL: form.baseURL.trim() } : {}),
        ...(modelAllowlist.length > 0 ? { modelAllowlist } : {}),
      };
      const c = await adminProviderCredentialsApi.create(req);
      onCreated(c);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "Create failed";
      setError(msg);
      onError(msg);
    } finally {
      setSaving(false);
    }
  };

  const PROVIDERS = ["openai", "anthropic", "google", "openrouter", "groq", "mistral", "zhipuai"];

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
      </div>

      <div>
        <label className="text-xs text-muted-foreground">
          Model allowlist <span className="text-muted-foreground/50">(optional, comma-separated)</span>
        </label>
        <input
          type="text"
          value={allowlistInput}
          onChange={(e) => setAllowlistInput(e.target.value)}
          placeholder="e.g. glm-5.1, gpt-4o"
          className="mt-0.5 h-8 w-full rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
        />
        <p className="mt-0.5 text-[10px] text-muted-foreground">
          Leave empty to allow all models from this provider.
        </p>
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
