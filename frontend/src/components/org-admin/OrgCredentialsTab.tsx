import { useCallback, useEffect, useState } from "react";
import { useOutletContext } from "react-router-dom";
import {
  orgsApi,
  type OrgCredential,
  type OrgResponse,
} from "../../api/orgs";
import { useToast } from "../../providers/ToastProvider";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";
import { ChevronDown, ChevronUp, KeyRound, Pencil, Trash2 } from "lucide-react";

interface CredentialsContext {
  org: OrgResponse;
  isAdmin: boolean;
}

interface ModelRow {
  id: string;
  enabled: boolean;
  contextLimit: string;
}

const PROVIDERS = [
  { id: "openai", label: "OpenAI" },
  { id: "anthropic", label: "Anthropic" },
  { id: "google", label: "Google" },
  { id: "custom", label: "Custom" },
];

export function OrgCredentialsTab() {
  const { org } = useOutletContext<CredentialsContext>();
  const [creds, setCreds] = useState<OrgCredential[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [showForm, setShowForm] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [editing, setEditing] = useState<OrgCredential | null>(null);
  const { toast } = useToast();

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setCreds(await orgsApi.listCredentials(org.id));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load credentials");
    } finally {
      setLoading(false);
    }
  }, [org.id]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const handleDelete = async (c: OrgCredential) => {
    try {
      await orgsApi.deleteCredential(org.id, c.id);
      toast(`Removed "${c.name}"`);
      setExpanded(null);
      refresh();
    } catch (e) {
      toast(e instanceof Error ? e.message : "Delete failed", "error");
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-xl font-semibold">Credentials</h2>
          <p className="text-sm text-muted-foreground">
            Shared provider keys for this organization. Auto-applied to all org workspaces on creation.
          </p>
        </div>
        <Button
          size="sm"
          onClick={() => {
            setEditing(null);
            setShowForm((s) => !s);
          }}
        >
          Add Credential
        </Button>
      </div>

      {showForm && (
        <CredentialForm
          orgId={org.id}
          mode="create"
          onDone={() => {
            setShowForm(false);
            setEditing(null);
            refresh();
          }}
          onCancel={() => {
            setShowForm(false);
            setEditing(null);
          }}
        />
      )}

      {editing && (
        <CredentialForm
          orgId={org.id}
          mode="edit"
          existing={editing}
          onDone={() => {
            setEditing(null);
            refresh();
          }}
          onCancel={() => setEditing(null)}
        />
      )}

      {loading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {error && <p className="text-sm text-red-500">{error}</p>}

      {!loading && creds.length === 0 && (
        <p className="text-sm text-muted-foreground">
          No org credentials configured. Add one to share across members.
        </p>
      )}

      <div className="space-y-3">
        {creds.map((c) => {
          const isOpen = expanded === c.id;
          return (
            <div
              key={c.id}
              className="rounded border border-border"
            >
              <button
                type="button"
                onClick={() => setExpanded(isOpen ? null : c.id)}
                className="flex w-full items-center justify-between p-4 text-left hover:bg-accent/30"
              >
                <div className="flex items-center gap-2">
                  <KeyRound className="h-4 w-4 text-muted-foreground" />
                  <div>
                    <p className="font-medium">{c.name}</p>
                    <p className="text-xs text-muted-foreground">
                      {c.provider}
                      {c.baseURL ? ` · ${c.baseURL}` : ""}
                    </p>
                  </div>
                </div>
                {isOpen ? (
                  <ChevronUp className="h-4 w-4 text-muted-foreground" />
                ) : (
                  <ChevronDown className="h-4 w-4 text-muted-foreground" />
                )}
              </button>

              {isOpen && (
                <div className="space-y-3 border-t border-border p-4">
                  {c.bindWarning && (
                    <p className="text-xs text-amber-600">{c.bindWarning}</p>
                  )}
                  {c.baseURL && (
                    <div>
                      <p className="text-xs font-medium text-muted-foreground">Base URL</p>
                      <p className="text-xs font-mono">{c.baseURL}</p>
                    </div>
                  )}
                  <div>
                    <p className="text-xs font-medium text-muted-foreground">
                      Allowed models ({c.modelAllowlist.length})
                    </p>
                    {c.modelAllowlist.length > 0 ? (
                      <div className="mt-1 flex flex-wrap gap-1">
                        {c.modelAllowlist.map((m) => (
                          <Badge key={m} variant="muted">
                            {m}
                          </Badge>
                        ))}
                      </div>
                    ) : (
                      <p className="text-xs italic text-muted-foreground">
                        All provider models allowed (no allowlist)
                      </p>
                    )}
                  </div>
                  {Object.keys(c.modelContextLimits ?? {}).length > 0 && (
                    <div>
                      <p className="text-xs font-medium text-muted-foreground">Context limits</p>
                      <ul className="mt-1 space-y-0.5 text-xs font-mono">
                        {Object.entries(c.modelContextLimits).map(([m, lim]) => (
                          <li key={m}>
                            {m}: {lim.toLocaleString()} tokens
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                  <div className="flex gap-2 pt-1">
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => {
                        setEditing(c);
                        setShowForm(false);
                      }}
                    >
                      <Pencil className="mr-1 h-3 w-3" /> Edit
                    </Button>
                    <Button
                      size="sm"
                      variant="destructive"
                      onClick={() => handleDelete(c)}
                    >
                      <Trash2 className="mr-1 h-3 w-3" /> Delete
                    </Button>
                  </div>
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function CredentialForm({
  orgId,
  mode,
  existing,
  onDone,
  onCancel,
}: {
  orgId: string;
  mode: "create" | "edit";
  existing?: OrgCredential;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(existing?.name ?? "");
  const [provider, setProvider] = useState(existing?.provider ?? "openai");
  const [apiKey, setApiKey] = useState("");
  const [baseURL, setBaseURL] = useState(existing?.baseURL ?? "");
  const [loading, setLoading] = useState(false);
  const [probing, setProbing] = useState(false);
  const [error, setError] = useState("");
  const [modelRows, setModelRows] = useState<ModelRow[]>(() => {
    if (!existing) return [];
    const limits = existing.modelContextLimits ?? {};
    const allow = existing.modelAllowlist ?? [];
    const rows: ModelRow[] = Object.keys(limits).map((id) => ({
      id,
      enabled: allow.includes(id),
      contextLimit: String(limits[id]),
    }));
    for (const id of allow) {
      if (!rows.some((r) => r.id === id)) {
        rows.push({ id, enabled: true, contextLimit: "" });
      }
    }
    return rows;
  });
  const [fetchWarning, setFetchWarning] = useState("");

  const isEdit = mode === "edit";

  const handleProbe = async (credId: string) => {
    setProbing(true);
    setFetchWarning("");
    try {
      const result = await orgsApi.probeCredentialModels(orgId, credId);
      if (result.warning) setFetchWarning(result.warning);
      const existingLimits: Record<string, string> = {};
      for (const r of modelRows) existingLimits[r.id] = r.contextLimit;
      const rows: ModelRow[] = (result.models ?? []).map((m) => ({
        id: m.id,
        enabled: true,
        contextLimit: existingLimits[m.id] ?? (m.contextLimit > 0 ? String(m.contextLimit) : ""),
      }));
      setModelRows(rows);
    } catch (e) {
      setFetchWarning(
        e instanceof Error ? e.message : "Failed to fetch models",
      );
    } finally {
      setProbing(false);
    }
  };

  const handleSubmit = async () => {
    if (!name.trim() || (!isEdit && !apiKey.trim())) {
      setError(isEdit ? "Name is required" : "Name and API key are required");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const allowlist = modelRows.filter((r) => r.enabled).map((r) => r.id);
      const limits: Record<string, number> = {};
      for (const r of modelRows) {
        if (r.enabled && r.contextLimit.trim() !== "") {
          const n = Number(r.contextLimit);
          if (Number.isFinite(n) && n > 0) limits[r.id] = n;
        }
      }
      if (isEdit) {
        await orgsApi.updateCredential(orgId, existing!.id, {
          name: name.trim(),
          ...(apiKey.trim() ? { apiKey: apiKey.trim() } : {}),
          baseURL: baseURL.trim() || undefined,
          modelAllowlist: allowlist,
          modelContextLimits: limits,
        });
        onDone();
        return;
      }
      await orgsApi.createCredential(orgId, {
        name: name.trim(),
        provider,
        apiKey: apiKey.trim(),
        baseURL: baseURL.trim() || undefined,
        modelAllowlist: allowlist,
        modelContextLimits: limits,
      });
      setName("");
      setApiKey("");
      setBaseURL("");
      setModelRows([]);
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to save credential");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="space-y-3 rounded border border-border bg-muted/30 p-4">
      <h3 className="text-sm font-medium">
        {isEdit ? `Edit "${existing?.name}"` : "New Org Credential"}
      </h3>
      {error && <p className="text-xs text-red-500">{error}</p>}
      <input
        className="w-full rounded border border-border bg-background px-3 py-1.5 text-sm"
        placeholder="Name (e.g. Team OpenAI Key)"
        value={name}
        onChange={(e) => setName(e.target.value)}
      />
      {!isEdit && (
        <select
          className="w-full rounded border border-border bg-background px-3 py-1.5 text-sm"
          value={provider}
          onChange={(e) => setProvider(e.target.value)}
          disabled={isEdit}
        >
          {PROVIDERS.map((p) => (
            <option key={p.id} value={p.id}>
              {p.label}
            </option>
          ))}
        </select>
      )}
      <input
        className="w-full rounded border border-border bg-background px-3 py-1.5 text-sm"
        type="password"
        placeholder={isEdit ? "New API key (leave blank to keep)" : "API Key"}
        value={apiKey}
        onChange={(e) => setApiKey(e.target.value)}
      />
      <input
        className="w-full rounded border border-border bg-background px-3 py-1.5 text-sm"
        placeholder="Custom base URL (optional)"
        value={baseURL}
        onChange={(e) => setBaseURL(e.target.value)}
      />

      {/* Model configuration table */}
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <p className="text-xs font-medium text-muted-foreground">
            Models ({modelRows.length})
          </p>
          {isEdit && existing && (
            <Button
              size="sm"
              variant="outline"
              disabled={probing}
              onClick={() => handleProbe(existing.id)}
            >
              {probing ? "Fetching…" : "Fetch models from provider"}
            </Button>
          )}
        </div>
        {fetchWarning && (
          <p className="text-xs text-amber-600">{fetchWarning}</p>
        )}
        {!isEdit && (
          <p className="text-xs italic text-muted-foreground">
            Save the credential first, then fetch its model list from the edit view.
          </p>
        )}
        <ModelConfigTable rows={modelRows} onChange={setModelRows} />
      </div>

      <div className="flex gap-2">
        <Button size="sm" onClick={handleSubmit} disabled={loading}>
          {loading
            ? "Saving…"
            : isEdit
              ? "Save Changes"
              : "Create Credential"}
        </Button>
        <Button size="sm" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
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

  const addManual = () => {
    const id = window.prompt("Model ID");
    if (id && id.trim() && !rows.some((r) => r.id === id.trim())) {
      onChange([...rows, { id: id.trim(), enabled: true, contextLimit: "" }]);
    }
  };

  if (rows.length === 0) {
    return (
      <div className="space-y-1">
        <p className="text-xs italic text-muted-foreground">
          No models configured. All provider models are allowed.
        </p>
        <button
          type="button"
          onClick={addManual}
          className="text-xs text-blue-600 hover:underline"
        >
          + Add model manually
        </button>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <div className="max-h-48 overflow-y-auto rounded-md border border-border">
        <table className="w-full text-xs">
          <thead className="sticky top-0 bg-muted/80 backdrop-blur-sm">
            <tr>
              <th className="px-2 py-1.5 text-left font-medium text-muted-foreground w-8">
                On
              </th>
              <th className="px-2 py-1.5 text-left font-medium text-muted-foreground">
                Model ID
              </th>
              <th className="px-2 py-1.5 text-left font-medium text-muted-foreground w-40">
                Context window (tokens)
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row, idx) => (
              <tr
                key={row.id}
                className="border-t border-border/50 hover:bg-muted/30"
              >
                <td className="px-2 py-1">
                  <input
                    type="checkbox"
                    checked={row.enabled}
                    onChange={(e) =>
                      update(idx, { enabled: e.target.checked })
                    }
                    className="h-3.5 w-3.5"
                  />
                </td>
                <td className="px-2 py-1 font-mono">{row.id}</td>
                <td className="px-2 py-1">
                  <input
                    type="number"
                    min={0}
                    value={row.contextLimit}
                    onChange={(e) =>
                      update(idx, { contextLimit: e.target.value })
                    }
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
      <button
        type="button"
        onClick={addManual}
        className="text-xs text-blue-600 hover:underline"
      >
        + Add model manually
      </button>
    </div>
  );
}
