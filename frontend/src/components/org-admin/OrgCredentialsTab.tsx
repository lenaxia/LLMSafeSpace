import { useCallback, useEffect, useState } from "react";
import { useOutletContext } from "react-router-dom";
import {
  orgsApi,
  type OrgCredential,
  type OrgResponse,
} from "../../api/orgs";
import { SDK_KINDS, SLUG_REGEX, slugFromName } from "../../api/providerCredentialTypes";
import { useToast } from "../../providers/ToastProvider";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";
import { ModelConfigTable, type ModelRow } from "../shared/ModelConfigTable";
import { ChevronDown, ChevronUp, KeyRound, Pencil, Trash2 } from "lucide-react";

interface CredentialsContext {
  org: OrgResponse;
  isAdmin: boolean;
}

// PROVIDERS used to list SDK kinds for the dropdown. Now sourced from the
// canonical SDK_KINDS constant in providerCredentialTypes.ts (Epic 55) so
// the frontend choices stay in lockstep with the DB CHECK enum.

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
                    <p className="font-medium">
                      {c.name}
                      <span className="ml-2 rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-muted-foreground" title={`SDK kind: ${c.kind}`}>
                        {c.slug}
                      </span>
                    </p>
                    <p className="text-xs text-muted-foreground">
                      {c.kind}
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
                  {(Object.keys(c.modelContextLimits ?? {}).length > 0 ||
                    Object.keys(c.modelOutputLimits ?? {}).length > 0) && (
                    <div>
                      <p className="text-xs font-medium text-muted-foreground">Per-model limits</p>
                      <ul className="mt-1 space-y-0.5 text-xs font-mono">
                        {Array.from(
                          new Set([
                            ...Object.keys(c.modelContextLimits ?? {}),
                            ...Object.keys(c.modelOutputLimits ?? {}),
                          ]),
                        ).map((m) => {
                          const ctx = c.modelContextLimits?.[m];
                          const out = c.modelOutputLimits?.[m];
                          return (
                            <li key={m}>
                              {m}: ctx {ctx ? ctx.toLocaleString() : "—"} / out{" "}
                              {out ? out.toLocaleString() : "—"}
                            </li>
                          );
                        })}
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
  // kind starts empty for new credentials so the user is forced to make an
  // explicit choice via the dropdown — a silent "openai" default would let a
  // user accidentally create an openai-kind credential when they meant
  // openai_compatible (LiteLLM/vLLM). When editing an existing cred, default
  // to that cred's kind so the dropdown reflects current state.
  const [kind, setKind] = useState<string>(existing?.kind ?? "");
  const [slug, setSlug] = useState<string>(existing?.slug ?? "");
  const [apiKey, setApiKey] = useState("");
  const [baseURL, setBaseURL] = useState(existing?.baseURL ?? "");
  const [loading, setLoading] = useState(false);
  const [probing, setProbing] = useState(false);
  const [error, setError] = useState("");
  const [modelRows, setModelRows] = useState<ModelRow[]>(() => {
    if (!existing) return [];
    const ctxLimits = existing.modelContextLimits ?? {};
    const outLimits = existing.modelOutputLimits ?? {};
    const allow = existing.modelAllowlist ?? [];
    const ids = Array.from(
      new Set([...Object.keys(ctxLimits), ...Object.keys(outLimits), ...allow]),
    );
    return ids.map((id) => ({
      id,
      enabled: allow.includes(id),
      contextLimit: ctxLimits[id] ? String(ctxLimits[id]) : "",
      outputLimit: outLimits[id] ? String(outLimits[id]) : "",
    }));
  });
  const [fetchWarning, setFetchWarning] = useState("");

  const isEdit = mode === "edit";

  const handleProbe = async (credId: string) => {
    setProbing(true);
    setFetchWarning("");
    try {
      const result = await orgsApi.probeCredentialModels(orgId, credId);
      if (result.warning) setFetchWarning(result.warning);
      const existingCtx: Record<string, string> = {};
      const existingOut: Record<string, string> = {};
      for (const r of modelRows) {
        existingCtx[r.id] = r.contextLimit;
        existingOut[r.id] = r.outputLimit;
      }
      const rows: ModelRow[] = (result.models ?? []).map((m) => ({
        id: m.id,
        enabled: true,
        contextLimit: existingCtx[m.id] ?? (m.contextLimit > 0 ? String(m.contextLimit) : ""),
        outputLimit: existingOut[m.id] ?? (m.outputLimit > 0 ? String(m.outputLimit) : ""),
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
    if (!isEdit) {
      if (!kind.trim() || !slug.trim()) {
        setError("Kind and slug are required");
        return;
      }
      if (!SLUG_REGEX.test(slug.trim())) {
        setError(
          "Slug must be 1–64 lowercase alphanumeric characters and hyphens, " +
          "starting and ending with alphanumeric (e.g. \"team-openai\")",
        );
        return;
      }
    }
    setLoading(true);
    setError("");
    try {
      const allowlist = modelRows.filter((r) => r.enabled).map((r) => r.id);
      const ctxLimits: Record<string, number> = {};
      const outLimits: Record<string, number> = {};
      for (const r of modelRows) {
        if (!r.enabled) continue;
        if (r.contextLimit.trim() !== "") {
          const n = Number(r.contextLimit);
          if (Number.isFinite(n) && n > 0) ctxLimits[r.id] = n;
        }
        if (r.outputLimit.trim() !== "") {
          const n = Number(r.outputLimit);
          if (Number.isFinite(n) && n > 0) outLimits[r.id] = n;
        }
      }
      if (isEdit) {
        // Only send baseURL when it changed from the stored value. This avoids
        // triggering a ciphertext re-encryption on every save (baseURL lives in
        // the encrypted blob). Sending "" explicitly clears a previously-set URL.
        const updateReq: Record<string, unknown> = {
          name: name.trim(),
          modelAllowlist: allowlist,
          modelContextLimits: ctxLimits,
          modelOutputLimits: outLimits,
        };
        if (apiKey.trim()) updateReq.apiKey = apiKey.trim();
        const prevBaseURL = existing?.baseURL ?? "";
        const nextBaseURL = baseURL.trim();
        if (nextBaseURL !== prevBaseURL) {
          updateReq.baseURL = nextBaseURL;
        }
        await orgsApi.updateCredential(orgId, existing!.id, updateReq);
        onDone();
        return;
      }
      await orgsApi.createCredential(orgId, {
        name: name.trim(),
        kind: kind.trim(),
        slug: slug.trim(),
        apiKey: apiKey.trim(),
        baseURL: baseURL.trim() || undefined,
        modelAllowlist: allowlist,
        modelContextLimits: ctxLimits,
        modelOutputLimits: outLimits,
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
        onChange={(e) => {
          const v = e.target.value;
          setName(v);
          // Auto-suggest slug from name while slug is empty or matches the auto-suggested value.
          if (!isEdit && (!slug || slug === slugFromName(name))) {
            setSlug(slugFromName(v));
          }
        }}
      />
      {!isEdit && (
        <>
          <select
            className="w-full rounded border border-border bg-background px-3 py-1.5 text-sm"
            value={kind}
            onChange={(e) => setKind(e.target.value)}
          >
            <option value="">— select SDK kind —</option>
            {SDK_KINDS.map((k) => (
              <option key={k} value={k}>{k}</option>
            ))}
          </select>
          <input
            className="w-full rounded border border-border bg-background px-3 py-1.5 font-mono text-sm"
            placeholder="Slug (lowercase alphanumeric + hyphens, e.g. team-openai)"
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
            pattern={SLUG_REGEX.source}
          />
          <p className="text-[10px] text-muted-foreground">
            Slug is the per-org identity. It appears in agent-config.json as the provider-map key (opencode persists it as <code>providerID</code>).
          </p>
        </>
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
        <OrgModelConfigTable rows={modelRows} onChange={setModelRows} />
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

// OrgModelConfigTable wraps the shared ModelConfigTable and adds the
// "Add model manually" affordance that is specific to the org credential form.
function OrgModelConfigTable({
  rows,
  onChange,
}: {
  rows: ModelRow[];
  onChange: (rows: ModelRow[]) => void;
}) {
  const addManual = () => {
    const id = window.prompt("Model ID");
    if (id && id.trim() && !rows.some((r) => r.id === id.trim())) {
      onChange([...rows, { id: id.trim(), enabled: true, contextLimit: "", outputLimit: "" }]);
    }
  };

  const manualAddButton = (
    <button
      type="button"
      onClick={addManual}
      className="text-xs text-blue-600 hover:underline"
    >
      + Add model manually
    </button>
  );

  if (rows.length === 0) {
    return (
      <div className="space-y-1">
        <p className="text-xs italic text-muted-foreground">
          No models configured. All provider models are allowed.
        </p>
        {manualAddButton}
      </div>
    );
  }

  return (
    <ModelConfigTable rows={rows} onChange={onChange} footer={manualAddButton} />
  );
}
