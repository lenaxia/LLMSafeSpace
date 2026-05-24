import { useEffect, useRef } from "react";
import { getEnv } from "../env";

export function useEventStream(
  sandboxId: string | undefined,
  onEvent: (data: unknown) => void,
) {
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;

  useEffect(() => {
    if (!sandboxId) return;

    const { apiBaseUrl } = getEnv();
    const es = new EventSource(`${apiBaseUrl}/sandboxes/${sandboxId}/events`, {
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
  }, [sandboxId]);
}
