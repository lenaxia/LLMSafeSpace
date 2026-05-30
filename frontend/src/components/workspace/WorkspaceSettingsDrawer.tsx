import * as Dialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { useState, useEffect } from "react";
import type { WorkspaceListItem } from "../../api/types";
import { Toggle } from "../ui/Toggle";
import { NumberInput } from "../ui/NumberInput";
import { secretsApi, type SecretResponse } from "../../api/secrets";
import { api } from "../../api/client";

interface Props {
  workspace: WorkspaceListItem;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSave: (settings: WorkspaceSettings) => Promise<void>;
}

export interface WorkspaceSettings {
  autoSuspendEnabled?: boolean;
  autoSuspendIdleMinutes?: number;
}

const SECRET_TYPE_LABELS: Record<string, { label: string; icon: string }> = {
  "api-key": { label: "LLM Providers", icon: "🤖" },
  "ssh-key": { label: "SSH Keys", icon: "🔑" },
  "git-credential": { label: "Git Credentials", icon: "📦" },
  "secret-file": { label: "Secret Files", icon: "📄" },
  "env-secret": { label: "Environment Variables", icon: "⚙️" },
};

export function WorkspaceSettingsDrawer({ workspace, open, onOpenChange, onSave }: Props) {
  const [autoSuspend, setAutoSuspend] = useState(true);
  const [idleMinutes, setIdleMinutes] = useState(60);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [allSecrets, setAllSecrets] = useState<SecretResponse[]>([]);
  const [boundIds, setBoundIds] = useState<Set<string>>(new Set());
  const [bindingsChanged, setBindingsChanged] = useState(false);

  useEffect(() => {
    if (!open) return;
    Promise.all([
      secretsApi.list(),
      api.get<{ bindings: { secretId: string }[] }>(`/workspaces/${workspace.id}/bindings`),
    ]).then(([secretsRes, bindingsRes]) => {
      setAllSecrets(secretsRes.secrets || []);
      setBoundIds(new Set((bindingsRes.bindings || []).map((b) => b.secretId)));
    }).catch(() => {});
  }, [open, workspace.id]);

  const handleSave = async () => {
    setSaving(true);
    setError(null);
    try {
      await onSave({
        autoSuspendEnabled: autoSuspend,
        autoSuspendIdleMinutes: idleMinutes,
      });
      if (bindingsChanged) {
        await api.put(`/workspaces/${workspace.id}/bindings`, { secretIds: Array.from(boundIds) });
      }
      onOpenChange(false);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  };

  const toggleBinding = (secretId: string) => {
    setBoundIds((prev) => {
      const next = new Set(prev);
      if (next.has(secretId)) next.delete(secretId);
      else next.add(secretId);
      return next;
    });
    setBindingsChanged(true);
  };

  // Group secrets by type
  const grouped = Object.entries(SECRET_TYPE_LABELS)
    .map(([type, meta]) => ({
      type,
      ...meta,
      secrets: allSecrets.filter((s) => s.type === type),
    }))
    .filter((g) => g.secrets.length > 0);

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/40 z-50 data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=closed]:animate-out data-[state=closed]:fade-out-0" />
        <Dialog.Content className="fixed right-0 top-0 z-50 h-full w-80 bg-background border-l border-border shadow-xl p-6 overflow-y-auto data-[state=open]:animate-in data-[state=open]:slide-in-from-right data-[state=closed]:animate-out data-[state=closed]:slide-out-to-right duration-200">
          <div className="flex items-center justify-between mb-6">
            <Dialog.Title className="text-sm font-semibold">
              Workspace Settings
            </Dialog.Title>
            <Dialog.Close className="rounded p-1 hover:bg-accent">
              <X className="h-4 w-4" />
            </Dialog.Close>
          </div>
          <Dialog.Description className="sr-only">
            Configure settings for this workspace
          </Dialog.Description>

          <p className="text-xs text-muted-foreground mb-6 truncate">{workspace.name}</p>

          <div className="space-y-5">
            {/* Auto-suspend */}
            <div className="flex items-center justify-between">
              <div>
                <label className="text-sm font-medium" htmlFor="autoSuspend">Auto-Suspend</label>
                <p className="text-xs text-muted-foreground">Suspend when no activity</p>
              </div>
              <Toggle id="autoSuspend" checked={autoSuspend} onCheckedChange={(v) => { setAutoSuspend(v); }} />
            </div>

            {!autoSuspend && (
              <p className="text-xs text-amber-600 bg-amber-50 dark:bg-amber-950/30 dark:text-amber-400 rounded px-2 py-1.5">
                ⚠️ Disabling auto-suspend will keep this workspace running indefinitely, consuming compute minutes and potentially causing unexpected costs.
              </p>
            )}

            {autoSuspend && (
              <div className="flex items-center justify-between">
                <div>
                  <label className="text-sm font-medium" htmlFor="idleMinutes">Idle Timeout (min)</label>
                  <p className="text-xs text-muted-foreground">Minutes of zero traffic before suspend. Active LLM conversations and streaming keep the workspace alive.</p>
                </div>
                <NumberInput id="idleMinutes" value={idleMinutes} onChange={setIdleMinutes} min={5} max={10080} />
              </div>
            )}

            {/* Attached Secrets - grouped by type */}
            {allSecrets.length > 0 && (
              <div className="border-t border-border pt-4">
                <label className="text-sm font-medium">Attached Secrets</label>
                <p className="text-xs text-muted-foreground mb-3">Secrets injected when this workspace starts</p>
                <div className="space-y-3 max-h-64 overflow-y-auto">
                  {grouped.map((group) => (
                    <div key={group.type}>
                      <p className="text-xs font-medium text-muted-foreground mb-1">
                        {group.icon} {group.label}
                      </p>
                      <div className="space-y-0.5 ml-1">
                        {group.secrets.map((s) => (
                          <label key={s.id} className="flex items-center gap-2 rounded px-2 py-1 hover:bg-accent/50 cursor-pointer">
                            <input
                              type="checkbox"
                              checked={boundIds.has(s.id)}
                              onChange={() => toggleBinding(s.id)}
                              className="rounded border-border"
                            />
                            <span className="text-sm">{s.name}</span>
                          </label>
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>

          {error && <p className="text-xs text-destructive mt-4">{error}</p>}

          <div className="mt-8 flex gap-2">
            <button
              onClick={handleSave}
              disabled={saving}
              className="flex-1 rounded-md bg-primary px-3 py-1.5 text-sm text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            >
              {saving ? "Saving..." : "Save"}
            </button>
            <Dialog.Close className="flex-1 rounded-md border border-border px-3 py-1.5 text-sm hover:bg-accent">
              Cancel
            </Dialog.Close>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
