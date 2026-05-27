import { useEffect, useRef } from "react";
import type { ReactNode } from "react";
import type { Message } from "../../api/types";
import { MessageBubble } from "./MessageBubble";

interface Props {
  messages: Message[];
  streaming?: boolean;
  streamingBubble?: ReactNode;
}

export function MessageList({ messages, streaming, streamingBubble }: Props) {
  const bottomRef = useRef<HTMLDivElement>(null);

  // Auto-scroll to bottom whenever messages change or streaming state changes
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages.length, streaming]);

  if (messages.length === 0 && !streamingBubble) {
    return (
      <div className="flex flex-1 items-center justify-center text-muted-foreground">
        <p className="text-sm">Send a message to start the conversation</p>
      </div>
    );
  }

  return (
    <div className="flex-1 overflow-y-auto" role="log" aria-live="polite" aria-label="Chat messages">
      <div className="flex flex-col gap-1 p-2">
        {messages.map((msg) => (
          <div key={msg.id} className="p-1">
            <MessageBubble message={msg} />
          </div>
        ))}
        {streamingBubble && (
          <div className="p-1">
            {streamingBubble}
          </div>
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}
