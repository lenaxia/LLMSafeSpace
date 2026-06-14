import { useReducer, useCallback, useRef, useEffect } from "react";
import { messagesApi } from "../api/messages";
import type { Message } from "../api/types";

export type QueuedMessage = {
  id: string;
  text: string;
  status: "pending" | "error";
  error?: string;
  sessionId: string;
};

type Action =
  | { type: "add"; msg: QueuedMessage }
  | { type: "mark_sent"; id: string }
  | { type: "mark_error"; id: string; error: string }
  | { type: "remove"; id: string }
  | { type: "clear" }
  | { type: "hydrate"; sessionId: string; messages: QueuedMessage[] }
  | { type: "reconcile"; historyIds: Set<string> };

function reduce(state: QueuedMessage[], action: Action): QueuedMessage[] {
  switch (action.type) {
    case "add":
      return [...state, action.msg];
    case "mark_sent":
      return state.filter((m) => m.id !== action.id);
    case "mark_error":
      return state.map((m) =>
        m.id === action.id ? { ...m, status: "error" as const, error: action.error } : m,
      );
    case "remove":
      return state.filter((m) => m.id !== action.id);
    case "clear":
      return [];
    case "hydrate":
      return state.some((m) => m.sessionId === action.sessionId)
        ? state
        : [...state, ...action.messages];
    case "reconcile":
      return state.filter((m) => !action.historyIds.has(m.id));
    default:
      return state;
  }
}

const RESTART_PHASES = ["Creating", "Pending", "Suspending"];

export function useMessageQueue(
  workspaceId: string | undefined,
  sessionId: string | undefined,
) {
  const [queuedMessages, dispatch] = useReducer(reduce, []);
  const hydratedRef = useRef<Set<string>>(new Set());

  useEffect(() => {
    if (!workspaceId || !sessionId) return;
    if (hydratedRef.current.has(sessionId)) return;
    hydratedRef.current.add(sessionId);

    messagesApi
      .getQueue(workspaceId, sessionId)
      .then((res) => {
        const msgs: QueuedMessage[] = (res.messages ?? []).map((m) => ({
          id: m.id,
          text: m.text,
          status: "pending" as const,
          sessionId: m.session_id,
        }));
        if (msgs.length > 0) {
          dispatch({ type: "hydrate", sessionId, messages: msgs });
        }
      })
      .catch(() => {});
  }, [workspaceId, sessionId]);

  const enqueue = useCallback(async (text: string) => {
    if (!workspaceId || !sessionId) return;
    try {
      const res = await messagesApi.queueMessage(workspaceId, sessionId, text);
      dispatch({
        type: "add",
        msg: { id: res.messageID, text, status: "pending", sessionId },
      });
    } catch {
      dispatch({
        type: "add",
        msg: { id: "err_" + Date.now(), text, status: "error", sessionId, error: "Failed to queue" },
      });
    }
  }, [workspaceId, sessionId]);

  const markSent = useCallback((id: string) => {
    dispatch({ type: "mark_sent", id });
  }, []);

  const markError = useCallback((id: string, error: string) => {
    dispatch({ type: "mark_error", id, error });
  }, []);

  const retry = useCallback(async (id: string) => {
    if (!workspaceId || !sessionId) return;
    const msg = queuedMessages.find((m) => m.id === id);
    if (!msg) return;
    dispatch({ type: "remove", id });
    await enqueue(msg.text);
  }, [workspaceId, sessionId, queuedMessages, enqueue]);

  const dismiss = useCallback((id: string) => {
    dispatch({ type: "remove", id });
  }, []);

  const clear = useCallback(() => dispatch({ type: "clear" }), []);

  const reconcile = useCallback((history: Message[]) => {
    const historyIds = new Set(history.filter((m) => m.role === "user").map((m) => m.id));
    dispatch({ type: "reconcile", historyIds });
  }, []);

  const onPhaseChange = useCallback((phase: string) => {
    if (RESTART_PHASES.includes(phase)) {
      dispatch({ type: "clear" });
    }
  }, []);

  const sessionQueue = sessionId
    ? queuedMessages.filter((m) => m.sessionId === sessionId)
    : [];

  return {
    queuedMessages: sessionQueue,
    enqueue,
    markSent,
    markError,
    retry,
    dismiss,
    clear,
    reconcile,
    onPhaseChange,
  };
}
