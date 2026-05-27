import type { Message, MessagePart } from "../../api/types";
import { MessageList } from "./MessageList";
import { Composer } from "./Composer";
import { StreamingIndicator } from "./StreamingIndicator";
import { AbortSessionButton } from "./AbortSessionButton";
import { MessageBubble } from "./MessageBubble";

interface Props {
  messages: Message[];
  streaming: boolean;
  streamedDisplayText: string;
  streamedThinkingText: string;
  disabled: boolean;
  onSend: (text: string) => void;
  onAbort: () => void;
}

export function ChatView({ messages, streaming, streamedDisplayText, streamedThinkingText, disabled, onSend, onAbort }: Props) {
  const hasStreamedContent = streamedDisplayText || streamedThinkingText;

  const streamedParts: MessagePart[] = [
    ...(streamedThinkingText ? [{ type: "thinking" as const, text: streamedThinkingText }] : []),
    ...(streamedDisplayText ? [{ type: "text" as const, text: streamedDisplayText }] : []),
  ];

  return (
    <div className="flex h-full flex-col">
      <div className="flex flex-1 flex-col overflow-hidden">
        <MessageList
          messages={messages}
          streaming={streaming}
          streamingBubble={
            streaming && hasStreamedContent ? (
              <MessageBubble
                message={{ id: "streaming", role: "assistant", parts: streamedParts }}
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
