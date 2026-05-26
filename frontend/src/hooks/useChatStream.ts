import { useCallback, useEffect, useRef, useState } from "react";
import { messagesApi } from "../api/messages";
import { registerTabCloseAbort } from "../api/events";
import { extractStreamText, parseCompleteStream } from "../lib/stream";
import type { Message, MessagePart } from "../api/types";

export function useChatStream(workspaceId: string | undefined, sessionId: string | undefined) {
  const [streaming, setStreaming] = useState(false);
  const [streamedDisplayText, setStreamedDisplayText] = useState("");
  const [streamedThinkingText, setStreamedThinkingText] = useState("");
  const [streamedParts, setStreamedParts] = useState<MessagePart[]>([]);
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
      setStreamedDisplayText("");
      setStreamedThinkingText("");
      setStreamedParts([]);
      setError(null);
      abortRef.current = new AbortController();
      cleanupBeaconRef.current = registerTabCloseAbort(workspaceId, sessionId);

      try {
        const res = await messagesApi.send(workspaceId, sessionId, {
          parts: [{ type: "text", text }],
        });

        const reader = res.body?.getReader();
        if (!reader) return;

        const decoder = new TextDecoder();
        let accumulated = "";

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          const chunk = decoder.decode(value, { stream: true });
          accumulated += chunk;

          const { displayText, thinkingText } = extractStreamText(accumulated);
          setStreamedDisplayText(displayText);
          setStreamedThinkingText(thinkingText);
        }

        const parsed = parseCompleteStream(accumulated);
        const parts: MessagePart[] = Array.isArray(parsed)
          ? parsed
          : [{ type: "text", text: parsed }];

        setStreamedParts(parts);

        const msg: Message = {
          id: `msg-${Date.now()}`,
          role: "assistant",
          parts,
        };
        onComplete(msg);
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : "Failed to send message";
        setError(message);
      } finally {
        setStreaming(false);
        setStreamedDisplayText("");
        setStreamedThinkingText("");
        setStreamedParts([]);
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
    streamedDisplayText,
    streamedThinkingText,
    streamedParts,
    error,
    clearError,
  };
}
