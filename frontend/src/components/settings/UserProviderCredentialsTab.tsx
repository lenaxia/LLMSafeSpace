// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Epic 30 US-30.8: User LLM Provider Credentials UI
// Users can add their own API keys. Each key is automatically available across
// all of the user's workspaces and takes priority over platform defaults.

import { useEffect, useState } from "react";
import {
  userProviderCredentialsApi,
  type UserProviderCredential,
  type CreateUserCredentialRequest,
  type CreateUserCredentialResponse,
  type ProbeModelEntry,
} from "../../api/providerCredentials";
import { SDK_KINDS, SLUG_REGEX, slugFromName } from "../../api/providerCredentialTypes";
import { useToast } from "../../providers/ToastProvider";
import { Spinner } from "../ui/Spinner";
import { ModelConfigTable, type ModelRow } from "../shared/ModelConfigTable";
import { MetaRow } from "./MetaRow";
import {
  KeyRound,
  Trash2,
  Plus,
  Eye,
  EyeOff,
  ChevronDown,
  ChevronUp,
  Lock,
  RefreshCw,
  Search,
} from "lucide-react";

// ─── Main tab ────────────────────────────────────────────────────────────────

export function UserProviderCredentialsTab() {
  const [creds, setCreds] = useState<UserProviderCredential[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
  const { toast } = useToast();

  const load = async () => {
    setLoading(true);
    setError(null);
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
    try {
      await userProviderCredentialsApi.delete(id);
      setCreds((prev) => prev.filter((c) => c.id !== id));
      if (expanded === id) setExpanded(null);
      toast(`Removed "${name}"`);
    } catch (e: unknown) {
      toast(e instanceof Error ? e.message : "Delete failed", "error");
    }
  };

  // handleCreated handles both 201 (success) and 207 (created, bind partially failed).
  const handleCreated = (res: CreateUserCredentialResponse) => {
    const cred: UserProviderCredential = res.credential ?? res;
    setCreds((prev) => [...prev, cred]);
    setShowCreate(false);
    if (res.bindWarning) {
      toast(`Added "${cred.name}" — it may not have synced to all existing workspaces; new workspaces will pick it up automatically`, "error");
    } else {
      toast(`Added "${cred.name}"`);
    }
  };

  if (loading) return <div className="flex justify-center p-8"><Spinner /></div>;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-lg font-medium">My LLM Provider Keys</h3>
          <p className="text-sm text-muted-foreground">
            Your personal API keys. They're automatically available across all of your workspaces and
            take priority over platform defaults.
          </p>
        </div>
        <button
          onClick={() => { setShowCreate(true); setExpanded(null); }}
          disabled={showCreate}
          className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent disabled:opacity-50"
        >
          <Plus className="h-3 w-3" /> Add key
        </button>
      </div>

      {/* Zero-knowledge security explainer */}
      <div className="rounded-md border border-border bg-muted/20 p-4">
        <div className="flex items-start gap-3">
          <Lock className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
          <div className="space-y-2 text-xs text-muted-foreground">
            <p className="font-medium text-foreground">How your keys are protected</p>
            <p>
              Your API keys are encrypted with a key derived from your password and protected in such
              a way that even we cannot read your secrets. If an attacker gets a full copy of our
              database, your secrets would still be protected. This is called Zero Knowledge
              encryption: we hold your keys, but only your password can unlock them. All secrets are
              encrypted at rest and only decrypted for use in active workspaces.
            </p>
            <p>
              As a consequence of Zero Knowledge encryption,{" "}
              <span className="font-medium text-foreground">
                if you ever forget your password and reset it by email, your saved keys cannot be
                recovered by anyone, ever.
              </span>{" "}
              We cannot restore them, because we cannot read them ourselves. They will be deleted and
              you will need to re-add them, so even if your account is compromised, an attacker would
              not be able to steal your secrets.
            </p>
          </div>
        </div>
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
        <CreateUserCredentialForm
          onCreated={handleCreated}
          onCancel={() => setShowCreate(false)}
          onError={(msg) => setError(msg)}
        />
      )}

      {/* Empty state */}
      {creds.length === 0 && !showCreate ? (
        <div className="rounded-md border border-dashed border-border p-8 text-center">
          <KeyRound className="mx-auto mb-2 h-8 w-8 text-muted-foreground" />
          <p className="text-sm text-muted-foreground">No personal provider keys added yet.</p>
          <p className="text-xs text-muted-foreground mt-1">
            You can use workspaces with free-tier opencode models without adding a key. Add a key to
            use your own provider quota.
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
            />
          ))}
        </div>
      )}

      {creds.length > 0 && (
        <p className="text-xs text-muted-foreground">
          Your keys are automatically available across all of your workspaces.
        </p>
      )}
    </div>
  );
}

// ─── Credential row with expand ───────────────────────────────────────────────

function CredentialRow({
  cred,
  expanded,
  onToggle,
  onDelete,
}: {
  cred: UserProviderCredential;
  expanded: boolean;
  onToggle: () => void;
  onDelete: () => void;
}) {
  const [confirmDelete, setConfirmDelete] = useState(false);

  return (
    <div>
      {/* Row header */}
      <div
        className="flex cursor-pointer items-center gap-3 px-4 py-3 hover:bg-accent/20 transition-colors"
        onClick={onToggle}
      >
        <KeyRound className="h-4 w-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium">{cred.name}</span>
            <span
              className="rounded bg-muted px-1.5 py-0.5 text-xs font-mono text-muted-foreground"
              title={`SDK kind: ${cred.kind}`}
            >
              {cred.slug}
            </span>
            <span className="rounded bg-secondary/30 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-muted-foreground">
              {cred.kind}
            </span>
            {(cred.modelAllowlist?.length ?? 0) > 0 && (
              <span className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
                {cred.modelAllowlist?.length} model{(cred.modelAllowlist?.length ?? 0) > 1 ? "s" : ""}
              </span>
            )}
          </div>
          <p className="text-xs text-muted-foreground">
            Added {new Date(cred.createdAt).toLocaleDateString()}
            {cred.baseURL && ` · ${cred.baseURL}`}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-1" onClick={(e) => e.stopPropagation()}>
          {confirmDelete ? (
            <span className="flex items-center gap-1 text-xs">
              <span className="text-muted-foreground">Remove?</span>
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
              title="Remove key"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          )}
          {expanded
            ? <ChevronUp className="h-4 w-4 text-muted-foreground" />
            : <ChevronDown className="h-4 w-4 text-muted-foreground" />}
        </div>
      </div>

      {/* Expanded panel — read-only details */}
      {expanded && (
        <div className="space-y-3 border-t border-border bg-muted/20 px-4 py-4">
          <div className="grid grid-cols-2 gap-x-6 gap-y-1 text-xs">
            <MetaRow label="ID" value={cred.id} mono />
            <MetaRow label="Updated" value={new Date(cred.updatedAt).toLocaleString()} />
            {cred.baseURL && (
              <div className="col-span-2">
                <MetaRow label="Base URL" value={cred.baseURL} />
              </div>
            )}
            {(cred.modelAllowlist?.length ?? 0) > 0 && (
              <div className="col-span-2">
                <MetaRow label="Model allowlist" value={cred.modelAllowlist?.join(", ") ?? ""} />
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

// ─── Create form ──────────────────────────────────────────────────────────────

function CreateUserCredentialForm({
  onCreated,
  onCancel,
  onError,
}: {
  onCreated: (c: CreateUserCredentialResponse) => void;
  onCancel: () => void;
  onError: (msg: string) => void;
}) {
  const [form, setForm] = useState<CreateUserCredentialRequest>({
    name: "",
    kind: "",
    slug: "",
    apiKey: "",
    baseURL: "",
  });
  const [showKey, setShowKey] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Inline model fetch state
  const [modelRows, setModelRows] = useState<ModelRow[]>([]);
  const [fetchState, setFetchState] = useState<"idle" | "loading" | "done" | "error">("idle");
  const [fetchWarning, setFetchWarning] = useState<string | null>(null);

  const setField = (k: keyof CreateUserCredentialRequest) =>
    (e: React.ChangeEvent<HTMLInputElement>) =>
      setForm((prev) => ({ ...prev, [k]: e.target.value }));

  const handleFetchModels = async () => {
    if (!form.apiKey?.trim() || !form.baseURL?.trim()) {
      setFetchWarning("Enter API key and base URL first to fetch models.");
      return;
    }
    setFetchState("loading");
    setFetchWarning(null);
    try {
      const result = await userProviderCredentialsApi.probeModelsAnon(
        form.apiKey?.trim() ?? "",
        form.baseURL?.trim() ?? ""
      );
      if (result.warning) setFetchWarning(result.warning);
      const rows: ModelRow[] = (result.models ?? []).map((m: ProbeModelEntry) => ({
        id: m.id,
        enabled: true,
        contextLimit: m.contextLimit > 0 ? String(m.contextLimit) : "",
        outputLimit: m.outputLimit > 0 ? String(m.outputLimit) : "",
      }));
      setModelRows(rows);
      setFetchState("done");
    } catch {
      setFetchState("error");
      setFetchWarning("Failed to fetch model list. You can still save without it.");
    }
  };

  const handleSubmit = async () => {
    if (!form.name.trim() || !form.kind.trim() || !form.slug.trim() || !form.apiKey.trim()) {
      setError("Name, kind, slug, and API key are required");
      return;
    }
    if (!SLUG_REGEX.test(form.slug.trim())) {
      setError(
        "Slug must be 1–64 lowercase alphanumeric characters and hyphens, " +
        "starting and ending with alphanumeric (e.g. \"my-openai\")",
      );
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const enabled = modelRows.filter((r) => r.enabled);
      const modelAllowlist = enabled.length > 0 ? enabled.map((r) => r.id) : undefined;
      const modelContextLimits: Record<string, number> = {};
      const modelOutputLimits: Record<string, number> = {};
      for (const r of enabled) {
        const ctx = parseInt(r.contextLimit, 10);
        if (ctx > 0) modelContextLimits[r.id] = ctx;
        const out = parseInt(r.outputLimit, 10);
        if (out > 0) modelOutputLimits[r.id] = out;
      }
      const req: CreateUserCredentialRequest = {
        name: form.name.trim(),
        kind: form.kind.trim(),
        slug: form.slug.trim(),
        apiKey: form.apiKey.trim(),
        ...(form.baseURL?.trim() ? { baseURL: form.baseURL.trim() } : {}),
        ...(modelAllowlist ? { modelAllowlist } : {}),
        ...(Object.keys(modelContextLimits).length > 0 ? { modelContextLimits } : {}),
        ...(Object.keys(modelOutputLimits).length > 0 ? { modelOutputLimits } : {}),
      };
      const c = await userProviderCredentialsApi.create(req);
      onCreated(c);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "Create failed";
      setError(msg);
      onError(msg);
    } finally {
      setSaving(false);
    }
  };

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
            onChange={(e) => {
              const v = e.target.value;
              setForm((prev) => ({
                ...prev,
                name: v,
                slug:
                  prev.slug && prev.slug !== slugFromName(prev.name)
                    ? prev.slug
                    : slugFromName(v),
              }));
            }}
            placeholder="e.g. My OpenAI Key"
            className="mt-0.5 h-8 w-full rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
        <div>
          <label className="text-xs text-muted-foreground">SDK Kind</label>
          <select
            value={form.kind}
            onChange={(e) => setForm((prev) => ({ ...prev, kind: e.target.value }))}
            className="mt-0.5 h-8 w-full rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
          >
            <option value="">— select SDK kind —</option>
            {SDK_KINDS.map((k) => (
              <option key={k} value={k}>{k}</option>
            ))}
          </select>
        </div>
      </div>

      <div>
        <label className="text-xs text-muted-foreground">Slug</label>
        <input
          type="text"
          value={form.slug}
          onChange={setField("slug")}
          placeholder="my-openai"
          pattern={SLUG_REGEX.source}
          className="mt-0.5 h-8 w-full rounded-md border border-border bg-background px-2 font-mono text-xs focus:outline-none focus:ring-2 focus:ring-ring"
        />
        <p className="mt-0.5 text-[10px] text-muted-foreground">
          Per-user identity. Appears in agent-config.json as the provider key. Lowercase alphanumeric + hyphens, 1–64 chars.
        </p>
      </div>

      <div>
        <label className="text-xs text-muted-foreground">API Key</label>
        <div className="relative mt-0.5">
          <input
            type={showKey ? "text" : "password"}
            value={form.apiKey}
            onChange={(e) => { setField("apiKey")(e); setFetchState("idle"); setModelRows([]); }}
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
        <div className="flex gap-2 mt-0.5">
          <input
            type="text"
            value={form.baseURL ?? ""}
            onChange={(e) => { setField("baseURL")(e); setFetchState("idle"); setModelRows([]); }}
            placeholder="https://api.example.com/v1"
            className="h-8 flex-1 rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
          />
          <button
            type="button"
            onClick={handleFetchModels}
            disabled={fetchState === "loading" || !form.baseURL?.trim() || !form.apiKey?.trim()}
            className="flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-accent disabled:opacity-40"
          >
            {fetchState === "loading" ? <Spinner className="h-3 w-3" /> : <Search className="h-3 w-3" />}
            Fetch models
          </button>
        </div>
      </div>

      {/* Model list shown after fetch */}
      {fetchWarning && (
        <p className="text-xs text-amber-600 bg-amber-50 dark:bg-amber-950/30 rounded px-2 py-1.5">
          {fetchWarning}
        </p>
      )}
      {(fetchState === "done" || (fetchState === "error" && modelRows.length > 0)) && (
        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <label className="text-xs text-muted-foreground">
              Models — toggle on/off and set context window size
            </label>
            <button
              type="button"
              onClick={handleFetchModels}
              disabled={(fetchState as string) === "loading"}
              className="flex items-center gap-1 text-[10px] text-muted-foreground hover:text-foreground disabled:opacity-40"
            >
              <RefreshCw className="h-3 w-3" /> Refresh
            </button>
          </div>
          <ModelConfigTable
            rows={modelRows}
            onChange={setModelRows}
            emptyMessage="No models found. Check your API key and base URL, or skip to save without a model list."
          />
        </div>
      )}

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
