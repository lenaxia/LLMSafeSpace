import { useState, useCallback, useRef, useEffect } from "react";
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
};

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

export function useMessageQueue(
  workspaceId: string | undefined,
  sessionId: string | undefined,
) {
  const [queuedMessages, setQueuedMessages] = useState<QueuedMessage[]>([]);
  const drainingRef = useRef(false);

  useEffect(() => {
    setQueuedMessages((prev) => prev.filter((m) => m.sessionId === sessionId));
  }, [sessionId]);

  useEffect(() => {
    const interval = setInterval(() => {
      const cutoff = Date.now() - SENDING_TIMEOUT_MS;
      setQueuedMessages((prev) => {
        let changed = false;
        const next = prev.map((m) => {
          if (m.status !== "sending") return m;
          if (!m._sentAt || m._sentAt < cutoff) {
            changed = true;
            return { ...m, status: "error" as const, error: "Send timed out", _sentAt: undefined };
          }
          return m;
        });
        return changed ? next : prev;
      });
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

    setQueuedMessages((prev) =>
      prev.map((m) => m.id === head.id ? { ...m, status: "sending" as const, _sentAt: sentAt } : m),
    );

    messagesApi
      .sendAsync(workspaceId, sessionId, {
        parts: [{ type: "text", text: head.text }],
        messageID: head.id,
      })
      .then(() => {
        setQueuedMessages((prev) => prev.filter((m) => m.id !== head.id));
        drainingRef.current = false;
      })
      .catch((err: unknown) => {
        let error = err instanceof Error ? err.message : "Failed to send";
        if (err instanceof ApiClientError && err.status === 429) {
          const retryAfter = ((err.body as unknown) as Record<string, unknown>).retryAfter ?? 60;
          error = `Rate limited. Retry after ${retryAfter}s`;
        }
        setQueuedMessages((prev) =>
          prev.map((m) => m.id === head.id ? { ...m, status: "error" as const, error, _sentAt: undefined } : m),
        );
        drainingRef.current = false;
      });
  }, [workspaceId, sessionId]);

  const enqueue = useCallback((text: string) => {
    if (!workspaceId || !sessionId) return;
    const id = "msg_" + uid();
    const msg: QueuedMessage = { id, text, status: "pending", sessionId };
    setQueuedMessages((prev) => [...prev, msg]);
  }, [workspaceId, sessionId]);

  const notifyIdle = useCallback(() => {
    setQueuedMessages((prev) => {
      drainOne(prev);
      return prev;
    });
  }, [drainOne]);

  const remove = useCallback((id: string) => {
    setQueuedMessages((prev) => prev.filter((m) => m.id !== id));
  }, []);

  const clear = useCallback(() => setQueuedMessages([]), []);

  const reconcile = useCallback((history: Message[]) => {
    const historyIds = new Set(history.filter((m) => m.role === "user").map((m) => m.id));
    setQueuedMessages((prev) => prev.filter((m) => !historyIds.has(m.id)));
  }, []);

  const retry = useCallback(async (id: string) => {
    if (!workspaceId || !sessionId) return;
    try {
      const history = await messagesApi.getHistory(workspaceId, sessionId);
      if (history.some((m) => m.id === id)) {
        setQueuedMessages((prev) => prev.filter((m) => m.id !== id));
        return;
      }
    } catch { /* history fetch failed — fall through to retry send */ }

    setQueuedMessages((prev) =>
      prev.map((m) =>
        m.id === id && m.status === "error"
          ? { ...m, status: "pending" as const, error: undefined }
          : m,
      ),
    );
  }, [workspaceId, sessionId]);

  const dismiss = useCallback((id: string) => {
    setQueuedMessages((prev) => prev.filter((m) => m.id !== id));
  }, []);

  const onPhaseChange = useCallback((phase: string) => {
    if (phase === "Creating" || phase === "Pending" || phase === "Suspending") {
      drainingRef.current = false;
      setQueuedMessages((prev) =>
        prev.map((m) =>
          m.status === "pending" || m.status === "sending"
            ? { ...m, status: "error" as const, error: "Workspace restarted", _sentAt: undefined }
            : m,
        ),
      );
    }
  }, []);

  const sessionQueue = sessionId
    ? queuedMessages.filter((m) => m.sessionId === sessionId)
    : [];

  return { queuedMessages: sessionQueue, enqueue, notifyIdle, remove, retry, dismiss, clear, reconcile, onPhaseChange };
}
