import { useCallback, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import type { Message } from "../../api/types";
import { MessageBubble } from "./MessageBubble";
import { ArrowDown, Loader2 } from "lucide-react";

interface Props {
  messages: Message[];
  streaming?: boolean;
  streamingBubble?: ReactNode;
  onLoadEarlier?: () => void;
  hasOlderMessages?: boolean;
  loadingOlder?: boolean;
}

const SCROLL_THRESHOLD = 60;
const ESTIMATED_ROW_HEIGHT = 80;

export function MessageList({ messages, streaming, streamingBubble, onLoadEarlier, hasOlderMessages, loadingOlder }: Props) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const [isAtBottom, setIsAtBottom] = useState(true);
  const isAutoScrolling = useRef(false);

  type ListItem =
    | { type: "load-marker" }
    | { type: "message"; msg: Message }
    | { type: "streaming" }
    | { type: "bottom" };

  const allItems: ListItem[] = [
    ...(hasOlderMessages ? [{ type: "load-marker" as const }] : []),
    ...messages.map((m) => ({ type: "message" as const, msg: m })),
    ...(streamingBubble ? [{ type: "streaming" as const }] : []),
    { type: "bottom" as const },
  ];

  const virtualizer = useVirtualizer({
    count: allItems.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ESTIMATED_ROW_HEIGHT,
    overscan: 10,
    paddingEnd: 8,
  });

  const checkIfAtBottom = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < SCROLL_THRESHOLD;
    setIsAtBottom(atBottom);
  }, []);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    el.addEventListener("scroll", checkIfAtBottom, { passive: true });
    return () => el.removeEventListener("scroll", checkIfAtBottom);
  }, [checkIfAtBottom]);

  const scrollToBottom = useCallback(() => {
    isAutoScrolling.current = true;
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
    setIsAtBottom(true);
    setTimeout(() => { isAutoScrolling.current = false; }, 100);
  }, []);

  useEffect(() => {
    if (isAtBottom) {
      bottomRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [messages.length, streamingBubble, isAtBottom]);

  useEffect(() => {
    if (streaming) {
      scrollToBottom();
    }
  }, [streaming, scrollToBottom]);

  useEffect(() => {
    if (!streaming || !isAtBottom) return;
    const el = scrollRef.current;
    if (!el) return;
    const observer = new MutationObserver(() => {
      if (isAtBottom) {
        el.scrollTop = el.scrollHeight;
      }
    });
    observer.observe(el, { childList: true, subtree: true, characterData: true });
    return () => observer.disconnect();
  }, [streaming, isAtBottom]);

  if (messages.length === 0 && !streamingBubble) {
    return (
      <div className="flex flex-1 items-center justify-center text-muted-foreground">
        <p className="text-sm">Send a message to start the conversation</p>
      </div>
    );
  }

  return (
    <div className="relative flex-1 overflow-hidden">
      <div
        ref={scrollRef}
        className="h-full overflow-y-auto overscroll-contain"
        role="log"
        aria-live="polite"
        aria-label="Chat messages"
      >
        <div
          style={{ height: virtualizer.getTotalSize(), position: "relative" }}
        >
          {virtualizer.getVirtualItems().map((virtualItem) => {
            const item = allItems[virtualItem.index];
            if (!item) return null;

            if (item.type === "load-marker") {
              return (
                <div
                  key="load-marker"
                  style={{ height: virtualItem.size, transform: `translateY(${virtualItem.start}px)` }}
                  className="absolute left-0 right-0 flex justify-center py-3"
                >
                  {loadingOlder ? (
                    <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
                  ) : (
                    <button
                      onClick={onLoadEarlier}
                      className="rounded-md border border-border bg-background px-3 py-1.5 text-xs text-muted-foreground hover:bg-accent transition-colors"
                    >
                      Load earlier messages
                    </button>
                  )}
                </div>
              );
            }

            if (item.type === "streaming") {
              return (
                <div
                  key="streaming"
                  style={{ height: virtualItem.size, transform: `translateY(${virtualItem.start}px)` }}
                  className="absolute left-0 right-0 px-1"
                >
                  {streamingBubble}
                </div>
              );
            }

            if (item.type === "bottom") {
              return (
                <div
                  key="bottom"
                  ref={bottomRef}
                  style={{ height: virtualItem.size, transform: `translateY(${virtualItem.start}px)` }}
                  className="absolute left-0 right-0"
                />
              );
            }

            return (
              <div
                key={item.msg.id}
                style={{ height: virtualItem.size, transform: `translateY(${virtualItem.start}px)` }}
                className="absolute left-0 right-0 px-1"
              >
                <MessageBubble message={item.msg} />
              </div>
            );
          })}
        </div>
      </div>

      {!isAtBottom && (
        <button
          onClick={scrollToBottom}
          className="absolute bottom-4 left-1/2 -translate-x-1/2 flex items-center gap-1.5 rounded-full bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground shadow-lg hover:bg-primary/90 transition-opacity"
          aria-label="Scroll to bottom"
        >
          <ArrowDown className="h-3.5 w-3.5" />
          {streaming ? "Resume tailing" : "Jump to bottom"}
        </button>
      )}
    </div>
  );
}
