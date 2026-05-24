import { useQuery } from "@tanstack/react-query";
import { workspacesApi } from "../api/workspaces";

export function useWorkspaces() {
  return useQuery({
    queryKey: ["workspaces"],
    queryFn: () => workspacesApi.list(),
  });
}

export function useWorkspaceStatus(workspaceId: string | undefined) {
  return useQuery({
    queryKey: ["workspace-status", workspaceId],
    queryFn: () => workspacesApi.getStatus(workspaceId!),
    enabled: !!workspaceId,
    refetchInterval: (query) => {
      const phase = query.state.data?.phase;
      // Poll while transitioning
      if (phase === "Resuming" || phase === "Suspending" || phase === "Creating") return 1000;
      return false;
    },
  });
}

export function useWorkspaceSandboxes(workspaceId: string | undefined) {
  return useQuery({
    queryKey: ["workspace-sandboxes", workspaceId],
    queryFn: () => workspacesApi.getSandboxes(workspaceId!),
    enabled: !!workspaceId,
  });
}
