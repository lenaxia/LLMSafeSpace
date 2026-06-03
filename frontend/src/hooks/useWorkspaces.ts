import { useQuery } from "@tanstack/react-query";
import { workspacesApi } from "../api/workspaces";
import { wsLog } from "../lib/wsLog";

export function useWorkspaces() {
  return useQuery({
    queryKey: ["workspaces"],
    queryFn: () => workspacesApi.list(),
  });
}

/**
 * Fetches the current workspace status once. Does not poll.
 *
 * Updates are driven by SSE: when the backend emits a workspace.phase event,
 * ChatPage invalidates ["workspace-status", workspaceId], which triggers a
 * fresh fetch. staleTime: 0 ensures invalidation always causes a re-fetch.
 */
export function useWorkspaceStatus(workspaceId: string | undefined) {
  return useQuery({
    queryKey: ["workspace-status", workspaceId],
    queryFn: async () => {
      wsLog("status.fetch_start", workspaceId);
      const result = await workspacesApi.getStatus(workspaceId!);
      wsLog("status.fetch_done", workspaceId, `phase=${result.phase}`);
      return result;
    },
    enabled: !!workspaceId,
    staleTime: 0,
  });
}
