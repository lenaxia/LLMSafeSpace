import { useEffect, useState } from "react";
import { AlertCircle } from "lucide-react";
import { Button } from "../ui/Button";

interface Props {
  retryAfter: number;
  onRetry: () => void;
}

export function AtCapBanner({ retryAfter, onRetry }: Props) {
  const [remaining, setRemaining] = useState(retryAfter);

  useEffect(() => {
    setRemaining(retryAfter);
  }, [retryAfter]);

  useEffect(() => {
    if (remaining <= 0) {
      onRetry();
      return;
    }
    const timer = setInterval(() => {
      setRemaining((r) => r - 1);
    }, 1000);
    return () => clearInterval(timer);
  }, [remaining, onRetry]);

  return (
    <div className="flex items-center justify-between gap-4 border-b border-border bg-destructive/5 px-4 py-3">
      <div className="flex items-center gap-2 text-sm text-destructive">
        <AlertCircle className="h-4 w-4" />
        <span>Session limit reached{remaining > 0 ? ` — retrying in ${remaining}s` : ""}</span>
      </div>
      <Button size="sm" variant="outline" onClick={onRetry}>
        Retry
      </Button>
    </div>
  );
}
