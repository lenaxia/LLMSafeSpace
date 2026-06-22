import { Lock } from "lucide-react";

interface Props {
  message?: string;
}

export const DEFAULT_READ_ONLY_MESSAGE =
  "Subtasks are view-only. Continue the conversation in the parent session.";

export function ReadOnlyBanner({ message = DEFAULT_READ_ONLY_MESSAGE }: Props) {
  return (
    <div
      role="status"
      className="flex items-center gap-2 border-t border-border bg-muted/50 px-4 py-3 text-sm text-muted-foreground"
    >
      <Lock className="h-4 w-4 shrink-0" />
      <span>{message}</span>
    </div>
  );
}
