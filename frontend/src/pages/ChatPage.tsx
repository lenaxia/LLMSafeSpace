import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { workspacesApi } from "../api/workspaces";
import { useWorkspaceStatus } from "../hooks/useWorkspaces";
import { useMessageHistory } from "../hooks/useMessageHistory";
import { useActivateWorkspace } from "../hooks/useActivateWorkspace";
import { useChatStream } from "../hooks/useChatStream";
import { useEventStream } from "../hooks/useEventStream";
import { ChatView } from "../components/chat/ChatView";
import { SuspendedBanner } from "../components/chat/SuspendedBanner";
import { AtCapBanner } from "../components/chat/AtCapBanner";
import { HealthBanner } from "../components/chat/HealthBanner";
import { Spinner } from "../components/ui/Spinner";
import { KebabMenu } from "../components/ui/KebabMenu";
import type { KebabMenuItem } from "../components/ui/KebabMenu";
import { sessionsApi } from "../api/sessions";
import type { Message, WorkspaceStreamEvent, OpenCodeEvent } from "../api/types";

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
  const workspaceName = workspace?.name ?? workspaceId ?? "";

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
  const [sseStreamText, setSseStreamText] = useState("");
  const [sseThinkingText, setSseThinkingText] = useState("");
  // Store the text the user just sent so we can strip the user echo from
  // the SSE stream. Opencode echoes the user's message as the first
  // message.part.updated event(s) before the assistant response begins.
  const sentTextRef = useRef<string>("");
  // Tracks which buffer to route message.part.delta events to.
  // Opencode sends all deltas with field:"text" regardless of whether they
  // are thinking or response content. The only signal is the preceding
  // message.part.updated partType.
  const activePartTypeRef = useRef<"user-echo" | "reasoning" | "text" | null>(null);

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

      // Route delta based on the active part type set by the last part.updated
      const target = activePartTypeRef.current;
      console.log("[SSE]", "delta", "route:", target, "text:", delta.slice(0, 60));
      if (target === "reasoning") {
        setSseThinkingText((prev) => prev + delta);
      } else if (target === "text") {
        setSseStreamText((prev) => prev + delta);
      }
      // "user-echo" and null: discard
    } else if (payload.type === "message.part.updated") {
      const part = props.part as Record<string, unknown> | undefined;
      if (!part) return;

      const partType = part.type as string | undefined;
      const prevRoute = activePartTypeRef.current;

      if (partType === "reasoning" || partType === "thinking") {
        activePartTypeRef.current = "reasoning";
        // If snapshot has text, set it (overwrites accumulated deltas)
        if (typeof part.text === "string" && part.text) {
          setSseThinkingText(part.text);
        }
      } else if (partType === "text") {
        const text = typeof part.text === "string" ? part.text : "";
        // Detect user echo: first text part.updated matching sent text
        if (sentTextRef.current && text === sentTextRef.current) {
          activePartTypeRef.current = "user-echo";
        } else if (sentTextRef.current && text.startsWith(sentTextRef.current)) {
          // Echo prefix case: strip and switch to text mode
          activePartTypeRef.current = "text";
          const stripped = text.slice(sentTextRef.current.length);
          if (stripped) setSseStreamText(stripped);
        } else {
          activePartTypeRef.current = "text";
          // Snapshot with content sets the text directly
          if (text) setSseStreamText(text);
        }
      } else if (partType === "tool" || partType === "tool_use" || partType === "tool_call") {
        activePartTypeRef.current = null;
      }
      // step-start, step-finish, and other types: don't change routing

      console.log("[SSE]", "part.updated", "type:", partType, "route:", prevRoute, "→", activePartTypeRef.current, "text:", typeof part.text === "string" ? part.text.slice(0, 40) : "");
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
    } else if (event.type === "opencode.event" && sessionId) {
      parseStreamEvent(event as OpenCodeEvent, sessionId);
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
    setSseStreamText("");
    setSseThinkingText("");
    sentTextRef.current = text;
    activePartTypeRef.current = null;
    const userMsg: Message = {
      id: `local-${Date.now()}`,
      role: "user",
      parts: [{ type: "text", text }],
    };
    setLocalMessages((prev) => [...prev, userMsg]);
    send(text, (assistantMsg) => {
      setLocalMessages((prev) => [...prev, assistantMsg]);
    });
  };

  const kebabItems: KebabMenuItem[] = [
    {
      label: "Rename",
      onClick: () => {
        const name = window.prompt("Workspace name:", workspaceName);
        if (name && name.trim()) {
          workspacesApi.renameWorkspace(workspaceId, name.trim()).then(() => {
            queryClient.invalidateQueries({ queryKey: ["workspaces"] });
          });
        }
      },
    },
    {
      label: "Suspend",
      onClick: () => {
        workspacesApi.suspend(workspaceId).then(() => {
          queryClient.invalidateQueries({ queryKey: ["workspaces"] });
          queryClient.invalidateQueries({ queryKey: ["workspace-status", workspaceId] });
        });
      },
    },
    {
      label: "Delete",
      onClick: () => {
        if (window.confirm(`Delete workspace "${workspaceName}"?`)) {
          workspacesApi.deleteWorkspace(workspaceId).then(() => {
            queryClient.invalidateQueries({ queryKey: ["workspaces"] });
            navigate("/chat");
          });
        }
      },
      destructive: true,
    },
  ];

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b border-border px-4 py-2">
        <h2 className="text-sm font-semibold truncate">{workspaceName}</h2>
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
        <div className="flex items-center gap-2 border-b border-border bg-muted/50 px-4 py-3 text-sm text-muted-foreground">
          <Spinner size="sm" />
          <span>Workspace is {phaseLabel}...</span>
        </div>
      )}

      {isReady && (
        <HealthBanner
          credentialState={status?.credentialState}
          agentHealth={status?.agentHealth}
        />
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
        <ChatView
          messages={allMessages}
          streaming={streaming}
          streamedDisplayText={sseStreamText}
          streamedThinkingText={sseThinkingText}
          disabled={!workspaceId || !sessionId || isSuspended}
          onSend={handleSend}
          onAbort={abort}
        />
      )}
    </div>
  );
}
