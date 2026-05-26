import { useCallback, useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useWorkspaceStatus } from "../hooks/useWorkspaces";
import { useMessageHistory } from "../hooks/useMessageHistory";
import { useActivateWorkspace } from "../hooks/useActivateWorkspace";
import { useChatStream } from "../hooks/useChatStream";
import { useEventStream } from "../hooks/useEventStream";
import { useSessionTitle } from "../hooks/useSessionTitle";
import { ChatView } from "../components/chat/ChatView";
import { SuspendedBanner } from "../components/chat/SuspendedBanner";
import { AtCapBanner } from "../components/chat/AtCapBanner";
import { Spinner } from "../components/ui/Spinner";
import { sessionsApi } from "../api/sessions";
import type { Message, WorkspaceStreamEvent } from "../api/types";

export function ChatPage() {
  const { workspaceId, sessionId } = useParams();
  const navigate = useNavigate();
  const [localMessages, setLocalMessages] = useState<Message[]>([]);
  const [atCap, setAtCap] = useState<number | null>(null);
  const queryClient = useQueryClient();

  useEffect(() => { setLocalMessages([]); }, [sessionId]);

  const { data: status } = useWorkspaceStatus(workspaceId);
  const activateMutation = useActivateWorkspace();

  // Workspace is ready when phase is Active
  const isReady = status?.phase === "Active";

  // Auto-create session when workspace is ready but no session selected
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
  const { send, abort, streaming, streamedText, error: chatError, clearError } = useChatStream(activeWorkspaceId, sessionId);

  // Fetch the session title from the opencode agent and keep it fresh.
  // When a title arrives (or changes), useSessionTitle invalidates ["sessions", workspaceId]
  // so the sidebar updates automatically.
  useSessionTitle(activeWorkspaceId, sessionId, isReady, streaming);

  // SSE event stream — handles workspace phase changes and session status events.
  const handleSSEEvent = useCallback((data: unknown) => {
    const event = data as WorkspaceStreamEvent;
    if (!event?.type) return;

    if (event.type === "workspace.phase" && workspaceId) {
      // Phase changed: refresh both the sidebar list and the status query so the
      // icon and the transitioning/suspended banners reflect the new state.
      queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      queryClient.invalidateQueries({ queryKey: ["workspace-status", workspaceId] });
    } else if (event.type === "session.status" && workspaceId) {
      // Session idle/busy: refresh the session list so active indicators update.
      queryClient.invalidateQueries({ queryKey: ["sessions", workspaceId] });
    }
  }, [queryClient, workspaceId]);

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
    setAtCap(null);
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

  return (
    <div className="flex h-full flex-col">
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

      {atCap !== null && (
        <AtCapBanner retryAfter={atCap} onRetry={() => setAtCap(null)} />
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
          streamedText={streamedText}
          disabled={!workspaceId || !sessionId || isSuspended}
          onSend={handleSend}
          onAbort={abort}
        />
      )}
    </div>
  );
}
