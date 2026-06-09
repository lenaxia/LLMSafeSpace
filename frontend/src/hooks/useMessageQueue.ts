import { useState, useEffect } from "react";
import { messagesApi } from "../api/messages";
import { ApiClientError } from "../api/client";
import type { Message } from "../api/types";

export type QueuedMessage = {
  id: string;
  text: string;
  sentAt: number;
  status: "pending" | "error";
  error?: string;
};

function uid(): string {
  if (typeof crypto !== "undefined" && crypto.randomUUID) {
    return crypto.randomUUID();
  }
  return `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

export function useMessageQueue(
  workspaceId: string | undefined,
  sessionId: string | undefined,
) {
  const [queuedMessages, setQueuedMessages] = useState<QueuedMessage[]>([]);

  const enqueue = (text: string) => {
    if (!workspaceId || !sessionId) return;
    const id = "msg_" + uid();
    const msg: QueuedMessage = { id, text, sentAt: Date.now(), status: "pending" };
    setQueuedMessages((prev) => [...prev, msg]);

    messagesApi
      .sendAsync(workspaceId, sessionId, {
        parts: [{ type: "text", text }],
        messageID: id,
      })
      .catch((err: unknown) => {
        let error = err instanceof Error ? err.message : "Failed to send";
        if (err instanceof ApiClientError && err.status === 429) {
          const retryAfter = ((err.body as unknown) as Record<string, unknown>).retryAfter ?? 60;
          error = `Rate limited. Retry after ${retryAfter}s`;
        }
        setQueuedMessages((prev) =>
          prev.map((m) => (m.id === id ? { ...m, status: "error", error } : m)),
        );
      });
  };

  const remove = (id: string) => {
    setQueuedMessages((prev) => prev.filter((m) => m.id !== id));
  };

  const clear = () => setQueuedMessages([]);

  const reconcile = (history: Message[]) => {
    const historyIds = new Set(history.filter((m) => m.role === "user").map((m) => m.id));
    setQueuedMessages((prev) => prev.filter((m) => !historyIds.has(m.id)));
  };

  const retry = async (id: string) => {
    if (!workspaceId || !sessionId) return;
    const msg = queuedMessages.find((m) => m.id === id);
    if (!msg || msg.status !== "error") return;

    try {
      const history = await messagesApi.getHistory(workspaceId, sessionId);
      if (history.some((m) => m.id === id)) {
        remove(id);
        return;
      }
    } catch { /* history fetch failed — fall through to retry send */ }

    setQueuedMessages((prev) =>
      prev.map((m) =>
        m.id === id ? { ...m, status: "pending", error: undefined, sentAt: Date.now() } : m,
      ),
    );

    messagesApi
      .sendAsync(workspaceId, sessionId, {
        parts: [{ type: "text", text: msg.text }],
        messageID: id,
      })
      .catch((err: unknown) => {
        let error = err instanceof Error ? err.message : "Failed to send";
        if (err instanceof ApiClientError && err.status === 429) {
          const retryAfter = ((err.body as unknown) as Record<string, unknown>).retryAfter ?? 60;
          error = `Rate limited. Retry after ${retryAfter}s`;
        }
        setQueuedMessages((prev) =>
          prev.map((m) => (m.id === id ? { ...m, status: "error", error } : m)),
        );
      });
  };

  const dismiss = (id: string) => remove(id);

  const onPhaseChange = (phase: string) => {
    if (phase === "Creating" || phase === "Pending" || phase === "Suspending") {
      setQueuedMessages((prev) =>
        prev.map((m) =>
          m.status === "pending" ? { ...m, status: "error", error: "Workspace restarted" } : m,
        ),
      );
    }
  };

  useEffect(() => {
    const interval = setInterval(() => {
      const now = Date.now();
      setQueuedMessages((prev) =>
        prev.map((m) =>
          m.status === "pending" && now - m.sentAt > 90_000
            ? { ...m, status: "error", error: "Timed out" }
            : m,
        ),
      );
    }, 15_000);
    return () => clearInterval(interval);
  }, []);

  return { queuedMessages, enqueue, remove, retry, dismiss, clear, reconcile, onPhaseChange };
}
