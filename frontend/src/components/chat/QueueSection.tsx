import type { QueuedMessage } from "../../hooks/useMessageQueue";
import { cn } from "../../lib/utils";

interface Props {
  messages: QueuedMessage[];
  onRetry: (id: string) => void;
  onDismiss: (id: string) => void;
}

export function QueueSection({ messages, onRetry, onDismiss }: Props) {
  if (messages.length === 0) return null;

  return (
    <div className="border-t border-border bg-muted/30 px-3 py-2">
      <div className="flex flex-wrap gap-2">
        {messages.map((m) => (
          <div
            key={m.id}
            className={cn(
              "inline-flex max-w-xs items-center gap-1 rounded-full px-3 py-1 text-sm",
              m.status === "error"
                ? "bg-destructive/20 text-destructive"
                : "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-200",
            )}
          >
            {m.status === "error" ? (
              <>
                <s className="truncate">{m.text}</s>
                <button
                  type="button"
                  aria-label="Retry"
                  className="ml-1 shrink-0 font-medium hover:underline"
                  onClick={() => onRetry(m.id)}
                >
                  ↻
                </button>
                <button
                  type="button"
                  aria-label="Dismiss"
                  className="shrink-0 font-medium hover:underline"
                  onClick={() => onDismiss(m.id)}
                >
                  ✕
                </button>
              </>
            ) : (
              <span className="truncate">{m.text}</span>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
