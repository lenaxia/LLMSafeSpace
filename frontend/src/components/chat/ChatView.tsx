import type { Message, MessagePart } from "../../api/types";
import type { ModelInfo } from "../../api/workspaces";
import { MessageList } from "./MessageList";
import { Composer } from "./Composer";
import { StreamingIndicator } from "./StreamingIndicator";
import { MessageBubble } from "./MessageBubble";
import { QueueSection } from "./QueueSection";
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
  queuedCount?: number;
  queuedMessages?: QueuedMessage[];
  onQueueRetry?: (id: string) => void;
  onQueueDismiss?: (id: string) => void;
  models?: ModelInfo[];
}

export function ChatView({ messages, streaming, streamParts, disabled, onSend, onAbort, prompts, onLoadEarlier, hasOlderMessages, loadingOlder, queuedCount = 0, queuedMessages = [], onQueueRetry, onQueueDismiss, models }: Props) {
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
          onLoadEarlier={onLoadEarlier}
          hasOlderMessages={hasOlderMessages}
          loadingOlder={loadingOlder}
        />
        {streaming && <StreamingIndicator />}
      </div>

      {prompts && <div className="px-4">{prompts}</div>}

      {queuedMessages.length > 0 && onQueueRetry && onQueueDismiss && (
        <QueueSection
          messages={queuedMessages}
          onRetry={onQueueRetry}
          onDismiss={onQueueDismiss}
        />
      )}

      <Composer onSend={onSend} onAbort={onAbort} disabled={disabled} streaming={streaming} queuedCount={queuedCount} />
    </div>
  );
}
