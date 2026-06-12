import { useRef, useState } from "react";
import type { FormEvent, KeyboardEvent } from "react";
import { Send, Square } from "lucide-react";
import { Button } from "../ui/Button";
import { cn } from "../../lib/utils";
import { useUserSetting } from "../../hooks/useUserSettings";

interface Props {
  onSend: (text: string) => void;
  onAbort?: () => void;
  disabled?: boolean;
  streaming?: boolean;
  placeholder?: string;
}

export function Composer({ onSend, onAbort, disabled, streaming, placeholder = "Type a message..." }: Props) {
  const [text, setText] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const sendOnEnter = useUserSetting("sendOnEnter", true);

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    const trimmed = text.trim();
    if (!trimmed || disabled) return;
    onSend(trimmed);
    setText("");
    if (textareaRef.current) textareaRef.current.style.height = "auto";
  };

  const handleKeyDown = (e: KeyboardEvent) => {
    if (sendOnEnter) {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        handleSubmit(e);
      }
    } else {
      if (e.key === "Enter" && e.shiftKey) {
        e.preventDefault();
        handleSubmit(e);
      }
    }
  };

  const handleInput = () => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`;
  };

  const canSend = text.trim().length > 0 && !disabled;

  return (
    <form onSubmit={handleSubmit} className="border-t border-border p-4">
      <div className="flex items-end gap-2">
        <textarea
          ref={textareaRef}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={handleKeyDown}
          onInput={handleInput}
          placeholder={placeholder}
          disabled={disabled}
          rows={1}
          className={cn(
            "min-h-[44px] flex-1 resize-none rounded-md border border-input bg-background px-3 py-2 text-base placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50",
          )}
        />
        <Button type="submit" size="icon" disabled={!canSend} className="min-h-[44px] min-w-[44px]">
          <Send className="h-4 w-4" />
        </Button>
        {streaming && onAbort && (
          <Button
            type="button"
            size="icon"
            variant="destructive"
            className="min-h-[44px] min-w-[44px]"
            aria-label="Stop generating"
            onClick={(e) => { e.preventDefault(); onAbort(); }}
          >
            <Square className="h-4 w-4" />
          </Button>
        )}
      </div>
    </form>
  );
}
