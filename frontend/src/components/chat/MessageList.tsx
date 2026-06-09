import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import type { Message } from "../../api/types";
import type { ModelInfo } from "../../api/workspaces";
import { MessageBubble } from "./MessageBubble";
import { ArrowDown, Loader2 } from "lucide-react";

interface Props {
  messages: Message[];
  streaming?: boolean;
  streamingBubble?: ReactNode;
  onLoadEarlier?: () => void;
  hasOlderMessages?: boolean;
  loadingOlder?: boolean;
  models?: ModelInfo[];
}

const SCROLL_THRESHOLD = 60;

const MemoizedBubble = memo(MessageBubble);

export function MessageList({ messages, streaming, streamingBubble, onLoadEarlier, hasOlderMessages, loadingOlder, models }: Props) {
  const modelMap = useMemo(() => new Map(models?.map(m => [m.id, m.name])), [models]);
  const scrollRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const [showJumpButton, setShowJumpButton] = useState(false);
  const stickToBottom = useRef(true);

  const rafId = useRef(0);
  const checkIfAtBottom = useCallback(() => {
    if (rafId.current) return;
    rafId.current = requestAnimationFrame(() => {
      rafId.current = 0;
      const el = scrollRef.current;
      if (!el) return;
      const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < SCROLL_THRESHOLD;
      stickToBottom.current = atBottom;
      setShowJumpButton((prev) => {
        const shouldShow = !atBottom;
        return prev === shouldShow ? prev : shouldShow;
      });
    });
  }, []);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    el.addEventListener("scroll", checkIfAtBottom, { passive: true });
    return () => {
      el.removeEventListener("scroll", checkIfAtBottom);
      if (rafId.current) cancelAnimationFrame(rafId.current);
    };
  }, [checkIfAtBottom]);

  const scrollToBottom = useCallback(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
    stickToBottom.current = true;
    setShowJumpButton(false);
  }, []);

  useLayoutEffect(() => {
    if (stickToBottom.current) {
      const el = scrollRef.current;
      if (el) el.scrollTop = el.scrollHeight;
    }
  }, [messages.length]);

  useEffect(() => {
    if (streaming) {
      scrollToBottom();
    }
  }, [streaming, scrollToBottom]);

  useEffect(() => {
    if (!streaming || !stickToBottom.current) return;
    const el = scrollRef.current;
    if (!el) return;
    let frameId = 0;
    const observer = new MutationObserver(() => {
      if (!stickToBottom.current || frameId) return;
      frameId = requestAnimationFrame(() => {
        frameId = 0;
        if (stickToBottom.current && el) {
          el.scrollTop = el.scrollHeight;
        }
      });
    });
    observer.observe(el, { childList: true, subtree: true, characterData: true });
    return () => {
      observer.disconnect();
      if (frameId) cancelAnimationFrame(frameId);
    };
  }, [streaming]);

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
        className="h-full overflow-y-auto overflow-x-hidden overscroll-contain"
        role="log"
        aria-live="polite"
        aria-label="Chat messages"
      >
        <div className="flex flex-col gap-2 p-2">
          {hasOlderMessages && (
            <div className="flex justify-center py-3">
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
          )}

          {messages.map((msg) => (
            <MemoizedBubble
              key={msg.id}
              message={msg}
              modelName={msg.modelID ? (modelMap.get(msg.modelID) || msg.modelID.split("/").pop()) : undefined}
            />
          ))}

          {streamingBubble && streamingBubble}

          <div ref={bottomRef} />
        </div>
      </div>

      {showJumpButton && (
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
