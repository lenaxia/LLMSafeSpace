import { useState } from "react";
import { useParams } from "react-router-dom";
import { useWorkspaceStatus, useWorkspaceSandboxes } from "../hooks/useWorkspaces";
import { useMessageHistory } from "../hooks/useMessageHistory";
import { useActivateWorkspace } from "../hooks/useActivateWorkspace";
import { useChatStream } from "../hooks/useChatStream";
import { MessageList } from "../components/chat/MessageList";
import { Composer } from "../components/chat/Composer";
import { SuspendedBanner } from "../components/chat/SuspendedBanner";
import { StreamingIndicator } from "../components/chat/StreamingIndicator";
import { Spinner } from "../components/ui/Spinner";
import type { Message } from "../api/types";

export function ChatPage() {
  const { workspaceId, sessionId } = useParams();
  const [localMessages, setLocalMessages] = useState<Message[]>([]);

  const { data: status } = useWorkspaceStatus(workspaceId);
  const { data: sandboxes } = useWorkspaceSandboxes(workspaceId);
  const activateMutation = useActivateWorkspace();

  const sandbox = sandboxes?.[0];
  const sandboxId = sandbox?.phase === "Running" ? sandbox.id : undefined;

  const { data: history, isLoading: historyLoading } = useMessageHistory(sandboxId, sessionId);
  const { send, streaming } = useChatStream(sandboxId, sessionId);

  const allMessages = [...(history ?? []), ...localMessages];

  if (!workspaceId) {
    return (
      <div className="flex h-full items-center justify-center text-muted-foreground">
        <p>Select a workspace to start chatting</p>
      </div>
    );
  }

  const isSuspended = status?.phase === "Suspended";
  const isTransitioning = status?.phase === "Resuming" || status?.phase === "Creating";

  const handleSend = (text: string) => {
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
          <span>Workspace is {status?.phase?.toLowerCase()}...</span>
        </div>
      )}

      {historyLoading ? (
        <div className="flex flex-1 items-center justify-center">
          <Spinner />
        </div>
      ) : (
        <MessageList messages={allMessages} streaming={streaming} />
      )}

      {streaming && <StreamingIndicator />}

      <Composer
        onSend={handleSend}
        disabled={!sandboxId || streaming || isSuspended}
        placeholder={isSuspended ? "Resume workspace to chat" : undefined}
      />
    </div>
  );
}
