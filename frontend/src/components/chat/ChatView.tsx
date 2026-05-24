import type { Message } from "../../api/types";
import { MessageList } from "./MessageList";
import { Composer } from "./Composer";
import { StreamingIndicator } from "./StreamingIndicator";
import { AbortSessionButton } from "./AbortSessionButton";
import { MessageBubble } from "./MessageBubble";

interface Props {
  messages: Message[];
  streaming: boolean;
  streamedText: string;
  disabled: boolean;
  onSend: (text: string) => void;
  onAbort: () => void;
}

export function ChatView({ messages, streaming, streamedText, disabled, onSend, onAbort }: Props) {
  return (
    <div className="flex h-full flex-col">
      <div className="flex flex-1 flex-col overflow-hidden">
        <MessageList messages={messages} streaming={streaming} />
        {streaming && streamedText && (
          <div className="px-4">
            <MessageBubble
              message={{ id: "streaming", role: "assistant", parts: [{ type: "text", text: streamedText }] }}
            />
          </div>
        )}
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
