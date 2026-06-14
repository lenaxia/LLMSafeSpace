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

  // Track which workspaces have been seeded from REST for BUSY state. Once
  // seeded, SSE events are the sole source of truth for busy — REST refetches
  // (triggered by invalidateQueries in ChatPage on every session.status event)
  // must not clobber SSE-tracked state. The set is per-provider-instance and
  // lives for the mount lifetime.
  const seededRef = useRef(new Set<string>());

  // Sessions the user explicitly cleared (viewed). Reconcile suppresses
  // re-adding them from a stale REST refetch (markSessionSeen PUT racing the
  // GET) until REST confirms hasUnread:false, or new activity arrives
  // (busy/idle SSE events). Keyed by sessionId → workspaceId.
  const clearedRef = useRef(new Map<string, string>());

  useEffect(() => {
    const queryCache = queryClient.getQueryCache();
    const seeded = seededRef.current;
    const cleared = clearedRef.current;

    // Busy is seeded from REST once per workspace; afterwards SSE is the sole
    // authority (REST enrichment can lag on multi-replica or timing gaps, so a
    // refetch returning status:"idle" must not clear an SSE-tracked busy session).
    function seedBusy() {
      let busyDelta: Map<string, string> | null = null;

      for (const query of queryCache.getAll()) {
        const key = query.queryKey;
        if (!Array.isArray(key) || key[0] !== "sessions" || typeof key[1] !== "string") continue;
        const wsId = key[1];
        if (seeded.has(wsId)) continue;
        const data = query.state.data;
        if (!Array.isArray(data)) continue;

        seeded.add(wsId);

        for (const session of data as Array<{ id: string; status?: string }>) {
          if (session.status === "active") {
            if (!busyDelta) busyDelta = new Map();
            busyDelta.set(session.id, wsId);
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
    }

    // Unread is reconciled against the durable REST `hasUnread` field on every
    // cache update. REST is the source of truth for unread (computed from
    // last_message_at vs last_seen_at in the DB), so re-reading it keeps the
    // pulse accurate across page refreshes, where no SSE idle event is replayed
    // for sessions that were already idle before the page loaded.
    //
    // Reconcile is ADD-ONLY: it never removes a session from pendingUnread when
    // REST says hasUnread:false. An SSE-set unread (a response that just
    // arrived) must survive a stale refetch where last_message_at hasn't
    // persisted yet. Removal happens only via clearPendingUnread (user viewed
    // the session) or workspace.phase. `cleared` suppresses re-adding a session
    // the user just viewed, bridging the window until REST confirms
    // hasUnread:false (at which point the suppression is released).
    function reconcileUnread() {
      const add: Map<string, string> = new Map();
      const releaseCleared: Set<string> = new Set();

      for (const query of queryCache.getAll()) {
        const key = query.queryKey;
        if (!Array.isArray(key) || key[0] !== "sessions" || typeof key[1] !== "string") continue;
        const wsId = key[1];
        const data = query.state.data;
        if (!Array.isArray(data)) continue;

        for (const session of data as Array<{ id: string; hasUnread?: boolean }>) {
          const sid = session.id;
          if (session.hasUnread) {
            if (!cleared.has(sid)) {
              add.set(sid, wsId);
            }
          } else if (cleared.has(sid)) {
            releaseCleared.add(sid);
          }
        }
      }

      if (add.size > 0) {
        setPendingUnread((prev) => {
          let next = prev;
          let changed = false;
          for (const [sid, wid] of add) {
            if (!next.has(sid)) {
              if (!changed) { next = new Map(prev); changed = true; }
              next.set(sid, wid);
            }
          }
          return next;
        });
      }

      for (const sid of releaseCleared) {
        cleared.delete(sid);
      }
    }

    seedBusy();
    reconcileUnread();

    const unsubscribe = queryCache.subscribe((event) => {
      if (event.type === "updated" || event.type === "added") {
        const key = event.query.queryKey;
        if (Array.isArray(key) && key[0] === "sessions") {
          seedBusy();
          reconcileUnread();
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
          clearedRef.current.delete(evt.session_id);
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
            clearedRef.current.delete(evt.session_id);
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
          for (const [sid, wid] of clearedRef.current) {
            if (wid === wsId) clearedRef.current.delete(sid);
          }
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

    // Record the clear so reconcileUnread suppresses a stale REST refetch
    // (markSessionSeen PUT racing the GET) from re-adding the session. The
    // entry is released once REST confirms hasUnread:false, or on new activity
    // (busy/idle SSE events). Resolve the workspaceId from the unread set or
    // the session cache so workspace.phase cleanup can target it.
    let wsId = pendingUnread.get(sessionId);
    if (!wsId) {
      for (const query of queryClient.getQueryCache().getAll()) {
        const key = query.queryKey;
        if (!Array.isArray(key) || key[0] !== "sessions" || typeof key[1] !== "string") continue;
        const data = query.state.data;
        if (Array.isArray(data) && (data as Array<{ id: string }>).some((s) => s.id === sessionId)) {
          wsId = key[1];
          break;
        }
      }
    }
    clearedRef.current.set(sessionId, wsId ?? "");
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
