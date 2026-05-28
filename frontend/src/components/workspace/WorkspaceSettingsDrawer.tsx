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
  maxActiveSessions?: number;
  autoSuspendEnabled?: boolean;
  autoSuspendIdleMinutes?: number;
}

export function WorkspaceSettingsDrawer({ workspace, open, onOpenChange, onSave }: Props) {
  const [maxSessions, setMaxSessions] = useState(workspace.maxActiveSessions ?? 5);
  const [autoSuspend, setAutoSuspend] = useState(true);
  const [idleMinutes, setIdleMinutes] = useState(60);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [allSecrets, setAllSecrets] = useState<SecretResponse[]>([]);
  const [boundIds, setBoundIds] = useState<Set<string>>(new Set());
  const [bindingsChanged, setBindingsChanged] = useState(false);

  useEffect(() => {
    if (!open) return;
    // Fetch user's secrets and current bindings for this workspace
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
        maxActiveSessions: maxSessions,
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
            <div className="flex items-center justify-between">
              <div>
                <label className="text-sm font-medium" htmlFor="maxSessions">Max Sessions</label>
                <p className="text-xs text-muted-foreground">Concurrent sessions</p>
              </div>
              <NumberInput id="maxSessions" value={maxSessions} onChange={setMaxSessions} min={1} max={20} />
            </div>

            <div className="flex items-center justify-between">
              <div>
                <label className="text-sm font-medium" htmlFor="autoSuspend">Auto-Suspend</label>
                <p className="text-xs text-muted-foreground">Suspend when idle</p>
              </div>
              <Toggle id="autoSuspend" checked={autoSuspend} onCheckedChange={setAutoSuspend} />
            </div>

            {autoSuspend && (
              <div className="flex items-center justify-between">
                <div>
                  <label className="text-sm font-medium" htmlFor="idleMinutes">Idle Timeout (min)</label>
                  <p className="text-xs text-muted-foreground">Minutes before suspend</p>
                </div>
                <NumberInput id="idleMinutes" value={idleMinutes} onChange={setIdleMinutes} min={5} max={10080} />
              </div>
            )}

            {allSecrets.length > 0 && (
              <div className="border-t border-border pt-4">
                <label className="text-sm font-medium">Attached Secrets</label>
                <p className="text-xs text-muted-foreground mb-2">Secrets injected when this workspace starts</p>
                <div className="space-y-1.5 max-h-48 overflow-y-auto">
                  {allSecrets.map((s) => (
                    <label key={s.id} className="flex items-center gap-2 rounded px-2 py-1.5 hover:bg-accent/50 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={boundIds.has(s.id)}
                        onChange={() => toggleBinding(s.id)}
                        className="rounded border-border"
                      />
                      <span className="text-sm">{s.name}</span>
                      <span className="text-xs text-muted-foreground">{s.type}</span>
                    </label>
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
