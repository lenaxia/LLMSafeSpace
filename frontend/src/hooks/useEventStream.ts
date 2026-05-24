import { useEffect, useRef } from "react";
import { getEnv } from "../env";

export function useEventStream(
  workspaceId: string | undefined,
  onEvent: (data: unknown) => void,
) {
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;

  useEffect(() => {
    if (!workspaceId) return;

    const { apiBaseUrl } = getEnv();
    const es = new EventSource(`${apiBaseUrl}/workspaces/${workspaceId}/events`, {
      withCredentials: true,
    });

    es.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data);
        onEventRef.current(data);
      } catch {
        // Ignore malformed messages
      }
    };

    return () => {
      es.close();
    };
  }, [workspaceId]);
}
