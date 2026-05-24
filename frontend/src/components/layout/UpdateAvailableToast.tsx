import { RefreshCw, X } from "lucide-react";
import { Button } from "../ui/Button";

interface Props {
  onUpdate: () => void;
  onDismiss: () => void;
}

export function UpdateAvailableToast({ onUpdate, onDismiss }: Props) {
  return (
    <div className="fixed bottom-4 right-4 z-50 flex items-center gap-3 rounded-lg border border-border bg-card p-4 shadow-lg" role="alert">
      <span className="text-sm">Update available</span>
      <Button size="sm" onClick={onUpdate} aria-label="Reload to update">
        <RefreshCw className="mr-1 h-3 w-3" />
        Reload
      </Button>
      <button onClick={onDismiss} className="rounded p-1 hover:bg-accent" aria-label="Dismiss update">
        <X className="h-4 w-4" />
      </button>
    </div>
  );
}
