import { useCallback, useEffect, useRef, useState } from "react";
import { messagesApi } from "../api/messages";
import { registerTabCloseAbort } from "../api/events";
import type { Message } from "../api/types";

export function useChatStream(sandboxId: string | undefined, sessionId: string | undefined) {
  const [streaming, setStreaming] = useState(false);
  const [streamedText, setStreamedText] = useState("");
  const abortRef = useRef<AbortController | null>(null);
  const cleanupBeaconRef = useRef<(() => void) | null>(null);

  // Clean up sendBeacon listener when streaming ends or component unmounts
  useEffect(() => {
    return () => {
      cleanupBeaconRef.current?.();
    };
  }, []);

  const send = useCallback(
    async (text: string, onComplete: (msg: Message) => void) => {
      if (!sandboxId || !sessionId) return;
      setStreaming(true);
      setStreamedText("");
      abortRef.current = new AbortController();

      // Register tab-close abort for the duration of streaming
      cleanupBeaconRef.current = registerTabCloseAbort(sandboxId, sessionId);

      try {
        const res = await messagesApi.send(sandboxId, sessionId, {
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
          setStreamedText(accumulated);
        }

        const msg: Message = {
          id: `msg-${Date.now()}`,
          role: "assistant",
          parts: [{ type: "text", text: accumulated }],
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
    [sandboxId, sessionId],
  );

  const abort = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  return { send, abort, streaming, streamedText };
}
