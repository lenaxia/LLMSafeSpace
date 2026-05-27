import { useEffect, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { workspacesApi } from "../api/workspaces";

/**
 * Fetches the opencode session title for the active session via the proxy.
 *
 * Behaviour:
 * - Fetches on mount when workspaceId + sessionId are available and active is true.
 * - Re-fetches whenever `streaming` transitions from true → false (i.e. after
 *   the agent finishes a response, which is when opencode generates a title).
 * - When a non-empty title is received, invalidates ["sessions", workspaceId]
 *   so the sidebar reflects the new title immediately.
 */
export function useSessionTitle(
  workspaceId: string | undefined,
  sessionId: string | undefined,
  active: boolean,
  streaming: boolean,
) {
  const queryClient = useQueryClient();
  const prevStreaming = useRef(streaming);

  const { data, refetch } = useQuery({
    queryKey: ["session-title", workspaceId, sessionId],
    queryFn: () => workspacesApi.getSession(workspaceId!, sessionId!),
    enabled: !!workspaceId && !!sessionId && active,
    // Don't auto-retry on 404 — a brand new session may not exist in opencode yet.
    retry: false,
    // Don't refetch on window focus; title changes only happen after messages.
    refetchOnWindowFocus: false,
  });

  // Re-fetch when streaming ends — opencode generates the title after the first exchange.
  useEffect(() => {
    if (prevStreaming.current && !streaming && workspaceId && sessionId && active) {
      console.log("[SessionTitle] streaming ended, refetching title for", sessionId);
      refetch();
    }
    prevStreaming.current = streaming;
  }, [streaming, workspaceId, sessionId, active, refetch]);

  // When a title arrives, push it into the sessions list cache so the sidebar
  // updates without a full refetch of the session list.
  useEffect(() => {
    console.log("[SessionTitle] data:", data?.id, "title:", data?.title);
    if (!data?.title || !workspaceId || !sessionId) return;
    queryClient.invalidateQueries({ queryKey: ["sessions", workspaceId] });
  }, [data?.title, workspaceId, sessionId, queryClient]);

  return data?.title ?? undefined;
}
