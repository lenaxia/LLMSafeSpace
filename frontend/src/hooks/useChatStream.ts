import { useCallback, useEffect, useRef, useState } from "react";
import { messagesApi } from "../api/messages";
import { registerTabCloseAbort } from "../api/events";
import { ApiClientError } from "../api/client";
import type { Message } from "../api/types";

// If session.status=idle SSE never arrives (e.g. connection drops), fall
// back to getHistory after this many ms.
const IDLE_WAIT_TIMEOUT_MS = 60_000;

export function useChatStream(workspaceId: string | undefined, sessionId: string | undefined, serverBusy = false) {
  const [localStreaming, setLocalStreaming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [atCapRetryAfter, setAtCapRetryAfter] = useState<number | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const cleanupBeaconRef = useRef<(() => void) | null>(null);
  const idleResolverRef = useRef<((sid: string) => void) | null>(null);

  useEffect(() => {
    return () => { cleanupBeaconRef.current?.(); };
  }, []);

  const notifySessionIdle = useCallback((idleSessionId: string) => {
    idleResolverRef.current?.(idleSessionId);
  }, []);

  const send = useCallback(
    async (text: string, onComplete: (msg: Message) => void) => {
      if (!workspaceId || !sessionId) return;
      setLocalStreaming(true);
      setError(null);
      setAtCapRetryAfter(null);
      abortRef.current = new AbortController();
      cleanupBeaconRef.current = registerTabCloseAbort(workspaceId, sessionId);

      try {
        await messagesApi.sendAsync(workspaceId, sessionId, {
          parts: [{ type: "text", text }],
        });

        // Wait for session.status=idle SSE OR a timeout fallback.
        // The SSE path is preferred (real-time), but if the connection drops
        // before idle fires we still need to fetch the response.
        const capturedSessionId = sessionId;
        await new Promise<void>((resolve) => {
          let timeoutId: ReturnType<typeof setTimeout>;

          idleResolverRef.current = (idleSessionId: string) => {
            if (idleSessionId === capturedSessionId) {
              clearTimeout(timeoutId);
              idleResolverRef.current = null;
              resolve();
            }
          };

          timeoutId = setTimeout(() => {
            idleResolverRef.current = null;
            resolve();
          }, IDLE_WAIT_TIMEOUT_MS);
        });

        const history = await messagesApi.getHistory(workspaceId, capturedSessionId);
        const lastAssistant = [...history].reverse().find((m) => m.role === "assistant");

        const msg: Message = lastAssistant ?? {
          id: `msg-${Date.now()}`,
          role: "assistant",
          parts: [],
        };
        onComplete(msg);
      } catch (err: unknown) {
        if (err instanceof ApiClientError && err.status === 429) {
          const retryAfter = Number(((err.body as unknown) as Record<string, unknown>).retryAfter ?? 60);
          setAtCapRetryAfter(isNaN(retryAfter) ? 60 : retryAfter);
        } else {
          const message = err instanceof Error ? err.message : "Failed to send message";
          setError(message);
        }
      } finally {
        setLocalStreaming(false);
        idleResolverRef.current = null;
        abortRef.current = null;
        cleanupBeaconRef.current?.();
        cleanupBeaconRef.current = null;
      }
    },
    [workspaceId, sessionId],
  );

  const abort = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  const clearError = useCallback(() => setError(null), []);
  const clearAtCap = useCallback(() => setAtCapRetryAfter(null), []);

  // effectiveStreaming: local send takes priority; server state supplements
  const effectiveStreaming = localStreaming || serverBusy;

  return {
    send,
    abort,
    streaming: effectiveStreaming,
    localStreaming,
    notifySessionIdle,
    error,
    clearError,
    atCapRetryAfter,
    clearAtCap,
  };
}
