import { useCallback, useRef, useState } from "react";
import { messagesApi } from "../api/messages";
import type { Message } from "../api/types";

export function useChatStream(sandboxId: string | undefined, sessionId: string | undefined) {
  const [streaming, setStreaming] = useState(false);
  const [streamedText, setStreamedText] = useState("");
  const abortRef = useRef<AbortController | null>(null);

  const send = useCallback(
    async (text: string, onComplete: (msg: Message) => void) => {
      if (!sandboxId || !sessionId) return;
      setStreaming(true);
      setStreamedText("");
      abortRef.current = new AbortController();

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
      }
    },
    [sandboxId, sessionId],
  );

  const abort = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  return { send, abort, streaming, streamedText };
}
