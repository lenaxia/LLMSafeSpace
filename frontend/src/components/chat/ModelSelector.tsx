import { useEffect, useState } from "react";
import { useQuery, useMutation, useQueryClient, keepPreviousData } from "@tanstack/react-query";
import { workspacesApi } from "../../api/workspaces";
import type { ModelInfo } from "../../api/workspaces";
import { useUserSetting } from "../../hooks/useUserSettings";
import { ChevronDown } from "lucide-react";

interface Props {
  workspaceId: string;
  disabled?: boolean;
}

export function ModelSelector({ workspaceId, disabled }: Props) {
  const [open, setOpen] = useState(false);
  const [toast, setToast] = useState<string | null>(null);
  // optimisticModel holds the locally-selected model ID while the mutation is
  // in-flight. It is set immediately on click and cleared (on success or error)
  // once the server confirms. On error it reverts to the server-confirmed value.
  const [optimisticModel, setOptimisticModel] = useState<string | null>(null);
  const queryClient = useQueryClient();
  // US-9.16: user's preferred default model (Tier-3 setting). Used to seed a
  // workspace that has no currentModel yet. Empty string = no preference.
  const preferredModel = useUserSetting<string>("preferredModel", "");

  const { data, isLoading, isError } = useQuery({
    queryKey: ["models", workspaceId],
    queryFn: () => workspacesApi.listModels(workspaceId),
    enabled: !!workspaceId,
    staleTime: 10_000,
    retry: 1,
    // Keep the previous data visible during background refetches so the
    // selector doesn't collapse to null while invalidateQueries re-fetches.
    placeholderData: keepPreviousData,
  });

  const setModelMutation = useMutation({
    mutationFn: (model: string) => workspacesApi.setModel(workspaceId, model),
    onSuccess: (data) => {
      setOptimisticModel(null);
      queryClient.invalidateQueries({ queryKey: ["models", workspaceId] });
      if (data && !data.applied) {
        setToast("Model saved — takes effect on your next message.");
      }
    },
    onError: () => {
      // Revert the optimistic selection and show an error.
      setOptimisticModel(null);
      setToast("Failed to set model. Please try again.");
    },
  });

  const handleSelectModel = (modelId: string) => {
    setOptimisticModel(modelId);
    setOpen(false);
    setModelMutation.mutate(modelId);
  };

  const models = data?.models ?? [];
  const serverModel = data?.currentModel || "";
  // Show the optimistic selection immediately; fall back to server-confirmed value.
  const currentModel = optimisticModel ?? serverModel;
  const currentDisplay = currentModel
    ? models.find((m) => m.id === currentModel)?.name || currentModel.split("/").pop()
    : "Select model";

  // US-9.16: Seed the workspace's model from the user's preferredModel when the
  // workspace has no currentModel yet. Fires at most once per workspace — the
  // dependency array excludes the mutation to avoid re-triggering on every
  // render. The setModel call flips serverModel non-empty, which causes the
  // guard to fail on subsequent runs.
  useEffect(() => {
    if (!workspaceId || !preferredModel || serverModel || models.length === 0) return;
    if (setModelMutation.isPending) return;
    const available = models.some((m) => m.id === preferredModel);
    if (!available) return;
    setModelMutation.mutate(preferredModel);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspaceId, preferredModel, serverModel, models]);

  // Auto-dismiss toast after 4 seconds
  useEffect(() => {
    if (!toast) return;
    const id = setTimeout(() => setToast(null), 4000);
    return () => clearTimeout(id);
  }, [toast]);

  // Show spinner only on the very first load (no prior data in cache).
  if (isLoading && models.length === 0) {
    return (
      <span className="text-xs text-muted-foreground px-2 py-1">
        Loading models...
      </span>
    );
  }

  if (isError && models.length === 0) {
    return (
      <span className="text-xs text-destructive px-2 py-1" title="Could not load models">
        ⚠ Models
      </span>
    );
  }

  // Return null only when we have a confirmed empty result (not a loading race).
  if (!isLoading && models.length === 0) {
    return null;
  }

  return (
    <div className="relative">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        disabled={disabled}
        className="flex items-center gap-1 rounded-md border border-input bg-background px-2 py-1 text-xs hover:bg-accent disabled:opacity-50"
      >
        <span className="max-w-[160px] truncate">{currentDisplay}</span>
        <ChevronDown className="h-3 w-3 shrink-0" />
      </button>

      {open && (
        <>
          {/* Backdrop to close on click outside */}
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div className="absolute right-0 top-full z-50 mt-1 max-h-64 w-64 overflow-y-auto rounded-md border border-border bg-popover shadow-md">
            {/* Backend already filters out unavailable models; no frontend re-filter needed */}
            {models.map((m: ModelInfo) => (
              <button
                key={m.id}
                type="button"
                onClick={() => handleSelectModel(m.id)}
                className={`flex w-full items-center justify-between px-3 py-2 text-left text-xs hover:bg-accent ${
                  m.id === currentModel ? "bg-accent/50 font-medium" : ""
                }`}
              >
                <span className="truncate">{m.name || m.id}</span>
                <span className={`ml-2 shrink-0 rounded px-1 py-0.5 text-[10px] ${
                  m.freeTier ? "bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200" : "bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200"
                }`}>
                  {m.tier}
                </span>
              </button>
            ))}
          </div>
        </>
      )}
      {toast && (
        <div className="absolute right-0 top-full z-50 mt-1 rounded-md border border-border bg-popover px-3 py-2 text-xs shadow-md">
          {toast}
        </div>
      )}
    </div>
  );
}
