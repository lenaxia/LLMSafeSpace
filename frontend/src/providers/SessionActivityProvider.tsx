import { createContext, useCallback, useContext, useEffect, useState } from "react";
import type { ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useParams } from "react-router-dom";
import { useUserEventStream } from "../hooks/useUserEventStream";

interface SessionActivityContextValue {
  isSessionBusy: (sessionId: string) => boolean;
  isSessionUnread: (sessionId: string) => boolean;
  workspaceBusyCount: (workspaceId: string) => number;
  clearPendingUnread: (sessionId: string) => void;
}

const SessionActivityContext = createContext<SessionActivityContextValue | null>(null);

const NON_ACTIVE_PHASES = new Set(["Suspending", "Suspended", "Terminating", "Terminated", "Failed"]);

export function SessionActivityProvider({ children }: { children: ReactNode }) {
  const [busySessions, setBusySessions] = useState<Map<string, string>>(new Map());
  const [pendingUnread, setPendingUnread] = useState<Map<string, string>>(new Map());
  const queryClient = useQueryClient();
  const params = useParams();
  const currentSessionId = params.sessionId;

  useEffect(() => {
    // Seed state from whatever sessions data is already in the cache, then
    // subscribe so we re-seed whenever any sessions query settles. This covers
    // two scenarios:
    //   1. Provider mounts after queries have already resolved (e.g. fast cache
    //      hit) — getAll() is non-empty on the first call.
    //   2. Provider mounts before queries resolve (typical page load, E2E) —
    //      the subscriber fires when each query transitions to "success".
    const queryCache = queryClient.getQueryCache();

    function seedFromCache() {
      const busy = new Map<string, string>();
      const unread = new Map<string, string>();
      for (const query of queryCache.getAll()) {
        const key = query.queryKey;
        if (!Array.isArray(key) || key[0] !== "sessions" || typeof key[1] !== "string") continue;
        const wsId = key[1];
        const data = query.state.data;
        if (!Array.isArray(data)) continue;
        for (const session of data as Array<{ id: string; status?: string; hasUnread?: boolean }>) {
          if (session.status === "active") busy.set(session.id, wsId);
          if (session.hasUnread) unread.set(session.id, wsId);
        }
      }
      setBusySessions(busy);
      setPendingUnread(unread);
    }

    // Seed immediately (handles case where data is already present)
    seedFromCache();

    // Re-seed on every cache update that touches a "sessions" query
    const unsubscribe = queryCache.subscribe((event) => {
      if (event.type === "updated" || event.type === "added") {
        const key = event.query.queryKey;
        if (Array.isArray(key) && key[0] === "sessions") {
          seedFromCache();
        }
      }
    });

    return unsubscribe;
  }, [queryClient]);

  useUserEventStream({
    onEvent: (data) => {
      const evt = data as {
        type: string;
        workspace_id?: string;
        session_id?: string;
        status?: string;
        phase?: string;
      };

      if (evt.type === "session.status" && evt.session_id && evt.workspace_id) {
        if (evt.status === "busy") {
          setBusySessions((prev) => {
            const next = new Map(prev);
            next.set(evt.session_id!, evt.workspace_id!);
            return next;
          });

          const sessionsKey = ["sessions", evt.workspace_id];
          const existing = queryClient.getQueryData(sessionsKey);
          if (existing) {
            queryClient.setQueryData(sessionsKey, (old: unknown) => {
              if (!Array.isArray(old)) return old;
              return old.map((s: Record<string, unknown>) =>
                s.id === evt.session_id ? { ...s, status: "active" } : s
              );
            });
          }
        } else if (evt.status === "idle") {
          setBusySessions((prev) => {
            const next = new Map(prev);
            next.delete(evt.session_id!);
            return next;
          });

          if (evt.session_id !== currentSessionId) {
            setPendingUnread((prev) => {
              const next = new Map(prev);
              next.set(evt.session_id!, evt.workspace_id!);
              return next;
            });

            // Write hasUnread:true into the cache so seedFromCache() (called
            // by queryCache.subscribe) rebuilds the correct unread state if it
            // fires after this SSE event. Without this, seedFromCache reads the
            // stale cache entry (hasUnread:false) and clobbers the functional
            // updater above.
            const sessionsKey = ["sessions", evt.workspace_id];
            const existing = queryClient.getQueryData(sessionsKey);
            if (existing) {
              queryClient.setQueryData(sessionsKey, (old: unknown) => {
                if (!Array.isArray(old)) return old;
                return old.map((s: Record<string, unknown>) =>
                  s.id === evt.session_id ? { ...s, status: "idle", hasUnread: true } : s
                );
              });
            }
          } else {
            const sessionsKey = ["sessions", evt.workspace_id];
            const existing = queryClient.getQueryData(sessionsKey);
            if (existing) {
              queryClient.setQueryData(sessionsKey, (old: unknown) => {
                if (!Array.isArray(old)) return old;
                return old.map((s: Record<string, unknown>) =>
                  s.id === evt.session_id ? { ...s, status: "idle" } : s
                );
              });
            }
          }
        }
      }

      if (evt.type === "workspace.phase" && evt.workspace_id && evt.phase && NON_ACTIVE_PHASES.has(evt.phase)) {
        const wsId = evt.workspace_id;
        setBusySessions((prev) => {
          const next = new Map();
          for (const [sid, wid] of prev) {
            if (wid !== wsId) next.set(sid, wid);
          }
          return next;
        });
        setPendingUnread((prev) => {
          const next = new Map<string, string>();
          for (const [sid, wid] of prev) {
            if (wid !== wsId) next.set(sid, wid);
          }
          return next;
        });
      }
    },
  });

  const isSessionBusy = useCallback(
    (sessionId: string) => busySessions.has(sessionId),
    [busySessions]
  );

  const isSessionUnread = useCallback(
    (sessionId: string) => pendingUnread.has(sessionId),
    [pendingUnread]
  );

  const workspaceBusyCount = useCallback(
    (workspaceId: string) => {
      let count = 0;
      for (const wid of busySessions.values()) {
        if (wid === workspaceId) count++;
      }
      return count;
    },
    [busySessions]
  );

  const clearPendingUnread = useCallback((sessionId: string) => {
    setPendingUnread((prev) => {
      if (!prev.has(sessionId)) return prev;
      const next = new Map(prev);
      next.delete(sessionId);
      return next;
    });
    // Also clear hasUnread in the query cache so seedFromCache does not
    // re-add the session to pendingUnread if it fires after this call
    // (e.g. on a subsequent cache update for the same workspace).
    const wsId = pendingUnread.get(sessionId);
    if (wsId) {
      const sessionsKey = ["sessions", wsId];
      const existing = queryClient.getQueryData(sessionsKey);
      if (existing) {
        queryClient.setQueryData(sessionsKey, (old: unknown) => {
          if (!Array.isArray(old)) return old;
          return old.map((s: Record<string, unknown>) =>
            s.id === sessionId ? { ...s, hasUnread: false } : s
          );
        });
      }
    }
  }, [pendingUnread, queryClient]);

  return (
    <SessionActivityContext.Provider
      value={{ isSessionBusy, isSessionUnread, workspaceBusyCount, clearPendingUnread }}
    >
      {children}
    </SessionActivityContext.Provider>
  );
}

export function useIsSessionBusy(sessionId: string): boolean {
  const ctx = useContext(SessionActivityContext);
  if (!ctx) return false;
  return ctx.isSessionBusy(sessionId);
}

export function useIsSessionUnread(sessionId: string): boolean {
  const ctx = useContext(SessionActivityContext);
  if (!ctx) return false;
  return ctx.isSessionUnread(sessionId);
}

export function useWorkspaceBusyCount(workspaceId: string): number {
  const ctx = useContext(SessionActivityContext);
  if (!ctx) return 0;
  return ctx.workspaceBusyCount(workspaceId);
}

export function useClearPendingUnread(): (sessionId: string) => void {
  const ctx = useContext(SessionActivityContext);
  if (!ctx) return () => {};
  return ctx.clearPendingUnread;
}
