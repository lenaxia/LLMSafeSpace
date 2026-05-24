import type { SessionListItem } from "../../api/types";
import { SessionItem } from "./SessionItem";

interface Props {
  sessions: SessionListItem[];
  selectedId?: string;
  onSelect: (id: string) => void;
}

export function SessionList({ sessions, selectedId, onSelect }: Props) {
  if (sessions.length === 0) {
    return (
      <p className="px-4 py-3 text-xs text-muted-foreground">No sessions yet</p>
    );
  }

  return (
    <div className="flex flex-col gap-0.5">
      {sessions.map((s) => (
        <SessionItem
          key={s.id}
          session={s}
          selected={s.id === selectedId}
          onSelect={() => onSelect(s.id)}
        />
      ))}
    </div>
  );
}
