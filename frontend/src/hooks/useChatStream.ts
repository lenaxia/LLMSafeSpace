import { useCallback, useEffect, useRef, useState } from "react";
import { messagesApi } from "../api/messages";
import { registerTabCloseAbort } from "../api/events";
import { ApiClientError } from "../api/client";
import type { Message } from "../api/types";

export function useChatStream(workspaceId: string | undefined, sessionId: string | undefined) {
  const [streaming, setStreaming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [atCapRetryAfter, setAtCapRetryAfter] = useState<number | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const cleanupBeaconRef = useRef<(() => void) | null>(null);
  // Resolves when notifySessionIdle is called with the matching sessionId
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
      setStreaming(true);
      setError(null);
      setAtCapRetryAfter(null);
      abortRef.current = new AbortController();
      cleanupBeaconRef.current = registerTabCloseAbort(workspaceId, sessionId);

      try {
        await messagesApi.sendAsync(workspaceId, sessionId, {
          parts: [{ type: "text", text }],
        });

        // Wait for the session.status=idle SSE event to signal the LLM has finished
        await new Promise<void>((resolve) => {
          idleResolverRef.current = (idleSessionId: string) => {
            if (idleSessionId === sessionId) {
              idleResolverRef.current = null;
              resolve();
            }
          };
        });

        const history = await messagesApi.getHistory(workspaceId, sessionId);
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
        setStreaming(false);
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

  return {
    send,
    abort,
    streaming,
    notifySessionIdle,
    error,
    clearError,
    atCapRetryAfter,
    clearAtCap,
  };
}
