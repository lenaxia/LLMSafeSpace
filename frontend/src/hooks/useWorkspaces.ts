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
 * Belt-and-suspenders: while the workspace is in a transitioning phase, a
 * 3-second refetchInterval is active. This covers the narrow window between
 * an SSE connect and a phase event that already fired before the connection
 * was established (e.g. API server restart followed immediately by a resume),
 * as well as any SSE reconnect backoff delay (2s initial, doubles to 30s).
 *
 * Once Active (or Suspended/terminal), the interval is disabled and updates
 * are purely SSE-driven.
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
    // Poll every 3 s while transitioning; disabled once stable.
    // This is intentionally conservative — SSE should deliver the Active event
    // within 1-2 s of the transition; the poll is a fallback, not the primary path.
    refetchInterval: (query) => {
      const phase = query.state.data?.phase;
      return phase && transitioningPhases.has(phase) ? 3_000 : false;
    },
  });
}
