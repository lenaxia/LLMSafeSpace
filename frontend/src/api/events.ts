import { getEnv } from "../env";

export interface SSEEvent {
  type: string;
  data: unknown;
}

const CHANNEL_NAME = "lsp-sse";

/**
 * BroadcastChannel-multiplexed SSE client.
 * Only the leader tab opens the EventSource; followers receive via BroadcastChannel.
 */
export function createEventStream(
  sandboxId: string,
  onEvent: (event: SSEEvent) => void,
): () => void {
  const channel = new BroadcastChannel(CHANNEL_NAME);
  let eventSource: EventSource | null = null;
  let isLeader = false;

  // Try to become leader
  const leaderKey = `lsp-sse-leader-${sandboxId}`;
  if (!sessionStorage.getItem(leaderKey)) {
    sessionStorage.setItem(leaderKey, "1");
    isLeader = true;
  }

  if (isLeader) {
    const { apiBaseUrl } = getEnv();
    eventSource = new EventSource(`${apiBaseUrl}/sandboxes/${sandboxId}/events`, {
      withCredentials: true,
    });
    eventSource.onmessage = (e) => {
      try {
        const parsed: SSEEvent = { type: e.type, data: JSON.parse(e.data) };
        onEvent(parsed);
        channel.postMessage(parsed);
      } catch { /* ignore malformed */ }
    };
    eventSource.onerror = () => {
      // EventSource auto-reconnects
    };
  }

  // All tabs listen to broadcast
  channel.onmessage = (e) => {
    if (!isLeader) onEvent(e.data as SSEEvent);
  };

  return () => {
    eventSource?.close();
    channel.close();
    if (isLeader) sessionStorage.removeItem(leaderKey);
  };
}
