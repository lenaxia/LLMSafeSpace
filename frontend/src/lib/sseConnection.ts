import { wsLog } from "./wsLog";

/**
 * Shared SSE fetch connection with:
 * - Read timeout (detects zombie TCP connections on mobile network drop)
 * - Exponential backoff with jitter on reconnect
 * - AbortController replacement on reconnect (releases hanging reader.read())
 * - reader.cancel() on timeout for immediate stream lock release
 *
 * READ_TIMEOUT_MS must exceed the backend heartbeat interval (25s).
 * If the backend changes its heartbeat interval, update DEFAULT_READ_TIMEOUT_MS.
 */

const DEFAULT_READ_TIMEOUT_MS = 35_000; // Must be > backend heartbeat (25s)
const DEFAULT_MIN_RECONNECT_MS = 1_000;
const DEFAULT_MAX_RECONNECT_MS = 30_000;

export interface SSEConnectionConfig {
  url: string;
  headers?: Record<string, string>;
  onEvent: (data: unknown) => void;
  onConnect?: () => void;
  onDisconnect?: () => void;
  /** Label for wsLog (e.g. "sse" or "user_stream") */
  logPrefix?: string;
  /** Workspace ID for logging */
  logId?: string;
  readTimeoutMs?: number;
  minReconnectMs?: number;
  maxReconnectMs?: number;
}

export interface SSEConnection {
  destroy: () => void;
}

export function createSSEConnection(config: SSEConnectionConfig): SSEConnection {
  const {
    url,
    headers: extraHeaders,
    onEvent,
    onConnect,
    onDisconnect,
    logPrefix = "sse",
    logId = "",
    readTimeoutMs = DEFAULT_READ_TIMEOUT_MS,
    minReconnectMs = DEFAULT_MIN_RECONNECT_MS,
    maxReconnectMs = DEFAULT_MAX_RECONNECT_MS,
  } = config;

  let cancelled = false;
  let retryDelay = minReconnectMs;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;
  let abortCtrl = new AbortController();

  function jitteredDelay(base: number): number {
    // Jitter: [0.5, 1.5] × base — prevents thundering herd
    return base * (0.5 + Math.random());
  }

  function scheduleReconnect() {
    if (cancelled) return;
    const delay = jitteredDelay(retryDelay);
    retryTimer = setTimeout(() => {
      if (cancelled) return;
      abortCtrl.abort();
      abortCtrl = new AbortController();
      retryDelay = Math.min(retryDelay * 2, maxReconnectMs);
      connect();
    }, delay);
  }

  async function connect() {
    if (cancelled) return;

    wsLog(`${logPrefix}.connecting`, logId, `url=${url}`);

    try {
      const res = await fetch(url, {
        credentials: "include",
        headers: {
          Accept: "text/event-stream",
          "Cache-Control": "no-cache",
          ...extraHeaders,
        },
        signal: abortCtrl.signal,
      });

      if (!res.ok || !res.body) {
        wsLog(`${logPrefix}.connect_failed`, logId, `http_status=${res.status}`);
        scheduleReconnect();
        return;
      }

      // Successful connection — reset backoff
      retryDelay = minReconnectMs;
      wsLog(`${logPrefix}.connected`, logId);
      onConnect?.();

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";

      try {
        while (true) {
          let timeoutId: ReturnType<typeof setTimeout> | undefined;
          const timeoutPromise = new Promise<"timeout">((resolve) => {
            timeoutId = setTimeout(() => resolve("timeout"), readTimeoutMs);
          });
          const result = await Promise.race([reader.read(), timeoutPromise]);
          clearTimeout(timeoutId);

          if (result === "timeout") {
            wsLog(`${logPrefix}.read_timeout`, logId,
              `no data for ${readTimeoutMs}ms — forcing reconnect`);
            // Cancel reader immediately to release the stream lock and TCP socket
            await reader.cancel();
            break;
          }

          const { done, value } = result;
          if (done || cancelled) break;
          buf += decoder.decode(value, { stream: true });

          // Parse SSE: split on double-newline boundaries
          while (buf.includes("\n\n")) {
            const idx = buf.indexOf("\n\n");
            const block = buf.slice(0, idx);
            buf = buf.slice(idx + 2);

            // Skip heartbeat comments (bare ":")
            if (block.trim() === ":") continue;

            for (const line of block.split("\n")) {
              if (line.startsWith("data: ")) {
                try {
                  onEvent(JSON.parse(line.slice(6)));
                } catch {
                  // Ignore malformed JSON
                }
              }
            }
          }
        }
      } finally {
        // Ensure reader is released even on unexpected errors
        reader.cancel().catch(() => {});
      }
    } catch (err: unknown) {
      if (cancelled) return;
      if ((err as Error)?.name === "AbortError") return;
      wsLog(`${logPrefix}.error`, logId, `err=${String(err)}`);
    }

    if (!cancelled) {
      wsLog(`${logPrefix}.disconnected`, logId);
      onDisconnect?.();
      scheduleReconnect();
    }
  }

  connect();

  return {
    destroy() {
      cancelled = true;
      if (retryTimer !== null) clearTimeout(retryTimer);
      abortCtrl.abort();
      wsLog(`${logPrefix}.teardown`, logId);
    },
  };
}
