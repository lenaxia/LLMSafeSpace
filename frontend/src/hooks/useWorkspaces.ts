import { useQuery } from "@tanstack/react-query";
import { workspacesApi } from "../api/workspaces";
import { wsLog } from "../lib/wsLog";

export function useWorkspaces() {
  return useQuery({
    queryKey: ["workspaces"],
    queryFn: () => workspacesApi.list(),
  });
}

// Phases that indicate the workspace is transitioning and not yet usable.
const transitioningPhases = new Set(["Pending", "Creating", "Resuming", "Suspending"]);

/**
 * Fetches the current workspace status.
 *
 * Primary update path: SSE workspace.phase events (from useEventStream in
 * ChatPage) invalidate ["workspace-status", workspaceId], triggering a fresh
 * fetch. staleTime: 0 ensures invalidation always causes a re-fetch.
 *
 * Belt-and-suspenders:
 * - While transitioning: 3-second poll to catch phase events that fired before
 *   SSE connected (e.g. API restart followed immediately by resume).
 * - While Active: 30-second poll to keep per-session context usage fresh.
 *   Context data (ContextUsed on each session) is not delivered via SSE —
 *   it is derived from agentd's statusz endpoint by the controller health loop.
 *   Without a background poll, the compaction indicator and DiskUsageBar
 *   context display would never see updates after initial page load.
 * - Suspended/terminal: no poll (data is stable).
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
    refetchInterval: (query) => {
      const phase = query.state.data?.phase;
      if (!phase) return false;
      if (transitioningPhases.has(phase)) return 3_000;
      if (phase === "Active") return 30_000;
      return false;
    },
  });
}
