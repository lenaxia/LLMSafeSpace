import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { getEnv } from "../env";
import { createSSEConnection } from "../lib/sseConnection";
import { wsLog } from "../lib/wsLog";

const MIN_RECONNECT_MS = 1000;
const MAX_RECONNECT_MS = 30_000;
const READ_TIMEOUT_MS = 35_000; // Must exceed backend heartbeat interval (25s)

/**
 * useUserEventStream connects to the user-scoped SSE endpoint (GET /api/v1/events)
 * and invalidates workspace caches when phase events arrive for any workspace.
 *
 * This hook mounts once from the root layout and stays connected for the lifetime
 * of the app. It handles reconnection with exponential backoff and Last-Event-ID
 * replay.
 */
export function useUserEventStream() {
  const queryClient = useQueryClient();
  const lastEventIDRef = useRef<string | null>(null);

  useEffect(() => {
    const { apiBaseUrl } = getEnv();

    function buildHeaders(): Record<string, string> {
      const h: Record<string, string> = {};
      if (lastEventIDRef.current !== null) {
        h["Last-Event-ID"] = lastEventIDRef.current;
      }
      return h;
    }

    let conn: ReturnType<typeof createSSEConnection> | null = null;

    function start() {
      conn = createSSEConnection({
        url: `${apiBaseUrl}/events`,
        headers: buildHeaders(),
        onEvent: (data) => {
          const evt = data as {
            event_id?: number;
            workspace_id?: string;
            type: string;
            phase?: string;
          };

          // Track last event ID for replay on reconnect
          if (evt.event_id && evt.event_id > 0) {
            lastEventIDRef.current = String(evt.event_id);
          }

          if (evt.type === "workspace.phase" && evt.workspace_id) {
            wsLog("user_stream.phase", evt.workspace_id, `phase=${evt.phase}`);
            queryClient.invalidateQueries({ queryKey: ["workspaces"] });
            queryClient.invalidateQueries({
              queryKey: ["workspace-status", evt.workspace_id],
            });
          } else if (evt.type === "resync") {
            wsLog("user_stream.resync", "");
            queryClient.invalidateQueries({ queryKey: ["workspaces"] });
            queryClient.invalidateQueries({ queryKey: ["workspace-status"] });
          }
        },
        onConnect: () => {
          // On reconnect, invalidate all workspace caches
          if (lastEventIDRef.current !== null) {
            wsLog("user_stream.reconnected", "");
            queryClient.invalidateQueries({ queryKey: ["workspaces"] });
            queryClient.invalidateQueries({ queryKey: ["workspace-status"] });
          } else {
            wsLog("user_stream.connected", "");
          }
        },
        logPrefix: "user_stream",
        readTimeoutMs: READ_TIMEOUT_MS,
        minReconnectMs: MIN_RECONNECT_MS,
        maxReconnectMs: MAX_RECONNECT_MS,
      });
    }

    start();

    return () => conn?.destroy();
  }, [queryClient]);
}
