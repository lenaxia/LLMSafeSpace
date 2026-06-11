// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// API Keys settings tab.
// Allows users to create, list, and delete personal API keys for programmatic
// access. The raw key value is shown exactly once after creation.

import { useEffect, useState } from "react";
import { apiKeysApi } from "../../api/apiKeys";
import type { ApiKey, CreateApiKeyResponse } from "../../api/apiKeys";
import { useToast } from "../../providers/ToastProvider";
import { Spinner } from "../ui/Spinner";
import { MetaRow } from "./MetaRow";
import { Key, Trash2, Plus, Copy, Check, AlertTriangle } from "lucide-react";

// ─── Main tab ─────────────────────────────────────────────────────────────────

export function ApiKeysTab() {
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [newKeyResult, setNewKeyResult] = useState<CreateApiKeyResponse | null>(null);
  const { toast } = useToast();

  const load = async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await apiKeysApi.list();
      setKeys(data);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to load API keys");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, []);

  const handleDelete = async (id: string, name: string) => {
    try {
      await apiKeysApi.delete(id);
      setKeys((prev) => prev.filter((k) => k.id !== id));
      toast(`Deleted "${name}"`);
    } catch (e: unknown) {
      toast(e instanceof Error ? e.message : "Delete failed", "error");
    }
  };

  const handleCreated = (res: CreateApiKeyResponse) => {
    setKeys((prev) => [...prev, res.apiKey]);
    setShowCreate(false);
    setNewKeyResult(res);
  };

  if (loading) return <div className="flex justify-center p-8"><Spinner /></div>;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-lg font-medium">API Keys</h3>
          <p className="text-sm text-muted-foreground">
            Manage your API keys for programmatic access.
          </p>
        </div>
        <button
          onClick={() => { setShowCreate(true); setNewKeyResult(null); }}
          disabled={showCreate}
          className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-accent disabled:opacity-50"
        >
          <Plus className="h-3 w-3" /> Create key
        </button>
      </div>

      {/* Error banner */}
      {error && (
        <div className="flex items-center justify-between rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <span>{error}</span>
          <button
            onClick={() => setError(null)}
            className="ml-2 shrink-0 text-destructive hover:opacity-70"
          >
            ✕
          </button>
        </div>
      )}

      {/* One-time new key banner */}
      {newKeyResult && (
        <NewKeyBanner
          result={newKeyResult}
          onDismiss={() => setNewKeyResult(null)}
        />
      )}

      {/* Inline create form */}
      {showCreate && (
        <CreateKeyForm
          onCreated={handleCreated}
          onCancel={() => setShowCreate(false)}
          onError={(msg) => setError(msg)}
        />
      )}

      {/* Empty state */}
      {keys.length === 0 && !showCreate ? (
        <div className="rounded-md border border-dashed border-border p-8 text-center">
          <Key className="mx-auto mb-2 h-8 w-8 text-muted-foreground" />
          <p className="text-sm text-muted-foreground">No API keys yet.</p>
          <p className="text-xs text-muted-foreground mt-1">
            Create a key to access the API programmatically.
          </p>
        </div>
      ) : (
        <div className="divide-y divide-border rounded-md border border-border">
          {keys.map((k) => (
            <KeyRow
              key={k.id}
              apiKey={k}
              onDelete={() => handleDelete(k.id, k.name)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ─── One-time key banner ──────────────────────────────────────────────────────

function NewKeyBanner({
  result,
  onDismiss,
}: {
  result: CreateApiKeyResponse;
  onDismiss: () => void;
}) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(result.key);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // fallback: select the text
    }
  };

  return (
    <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-4 space-y-3">
      <div className="flex items-start gap-2">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
        <div>
          <p className="text-sm font-medium text-amber-700 dark:text-amber-400">
            Copy your key now — it will never be shown again
          </p>
          <p className="text-xs text-muted-foreground mt-0.5">
            Key created: <span className="font-medium">{result.apiKey.name}</span>
          </p>
        </div>
      </div>

      <div className="flex items-center gap-2">
        <code
          data-testid="new-key-value"
          className="flex-1 rounded border border-border bg-background px-3 py-1.5 font-mono text-xs break-all select-all"
        >
          {result.key}
        </code>
        <button
          onClick={handleCopy}
          title="Copy key"
          className="shrink-0 rounded border border-border p-1.5 hover:bg-accent"
        >
          {copied ? (
            <Check className="h-3.5 w-3.5 text-green-500" />
          ) : (
            <Copy className="h-3.5 w-3.5" />
          )}
        </button>
      </div>

      <button
        onClick={onDismiss}
        className="text-xs text-muted-foreground hover:text-foreground underline"
      >
        I've copied my key, dismiss this
      </button>
    </div>
  );
}

// ─── Key row ──────────────────────────────────────────────────────────────────

function KeyRow({
  apiKey,
  onDelete,
}: {
  apiKey: ApiKey;
  onDelete: () => void;
}) {
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [expanded, setExpanded] = useState(false);

  return (
    <div>
      {/* Row header */}
      <div
        className="flex cursor-pointer items-center gap-3 px-4 py-3 hover:bg-accent/20 transition-colors"
        onClick={() => setExpanded((v) => !v)}
      >
        <Key className="h-4 w-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <span className="text-sm font-medium">{apiKey.name}</span>
          <p className="text-xs text-muted-foreground">
            Prefix: <span className="font-mono">{apiKey.prefix}</span>
            {" · "}
            Created {new Date(apiKey.createdAt).toLocaleDateString()}
            {apiKey.lastUsedAt
              ? ` · Last used ${new Date(apiKey.lastUsedAt).toLocaleDateString()}`
              : " · Never used"}
          </p>
        </div>

        {/* Delete control — stops propagation so it doesn't toggle expand */}
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
              title="Delete key"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      </div>

      {/* Expanded metadata */}
      {expanded && (
        <div className="border-t border-border bg-muted/20 px-4 py-4">
          <div className="grid grid-cols-2 gap-x-6 gap-y-1 text-xs">
            <MetaRow label="ID" value={apiKey.id} mono />
            <MetaRow label="Prefix" value={apiKey.prefix} mono />
            <MetaRow label="Created" value={new Date(apiKey.createdAt).toLocaleString()} />
            {apiKey.lastUsedAt && (
              <MetaRow label="Last used" value={new Date(apiKey.lastUsedAt).toLocaleString()} />
            )}
          </div>
        </div>
      )}
    </div>
  );
}

// ─── Create form ──────────────────────────────────────────────────────────────

function CreateKeyForm({
  onCreated,
  onCancel,
  onError,
}: {
  onCreated: (res: CreateApiKeyResponse) => void;
  onCancel: () => void;
  onError: (msg: string) => void;
}) {
  const [name, setName] = useState("");
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const handleSubmit = async () => {
    if (!name.trim()) {
      setFormError("Name is required");
      return;
    }
    setSaving(true);
    setFormError(null);
    try {
      const res = await apiKeysApi.create({ name: name.trim() });
      onCreated(res);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "Create failed";
      setFormError(msg);
      onError(msg);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="rounded-md border border-border bg-muted/20 p-4 space-y-3">
      <h3 className="text-sm font-semibold">New API Key</h3>
      {formError && <p className="text-xs text-destructive">{formError}</p>}

      <div>
        <label className="text-xs text-muted-foreground">Name</label>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && handleSubmit()}
          placeholder="e.g. CI / Production"
          autoFocus
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
