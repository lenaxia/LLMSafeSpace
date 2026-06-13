import { useReducer, useCallback, useRef, useEffect } from "react";
import { messagesApi } from "../api/messages";
import { ApiClientError } from "../api/client";
import type { Message } from "../api/types";

export type QueuedMessage = {
  id: string;
  text: string;
  status: "pending" | "sending" | "error";
  error?: string;
  sessionId: string;
  /** @internal used for sending timeout tracking */
  _sentAt?: number;
  /** @internal retry counter for 409 handling */
  _retryCount?: number;
};

const MAX_RETRIES = 5;

type Action =
  | { type: "enqueue"; msg: QueuedMessage }
  | { type: "mark_sending"; id: string; sentAt: number }
  | { type: "mark_error"; id: string; error: string }
  | { type: "mark_pending"; id: string }
  | { type: "send_success"; id: string }
  | { type: "remove"; id: string }
  | { type: "clear" }
  | { type: "filter_session"; sessionId: string }
  | { type: "timeout_sending"; cutoff: number }
  | { type: "retry"; id: string }
  | { type: "phase_error" }
  | { type: "reconcile"; historyIds: Set<string> };

function reduce(state: QueuedMessage[], action: Action): QueuedMessage[] {
  switch (action.type) {
    case "enqueue":
      return [...state, action.msg];
    case "mark_sending":
      return state.map((m) =>
        m.id === action.id ? { ...m, status: "sending" as const, _sentAt: action.sentAt } : m,
      );
    case "mark_error":
      return state.map((m) =>
        m.id === action.id ? { ...m, status: "error" as const, error: action.error, _sentAt: undefined } : m,
      );
    case "mark_pending":
      return state.map((m) =>
        m.id === action.id && m.status === "sending"
          ? { ...m, status: "pending" as const, _sentAt: undefined, _retryCount: (m._retryCount ?? 0) + 1 }
          : m,
      );
    case "send_success":
      return state.find((m) => m.id === action.id && m.status === "sending")
        ? state.filter((m) => m.id !== action.id)
        : state;
    case "remove":
      return state.filter((m) => m.id !== action.id);
    case "clear":
      return [];
    case "filter_session":
      return state.filter((m) => m.sessionId === action.sessionId);
    case "timeout_sending": {
      const cutoff = action.cutoff;
      return state.map((m) => {
        if (m.status !== "sending") return m;
        if (!m._sentAt || m._sentAt < cutoff) {
          return { ...m, status: "error" as const, error: "Send timed out", _sentAt: undefined };
        }
        return m;
      });
    }
    case "retry":
      return state.map((m) =>
        m.id === action.id && m.status === "error"
          ? { ...m, status: "pending" as const, error: undefined }
          : m,
      );
    case "phase_error":
      return state.map((m) =>
        m.status === "pending" || m.status === "sending"
          ? { ...m, status: "error" as const, error: "Workspace restarted", _sentAt: undefined }
          : m,
      );
    case "reconcile":
      return state.filter((m) => !action.historyIds.has(m.id));
    default:
      return state;
  }
}

let _lastTs = 0;
let _counter = 0;
const BASE62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz";

function uid(): string {
  const now = Date.now();
  if (now !== _lastTs) { _lastTs = now; _counter = 0; }
  _counter++;

  let ts = BigInt(now) * BigInt(0x1000) + BigInt(_counter);
  const timeBytes: number[] = [];
  for (let i = 0; i < 6; i++) {
    timeBytes.unshift(Number(ts & BigInt(0xff)));
    ts >>= BigInt(8);
  }
  const timeHex = timeBytes.map((b) => b.toString(16).padStart(2, "0")).join("");

  const randBytes =
    typeof crypto !== "undefined" && crypto.getRandomValues
      ? crypto.getRandomValues(new Uint8Array(14))
      : new Uint8Array(14).map(() => Math.floor(Math.random() * 256));
  let rand = "";
  for (let i = 0; i < 14; i++) rand += BASE62[randBytes[i]! % 62];

  return timeHex + rand;
}

const SENDING_TIMEOUT_MS = 60_000;
const RESTART_PHASES = ["Creating", "Pending", "Suspending"];

export function useMessageQueue(
  workspaceId: string | undefined,
  sessionId: string | undefined,
) {
  const [queuedMessages, dispatch] = useReducer(reduce, []);
  const drainingRef = useRef(false);
  const stateRef = useRef(queuedMessages);
  stateRef.current = queuedMessages;

  useEffect(() => {
    dispatch({ type: "filter_session", sessionId: sessionId! });
  }, [sessionId]);

  useEffect(() => {
    const interval = setInterval(() => {
      const prev = stateRef.current;
      const cutoff = Date.now() - SENDING_TIMEOUT_MS;
      const hasStuck = prev.some((m) => m.status === "sending" && (!m._sentAt || m._sentAt < cutoff));
      if (hasStuck) {
        dispatch({ type: "timeout_sending", cutoff });
        drainingRef.current = false;
      }
    }, 10_000);
    return () => clearInterval(interval);
  }, []);

  const drainOne = useCallback((msgs: QueuedMessage[]) => {
    if (!workspaceId || !sessionId) return;
    if (drainingRef.current) return;
    const head = msgs.find((m) => m.status === "pending");
    if (!head) return;

    drainingRef.current = true;
    const sentAt = Date.now();

    dispatch({ type: "mark_sending", id: head.id, sentAt });

    messagesApi
      .sendAsync(workspaceId, sessionId, {
        parts: [{ type: "text", text: head.text }],
        messageID: head.id,
      })
      .then(() => {
        dispatch({ type: "send_success", id: head.id });
        drainingRef.current = false;
      })
      .catch((err: unknown) => {
        if (err instanceof ApiClientError && err.status === 409) {
          const retryCount = head._retryCount ?? 0;
          if (retryCount >= MAX_RETRIES) {
            dispatch({ type: "mark_error", id: head.id, error: "Session busy — retry manually" });
          } else {
            dispatch({ type: "mark_pending", id: head.id });
          }
          drainingRef.current = false;
          return;
        }
        let error = err instanceof Error ? err.message : "Failed to send";
        if (err instanceof ApiClientError && err.status === 429) {
          const retryAfter = ((err.body as unknown) as Record<string, unknown>).retryAfter ?? 60;
          error = `Rate limited. Retry after ${retryAfter}s`;
        }
        dispatch({ type: "mark_error", id: head.id, error });
        drainingRef.current = false;
      });
  }, [workspaceId, sessionId]);

  const enqueue = useCallback((text: string) => {
    if (!workspaceId || !sessionId) return;
    const id = "msg_" + uid();
    dispatch({ type: "enqueue", msg: { id, text, status: "pending", sessionId } });
  }, [workspaceId, sessionId]);

  const notifyIdle = useCallback(() => {
    drainOne(stateRef.current);
  }, [drainOne]);

  const clear = useCallback(() => dispatch({ type: "clear" }), []);

  const reconcile = useCallback((history: Message[]) => {
    const historyIds = new Set(history.filter((m) => m.role === "user").map((m) => m.id));
    dispatch({ type: "reconcile", historyIds });
  }, []);

  const retry = useCallback(async (id: string) => {
    if (!workspaceId || !sessionId) return;
    try {
      const history = await messagesApi.getHistory(workspaceId, sessionId);
      if (history.some((m) => m.id === id)) {
        dispatch({ type: "remove", id });
        return;
      }
    } catch { /* history fetch failed — fall through to retry send */ }

    dispatch({ type: "retry", id });
  }, [workspaceId, sessionId]);

  const dismiss = useCallback((id: string) => {
    dispatch({ type: "remove", id });
  }, []);

  const onPhaseChange = useCallback((phase: string) => {
    if (RESTART_PHASES.includes(phase)) {
      drainingRef.current = false;
      dispatch({ type: "phase_error" });
    }
  }, []);

  const sessionQueue = sessionId
    ? queuedMessages.filter((m) => m.sessionId === sessionId)
    : [];

  return { queuedMessages: sessionQueue, enqueue, notifyIdle, retry, dismiss, clear, reconcile, onPhaseChange };
}
