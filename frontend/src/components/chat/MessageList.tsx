import { memo, useEffect, useRef } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import type { Message } from "../../api/types";
import { MessageBubble } from "./MessageBubble";

interface Props {
  messages: Message[];
  streaming?: boolean;
}

const MemoizedBubble = memo(MessageBubble, (prev, next) => prev.message.id === next.message.id);

export function MessageList({ messages, streaming }: Props) {
  const parentRef = useRef<HTMLDivElement>(null);

  const virtualizer = useVirtualizer({
    count: messages.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 80,
    overscan: 5,
  });

  // Auto-scroll to bottom on new messages or while streaming
  useEffect(() => {
    if (messages.length > 0) {
      virtualizer.scrollToIndex(messages.length - 1, { align: "end", behavior: "smooth" });
    }
  }, [messages.length, streaming, virtualizer]);

  if (messages.length === 0) {
    return (
      <div className="flex flex-1 items-center justify-center text-muted-foreground">
        <p className="text-sm">Send a message to start the conversation</p>
      </div>
    );
  }

  return (
    <div ref={parentRef} className="flex-1 overflow-y-auto" role="log" aria-live="polite" aria-label="Chat messages">
      <div
        style={{ height: `${virtualizer.getTotalSize()}px`, width: "100%", position: "relative" }}
      >
        {virtualizer.getVirtualItems().map((virtualItem) => {
          const msg = messages[virtualItem.index]!;
          return (
            <div
              key={msg.id}
              data-index={virtualItem.index}
              ref={virtualizer.measureElement}
              style={{
                position: "absolute",
                top: 0,
                left: 0,
                width: "100%",
                transform: `translateY(${virtualItem.start}px)`,
              }}
              className="p-2"
            >
              <MemoizedBubble message={msg} />
            </div>
          );
        })}
      </div>
    </div>
  );
}
