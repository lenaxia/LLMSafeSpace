// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Epic 30 US-30.8: User LLM Provider Credentials UI
// Users can add their own API keys which take priority over platform defaults
// when bound to a workspace.

import { useEffect, useState } from "react";
import {
  userProviderCredentialsApi,
  type UserProviderCredential,
  type CreateUserCredentialRequest,
  type CredentialBindingInfo,
  type CreateUserCredentialResponse,
} from "../../api/providerCredentials";
import { workspacesApi } from "../../api/workspaces";
import type { WorkspaceListItem } from "../../api/types";
import { useToast } from "../../providers/ToastProvider";
import { Spinner } from "../ui/Spinner";
import { MetaRow } from "./MetaRow";
import {
  KeyRound,
  Trash2,
  Plus,
  Eye,
  EyeOff,
  ChevronDown,
  ChevronUp,
  Link,
  Unlink,
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
      toast(`Added "${cred.name}" — workspace auto-bind failed; bind manually`, "error");
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
            Your personal API keys. These take priority over platform defaults when bound to a workspace.
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
              onError={(msg) => setError(msg)}
            />
          ))}
        </div>
      )}

      {creds.length > 0 && (
        <p className="text-xs text-muted-foreground">
          Expand a key to bind or unbind it from specific workspaces.
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
  onError,
}: {
  cred: UserProviderCredential;
  expanded: boolean;
  onToggle: () => void;
  onDelete: () => void;
  onError: (msg: string) => void;
}) {
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [workspaces, setWorkspaces] = useState<WorkspaceListItem[] | null>(null);
  const [bindings, setBindings] = useState<CredentialBindingInfo[]>([]);
  const [loadingWs, setLoadingWs] = useState(false);
  const [bindingId, setBindingId] = useState<string | null>(null);
  const { toast } = useToast();

  // Load workspace list + bindings (with sourceType) on expand (M-1 fix).
  useEffect(() => {
    if (!expanded || workspaces !== null) return;
    setLoadingWs(true);

    Promise.all([
      workspacesApi.list(),
      userProviderCredentialsApi.listBindings(cred.id),
    ])
      .then(([wsRes, bindingsRes]) => {
        const list = (wsRes as { workspaces?: WorkspaceListItem[] }).workspaces ?? [];
        setWorkspaces(list);
        setBindings(bindingsRes.bindings ?? []);
      })
      .catch(() => setWorkspaces([]))
      .finally(() => setLoadingWs(false));
  }, [expanded, workspaces, cred.id]);

  const getBinding = (wsId: string): CredentialBindingInfo | undefined =>
    bindings.find((b) => b.workspaceId === wsId);

  const handleBind = async (wsId: string) => {
    setBindingId(wsId);
    try {
      await userProviderCredentialsApi.bindToWorkspace(cred.id, wsId);
      setBindings((prev) => [...prev, { workspaceId: wsId, sourceType: "explicit" }]);
      toast(`Bound to workspace`);
    } catch (e: unknown) {
      onError(e instanceof Error ? e.message : "Bind failed");
    } finally {
      setBindingId(null);
    }
  };

  const handleUnbind = async (wsId: string) => {
    setBindingId(wsId);
    try {
      await userProviderCredentialsApi.unbindFromWorkspace(cred.id, wsId);
      setBindings((prev) => prev.filter((b) => b.workspaceId !== wsId));
      toast(`Unbound from workspace`);
    } catch (e: unknown) {
      // 409 = auto-binding protected (H-1 fix: surface meaningful message)
      const msg = e instanceof Error ? e.message : "Unbind failed";
      onError(msg);
    } finally {
      setBindingId(null);
    }
  };

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
            <span className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
              {cred.provider}
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

      {/* Expanded panel */}
      {expanded && (
        <div className="space-y-3 border-t border-border bg-muted/20 px-4 py-4">
          {/* Details */}
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

          {/* Workspace bindings */}
          <div>
            <p className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
              <Link className="h-3 w-3" /> Workspace bindings
            </p>

            {loadingWs && <p className="text-xs text-muted-foreground">Loading workspaces…</p>}

            {!loadingWs && workspaces !== null && workspaces.length === 0 && (
              <p className="text-xs text-muted-foreground">
                No workspaces found. Create a workspace first.
              </p>
            )}

            {!loadingWs && workspaces !== null && workspaces.length > 0 && (
              <div className="divide-y divide-border rounded border border-border">
                {workspaces.map((ws) => {
                  const binding = getBinding(ws.id);
                  const isAuto = binding?.sourceType === "auto";
                  const isBound = !!binding;
                  const isPending = bindingId === ws.id;
                  return (
                    <div key={ws.id} className="flex items-center justify-between px-3 py-2">
                      <div className="min-w-0">
                        <span className="text-xs font-medium">{ws.name}</span>
                        <span className="ml-2 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                          {ws.phase || "unknown"}
                        </span>
                        {isAuto && (
                          <span className="ml-2 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground"
                            title="Auto-bound by credential seeding. Cannot be manually unbound.">
                            auto
                          </span>
                        )}
                      </div>
                      {isAuto ? (
                        <span className="text-[10px] text-muted-foreground" title="Auto-bindings are managed automatically">
                          seeded
                        </span>
                      ) : (
                        <button
                          onClick={() => isBound ? handleUnbind(ws.id) : handleBind(ws.id)}
                          disabled={isPending}
                          className={`flex items-center gap-1 rounded px-2 py-1 text-xs transition-colors disabled:opacity-50 ${
                            isBound
                              ? "text-destructive hover:bg-destructive/10"
                              : "text-primary hover:bg-primary/10"
                          }`}
                        >
                          {isPending ? (
                            "…"
                          ) : isBound ? (
                            <><Unlink className="h-3 w-3" /> Unbind</>
                          ) : (
                            <><Link className="h-3 w-3" /> Bind</>
                          )}
                        </button>
                      )}
                    </div>
                  );
                })}
              </div>
            )}
            <p className="mt-1.5 text-[10px] text-muted-foreground">
              Binding activates this key for that workspace. The workspace agent reloads secrets automatically.
            </p>
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
    provider: "",
    apiKey: "",
    baseURL: "",
  });
  const [showKey, setShowKey] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const setField = (k: keyof CreateUserCredentialRequest) =>
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
      const req: CreateUserCredentialRequest = {
        name: form.name.trim(),
        provider: form.provider.trim(),
        apiKey: form.apiKey.trim(),
        ...(form.baseURL?.trim() ? { baseURL: form.baseURL.trim() } : {}),
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

  const PROVIDERS = ["openai", "anthropic", "google", "openrouter", "groq", "mistral", "zhipuai"];

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
            onChange={setField("name")}
            placeholder="e.g. My OpenAI Key"
            className="mt-0.5 h-8 w-full rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
        <div>
          <label className="text-xs text-muted-foreground">Provider</label>
          <input
            list="user-provider-suggestions"
            type="text"
            value={form.provider}
            onChange={setField("provider")}
            placeholder="e.g. openai"
            className="mt-0.5 h-8 w-full rounded-md border border-border bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-ring"
          />
          <datalist id="user-provider-suggestions">
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
          onChange={setField("baseURL")}
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
