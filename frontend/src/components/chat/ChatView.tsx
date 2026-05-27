import type { Message, MessagePart } from "../../api/types";
import { MessageList } from "./MessageList";
import { Composer } from "./Composer";
import { StreamingIndicator } from "./StreamingIndicator";
import { AbortSessionButton } from "./AbortSessionButton";
import { MessageBubble } from "./MessageBubble";

interface StreamingPart {
  type: "thinking" | "text" | "tool";
  text: string;
}

interface Props {
  messages: Message[];
  streaming: boolean;
  streamParts: StreamingPart[];
  disabled: boolean;
  onSend: (text: string) => void;
  onAbort: () => void;
}

export function ChatView({ messages, streaming, streamParts, disabled, onSend, onAbort }: Props) {
  const hasStreamedContent = streamParts.length > 0;

  const streamedMessageParts: MessagePart[] = streamParts.map((p) => ({
    type: p.type === "tool" ? "tool_use" as const : p.type,
    text: p.text,
  }));

  return (
    <div className="flex h-full flex-col">
      <div className="flex flex-1 flex-col overflow-hidden">
        <MessageList
          messages={messages}
          streaming={streaming}
          streamingBubble={
            streaming && hasStreamedContent ? (
              <MessageBubble
                message={{ id: "streaming", role: "assistant", parts: streamedMessageParts }}
                isStreaming
              />
            ) : undefined
          }
        />
        {streaming && <StreamingIndicator />}
      </div>

      {streaming && (
        <div className="flex justify-center py-2">
          <AbortSessionButton onAbort={onAbort} />
        </div>
      )}

      <Composer onSend={onSend} disabled={disabled || streaming} />
    </div>
  );
}
