import { getEnv } from "../env";
import { wsLog } from "../lib/wsLog";

export interface SSEEvent {
  type: string;
  data: unknown;
}

const CHANNEL_PREFIX = "lsp-sse-";
const HEARTBEAT_MS = 2000;
const LEADER_TIMEOUT_MS = 5000;
const EVENT_SOURCE_ERROR_TIMEOUT_MS = 15_000;

/**
 * BroadcastChannel-multiplexed SSE client with leader election.
 *
 * One tab becomes leader and opens the EventSource. Other tabs receive
 * events via BroadcastChannel. If the leader tab closes, another tab
 * takes over after LEADER_TIMEOUT_MS.
 */
export function createEventStream(
  workspaceId: string,
  onEvent: (event: SSEEvent) => void,
): () => void {
  const channelName = `${CHANNEL_PREFIX}${workspaceId}`;
  const channel = new BroadcastChannel(channelName);
  const tabId = `${Date.now()}-${Math.random().toString(36).slice(2)}`;

  let eventSource: EventSource | null = null;
  let isLeader = false;
  let heartbeatInterval: ReturnType<typeof setInterval> | null = null;
  let lastLeaderHeartbeat = 0;
  let electionTimeout: ReturnType<typeof setTimeout> | null = null;
  let lastEventSourceError: number | null = null;
  let resignationCount = 0;

  function becomeLeader() {
    if (isLeader) return;
    isLeader = true;
    lastEventSourceError = null;

    const { apiBaseUrl } = getEnv();
    eventSource = new EventSource(`${apiBaseUrl}/workspaces/${workspaceId}/events`, {
      withCredentials: true,
    });

    eventSource.onmessage = (e) => {
      // Clear error state — successfully received data
      lastEventSourceError = null;
      resignationCount = 0;
      try {
        const parsed: SSEEvent = { type: e.type, data: JSON.parse(e.data) };
        onEvent(parsed);
        channel.postMessage({ type: "event", payload: parsed });
      } catch { /* ignore malformed */ }
    };

    eventSource.onerror = () => {
      // EventSource auto-reconnects, but track failures so leader resigns
      // if the connection cannot recover (e.g. browser gives up on mobile).
      if (lastEventSourceError === null) {
        lastEventSourceError = Date.now();
        wsLog("sse.leader_event_source_error", workspaceId, "tracking for possible resignation");
      }
    };

    eventSource.onopen = () => {
      // Successfully (re)connected — clear error state
      lastEventSourceError = null;
    };

    // Broadcast heartbeat so followers know leader is alive
    heartbeatInterval = setInterval(() => {
      channel.postMessage({ type: "heartbeat", tabId });
    }, HEARTBEAT_MS);
    channel.postMessage({ type: "heartbeat", tabId });
  }

  function handleChannelMessage(e: MessageEvent) {
    const msg = e.data;
    if (msg.type === "event" && !isLeader) {
      onEvent(msg.payload as SSEEvent);
    } else if (msg.type === "heartbeat" && msg.tabId !== tabId) {
      lastLeaderHeartbeat = Date.now();
      // Another tab is leader; cancel any pending election
      if (electionTimeout) {
        clearTimeout(electionTimeout);
        electionTimeout = null;
      }
    } else if (msg.type === "leader-resign") {
      // Leader resigned; start election
      startElection();
    }
  }

  function startElection() {
    if (isLeader || electionTimeout) return;
    // Backoff on repeated resignations to prevent tight resign/re-elect loops
    // on single-tab with persistent network failure
    const backoff = Math.min(resignationCount * 2_000, 30_000);
    const delay = backoff + Math.random() * 500;
    electionTimeout = setTimeout(() => {
      electionTimeout = null;
      becomeLeader();
    }, delay);
  }

  function checkLeaderAlive() {
    if (isLeader) {
      // If leader's EventSource has been errored too long, resign so another
      // tab can take over (the browser may have given up reconnecting).
      if (lastEventSourceError !== null &&
          Date.now() - lastEventSourceError > EVENT_SOURCE_ERROR_TIMEOUT_MS) {
        wsLog("sse.leader_resigning", workspaceId,
          `EventSource errored for ${EVENT_SOURCE_ERROR_TIMEOUT_MS}ms`);
        channel.postMessage({ type: "leader-resign" });
        eventSource?.close();
        eventSource = null;
        if (heartbeatInterval) clearInterval(heartbeatInterval);
        heartbeatInterval = null;
        isLeader = false;
        lastEventSourceError = null;
        resignationCount++;
        // This tab may become leader again if no other tab takes over
        startElection();
      }
      return;
    }
    if (Date.now() - lastLeaderHeartbeat > LEADER_TIMEOUT_MS) {
      startElection();
    }
  }

  channel.onmessage = handleChannelMessage;

  // Try to become leader immediately (first tab wins via heartbeat race)
  // Wait briefly to see if another leader announces
  lastLeaderHeartbeat = Date.now();
  electionTimeout = setTimeout(() => {
    electionTimeout = null;
    becomeLeader();
  }, 300 + Math.random() * 200);

  // Periodically check if leader is still alive
  const aliveCheck = setInterval(checkLeaderAlive, LEADER_TIMEOUT_MS);

  // Cleanup
  return () => {
    if (isLeader) {
      channel.postMessage({ type: "leader-resign" });
    }
    eventSource?.close();
    if (heartbeatInterval) clearInterval(heartbeatInterval);
    if (electionTimeout) clearTimeout(electionTimeout);
    clearInterval(aliveCheck);
    channel.close();
  };
}
