import { useCallback, useEffect, useRef, useState } from "react";
import { messagesApi } from "../api/messages";
import { registerTabCloseAbort } from "../api/events";
import type { Message, MessagePart } from "../api/types";

export function useChatStream(workspaceId: string | undefined, sessionId: string | undefined) {
  const [streaming, setStreaming] = useState(false);
  const [streamedThinkingText, setStreamedThinkingText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const cleanupBeaconRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    return () => { cleanupBeaconRef.current?.(); };
  }, []);

  const send = useCallback(
    async (text: string, onComplete: (msg: Message) => void) => {
      if (!workspaceId || !sessionId) return;
      setStreaming(true);
      setStreamedThinkingText("");
      setError(null);
      abortRef.current = new AbortController();
      cleanupBeaconRef.current = registerTabCloseAbort(workspaceId, sessionId);

      try {
        await messagesApi.sendAsync(workspaceId, sessionId, {
          parts: [{ type: "text", text }],
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
        const message = err instanceof Error ? err.message : "Failed to send message";
        setError(message);
      } finally {
        setStreaming(false);
        setStreamedThinkingText("");
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

  return {
    send,
    abort,
    streaming,
    streamedDisplayText: "",
    streamedThinkingText,
    streamedParts: [] as MessagePart[],
    error,
    clearError,
  };
}
