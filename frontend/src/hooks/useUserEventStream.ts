import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { getEnv } from "../env";
import { wsLog } from "../lib/wsLog";

const MIN_RECONNECT_MS = 1000;
const MAX_RECONNECT_MS = 30_000;
const READ_TIMEOUT_MS = 35_000;

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
    let cancelled = false;
    let retryDelay = MIN_RECONNECT_MS;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;
    let abortCtrl = new AbortController();

    function scheduleReconnect() {
      if (cancelled) return;
      retryTimer = setTimeout(() => {
        if (cancelled) return;
        // Abort the old controller so any hanging reader.read() is released
        abortCtrl.abort();
        abortCtrl = new AbortController();
        retryDelay = Math.min(retryDelay * 2, MAX_RECONNECT_MS);
        connect();
      }, retryDelay);
    }

    async function connect() {
      if (cancelled) return;
      const { apiBaseUrl } = getEnv();
      const url = `${apiBaseUrl}/events`;

      wsLog("user_stream.connecting", "", `url=${url}`);

      try {
        const headers: Record<string, string> = {
          Accept: "text/event-stream",
          "Cache-Control": "no-cache",
        };
        // F5: only send Last-Event-ID on reconnect (not first connect)
        if (lastEventIDRef.current !== null) {
          headers["Last-Event-ID"] = lastEventIDRef.current;
        }

        const res = await fetch(url, {
          credentials: "include",
          headers,
          signal: abortCtrl.signal,
        });

        if (!res.ok || !res.body) {
          wsLog("user_stream.connect_failed", "", `http_status=${res.status}`);
          scheduleReconnect();
          return;
        }

        // Successful connection — reset backoff
        retryDelay = MIN_RECONNECT_MS;

        // FM9: on reconnect, invalidate all workspace caches
        if (lastEventIDRef.current !== null) {
          wsLog("user_stream.reconnected", "");
          queryClient.invalidateQueries({ queryKey: ["workspaces"] });
          queryClient.invalidateQueries({ queryKey: ["workspace-status"] });
        } else {
          wsLog("user_stream.connected", "");
        }

        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";

        while (true) {
          let timeoutId: ReturnType<typeof setTimeout> | undefined;
          const timeoutPromise = new Promise<'timeout'>(resolve => {
            timeoutId = setTimeout(() => resolve('timeout'), READ_TIMEOUT_MS);
          });
          const result = await Promise.race([reader.read(), timeoutPromise]);
          clearTimeout(timeoutId);

          if (result === 'timeout') {
            wsLog("user_stream.read_timeout", "", `no data for ${READ_TIMEOUT_MS}ms — forcing reconnect`);
            break;
          }

          const { done, value } = result;
          if (done) break;

          buf += decoder.decode(value, { stream: true });

          // Parse SSE events from buffer
          while (buf.includes("\n\n")) {
            const idx = buf.indexOf("\n\n");
            const block = buf.slice(0, idx);
            buf = buf.slice(idx + 2);

            // Skip heartbeat comments
            if (block.trim() === ":") continue;

            let dataLine = "";
            for (const line of block.split("\n")) {
              if (line.startsWith("data: ")) {
                dataLine = line.slice(6);
              }
            }
            if (!dataLine) continue;

            try {
              const evt = JSON.parse(dataLine) as {
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
            } catch {
              // Ignore malformed JSON
            }
          }
        }
      } catch (err: unknown) {
        if ((err as Error)?.name === "AbortError") return;
        wsLog("user_stream.error", "", String(err));
      }

      // Connection ended — reconnect
      if (!cancelled) {
        scheduleReconnect();
      }
    }

    connect();

    return () => {
      cancelled = true;
      abortCtrl.abort();
      if (retryTimer) clearTimeout(retryTimer);
    };
  }, [queryClient]);
}
