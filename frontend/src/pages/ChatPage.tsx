import { useCallback, useEffect, useRef, useState } from "react";
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
import type { Message, SessionListItem, WorkspaceStreamEvent, OpenCodeEvent } from "../api/types";

type StreamPart = { type: "text" | "thinking" | "tool"; text: string; toolState?: string; toolCallID?: string; toolInput?: unknown; toolOutput?: string };


export function ChatPage() {
  const { workspaceId, sessionId } = useParams();
  const navigate = useNavigate();
  const [localMessages, setLocalMessages] = useState<Message[]>([]);
  const queryClient = useQueryClient();

  useEffect(() => { setLocalMessages([]); }, [sessionId]);

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
  const { data: history, isLoading: historyLoading } = useMessageHistory(activeWorkspaceId, sessionId);
  const { send, abort, streaming, notifySessionIdle, error: chatError, clearError, atCapRetryAfter, clearAtCap } = useChatStream(activeWorkspaceId, sessionId);
  const sessionTitle = useSessionTitle(activeWorkspaceId, sessionId, isReady, streaming);

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
  const sseStreamPartsRef = useRef<StreamPart[]>([]);
  useEffect(() => { sseStreamPartsRef.current = sseStreamParts; }, [sseStreamParts]);
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
      if (event.status === "idle" && event.session_id) {
        notifySessionIdle(event.session_id);
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
        }
      }
      // Route streaming events to the active session parser
      if (sessionId) {
        parseStreamEvent(oe, sessionId);
      }
    }
  }, [queryClient, workspaceId, sessionId, parseStreamEvent, notifySessionIdle]);

  useEventStream(activeWorkspaceId, handleSSEEvent);

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
    const userMsg: Message = {
      id: `local-${Date.now()}`,
      role: "user",
      parts: [{ type: "text", text }],
    };
    setLocalMessages((prev) => [...prev, userMsg]);
    send(text, (assistantMsg) => {
      // Prefer streaming parts (preserves thinking/tool structure) over
      // history-only message which may strip them
      const streamedParts = sseStreamPartsRef.current.filter(p => p.text || p.type === "tool");
      const finalMsg: Message = streamedParts.length > 0
        ? {
            id: assistantMsg.id,
            role: "assistant",
            parts: streamedParts.map(p => ({
              type: p.type === "tool" ? "tool_use" as const : p.type,
              text: p.text,
              ...(p.toolState ? { toolState: p.toolState } : {}),
              ...(p.toolInput != null ? { input: p.toolInput } : {}),
              ...(p.toolOutput ? { toolOutput: p.toolOutput } : {}),
            })),
          }
        : assistantMsg;
      setLocalMessages((prev) => [...prev, finalMsg]);
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
        <KebabMenu items={kebabItems} />
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
          />
        </div>
      )}
    </div>
  );
}
