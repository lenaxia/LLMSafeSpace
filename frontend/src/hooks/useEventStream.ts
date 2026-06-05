import { useEffect, useRef } from "react";
import { getEnv } from "../env";
import { createSSEConnection } from "../lib/sseConnection";

const MIN_RECONNECT_MS = 2_000;
const MAX_RECONNECT_MS = 30_000;
const READ_TIMEOUT_MS = 35_000; // Must exceed backend heartbeat interval (25s)

export function useEventStream(
  workspaceId: string | undefined,
  onEvent: (data: unknown) => void,
  options?: { onReconnect?: () => void },
) {
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;
  const onReconnectRef = useRef(options?.onReconnect);
  onReconnectRef.current = options?.onReconnect;

  useEffect(() => {
    if (!workspaceId) return;

    let hasConnectedOnce = false;
    const { apiBaseUrl } = getEnv();
    const url = `${apiBaseUrl}/workspaces/${workspaceId}/session-events`;

    const conn = createSSEConnection({
      url,
      onEvent: (data) => onEventRef.current(data),
      onConnect: () => {
        if (hasConnectedOnce) {
          onReconnectRef.current?.();
        }
        hasConnectedOnce = true;
      },
      logPrefix: "sse",
      logId: workspaceId,
      readTimeoutMs: READ_TIMEOUT_MS,
      minReconnectMs: MIN_RECONNECT_MS,
      maxReconnectMs: MAX_RECONNECT_MS,
    });

    return () => conn.destroy();
  }, [workspaceId]);
}
