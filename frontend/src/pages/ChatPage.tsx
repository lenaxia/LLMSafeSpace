import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { workspacesApi } from "../api/workspaces";
import { useWorkspaceStatus } from "../hooks/useWorkspaces";
import { useMessageHistory } from "../hooks/useMessageHistory";
import { useActivateWorkspace } from "../hooks/useActivateWorkspace";
import { useChatStream } from "../hooks/useChatStream";
import { useEventStream } from "../hooks/useEventStream";
import { useSessionTitle } from "../hooks/useSessionTitle";
import { ChatView } from "../components/chat/ChatView";
import { SuspendedBanner } from "../components/chat/SuspendedBanner";
import { AtCapBanner } from "../components/chat/AtCapBanner";
import { HealthBanner } from "../components/chat/HealthBanner";
import { DiskUsageBar } from "../components/workspace/DiskUsageBar";
import { Spinner } from "../components/ui/Spinner";
import { KebabMenu } from "../components/ui/KebabMenu";
import type { KebabMenuItem } from "../components/ui/KebabMenu";
import { sessionsApi } from "../api/sessions";
import type { Message, SessionListItem, WorkspaceStreamEvent, OpenCodeEvent, QuestionRequest, PermissionRequest } from "../api/types";
import { QuestionPrompt } from "../components/chat/QuestionPrompt";
import { PermissionPrompt } from "../components/chat/PermissionPrompt";

type StreamPart = { type: "text" | "thinking" | "tool"; text: string; toolState?: string; toolCallID?: string; toolInput?: unknown; toolOutput?: string };


export function ChatPage() {
  const { workspaceId, sessionId } = useParams();
  const navigate = useNavigate();
  const [localMessages, setLocalMessages] = useState<Message[]>([]);
  const queryClient = useQueryClient();

  useEffect(() => {
    setLocalMessages([]);
    setSseStreamParts([]);
    setServerBusy(false);
    sseHasDrivenBusy.current = false;
    setPendingQuestions([]);
    setPendingPermissions([]);
  }, [sessionId]);

  const { data: status } = useWorkspaceStatus(workspaceId);

  const { data: workspaces } = useQuery({
    queryKey: ["workspaces"],
    queryFn: () => workspacesApi.list(),
  });

  const workspace = workspaces?.items?.find((w) => w.id === workspaceId);
  const workspaceName = workspace?.name ?? (workspaceId ? `workspace-${workspaceId.slice(0, 8)}` : "");

  const activateMutation = useActivateWorkspace();

  const isReady = status?.phase === "Active";

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

  const activeWorkspaceId = isReady ? workspaceId : undefined;
  const { data: historyPages, isLoading: historyLoading, fetchNextPage, hasNextPage, isFetchingNextPage } = useMessageHistory(activeWorkspaceId, sessionId);
  const history = useMemo(() => {
    if (!historyPages?.pages) return [];
    // Pages are fetched newest-page-first (cursor goes backwards), so reverse
    // pages to get chronological order. Messages within each page are newest-first
    // (opencode orders by desc(time_created)), so reverse those too.
    return [...historyPages.pages].reverse().flatMap((p) => [...p.messages].reverse());
  }, [historyPages?.pages]);

  // US-15.1: Derive serverBusy from workspace status
  const sessionStatus = status?.sessions?.find((s) => s.id === sessionId);
  const [serverBusy, setServerBusy] = useState(false);
  // Track whether SSE has taken over serverBusy (to avoid status poll overriding SSE)
  const sseHasDrivenBusy = useRef(false);

  // US-16.11: Pending input requests from the agent
  const [pendingQuestions, setPendingQuestions] = useState<QuestionRequest[]>([]);
  const [pendingPermissions, setPendingPermissions] = useState<PermissionRequest[]>([]);

  // Sync serverBusy from status poll (on mount / after invalidation)
  // Only applies when SSE hasn't already driven the state
  useEffect(() => {
    if (sessionStatus && !sseHasDrivenBusy.current) {
      setServerBusy(sessionStatus.status === "busy");
    }
  }, [sessionStatus]);


  const { send, abort, streaming, localStreaming, notifySessionIdle, error: chatError, clearError, atCapRetryAfter, clearAtCap } = useChatStream(activeWorkspaceId, sessionId, serverBusy);
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

  // Reset reconnect state on session change
  useEffect(() => {
    isReconnectMode.current = false;
    knownLivePartIds.current.clear();
  }, [sessionId]);

  // US-15.5: Reconcile on idle — fetch authoritative history and clear streaming state
  const reconcileOnIdle = useCallback(async () => {
    if (!workspaceId || !sessionId) return;
    try {
      // Keep only the first page (avoids loading flash), drop older cached pages,
      // then refetch the first page for authoritative state after the turn.
      queryClient.setQueryData(["messages", workspaceId, sessionId], (old: unknown) => {
        if (!old) return old;
        const inf = old as { pages: unknown[]; pageParams: unknown[] };
        return { pages: inf.pages.slice(0, 1), pageParams: inf.pageParams.slice(0, 1) };
      });
      await queryClient.refetchQueries({ queryKey: ["messages", workspaceId, sessionId] });
      setSseStreamParts([]);
      // History is now authoritative for this session — clear localMessages
      // so the merged view (history + localMessages) does not double-render
      // every completed turn. localMessages is only useful as optimistic UI
      // during an in-flight send; once idle reconcile lands, history has
      // the canonical record.
      setLocalMessages([]);
      isReconnectMode.current = false;
      knownLivePartIds.current.clear();
      sentTextRef.current = "";
      activePartTypeRef.current = null;
      currentThinkingIdxRef.current = -1;
      currentTextIdxRef.current = -1;
    } catch {
      // History fetch failed — keep streaming parts AND localMessages visible
      // so the user doesn't lose context.
    }
  }, [workspaceId, sessionId, queryClient]);

  // Auto-rename workspace from first session title if name is still auto-generated
  const hasAutoRenamedRef = useRef(false);
  useEffect(() => {
    console.log("[Workspace] sessionTitle:", sessionTitle, "workspace:", workspace?.name, "pattern match:", workspace?.name ? /^[a-z]+-[a-z]+-\d+$/.test(workspace.name) : "n/a");
    if (!sessionTitle || !workspace || !workspaceId || hasAutoRenamedRef.current) return;
    // Skip temporary opencode titles (e.g. "New session - 2026-05-27T23:03:56.256Z")
    if (/^New session\s*-\s*\d{4}-/.test(sessionTitle)) return;
    // Detect auto-generated name: adjective-noun-number OR "New session - <timestamp>"
    const isAutoName = /^[a-z]+-[a-z]+-\d+$/.test(workspace.name) ||
      /^New session\s*-\s*\d{4}-/.test(workspace.name);
    if (isAutoName) {
      hasAutoRenamedRef.current = true;
      console.log("[Workspace] auto-renaming to:", sessionTitle);
      workspacesApi.renameWorkspace(workspaceId, sessionTitle).then(() => {
        queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      });
    }
  }, [sessionTitle, workspace, workspaceId, queryClient]);
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

    if (event.type === "workspace.phase" && workspaceId) {
      queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      queryClient.invalidateQueries({ queryKey: ["workspace-status", workspaceId] });
    } else if (event.type === "session.status" && workspaceId) {
      queryClient.invalidateQueries({ queryKey: ["sessions", workspaceId] });
      if (event.session_id === sessionId) {
        if (event.status === "idle") {
          sseHasDrivenBusy.current = true;
          notifySessionIdle(event.session_id);
          setServerBusy(false);
          reconcileOnIdle();
          // US-16.12: Clear stale prompts on session idle
          setPendingQuestions([]);
          setPendingPermissions([]);
        } else if (event.status === "busy") {
          sseHasDrivenBusy.current = true;
          setServerBusy(true);
        }
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
      // Handle session.error — surface LLM/provider errors as a message bubble
      if (oe.event_type === "session.error" && sessionId) {
        const payload = oe.data as Record<string, unknown> | undefined;
        const props = (payload?.properties ?? (payload?.payload && (payload.payload as Record<string, unknown>)?.properties)) as Record<string, unknown> | undefined;
        const sid = (props?.sessionID as string) || (props?.id as string);
        if (sid === sessionId) {
          const err = props?.error as Record<string, unknown> | undefined;
          const errData = err?.data as Record<string, unknown> | undefined;
          const message = (errData?.message as string) || (err?.name as string) || "Agent error";
          setLocalMessages((prev) => [...prev, {
            id: `error-${Date.now()}`,
            role: "assistant",
            parts: [{ type: "error" as const, text: `⚠️ ${message}` }],
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
      // US-16.11: Agent question event
      const req = event.data as QuestionRequest;
      if (req.session_id === sessionId) {
        setPendingQuestions((prev) => prev.some((q) => q.id === req.id) ? prev : [...prev, req]);
      }
    } else if (event.type === "agent.question.resolved") {
      const { request_id } = event.data as { request_id: string };
      setPendingQuestions((prev) => prev.filter((q) => q.id !== request_id));
    } else if (event.type === "agent.permission") {
      const req = event.data as PermissionRequest;
      if (req.session_id === sessionId) {
        setPendingPermissions((prev) => prev.some((p) => p.id === req.id) ? prev : [...prev, req]);
      }
    } else if (event.type === "agent.permission.resolved") {
      const { request_id } = event.data as { request_id: string };
      setPendingPermissions((prev) => prev.filter((p) => p.id !== request_id));
    }
  }, [queryClient, workspaceId, sessionId, parseStreamEvent, notifySessionIdle, reconcileOnIdle]);

  // US-15.2: On SSE reconnect, re-poll status to catch missed transitions
  const handleSSEReconnect = useCallback(() => {
    if (workspaceId) {
      sseHasDrivenBusy.current = false;
      queryClient.invalidateQueries({ queryKey: ["workspace-status", workspaceId] });
    }
  }, [queryClient, workspaceId]);

  useEventStream(activeWorkspaceId, handleSSEEvent, { onReconnect: handleSSEReconnect });

  const allMessages = [...(history ?? []), ...localMessages];

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
    setSseStreamParts([]);
    sentTextRef.current = text;
    activePartTypeRef.current = null;
    currentThinkingIdxRef.current = -1;
    currentTextIdxRef.current = -1;
    isReconnectMode.current = false;
    knownLivePartIds.current.clear();
    const userMsg: Message = {
      id: `local-${Date.now()}`,
      role: "user",
      parts: [{ type: "text", text }],
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
    send(text, () => {
      // No-op: the assistant message is delivered via the streaming bubble
      // (live) and via history (after reconcile). We don't need to manage
      // it in localMessages.
    });
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
          queryClient.invalidateQueries({ queryKey: ["sessions", workspaceId] });
          navigate(`/chat/${workspaceId}`);
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
        <KebabMenu items={kebabItems} footer={[
          ...(status?.agentHealth?.agentVersion ? [`opencode v${status.agentHealth.agentVersion}`] : []),
          ...(status?.imageTag ? [`image: ${status.imageTag}`] : []),
        ]} />
      </div>

      {isSuspended && (
        <SuspendedBanner
          workspaceName={workspaceId}
          onActivate={() => activateMutation.mutate(workspaceId)}
          activating={activateMutation.isPending}
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

      {isReady && status?.diskTotalBytes && (
        <DiskUsageBar usedBytes={status.diskUsedBytes} totalBytes={status.diskTotalBytes} />
      )}

      {atCapRetryAfter !== null && (
        <AtCapBanner retryAfter={atCapRetryAfter} onRetry={clearAtCap} />
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
            onAbort={abort}
            onLoadEarlier={() => fetchNextPage()}
            hasOlderMessages={hasNextPage}
            loadingOlder={isFetchingNextPage}
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
