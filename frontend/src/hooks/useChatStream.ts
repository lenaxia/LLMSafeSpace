import { useCallback, useEffect, useRef, useState } from "react";
import { messagesApi } from "../api/messages";
import { ApiClientError } from "../api/client";
import type { Message } from "../api/types";

// If session.status=idle SSE never arrives (e.g. connection drops), fall
// back to getHistory after this many ms.
const IDLE_WAIT_TIMEOUT_MS = 60_000;

export function useChatStream(workspaceId: string | undefined, sessionId: string | undefined, serverBusy = false) {
  const [localStreaming, setLocalStreaming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [atCapRetryAfter, setAtCapRetryAfter] = useState<number | null>(null);
  const [streamTimedOut, setStreamTimedOut] = useState(false);
  const abortRef = useRef<AbortController | null>(null);
  const idleResolverRef = useRef<((sid: string) => void) | null>(null);
  const currentSessionRef = useRef(sessionId);
  // Keep a live ref to serverBusy so the send closure can read the current
  // value at timeout-fire time without being in the useCallback dep array.
  const serverBusyRef = useRef(serverBusy);
  useEffect(() => { serverBusyRef.current = serverBusy; }, [serverBusy]);

  // Reset UI state when session changes — the in-flight send uses
  // capturedSessionId internally and handles its own scoping, so we don't
  // abort it. We just clear the visual indicators for the new session.
  const prevSessionRef = useRef(sessionId);
  useEffect(() => {
    if (prevSessionRef.current !== sessionId) {
      prevSessionRef.current = sessionId;
      currentSessionRef.current = sessionId;
      setLocalStreaming(false);
      setError(null);
      setAtCapRetryAfter(null);
      setStreamTimedOut(false);
    }
  }, [sessionId]);

  const notifySessionIdle = useCallback((idleSessionId: string) => {
    idleResolverRef.current?.(idleSessionId);
  }, []);

  const send = useCallback(
    async (text: string, onComplete: (msg: Message) => void, model?: { providerID: string; modelID: string }) => {
      if (!workspaceId || !sessionId) return;
      const capturedSessionId = sessionId;
      setLocalStreaming(true);
      setError(null);
      setAtCapRetryAfter(null);
      abortRef.current = new AbortController();

      try {
        let idleAlreadyFired = false;
        idleResolverRef.current = (idleSessionId: string) => {
          if (idleSessionId === capturedSessionId) {
            idleAlreadyFired = true;
          }
        };

        await messagesApi.sendAsync(workspaceId, sessionId, {
          parts: [{ type: "text", text }],
          ...(model && { model }),
        });

        // Wait for session.status=idle SSE OR a timeout fallback.
        // The SSE path is preferred (real-time), but if the connection drops
        // before idle fires we still need to fetch the response.
        // Note: only flag streamTimedOut when the server is NOT still busy —
        // if serverBusyRef.current is true the agent is legitimately still
        // running (slow response), not an interrupted connection.
        let resolvedViaSSE = false;
        await new Promise<void>((resolve) => {
          if (idleAlreadyFired) {
            resolvedViaSSE = true;
            idleResolverRef.current = null;
            resolve();
            return;
          }
          const timeoutId = setTimeout(() => {
            idleResolverRef.current = null;
            resolve();
            // resolvedViaSSE stays false — timeout path
          }, IDLE_WAIT_TIMEOUT_MS);

          idleResolverRef.current = (idleSessionId: string) => {
            if (idleSessionId === capturedSessionId) {
              resolvedViaSSE = true;
              clearTimeout(timeoutId);
              idleResolverRef.current = null;
              resolve();
            }
          };
        });

        // Only show the interrupted banner when:
        // 1. The idle SSE never arrived (resolvedViaSSE=false), AND
        // 2. The server is not still actively busy (would indicate slow response)
        if (!resolvedViaSSE && !serverBusyRef.current && currentSessionRef.current === capturedSessionId) {
          setStreamTimedOut(true);
        }

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
        if (currentSessionRef.current === capturedSessionId) {
          setLocalStreaming(false);
        }
        idleResolverRef.current = null;
        abortRef.current = null;
      }
    },
    [workspaceId, sessionId],
  );

  const abort = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  const clearError = useCallback(() => setError(null), []);
  const clearAtCap = useCallback(() => setAtCapRetryAfter(null), []);
  const clearStreamTimedOut = useCallback(() => setStreamTimedOut(false), []);

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
    streamTimedOut,
    clearStreamTimedOut,
  };
}
