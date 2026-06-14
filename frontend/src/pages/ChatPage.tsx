import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient, keepPreviousData } from "@tanstack/react-query";
import { workspacesApi } from "../api/workspaces";
import { useWorkspaceStatus } from "../hooks/useWorkspaces";
import { useMessageHistory } from "../hooks/useMessageHistory";
import { useActivateWorkspace } from "../hooks/useActivateWorkspace";
import { useChatStream } from "../hooks/useChatStream";
import { useEventStream } from "../hooks/useEventStream";
import { useSessionTitle } from "../hooks/useSessionTitle";
import { useMessageQueue } from "../hooks/useMessageQueue";
import { wsLog } from "../lib/wsLog";
import { ChatView } from "../components/chat/ChatView";
import { SuspendedBanner } from "../components/chat/SuspendedBanner";
import { AtCapBanner } from "../components/chat/AtCapBanner";
import { HealthBanner } from "../components/chat/HealthBanner";
import { SessionRetryBanner, type RetryStatus } from "../components/chat/SessionRetryBanner";
import { AgentReloadBanner } from "../components/workspace/AgentReloadBanner";
import { DiskUsageBar } from "../components/workspace/DiskUsageBar";
import { ModelSelector } from "../components/chat/ModelSelector";
import { Spinner } from "../components/ui/Spinner";
import { KebabMenu } from "../components/ui/KebabMenu";
import type { KebabMenuItem } from "../components/ui/KebabMenu";
import { sessionsApi } from "../api/sessions";
import type { Message, SessionListItem, WorkspaceStreamEvent, OpenCodeEvent, QuestionRequest, PermissionRequest } from "../api/types";
import { QuestionPrompt } from "../components/chat/QuestionPrompt";
import { PermissionPrompt } from "../components/chat/PermissionPrompt";
import { useClearPendingUnread, useAddPendingAction, useRemovePendingAction } from "../providers/SessionActivityProvider";

type StreamPart = { type: "text" | "thinking" | "tool"; text: string; toolState?: string; toolCallID?: string; toolInput?: unknown; toolOutput?: string };


export function ChatPage() {
  const { workspaceId, sessionId } = useParams();
  const navigate = useNavigate();
  const [localMessages, setLocalMessages] = useState<Message[]>([]);
  // sessionErrors holds error messages surfaced by session.error SSE events.
  // Kept separate from localMessages so they survive between send and idle.
  // Cleared in reconcileOnIdle (session goes idle → history is authoritative)
  // and on session change.
  const [sessionErrors, setSessionErrors] = useState<Message[]>([]);
  const queryClient = useQueryClient();

  useEffect(() => {
    setLocalMessages([]);
    setSessionErrors([]);
    setSseStreamParts([]);
    setServerBusy(false);
    setRetryStatus(null);
    sseHasDrivenBusy.current = false;
    setPendingQuestions([]);
    setPendingPermissions([]);
    // Reset compaction state on session switch to prevent false positives:
    // prevContextUsedRef from the old session would otherwise be compared against
    // the new session's first contextUsed value, triggering spurious compaction banners.
    prevContextUsedRef.current = undefined;
    setCompactionDetected(false);
  }, [sessionId]);

  const { data: status } = useWorkspaceStatus(workspaceId);

  const { data: workspaceName } = useQuery({
    queryKey: ["workspaces"],
    queryFn: () => workspacesApi.list(),
    select: (data) => {
      const ws = data.items?.find((w) => w.id === workspaceId);
      return ws?.name ?? (workspaceId ? `workspace-${workspaceId.slice(0, 8)}` : "");
    },
  });

  const { data: activeWorkspaceData } = useQuery({
    queryKey: ["workspaces"],
    queryFn: () => workspacesApi.list(),
    select: (data) => data.items?.find((w) => w.id === workspaceId),
  });

  const activateMutation = useActivateWorkspace();

  const isReady = status?.phase === "Active";
  const clearPendingUnread = useClearPendingUnread();
  const addPendingAction = useAddPendingAction();
  const removePendingAction = useRemovePendingAction();

  useEffect(() => {
    if (!workspaceId || !sessionId || !isReady) return;

    clearPendingUnread(sessionId);

    workspacesApi.markSessionSeen(workspaceId, sessionId).catch(() => {});

    queryClient.invalidateQueries({ queryKey: ["sessions", workspaceId] });
  }, [sessionId, workspaceId, isReady]); // eslint-disable-line react-hooks/exhaustive-deps

  const prevSessionRef = useRef<{ wsId: string; sId: string } | null>(null);
  const markSeenDebounceRef = useRef<ReturnType<typeof setTimeout>>(undefined);

  useEffect(() => {
    if (prevSessionRef.current) {
      const { wsId, sId } = prevSessionRef.current;
      if (markSeenDebounceRef.current) clearTimeout(markSeenDebounceRef.current);
      markSeenDebounceRef.current = setTimeout(() => {
        workspacesApi.markSessionSeen(wsId, sId).catch(() => {});
      }, 1000);
    }

    prevSessionRef.current = workspaceId && sessionId ? { wsId: workspaceId, sId: sessionId } : null;

    return () => {
      if (markSeenDebounceRef.current) clearTimeout(markSeenDebounceRef.current);
    };
  }, [sessionId, workspaceId]);

  // Subscribe to sessions query so lastSeenAt is reactive: re-renders when
  // the sessions list refetches (e.g. after mark-seen invalidates the query).
  const { data: lastSeenAt } = useQuery({
    queryKey: ["sessions", workspaceId],
    queryFn: () => workspacesApi.getSessions(workspaceId!),
    enabled: !!workspaceId && !!sessionId,
    select: (sessions) => sessions.find((s) => s.id === sessionId)?.lastSeenAt,
    staleTime: 30_000,
    notifyOnChangeProps: ["data"],
  });

  // Reactive subscription to sessions list for context_used.
  // Uses the same query key as the Sidebar's sessions query so no extra fetch is made.
  // staleTime:Infinity prevents re-fetching (Sidebar/useSessions owns the fetch lifecycle).
  // notifyOnChangeProps:["data"] limits re-renders to data changes only.
  // We find the active session from the full list in the render body (not via `select`)
  // to avoid TanStack Query's structural-sharing optimisation dropping updates.
  const { data: sessionsListData } = useQuery({
    queryKey: ["sessions", workspaceId],
    queryFn: () => workspacesApi.getSessions(workspaceId!),
    enabled: !!workspaceId,
    staleTime: Infinity,
    notifyOnChangeProps: ["data"],
  });
  const activeSessionData = sessionsListData?.find((s) => s.id === sessionId);

  // Current model for prompt injection — subscribes to the same cache key that
  // ModelSelector populates. enabled:!!workspaceId (not gated on isReady) so
  // it fires at the same time as ModelSelector's query and shares the cache.
  // staleTime matches ModelSelector so no duplicate re-fetches are triggered.
  // notifyOnChangeProps keeps re-renders minimal.
  const { data: modelsData } = useQuery({
    queryKey: ["models", workspaceId],
    queryFn: () => workspacesApi.listModels(workspaceId!),
    enabled: !!workspaceId,
    staleTime: 10_000,
    placeholderData: keepPreviousData,
    notifyOnChangeProps: ["data"],
  });

  // [ws-timing] Log every phase change and the moment isReady flips true.
  // prevPhaseRef tracks the last seen phase so we only log on actual changes.
  const prevPhaseRef = useRef<string | undefined>(undefined);
  useEffect(() => {
    const phase = status?.phase;
    if (phase !== prevPhaseRef.current) {
      wsLog("ui.phase_changed", workspaceId,
        `prev=${prevPhaseRef.current ?? "none"} → next=${phase ?? "none"}`);
      if (phase === "Active" && prevPhaseRef.current !== "Active") {
        wsLog("ui.workspace_ready", workspaceId,
          "spinner dismissed — chat UI now visible");
      }
      prevPhaseRef.current = phase;
    }
  }, [status?.phase, workspaceId]);

  const createSessionMutation = useMutation({
    mutationFn: (wsId: string) => sessionsApi.create(wsId, "New chat"),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["sessions", workspaceId] });
      if (workspaceId && data.sessionId) {
        navigate(`/chat/${workspaceId}/${data.sessionId}`, { replace: true });
      }
    },
  });

  useEffect(() => {
    if (isReady && workspaceId && !sessionId && !createSessionMutation.isPending) {
      createSessionMutation.mutate(workspaceId);
    }
  }, [isReady, workspaceId, sessionId]); // eslint-disable-line react-hooks/exhaustive-deps

  // activeWorkspaceId gates history fetching, chat, and session hooks on the
  // workspace being Active — these all require a reachable pod.
  //
  // sseWorkspaceId is NOT gated on isReady. SSE connects as soon as the
  // workspace page loads so that workspace.phase events (including the
  // Creating→Active transition) are received and drive the status invalidation
  // that dismisses the spinner. The backend SSE endpoint accepts connections
  // for non-Active workspaces (verified: returns 200 for Suspended).
  //
  // Without this separation, the SSE connection only opens after the workspace
  // is already Active, making the transition detection entirely dependent on
  // polling. See worklog 0132 and the frontend timing analysis for the full
  // root-cause trace.
  const activeWorkspaceId = isReady ? workspaceId : undefined;
  const sseWorkspaceId = workspaceId;
  const { data: history, isLoading: historyLoading, fetchNextPage, hasNextPage, isFetchingNextPage } = useMessageHistory(activeWorkspaceId, sessionId);

  // US-15.1: Derive serverBusy from workspace status
  const sessionStatus = status?.sessions?.find((s) => s.id === sessionId);
  const [serverBusy, setServerBusy] = useState(false);
  // Track whether SSE has taken over serverBusy (to avoid status poll overriding SSE)
  const sseHasDrivenBusy = useRef(false);

  // Real-time context_used from session.next.step.ended SSE events.
  // The ref map is updated synchronously on each event; setContextVersion triggers
  // a re-render so contextUsedForDisplay reads the new ref value.
  const contextBySessionRef = useRef<Map<string, number>>(new Map());
  const [contextVersion, setContextVersion] = useState(0);

  // Derive the current session's context_used: SSE real-time value takes precedence
  // over the durable DB value from the sessions list query (cold-start fallback).
  // contextVersion is intentionally read to make this block reactive when SSE fires.
  const contextUsedForDisplay: number | undefined = (() => {
    void contextVersion; // consumed to trigger re-evaluation when SSE updates the ref
    const realtimeValue = contextBySessionRef.current.get(sessionId ?? "");
    if (realtimeValue !== undefined) return realtimeValue;
    return activeSessionData?.contextUsed ?? undefined;
  })();

  // Compaction indicator — detect when contextUsed drops >50% (opencode auto-compact).
  // Uses useLayoutEffect (runs synchronously after DOM update, before paint) so that
  // prevContextUsedRef is always up-to-date before the next render's comparison.
  const prevContextUsedRef = useRef<number | undefined>(undefined);
  const [compactionDetected, setCompactionDetected] = useState(false);
  useLayoutEffect(() => {
    const cur = contextUsedForDisplay;
    const prev = prevContextUsedRef.current;
    if (prev != null && cur != null && prev > 0 && cur < prev * 0.5) {
      setCompactionDetected(true);
    }
    if (cur != null) {
      prevContextUsedRef.current = cur;
    }
  }, [contextUsedForDisplay]);

  // US-16.11: Pending input requests from the agent
  const [pendingQuestions, setPendingQuestions] = useState<QuestionRequest[]>([]);
  const [pendingPermissions, setPendingPermissions] = useState<PermissionRequest[]>([]);

  const queue = useMessageQueue(activeWorkspaceId, sessionId);

  const idCounterRef = useRef(0);

  // Sync serverBusy from status poll (on mount / after invalidation)
  // Only applies when SSE hasn't already driven the state
  useEffect(() => {
    if (sessionStatus && !sseHasDrivenBusy.current) {
      setServerBusy(sessionStatus.status === "busy");
    }
  }, [sessionStatus]);


  const { send, abort, streaming, localStreaming, notifySessionIdle, error: chatError, clearError, atCapRetryAfter, clearAtCap, streamTimedOut, clearStreamTimedOut } = useChatStream(activeWorkspaceId, sessionId, serverBusy);
  const [retryStatus, setRetryStatus] = useState<RetryStatus | null>(null);
  const sessionTitle = useSessionTitle(activeWorkspaceId, sessionId, isReady, streaming);

  // US-15.3: Compute historyPartIds from fetched history for boundary detection
  const historyPartIds = useRef<Set<string>>(new Set());
  useEffect(() => {
    const ids = new Set<string>();
    if (history) {
      for (const msg of history) {
        for (const part of msg.parts) {
          if (part.id) ids.add(part.id);
        }
      }
    }
    historyPartIds.current = ids;
  }, [history]);

  // US-15.4: Reconnect mode — active when page loads into a busy session
  const isReconnectMode = useRef(false);
  const knownLivePartIds = useRef<Set<string>>(new Set());

  // Enter reconnect mode when session is busy on mount (serverBusy from status poll)
  useEffect(() => {
    if (serverBusy && !localStreaming) {
      isReconnectMode.current = true;
    }
  }, [serverBusy, localStreaming]);

  const [sessionWasInterrupted, setSessionWasInterrupted] = useState(false);
  const hasAutoAbortedRef = useRef(false);

  // Reset reconnect state on session change
  useEffect(() => {
    isReconnectMode.current = false;
    hasAutoAbortedRef.current = false;
    knownLivePartIds.current.clear();
    setSessionWasInterrupted(false);
    // S36.4: Reset compaction state when navigating to a different session
    prevContextUsedRef.current = undefined;
    setCompactionDetected(false);
  }, [sessionId]);

  // US-15.5: Reconcile on idle — fetch authoritative history and clear streaming state
  const reconcileOnIdle = useCallback(async () => {
    if (!workspaceId || !sessionId) return;
    try {
      queryClient.setQueryData(["messages", workspaceId, sessionId], (old: unknown) => {
        if (!old) return old;
        const inf = old as { pages: unknown[]; pageParams: unknown[] };
        return { pages: inf.pages.slice(0, 1), pageParams: inf.pageParams.slice(0, 1) };
      });
      await queryClient.refetchQueries({ queryKey: ["messages", workspaceId, sessionId] });
      const freshHistory = queryClient.getQueryData<{ pages: Array<{ messages: Message[] }> }>(
        ["messages", workspaceId, sessionId],
      );
      const msgs = freshHistory?.pages.flatMap((p) => p.messages) ?? [];
      if (msgs.length > 0) {
        setSseStreamParts([]);
        setLocalMessages([]);
      }
      setSessionErrors([]);
      isReconnectMode.current = false;
      knownLivePartIds.current.clear();
      sentTextRef.current = "";
      activePartTypeRef.current = null;
      currentThinkingIdxRef.current = -1;
      currentTextIdxRef.current = -1;
      if (freshHistory) {
        queue.reconcile(msgs);
      }
    } catch {
    }
  }, [workspaceId, sessionId, queryClient, queue]);

  // Auto-abort sessions that are stuck on a question/permission tool that opencode
  // lost from its queue (e.g. due to opencode restarting while a question was pending).
  //
  // Trigger: reconnect mode (busy on page load) + history has loaded + last assistant
  // message ends with a question or permission tool in "running" state + no pending
  // questions/permissions arrived via SSE (meaning opencode's queue is empty).
  //
  // After abort we reconcile history and surface an "interrupted" banner.
  useEffect(() => {
    if (!isReconnectMode.current) return;
    if (!workspaceId || !sessionId) return;
    if (!history || history.length === 0) return;
    if (hasAutoAbortedRef.current) return;
    // If SSE already delivered the question/permission, don't abort — let the user answer.
    if (pendingQuestions.length > 0 || pendingPermissions.length > 0) return;

    const lastAssistant = [...history].reverse().find((m) => m.role === "assistant");
    if (!lastAssistant) return;

    const stuckTool = lastAssistant.parts.find(
      (p) =>
        p.type === "tool_use" &&
        p.toolState === "running" &&
        (p.text?.startsWith("question") || p.text?.startsWith("permission")),
    );
    if (!stuckTool) return;

    hasAutoAbortedRef.current = true;
    workspacesApi.abortSession(workspaceId, sessionId)
      .then(() => { setSessionWasInterrupted(true); reconcileOnIdle(); })
      .catch(() => { setSessionWasInterrupted(true); reconcileOnIdle(); });
  }, [workspaceId, sessionId, history, pendingQuestions, pendingPermissions, reconcileOnIdle]);
  const hasAutoRenamedRef = useRef(false);
  useEffect(() => {
    if (!sessionTitle || !workspaceName || !workspaceId || hasAutoRenamedRef.current) return;
    // Skip temporary opencode titles (e.g. "New session - 2026-05-27T23:03:56.256Z")
    if (/^New session\s*-\s*\d{4}-/.test(sessionTitle)) return;
    // Detect auto-generated name: adjective-noun-number OR "New session - <timestamp>"
    const isAutoName = /^[a-z]+-[a-z]+-\d+$/.test(workspaceName) ||
      /^New session\s*-\s*\d{4}-/.test(workspaceName);
    if (isAutoName) {
      hasAutoRenamedRef.current = true;
      workspacesApi.renameWorkspace(workspaceId, sessionTitle).then(() => {
        queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      });
    }
  }, [sessionTitle, workspaceName, workspaceId, queryClient]);
  const [sseStreamParts, setSseStreamParts] = useState<StreamPart[]>([]);
  // Store the text the user just sent so we can strip the user echo from
  // the SSE stream. Opencode echoes the user's message as the first
  // message.part.updated event(s) before the assistant response begins.
  const sentTextRef = useRef<string>("");
  // Tracks which buffer to route message.part.delta events to.
  const activePartTypeRef = useRef<"user-echo" | "reasoning" | "text" | null>(null);
  const currentThinkingIdxRef = useRef<number>(-1);
  const currentTextIdxRef = useRef<number>(-1);

  const parseStreamEvent = useCallback((event: OpenCodeEvent, currentSessionId: string) => {
    let payload = event.data as Record<string, unknown> | undefined;
    if (!payload) return;

    if (!payload.type && payload.payload && typeof payload.payload === "object") {
      payload = payload.payload as Record<string, unknown>;
    }

    if (!payload?.type) return;

    const props = payload.properties as Record<string, unknown> | undefined;
    if (!props) return;

    const eventSessionId = (props.sessionID as string) || (props.session_id as string);
    if (eventSessionId && eventSessionId !== currentSessionId) return;

    // US-15.4: Boundary detection gate — in reconnect mode, ignore events for parts already in history
    if (isReconnectMode.current) {
      if (payload.type === "message.part.updated") {
        const part = props.part as Record<string, unknown> | undefined;
        const partId = part?.id as string | undefined;
        if (partId && historyPartIds.current.has(partId)) {
          return; // Already rendered from history
        }
        if (partId) {
          knownLivePartIds.current.add(partId);
        }
      } else if (payload.type === "message.part.delta") {
        const partId = props.partID as string | undefined;
        if (partId && historyPartIds.current.has(partId)) {
          return; // Delta for a history part — ignore
        }
        if (partId && !knownLivePartIds.current.has(partId)) {
          return; // Orphan delta — ignore
        }
      }
    }

    if (payload.type === "message.part.delta") {
      const delta = props.delta as string | undefined;
      if (!delta) return;

      const target = activePartTypeRef.current;
      if (target === "reasoning" || target === "text") {
        const expectedType = target === "reasoning" ? "thinking" : "text";
        setSseStreamParts((prev) => {
          if (prev.length === 0) return prev;
          const last: StreamPart | undefined = prev[prev.length - 1];
          if (!last || last.type !== expectedType) return prev;
          return [...prev.slice(0, -1), { type: last.type, text: last.text + delta }];
        });
      }
      // "user-echo" and null: discard
    } else if (payload.type === "message.part.updated") {
      const part = props.part as Record<string, unknown> | undefined;
      if (!part) return;

      const partType = part.type as string | undefined;
      if (partType === "reasoning" || partType === "thinking") {
        activePartTypeRef.current = "reasoning";
        const text = typeof part.text === "string" ? part.text : "";
        if (text) {
          // Snapshot: update the current thinking block by tracked index
          const idx = currentThinkingIdxRef.current;
          setSseStreamParts((prev) => {
            if (idx >= 0 && idx < prev.length && prev[idx]!.type === "thinking") {
              const updated = [...prev];
              updated[idx] = { type: "thinking", text };
              return updated;
            }
            // Fallback: append if no tracked block
            return [...prev, { type: "thinking", text }];
          });
        } else {
          // Empty text = new thinking block starting; track its index
          setSseStreamParts((prev) => {
            currentThinkingIdxRef.current = prev.length;
            return [...prev, { type: "thinking", text: "" }];
          });
        }
      } else if (partType === "text") {
        const text = typeof part.text === "string" ? part.text : "";
        // Detect user echo
        if (sentTextRef.current && text === sentTextRef.current) {
          activePartTypeRef.current = "user-echo";
        } else if (sentTextRef.current && text.startsWith(sentTextRef.current)) {
          activePartTypeRef.current = "text";
          const stripped = text.slice(sentTextRef.current.length);
          const idx = currentTextIdxRef.current;
          setSseStreamParts((prev) => {
            if (idx >= 0 && idx < prev.length && prev[idx]!.type === "text") {
              const updated = [...prev];
              updated[idx] = { type: "text", text: stripped };
              return updated;
            }
            return [...prev, { type: "text", text: stripped }];
          });
        } else {
          activePartTypeRef.current = "text";
          if (text) {
            const idx = currentTextIdxRef.current;
            setSseStreamParts((prev) => {
              if (idx >= 0 && idx < prev.length && prev[idx]!.type === "text") {
                const updated = [...prev];
                updated[idx] = { type: "text", text };
                return updated;
              }
              return [...prev, { type: "text", text }];
            });
          } else {
            // Empty = new text block starting; track its index
            setSseStreamParts((prev) => {
              currentTextIdxRef.current = prev.length;
              return [...prev, { type: "text", text: "" }];
            });
          }
        }
      } else if (partType === "tool" || partType === "tool_use" || partType === "tool_call") {
        // opencode ToolPart: { type:"tool", tool:"bash", callID:"...", state:{status,input,output,title} }
        const toolName = (part.tool as string) || (part.name as string) || "";
        const state = part.state as Record<string, unknown> | undefined;
        const toolState = (state?.status as string) || "";
        const title = (state?.title as string) || "";
        const displayText = title ? `${toolName}: ${title}` : toolName;
        const callID = (part.callID as string) || undefined;
        const toolInput = state?.input;
        const toolOutput = (state?.output as string) || undefined;
        setSseStreamParts((prev) => {
          // If this is an update to an existing tool call (same callID), update in place
          if (callID) {
            const existingIdx = prev.findIndex((p: StreamPart) => p.type === "tool" && p.toolCallID === callID);
            if (existingIdx >= 0) {
              const updated = [...prev];
              // Preserve original tool name if current event doesn't have one
              const existingName = prev[existingIdx]!.text.split(":")[0] || "";
              const effectiveName = toolName || existingName;
              const effectiveText = title ? `${effectiveName}: ${title}` : effectiveName;
              updated[existingIdx] = { type: "tool", text: effectiveText, toolState, toolCallID: callID, toolInput, toolOutput };
              return updated;
            }
          }
          return [...prev, { type: "tool", text: displayText, toolState, toolCallID: callID, toolInput, toolOutput }];
        });
        activePartTypeRef.current = null;
      }
      // step-start, step-finish: don't change routing or parts

    }
  }, []);

  const handleSSEEvent = useCallback((data: unknown) => {
    const event = data as WorkspaceStreamEvent;
    if (!event?.type) return;

    if (event.type === "workspace.phase") {
      queue.onPhaseChange(event.phase);
    }

    if (event.type === "session.status" && workspaceId) {
      queryClient.invalidateQueries({ queryKey: ["sessions", workspaceId] });
      if (event.session_id === sessionId) {
        if (event.status === "idle") {
          sseHasDrivenBusy.current = true;
          notifySessionIdle(event.session_id);
          setServerBusy(false);
          setRetryStatus(null);
          clearStreamTimedOut();
          reconcileOnIdle();
          // US-16.12: Clear stale prompts on session idle
          setPendingQuestions([]);
          setPendingPermissions([]);
        } else if (event.status === "busy") {
          sseHasDrivenBusy.current = true;
          setServerBusy(true);
          setRetryStatus(null);
        }
      }
    } else if (event.type === "queue.update" && workspaceId) {
      const qe = (event.data ?? {}) as { event?: string; messageID?: string; error?: string };
      if (qe.event === "sent" && qe.messageID) {
        queue.markSent(qe.messageID);
      } else if (qe.event === "error" && qe.messageID) {
        queue.markError(qe.messageID, qe.error ?? "Send failed");
      }
    } else if (event.type === "opencode.event" && workspaceId) {
      const oe = event as OpenCodeEvent;
      // Handle session.updated — update sidebar title in real-time
      if (oe.event_type === "session.updated") {
        const payload = oe.data as Record<string, unknown> | undefined;
        const props = (payload?.properties ?? (payload?.payload && (payload.payload as Record<string, unknown>)?.properties)) as Record<string, unknown> | undefined;
        const sid = (props?.id as string) || (props?.sessionID as string);
        const title = props?.title as string | undefined;
        if (sid && title) {
          queryClient.setQueryData<SessionListItem[]>(["sessions", workspaceId], (old) => {
            if (!old) return old;
            return old.map((s) => s.id === sid ? { ...s, title } : s);
          });
        }
      }
      // Handle session.status inside opencode.event — this is where the full
      // retry payload lives. The proxy also synthesizes a string "busy" event
      // on the session.status channel for retry, but the rich retry fields
      // (attempt, message, next, action) only travel through this path.
      if (oe.event_type === "session.status" && sessionId) {
        const payload = oe.data as Record<string, unknown> | undefined;
        const props = (payload?.properties ?? (payload?.payload && (payload.payload as Record<string, unknown>)?.properties)) as Record<string, unknown> | undefined;
        const sid = (props?.sessionID as string) || (props?.id as string);
        if (sid === sessionId) {
          const statusObj = props?.status as Record<string, unknown> | undefined;
          if (statusObj?.type === "retry") {
            setRetryStatus({
              attempt: typeof statusObj.attempt === "number" ? statusObj.attempt : 1,
              message: typeof statusObj.message === "string" ? statusObj.message : "",
              next: typeof statusObj.next === "number" ? statusObj.next : Date.now(),
              action: statusObj.action as RetryStatus["action"],
            });
          }
        }
      }
      // Handle session.next.step.ended — update context_used in real time.
      // The proxy also persists this to session_index (DB) for cold-start.
      // Here we update the in-memory ref map so the DiskUsageBar reflects the
      // new value immediately without waiting for the next sessions poll.
      if (oe.event_type === "session.next.step.ended") {
        const payload = oe.data as Record<string, unknown> | undefined;
        const props = (payload?.properties ?? (payload?.payload && (payload.payload as Record<string, unknown>)?.properties)) as Record<string, unknown> | undefined;
        const sid = props?.sessionID as string | undefined;
        const tokens = props?.tokens as Record<string, unknown> | undefined;
        if (sid && tokens) {
          const input = typeof tokens.input === "number" ? tokens.input : 0;
          const cache = tokens.cache as Record<string, unknown> | undefined;
          const cacheRead = typeof cache?.read === "number" ? cache.read : 0;
          const cacheWrite = typeof cache?.write === "number" ? cache.write : 0;
          const promptTokens = input + cacheRead + cacheWrite;
          contextBySessionRef.current.set(sid, promptTokens);
          setContextVersion((v) => v + 1);
        }
      }
      // Handle session.error — surface LLM/provider errors as a message bubble.
      // Written to sessionErrors (not localMessages) so reconcileOnIdle's
      // setLocalMessages([]) cannot wipe the error before the user sees it.
      if (oe.event_type === "session.error" && sessionId) {
        const payload = oe.data as Record<string, unknown> | undefined;
        const props = (payload?.properties ?? (payload?.payload && (payload.payload as Record<string, unknown>)?.properties)) as Record<string, unknown> | undefined;
        const sid = (props?.sessionID as string) || (props?.id as string);
        if (sid === sessionId) {
          const err = props?.error as Record<string, unknown> | undefined;
          const errData = err?.data as Record<string, unknown> | undefined;
          const errName = err?.name as string | undefined;
          const rawMessage = errData?.message as string | undefined;

          // Map known error names to actionable user-facing messages.
          let text: string;
          if (errName === "ContextOverflowError") {
            text = "Context limit reached — type /compact to summarize the conversation and continue";
          } else if (errName === "MessageOutputLengthError") {
            text = "Response was too long for this model's output limit";
          } else if (errName === "ProviderAuthError") {
            const provider = errData?.providerID as string | undefined;
            text = provider
              ? `Authentication failed for ${provider} — check your credentials`
              : (rawMessage ?? "Authentication failed — check your credentials");
          } else {
            text = rawMessage ?? errName ?? "Agent error";
          }

          setSessionErrors((prev) => [...prev, {
            id: `error-${++idCounterRef.current}`,
            role: "assistant",
            parts: [{ type: "error" as const, text: `⚠️ ${text}` }],
          }]);
          // US-16.12: Clear stale prompts on session error
          setPendingQuestions([]);
          setPendingPermissions([]);
        }
      }
      // Route streaming events to the active session parser
      if (sessionId) {
        parseStreamEvent(oe, sessionId);
      }
    } else if (event.type === "agent.question") {
      const req = event.data as QuestionRequest;
      const eventRoot = req.root_session_id ?? req.session_id;
      if (eventRoot === sessionId || req.session_id === sessionId) {
        setPendingQuestions((prev) => prev.some((q) => q.id === req.id) ? prev : [...prev, req]);
      }
      addPendingAction(workspaceId ?? "", req.session_id, req.id);
    } else if (event.type === "agent.question.resolved") {
      const { request_id } = event.data as { request_id: string };
      setPendingQuestions((prev) => prev.filter((q) => q.id !== request_id));
      removePendingAction(request_id);
    } else if (event.type === "agent.permission") {
      const req = event.data as PermissionRequest;
      const eventRoot = req.root_session_id ?? req.session_id;
      if (eventRoot === sessionId || req.session_id === sessionId) {
        setPendingPermissions((prev) => prev.some((p) => p.id === req.id) ? prev : [...prev, req]);
      }
      addPendingAction(workspaceId ?? "", req.session_id, req.id);
    } else if (event.type === "agent.permission.resolved") {
      const { request_id } = event.data as { request_id: string };
      setPendingPermissions((prev) => prev.filter((p) => p.id !== request_id));
      removePendingAction(request_id);
    }
  }, [queryClient, workspaceId, sessionId, parseStreamEvent, notifySessionIdle, reconcileOnIdle, queue, addPendingAction, removePendingAction]);

  // US-15.2: On SSE reconnect, re-poll status to catch missed transitions
  const handleSSEReconnect = useCallback(() => {
    if (workspaceId) {
      sseHasDrivenBusy.current = false;
      queryClient.invalidateQueries({ queryKey: ["workspace-status", workspaceId] });
    }
  }, [queryClient, workspaceId]);

  // Connect SSE unconditionally (even before workspace is Active) so we can
  // detect the Pending→Active phase transition and auto-create a session.
  useEventStream(sseWorkspaceId, handleSSEEvent, { onReconnect: handleSSEReconnect });

  // doSendNow MUST be defined before the early return below.
  // Placing any hook after an early return violates the Rules of Hooks — React
  // throws error #310 ("Rendered more hooks than during the previous render").
  const doSendNow = (text: string) => {
    // Resolve current model selection into opencode's PromptInput.model format.
    // currentModel is the flat model ID stored in the DB (e.g. "glm-5.1", never
    // "provider/model"). The backend resolves the providerID and returns it as
    // currentModelProviderID. Fall back to a find() on the models array for
    // older API responses that don't include currentModelProviderID, or when
    // the backend detected a collision (currentModelProviderID === "").
    const currentModelRef = (() => {
      const id = modelsData?.currentModel;
      if (!id) return undefined;
      const providerID =
        modelsData?.currentModelProviderID ||
        modelsData?.models?.find((m) => m.id === id)?.providerID;
      if (!providerID) return undefined;
      return { providerID, modelID: id };
    })();

    setSseStreamParts([]);
    sentTextRef.current = text;
    activePartTypeRef.current = null;
    currentThinkingIdxRef.current = -1;
    currentTextIdxRef.current = -1;
    isReconnectMode.current = false;
    knownLivePartIds.current.clear();
    const userMsg: Message = {
      id: `local-${++idCounterRef.current}`,
      role: "user",
      parts: [{ type: "text", text }],
      createdAt: new Date().toISOString(),
    };
    setLocalMessages((prev) => [...prev, userMsg]);
    // Note: we deliberately do NOT add the assistant response to
    // localMessages here. The streaming bubble shows it during streaming,
    // and reconcileOnIdle's history refetch is authoritative once idle.
    // Adding it here causes a race with reconcileOnIdle: if reconcile's
    // refetch resolves first (clears localMessages, populates history),
    // then this onComplete re-adds the assistant message → it renders
    // twice (once from history, once from localMessages).
    // The user message stays in localMessages until reconcileOnIdle clears
    // it (after history catches up), preserving optimistic UX.
    send(text, (_msg: Message) => {
      reconcileOnIdle();
    }, currentModelRef);
  };

  const allMessages = [...(history ?? []), ...localMessages, ...sessionErrors];

  if (!workspaceId) {
    return (
      <div className="flex h-full items-center justify-center text-muted-foreground">
        <p>Select a workspace to start chatting</p>
      </div>
    );
  }

  const isSuspended = status?.phase === "Suspended";
  const isTransitioning = !status?.phase || status?.phase === "Pending" || status?.phase === "Creating" || status?.phase === "Resuming" || status?.phase === "Suspending";
  const phaseLabel = status?.phase ? status.phase.toLowerCase() : "loading";

  const handleSend = (text: string) => {
    // If busy, hold the message locally — it will be sent when the session
    // next goes idle (matching TUI serialized queue behavior).
    if (serverBusy || streaming) {
      queue.enqueue(text);
      return;
    }
    doSendNow(text);
  };

  const sessionDisplayName = sessionTitle || "New chat";
  const kebabItems: KebabMenuItem[] = [
    {
      label: "Copy link",
      onClick: () => navigator.clipboard.writeText(`${window.location.origin}/chat/${workspaceId}/${sessionId}`),
    },
    {
      label: "Rename session",
      onClick: () => {
        const name = window.prompt("Session name:", sessionDisplayName);
        if (name && name.trim() && workspaceId && sessionId) {
          workspacesApi.renameSession(workspaceId, sessionId, name.trim()).then(() => {
            queryClient.invalidateQueries({ queryKey: ["sessions", workspaceId] });
            queryClient.invalidateQueries({ queryKey: ["session-title", workspaceId, sessionId] });
          });
        }
      },
    },
    {
      label: "Delete session",
      onClick: () => {
        if (window.confirm("Delete this session?") && workspaceId && sessionId) {
          workspacesApi.deleteSession(workspaceId, sessionId)
            .catch((err) => {
              if (err?.status !== 404) throw err;
            })
            .then(() => {
              queryClient.invalidateQueries({ queryKey: ["sessions", workspaceId] });
              navigate(`/chat/${workspaceId}`);
            })
            .catch(() => {
              window.alert("Failed to delete session.");
            });
        }
      },
      destructive: true,
    },
  ];

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b border-border px-4 py-2">
        <h2 className="text-sm font-semibold truncate">
          <span className="text-muted-foreground">{workspaceName}</span>
          <span className="text-muted-foreground/50 mx-1">/</span>
          <span>{sessionDisplayName}</span>
        </h2>
        <div className="flex items-center gap-2">
          {isReady && workspaceId && (
            <ModelSelector workspaceId={workspaceId} disabled={!isReady} />
          )}
          <KebabMenu items={kebabItems} footer={[
            ...(status?.agentHealth?.agentVersion ? [`opencode v${status.agentHealth.agentVersion}`] : []),
            ...(status?.imageTag ? [`image: ${status.imageTag}`] : []),
          ]} />
        </div>
      </div>

      {isSuspended && (
        <SuspendedBanner
          workspaceName={workspaceId}
          onActivate={() => {
            wsLog("ui.user_clicked_activate", workspaceId);
            activateMutation.mutate(workspaceId);
          }}
          activating={activateMutation.isPending}
        />
      )}

      {isReady && activeWorkspaceData?.agentNeedsRefresh && (
        <AgentReloadBanner
          workspaceId={workspaceId!}
          workspaceName={workspaceName || "this workspace"}
          credentialsPendingSince={activeWorkspaceData.credentialsPendingSince}
          onReloaded={() => {
            queryClient.invalidateQueries({ queryKey: ["workspaces"] });
          }}
        />
      )}

      {isTransitioning && (
        <div className="flex flex-1 flex-col items-center justify-center gap-4 text-muted-foreground">
          <Spinner size="lg" />
          <div className="text-center">
            <p className="text-base font-medium">Workspace is {phaseLabel}...</p>
            <p className="mt-1 text-sm">This usually takes a few seconds</p>
          </div>
        </div>
      )}

      {isReady && (
        <HealthBanner
          credentialState={status?.credentialState}
          agentHealth={status?.agentHealth}
        />
      )}

      {isReady && sessionWasInterrupted && (
        <div className="flex items-center gap-2 border-b border-yellow-200 bg-yellow-50 px-4 py-2 text-xs text-yellow-800 dark:border-yellow-800 dark:bg-yellow-950 dark:text-yellow-200">
          <span>⚠ Session was interrupted while waiting for your input. You can continue in this session or start a new one.</span>
          <button
            className="ml-auto shrink-0 underline hover:no-underline"
            onClick={() => setSessionWasInterrupted(false)}
          >
            Dismiss
          </button>
        </div>
      )}

      {isReady && (
        <DiskUsageBar
          diskUsedBytes={status?.diskUsedBytes}
          diskTotalBytes={status?.diskTotalBytes}
          memoryUsedBytes={status?.memoryUsedBytes}
          memoryTotalBytes={status?.memoryTotalBytes}
          contextUsed={contextUsedForDisplay ?? 0}
          contextTotal={status?.contextTotal ?? 0}
        />
      )}

      {compactionDetected && (
        <div className="flex items-center justify-between gap-2 border-b border-blue-500/30 bg-blue-500/10 px-4 py-2 text-xs text-blue-700 dark:text-blue-300">
          <span>Context compacted — conversation history was summarised to free context space.</span>
          <button onClick={() => setCompactionDetected(false)} className="underline hover:no-underline shrink-0">Dismiss</button>
        </div>
      )}

      {atCapRetryAfter !== null && (
        <AtCapBanner retryAfter={atCapRetryAfter} onRetry={clearAtCap} />
      )}

      {retryStatus && (
        <SessionRetryBanner status={retryStatus} />
      )}

      {streamTimedOut && (
        <div className="flex items-center justify-between gap-2 border-b border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          <span>Response interrupted — the connection timed out</span>
          <button onClick={clearStreamTimedOut} className="underline hover:no-underline">Dismiss</button>
        </div>
      )}

      {chatError && (
        <div className="flex items-center justify-between gap-2 border-b border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          <span>{chatError}</span>
          <button onClick={clearError} className="underline hover:no-underline">Dismiss</button>
        </div>
      )}

      {historyLoading || createSessionMutation.isPending ? (
        <div className="flex flex-1 items-center justify-center">
          <Spinner />
        </div>
      ) : (
        <div className="flex-1 min-h-0">
          <ChatView
            messages={allMessages}
            streaming={streaming}
            streamParts={sseStreamParts}
            disabled={!workspaceId || !sessionId || isSuspended}
            onSend={handleSend}
            onAbort={() => {
              if (workspaceId && sessionId) {
                workspacesApi.abortSession(workspaceId, sessionId);
              }
              abort();
              queue.clear();
            }}
            onLoadEarlier={() => fetchNextPage()}
            hasOlderMessages={hasNextPage}
            loadingOlder={isFetchingNextPage}
            queuedMessages={queue.queuedMessages}
            onQueueRetry={queue.retry}
            onQueueDismiss={queue.dismiss}
            models={modelsData?.models}
            lastSeenAt={lastSeenAt}
            prompts={
              (pendingQuestions.length > 0 || pendingPermissions.length > 0) ? (
                <>
                  {pendingQuestions.map((q) => (
                    <QuestionPrompt key={q.id} workspaceId={workspaceId!} request={q}
                      onResolved={() => setPendingQuestions((prev) => prev.filter((x) => x.id !== q.id))} />
                  ))}
                  {pendingPermissions.map((p) => (
                    <PermissionPrompt key={p.id} workspaceId={workspaceId!} request={p}
                      onResolved={() => setPendingPermissions((prev) => prev.filter((x) => x.id !== p.id))} />
                  ))}
                </>
              ) : undefined
            }
          />
        </div>
      )}
    </div>
  );
}
