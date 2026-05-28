import { useCallback, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import type { Message } from "../../api/types";
import { MessageBubble } from "./MessageBubble";
import { ArrowDown } from "lucide-react";

interface Props {
  messages: Message[];
  streaming?: boolean;
  streamingBubble?: ReactNode;
}

const SCROLL_THRESHOLD = 60; // px from bottom to consider "at bottom"

export function MessageList({ messages, streaming, streamingBubble }: Props) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const [isAtBottom, setIsAtBottom] = useState(true);

  const scrollToBottom = useCallback(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
    setIsAtBottom(true);
  }, []);

  const checkIfAtBottom = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < SCROLL_THRESHOLD;
    setIsAtBottom(atBottom);
  }, []);

  // Detect user scroll
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    el.addEventListener("scroll", checkIfAtBottom, { passive: true });
    return () => el.removeEventListener("scroll", checkIfAtBottom);
  }, [checkIfAtBottom]);

  // Auto-scroll when new messages arrive or streaming content updates (only if at bottom)
  useEffect(() => {
    if (isAtBottom) {
      bottomRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [messages.length, streamingBubble, isAtBottom]);

  // Force scroll to bottom when streaming starts
  useEffect(() => {
    if (streaming) {
      scrollToBottom();
    }
  }, [streaming, scrollToBottom]);

  // Continuously tail during streaming if at bottom (for delta updates that don't change streamingBubble reference)
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
