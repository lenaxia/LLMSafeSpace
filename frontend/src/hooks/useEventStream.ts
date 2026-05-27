import { useEffect, useRef } from "react";
import { getEnv } from "../env";

const MIN_RECONNECT_MS = 2_000;
const MAX_RECONNECT_MS = 30_000;

export function useEventStream(
  workspaceId: string | undefined,
  onEvent: (data: unknown) => void,
) {
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;

  useEffect(() => {
    if (!workspaceId) return;

    let cancelled = false;
    let retryDelay = MIN_RECONNECT_MS;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;
    let abortCtrl = new AbortController();

    async function connect() {
      if (cancelled) return;
      const { apiBaseUrl } = getEnv();
      const url = `${apiBaseUrl}/workspaces/${workspaceId}/events`;

      try {
        const res = await fetch(url, {
          credentials: "include",
          headers: { Accept: "text/event-stream", "Cache-Control": "no-cache" },
          signal: abortCtrl.signal,
        });

        if (!res.ok || !res.body) {
          // Back off on errors (including 429)
          scheduleReconnect();
          return;
        }

        // Successful connection — reset backoff
        retryDelay = MIN_RECONNECT_MS;

        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";

        while (true) {
          const { done, value } = await reader.read();
          if (done || cancelled) break;
          buf += decoder.decode(value, { stream: true });

          // Split on double-newline SSE event boundaries
          const parts = buf.split("\n\n");
          buf = parts.pop() ?? "";

          for (const part of parts) {
            for (const line of part.split("\n")) {
              if (line.startsWith("data: ")) {
                try {
                  onEventRef.current(JSON.parse(line.slice(6)));
                } catch {
                  // ignore malformed
                }
              }
            }
          }
        }
      } catch (err: unknown) {
        if (cancelled) return;
        // AbortError = intentional close, not an error
        if (err instanceof DOMException && err.name === "AbortError") return;
      }

      scheduleReconnect();
    }

    function scheduleReconnect() {
      if (cancelled) return;
      retryTimer = setTimeout(() => {
        if (cancelled) return;
        abortCtrl = new AbortController();
        connect();
        retryDelay = Math.min(retryDelay * 2, MAX_RECONNECT_MS);
      }, retryDelay);
    }

    connect();

    return () => {
      cancelled = true;
      if (retryTimer !== null) clearTimeout(retryTimer);
      abortCtrl.abort();
    };
  }, [workspaceId]);
}
