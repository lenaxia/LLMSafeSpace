import type { Message, MessagePart } from "../../api/types";
import type { ModelInfo } from "../../api/workspaces";
import { MessageList } from "./MessageList";
import { Composer } from "./Composer";
import { ReadOnlyBanner } from "./ReadOnlyBanner";
import { StreamingIndicator } from "./StreamingIndicator";
import { MessageBubble } from "./MessageBubble";
import { QueueSection } from "./QueueSection";
import { useIsMobile } from "../../hooks/useMediaQuery";
import type { QueuedMessage } from "../../hooks/useMessageQueue";

interface StreamingPart {
  type: "thinking" | "text" | "tool";
  text: string;
  toolState?: string;
  toolCallID?: string;
  toolInput?: unknown;
  toolOutput?: string;
}

interface Props {
  messages: Message[];
  streaming: boolean;
  streamParts: StreamingPart[];
  disabled: boolean;
  onSend: (text: string) => void;
  onAbort: () => void;
  prompts?: React.ReactNode;
  onLoadEarlier?: () => void;
  hasOlderMessages?: boolean;
  loadingOlder?: boolean;
  queuedMessages?: QueuedMessage[];
  onQueueRetry?: (id: string) => void;
  onQueueDismiss?: (id: string) => void;
  models?: ModelInfo[];
  lastSeenAt?: string;
  /**
   * When true, the chat is read-only: the composer and message queue are
   * hidden and a view-only banner is rendered in their place. Used for
   * subagent/subtask sessions, which are driven by their parent session and
   * must not be chatted with directly (helps enforce max session limits).
   */
  viewOnly?: boolean;
  viewOnlyMessage?: string;
}

export function ChatView({ messages, streaming, streamParts, disabled, onSend, onAbort, prompts, onLoadEarlier, hasOlderMessages, loadingOlder, queuedMessages = [], onQueueRetry, onQueueDismiss, models, lastSeenAt, viewOnly = false, viewOnlyMessage }: Props) {
  const isMobile = useIsMobile();
  const hasStreamedContent = streamParts.length > 0;

  const streamedMessageParts: MessagePart[] = streamParts.map((p) => ({
    type: p.type === "tool" ? "tool_use" as const : p.type,
    text: p.text,
    ...(p.toolState ? { toolState: p.toolState } : {}),
    ...(p.toolInput != null ? { input: p.toolInput } : {}),
    ...(p.toolOutput ? { toolOutput: p.toolOutput } : {}),
  }));

  return (
    <div className="flex h-full flex-col">
      <div className="flex flex-1 flex-col overflow-hidden">
        <MessageList
          messages={messages}
          streaming={streaming}
          models={models}
          streamingBubble={
            streaming && hasStreamedContent ? (
              <MessageBubble
                message={{ id: "streaming", role: "assistant", parts: streamedMessageParts }}
                isStreaming
              />
            ) : undefined
          }
          trailingPrompts={prompts}
          onLoadEarlier={onLoadEarlier}
          hasOlderMessages={hasOlderMessages}
          loadingOlder={loadingOlder}
          lastSeenAt={lastSeenAt}
        />
        {streaming && <StreamingIndicator />}
      </div>

      {viewOnly ? (
        <ReadOnlyBanner message={viewOnlyMessage} />
      ) : (
        <>
          {onQueueRetry && onQueueDismiss && (
            <QueueSection
              messages={queuedMessages}
              onRetry={onQueueRetry}
              onDismiss={onQueueDismiss}
              isMobile={isMobile}
            />
          )}

          <Composer onSend={onSend} onAbort={onAbort} disabled={disabled} streaming={streaming} />
        </>
      )}
    </div>
  );
}
