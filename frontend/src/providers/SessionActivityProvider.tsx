import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useParams } from "react-router-dom";
import { useUserEventStream } from "../hooks/useUserEventStream";
import type { QuestionRequest, PermissionRequest } from "../api/types";

interface SessionActivityContextValue {
  isSessionBusy: (sessionId: string) => boolean;
  isSessionUnread: (sessionId: string) => boolean;
  workspaceBusyCount: (workspaceId: string) => number;
  clearPendingUnread: (sessionId: string) => void;
  isSessionPendingAction: (sessionId: string) => boolean;
  pendingActionSessionIds: Set<string>;
  addPendingAction: (workspaceId: string, sessionId: string, requestId: string) => void;
  removePendingAction: (requestId: string) => void;
  clearWorkspacePendingActions: (workspaceId: string) => void;
  // Pending prompt CONTENT (issue #346). The indicator (pendingActions above)
  // drives the sidebar pulse; the content (question/permission bodies) drives
  // the in-chat prompt UI. Content lives in this global layer — not in
  // ChatPage session-local state — so it survives within-tab navigation
  // between a parent session and its subtasks. Filtered by session at read.
  addPendingQuestion: (workspaceId: string, req: QuestionRequest) => void;
  addPendingPermission: (workspaceId: string, req: PermissionRequest) => void;
  pendingQuestionsForSession: (sessionId: string) => QuestionRequest[];
  pendingPermissionsForSession: (sessionId: string) => PermissionRequest[];
  clearSessionPendingPrompts: (sessionId: string) => void;
}

const SessionActivityContext = createContext<SessionActivityContextValue | null>(null);

const NON_ACTIVE_PHASES = new Set(["Suspending", "Suspended", "Terminating", "Terminated", "Failed"]);

// pruneMany returns a copy of m with every key in doomed removed, or m itself
// if none matched (avoids needless re-renders).
function pruneMany<V>(m: Map<string, V>, doomed: Set<string>): Map<string, V> {
  let next: Map<string, V> | null = null;
  for (const k of doomed) {
    if (m.has(k)) {
      if (!next) next = new Map(m);
      next.delete(k);
    }
  }
  return next ?? m;
}

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

  // Sessions with pending agent questions or permission requests.
  // Keyed by sessionId → Set<requestId> so multiple concurrent prompts
  // per session are tracked independently.
  const [pendingActions, setPendingActions] = useState<Map<string, Set<string>>>(new Map());

  // Reverse lookup: requestId → sessionId so resolved events (which carry
  // only request_id) can find the correct session to decrement.
  const requestToSessionRef = useRef(new Map<string, string>());

  // Track which workspace each session belongs to so clearWorkspacePendingActions
  // can scope its clearing to a single workspace.
  const pendingActionWsRef = useRef(new Map<string, string>());

  // Pending prompt CONTENT, keyed by requestId. Paired with pendingActions
  // (the ID-only indicator). Content is global so it survives within-tab
  // session navigation (#346); consumers filter by session at read time.
  const [pendingQuestionContent, setPendingQuestionContent] = useState<Map<string, QuestionRequest>>(new Map());
  const [pendingPermissionContent, setPendingPermissionContent] = useState<Map<string, PermissionRequest>>(new Map());

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
            setPendingActions((prev) => {
              if (!prev.has(evt.session_id!)) return prev;
              const next = new Map(prev);
              next.delete(evt.session_id!);
              return next;
            });
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
            setPendingActions((prev) => {
              if (!prev.has(evt.session_id!)) return prev;
              const next = new Map(prev);
              next.delete(evt.session_id!);
              return next;
            });
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
          clearWorkspacePendingActions(wsId);
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

  const addPendingAction = useCallback((workspaceId: string, sessionId: string, requestId: string) => {
    requestToSessionRef.current.set(requestId, sessionId);
    pendingActionWsRef.current.set(sessionId, workspaceId);
    setPendingActions((prev) => {
      const existing = prev.get(sessionId);
      if (existing?.has(requestId)) return prev;
      const next = new Map(prev);
      const set = new Set(existing ?? []);
      set.add(requestId);
      next.set(sessionId, set);
      return next;
    });
  }, []);

  const removePendingAction = useCallback((requestId: string) => {
    // Clear prompt content first (unconditionally) so a resolved event always
    // drops the in-chat prompt even if the indicator entry was already cleared
    // by a session-scoped clear.
    setPendingQuestionContent((prev) => {
      if (!prev.has(requestId)) return prev;
      const next = new Map(prev);
      next.delete(requestId);
      return next;
    });
    setPendingPermissionContent((prev) => {
      if (!prev.has(requestId)) return prev;
      const next = new Map(prev);
      next.delete(requestId);
      return next;
    });

    const sessionId = requestToSessionRef.current.get(requestId);
    if (!sessionId) return;
    requestToSessionRef.current.delete(requestId);
    setPendingActions((prev) => {
      const existing = prev.get(sessionId);
      if (!existing || !existing.has(requestId)) return prev;
      const next = new Map(prev);
      const set = new Set(existing);
      set.delete(requestId);
      if (set.size === 0) {
        next.delete(sessionId);
      } else {
        next.set(sessionId, set);
      }
      return next;
    });
  }, []);

  const clearWorkspacePendingActions = useCallback((workspaceId: string) => {
    // Collect requestIds belonging to this workspace's sessions so the
    // content maps can be pruned in lockstep with the indicator.
    const doomedRequests = new Set<string>();
    setPendingActions((prev) => {
      let next: Map<string, Set<string>> | null = null;
      for (const [sid] of prev) {
        if (pendingActionWsRef.current.get(sid) === workspaceId) {
          if (!next) next = new Map(prev);
          next!.delete(sid);
          for (const rid of prev.get(sid) ?? []) doomedRequests.add(rid);
        }
      }
      return next ?? prev;
    });
    if (doomedRequests.size > 0) {
      setPendingQuestionContent((prev) => pruneMany(prev, doomedRequests));
      setPendingPermissionContent((prev) => pruneMany(prev, doomedRequests));
    }
  }, []);

  const addPendingQuestion = useCallback((workspaceId: string, req: QuestionRequest) => {
    addPendingAction(workspaceId, req.session_id, req.id);
    setPendingQuestionContent((prev) => {
      if (prev.has(req.id)) return prev;
      const next = new Map(prev);
      next.set(req.id, req);
      return next;
    });
  }, [addPendingAction]);

  const addPendingPermission = useCallback((workspaceId: string, req: PermissionRequest) => {
    addPendingAction(workspaceId, req.session_id, req.id);
    setPendingPermissionContent((prev) => {
      if (prev.has(req.id)) return prev;
      const next = new Map(prev);
      next.set(req.id, req);
      return next;
    });
  }, [addPendingAction]);

  // pendingQuestionsForSession matches both the owning session and any session
  // whose root_session_id points here, so subtask prompts bubble to the parent
  // view (same rule ChatPage previously applied at write time — now applied at
  // read time so the content is stored regardless of the currently-viewed session).
  const pendingQuestionsForSession = useCallback((sessionId: string): QuestionRequest[] => {
    const out: QuestionRequest[] = [];
    for (const q of pendingQuestionContent.values()) {
      const root = q.root_session_id ?? q.session_id;
      if (root === sessionId || q.session_id === sessionId) out.push(q);
    }
    return out;
  }, [pendingQuestionContent]);

  const pendingPermissionsForSession = useCallback((sessionId: string): PermissionRequest[] => {
    const out: PermissionRequest[] = [];
    for (const p of pendingPermissionContent.values()) {
      const root = p.root_session_id ?? p.session_id;
      if (root === sessionId || p.session_id === sessionId) out.push(p);
    }
    return out;
  }, [pendingPermissionContent]);

  // clearSessionPendingPrompts drops all prompt content + the indicator for one
  // session (US-16.12: clear stale prompts on session idle/error).
  const clearSessionPendingPrompts = useCallback((sessionId: string) => {
    const doomed = new Set<string>();
    for (const rid of pendingActions.get(sessionId) ?? []) doomed.add(rid);
    // Also include content whose owning or root session matches.
    const collect = <T extends { id: string; session_id: string; root_session_id?: string }>(m: Map<string, T>) => {
      for (const v of m.values()) {
        const root = v.root_session_id ?? v.session_id;
        if (root === sessionId || v.session_id === sessionId) doomed.add(v.id);
      }
    };
    collect(pendingQuestionContent);
    collect(pendingPermissionContent);
    if (doomed.size === 0) return;
    setPendingQuestionContent((prev) => pruneMany(prev, doomed));
    setPendingPermissionContent((prev) => pruneMany(prev, doomed));
    // Clear the indicator entry for this session too.
    setPendingActions((prev) => {
      if (!prev.has(sessionId)) return prev;
      const next = new Map(prev);
      next.delete(sessionId);
      return next;
    });
    for (const rid of doomed) requestToSessionRef.current.delete(rid);
  }, [pendingActions, pendingQuestionContent, pendingPermissionContent]);

  const isSessionPendingAction = useCallback(
    (sessionId: string) => pendingActions.has(sessionId),
    [pendingActions]
  );

  const pendingActionSessionIds = useMemo(
    () => new Set(pendingActions.keys()),
    [pendingActions]
  );

  return (
    <SessionActivityContext.Provider
      value={{ isSessionBusy, isSessionUnread, workspaceBusyCount, clearPendingUnread, isSessionPendingAction, pendingActionSessionIds, addPendingAction, removePendingAction, clearWorkspacePendingActions, addPendingQuestion, addPendingPermission, pendingQuestionsForSession, pendingPermissionsForSession, clearSessionPendingPrompts }}
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

export function useIsSessionPendingAction(sessionId: string): boolean {
  const ctx = useContext(SessionActivityContext);
  if (!ctx) return false;
  return ctx.isSessionPendingAction(sessionId);
}

export function useAddPendingAction(): (workspaceId: string, sessionId: string, requestId: string) => void {
  const ctx = useContext(SessionActivityContext);
  if (!ctx) return () => {};
  return ctx.addPendingAction;
}

export function useRemovePendingAction(): (requestId: string) => void {
  const ctx = useContext(SessionActivityContext);
  if (!ctx) return () => {};
  return ctx.removePendingAction;
}

export function useSessionPendingActions(): Set<string> {
  const ctx = useContext(SessionActivityContext);
  if (!ctx) return new Set();
  return ctx.pendingActionSessionIds;
}

export function useAddPendingQuestion(): (workspaceId: string, req: QuestionRequest) => void {
  const ctx = useContext(SessionActivityContext);
  if (!ctx) return () => {};
  return ctx.addPendingQuestion;
}

export function useAddPendingPermission(): (workspaceId: string, req: PermissionRequest) => void {
  const ctx = useContext(SessionActivityContext);
  if (!ctx) return () => {};
  return ctx.addPendingPermission;
}

export function usePendingQuestionsForSession(sessionId: string): QuestionRequest[] {
  const ctx = useContext(SessionActivityContext);
  if (!ctx) return [];
  return ctx.pendingQuestionsForSession(sessionId);
}

export function usePendingPermissionsForSession(sessionId: string): PermissionRequest[] {
  const ctx = useContext(SessionActivityContext);
  if (!ctx) return [];
  return ctx.pendingPermissionsForSession(sessionId);
}

export function useClearSessionPendingPrompts(): (sessionId: string) => void {
  const ctx = useContext(SessionActivityContext);
  if (!ctx) return () => {};
  return ctx.clearSessionPendingPrompts;
}
