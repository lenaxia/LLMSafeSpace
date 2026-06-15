import { MessageSquare } from "lucide-react";
import type { SessionListItem } from "../../api/types";
import { cn } from "../../lib/utils";
import { sessionDisplayTitle } from "../../lib/names";
import { formatRelativeTime } from "../../lib/time";

interface Props {
  session: SessionListItem;
  selected: boolean;
  onSelect: () => void;
}

export function SessionItem({ session, selected, onSelect }: Props) {
  const title = sessionDisplayTitle(session.title, session.lastMessageAt);

  return (
    <button
      onClick={onSelect}
      className={cn(
        "flex w-full items-center gap-2 rounded-md px-3 py-1.5 text-left text-sm transition-colors",
        selected ? "bg-accent text-accent-foreground" : "hover:bg-accent/50",
      )}
      aria-current={selected ? "page" : undefined}
    >
      <MessageSquare className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
      <span className="flex-1 truncate">{title}</span>
      {session.status === "active" && (
        <span className="h-1.5 w-1.5 rounded-full bg-blue-500" aria-label="Active" />
      )}
      {session.lastMessageAt && (
        <span className="text-xs text-muted-foreground">
          {formatRelativeTime(session.lastMessageAt)}
        </span>
      )}
    </button>
  );
}
