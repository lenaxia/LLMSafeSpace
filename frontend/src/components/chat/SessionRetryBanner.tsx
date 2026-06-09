import { useEffect, useState } from "react";
import { RefreshCw } from "lucide-react";

interface RetryAction {
  reason: string;
  provider: string;
  title: string;
  message: string;
  label: string;
  link?: string;
}

export interface RetryStatus {
  attempt: number;
  message: string;
  next: number; // absolute epoch timestamp in ms (Date.now() + delay) when next attempt fires
  action?: RetryAction;
}

interface Props {
  status: RetryStatus;
}

export function SessionRetryBanner({ status }: Props) {
  // next is an absolute epoch ms timestamp — compute remaining duration
  const [remainingMs, setRemainingMs] = useState(() => Math.max(0, status.next - Date.now()));

  // Recompute remaining whenever a new retry status arrives (new attempt)
  useEffect(() => {
    setRemainingMs(Math.max(0, status.next - Date.now()));
  }, [status.next, status.attempt]);

  useEffect(() => {
    if (remainingMs <= 0) return;
    const interval = setInterval(() => {
      setRemainingMs((r) => Math.max(0, r - 500));
    }, 500);
    return () => clearInterval(interval);
  }, [remainingMs]);

  const remainingSec = Math.ceil(remainingMs / 1000);

  return (
    <div className="flex items-center justify-between gap-4 border-b border-yellow-500/30 bg-yellow-500/5 px-4 py-2 text-sm text-yellow-600 dark:text-yellow-400">
      <div className="flex items-center gap-2 min-w-0">
        <RefreshCw className="h-3.5 w-3.5 shrink-0 animate-spin" style={{ animationDuration: "2s" }} />
        <span className="truncate">
          {status.message || "Retrying"}
          {status.attempt > 1 && (
            <span className="text-yellow-500/70 dark:text-yellow-500/50"> · attempt {status.attempt}</span>
          )}
          {remainingSec > 0 && (
            <span className="text-yellow-500/70 dark:text-yellow-500/50"> · retrying in {remainingSec}s</span>
          )}
        </span>
      </div>
      {status.action?.link && (
        <a
          href={status.action.link}
          target="_blank"
          rel="noopener noreferrer"
          className="shrink-0 text-xs underline hover:no-underline whitespace-nowrap"
        >
          {status.action.label} →
        </a>
      )}
    </div>
  );
}
