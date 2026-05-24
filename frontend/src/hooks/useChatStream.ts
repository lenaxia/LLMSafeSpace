import { useCallback, useEffect, useRef, useState } from "react";
import { messagesApi } from "../api/messages";
import { registerTabCloseAbort } from "../api/events";
import { extractStreamText, parseCompleteStream } from "../lib/stream";
import type { Message } from "../api/types";

export function useChatStream(workspaceId: string | undefined, sessionId: string | undefined) {
  const [streaming, setStreaming] = useState(false);
  const [streamedText, setStreamedText] = useState("");
  const abortRef = useRef<AbortController | null>(null);
  const cleanupBeaconRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    return () => { cleanupBeaconRef.current?.(); };
  }, []);

  const send = useCallback(
    async (text: string, onComplete: (msg: Message) => void) => {
      if (!workspaceId || !sessionId) return;
      setStreaming(true);
      setStreamedText("");
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

          // Extract displayable text from the accumulated buffer
          const { displayText } = extractStreamText(accumulated);
          if (displayText) {
            setStreamedText(displayText);
          }
        }

        // Parse the complete response to get final text
        const finalText = parseCompleteStream(accumulated);

        const msg: Message = {
          id: `msg-${Date.now()}`,
          role: "assistant",
          parts: [{ type: "text", text: finalText }],
        };
        onComplete(msg);
      } finally {
        setStreaming(false);
        setStreamedText("");
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

  return { send, abort, streaming, streamedText };
}
