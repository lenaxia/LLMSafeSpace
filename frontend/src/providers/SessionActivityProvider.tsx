import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
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

  // Track which workspaces have been seeded from REST. Once seeded, SSE
  // events are the sole source of truth — REST refetches (triggered by
  // invalidateQueries in ChatPage on every session.status event) must not
  // clobber SSE-tracked state. The set is per-provider-instance and lives
  // for the mount lifetime.
  const seededRef = useRef(new Set<string>());

  useEffect(() => {
    const queryCache = queryClient.getQueryCache();
    const seeded = seededRef.current;

    function seedNewWorkspaces() {
      let busyDelta: Map<string, string> | null = null;
      let unreadDelta: Map<string, string> | null = null;

      for (const query of queryCache.getAll()) {
        const key = query.queryKey;
        if (!Array.isArray(key) || key[0] !== "sessions" || typeof key[1] !== "string") continue;
        const wsId = key[1];
        if (seeded.has(wsId)) continue;
        const data = query.state.data;
        if (!Array.isArray(data)) continue;

        seeded.add(wsId);

        for (const session of data as Array<{ id: string; status?: string; hasUnread?: boolean }>) {
          if (session.status === "active") {
            if (!busyDelta) busyDelta = new Map();
            busyDelta.set(session.id, wsId);
          }
          if (session.hasUnread) {
            if (!unreadDelta) unreadDelta = new Map();
            unreadDelta.set(session.id, wsId);
          }
        }
      }

      if (busyDelta && busyDelta.size > 0) {
        setBusySessions((prev) => {
          const next = new Map(prev);
          for (const [sid, wid] of busyDelta!) {
            next.set(sid, wid);
          }
          return next;
        });
      }
      if (unreadDelta && unreadDelta.size > 0) {
        setPendingUnread((prev) => {
          const next = new Map(prev);
          for (const [sid, wid] of unreadDelta!) {
            next.set(sid, wid);
          }
          return next;
        });
      }
    }

    seedNewWorkspaces();

    const unsubscribe = queryCache.subscribe((event) => {
      if (event.type === "updated" || event.type === "added") {
        const key = event.query.queryKey;
        if (Array.isArray(key) && key[0] === "sessions") {
          seedNewWorkspaces();
        }
      }
    });

    return unsubscribe;
  }, [queryClient]);

  useUserEventStream({
    onReconnect: () => {
      seededRef.current.clear();
      setBusySessions(new Map());
    },
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

      if (evt.type === "workspace.phase" && evt.workspace_id && evt.phase) {
        const wsId = evt.workspace_id;
        if (NON_ACTIVE_PHASES.has(evt.phase)) {
          seededRef.current.delete(wsId);
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
        } else if (evt.phase === "Active") {
          seededRef.current.delete(wsId);
        }
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
