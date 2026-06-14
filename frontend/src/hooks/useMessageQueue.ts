import { useState, useCallback, useRef, useEffect } from "react";
import { messagesApi } from "../api/messages";

export type QueuedMessage = {
  id: string;
  text: string;
  status: "pending" | "error";
  error?: string;
  sessionId: string;
};

const RESTART_PHASES = ["Creating", "Pending", "Suspending"];

export function useMessageQueue(
  workspaceId: string | undefined,
  sessionId: string | undefined,
) {
  const [queuedMessages, setQueuedMessages] = useState<QueuedMessage[]>([]);
  const refreshInFlightRef = useRef(false);

  const refreshQueue = useCallback(async () => {
    if (!workspaceId || !sessionId) return;
    if (refreshInFlightRef.current) return;
    refreshInFlightRef.current = true;
    try {
      const res = await messagesApi.getQueue(workspaceId, sessionId);
      setQueuedMessages((prev) => {
        const redisIds = new Set(res.messages.map((m) => m.id));
        const kept = prev.filter((m) => m.status === "error" || redisIds.has(m.id));
        const existingIds = new Set(kept.map((m) => m.id));
        const added: QueuedMessage[] = res.messages
          .filter((m) => !existingIds.has(m.id))
          .map((m) => ({ id: m.id, text: m.text, status: "pending", sessionId: m.session_id }));
        return [...kept, ...added];
      });
    } catch {
    } finally {
      refreshInFlightRef.current = false;
    }
  }, [workspaceId, sessionId]);

  useEffect(() => {
    refreshQueue();
  }, [refreshQueue]);

  const enqueue = useCallback(async (text: string) => {
    if (!workspaceId || !sessionId) return;
    try {
      const res = await messagesApi.queueMessage(workspaceId, sessionId, text);
      setQueuedMessages((prev) => [
        ...prev,
        { id: res.messageID, text, status: "pending", sessionId },
      ]);
    } catch {
      setQueuedMessages((prev) => [
        ...prev,
        { id: "err_" + Date.now(), text, status: "error", sessionId, error: "Failed to queue" },
      ]);
    }
  }, [workspaceId, sessionId]);

  const markError = useCallback((id: string, error: string) => {
    setQueuedMessages((prev) =>
      prev.map((m) => (m.id === id ? { ...m, status: "error", error } : m)),
    );
  }, []);

  const retry = useCallback(async (id: string) => {
    if (!workspaceId || !sessionId) return;
    const msg = queuedMessages.find((m) => m.id === id);
    setQueuedMessages((prev) => prev.filter((m) => m.id !== id));
    if (msg) await enqueue(msg.text);
  }, [workspaceId, sessionId, queuedMessages, enqueue]);

  const dismiss = useCallback(async (id: string) => {
    if (!workspaceId || !sessionId) return;
    setQueuedMessages((prev) => prev.filter((m) => m.id !== id));
    try {
      await messagesApi.deleteQueueMessage(workspaceId, sessionId, id);
    } catch {
    }
    void refreshQueue();
  }, [workspaceId, sessionId, refreshQueue]);

  const clear = useCallback(() => setQueuedMessages([]), []);

  const onPhaseChange = useCallback((phase: string) => {
    if (RESTART_PHASES.includes(phase)) {
      setQueuedMessages([]);
    }
  }, []);

  const sessionQueue = sessionId
    ? queuedMessages.filter((m) => m.sessionId === sessionId)
    : [];

  return {
    queuedMessages: sessionQueue,
    enqueue,
    refreshQueue,
    markError,
    retry,
    dismiss,
    clear,
    onPhaseChange,
  };
}
