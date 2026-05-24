import { Pause } from "lucide-react";
import { Button } from "../ui/Button";

interface Props {
  workspaceName: string;
  onActivate: () => void;
  activating?: boolean;
}

export function SuspendedBanner({ workspaceName, onActivate, activating }: Props) {
  return (
    <div className="flex items-center justify-between gap-4 border-b border-border bg-muted/50 px-4 py-3">
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Pause className="h-4 w-4" />
        <span><strong>{workspaceName}</strong> is suspended</span>
      </div>
      <Button size="sm" onClick={onActivate} disabled={activating}>
        {activating ? "Resuming..." : "Resume to chat"}
      </Button>
    </div>
  );
}
