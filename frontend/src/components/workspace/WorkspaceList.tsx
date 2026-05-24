import type { WorkspaceListItem } from "../../api/types";
import { WorkspaceItem } from "./WorkspaceItem";

interface Props {
  workspaces: WorkspaceListItem[];
  selectedId?: string;
  onSelect: (id: string) => void;
}

export function WorkspaceList({ workspaces, selectedId, onSelect }: Props) {
  if (workspaces.length === 0) {
    return (
      <div className="px-4 py-8 text-center text-sm text-muted-foreground">
        No workspaces yet
      </div>
    );
  }

  return (
    <nav className="flex flex-col gap-0.5 p-2" aria-label="Workspaces">
      {workspaces.map((ws) => (
        <WorkspaceItem
          key={ws.id}
          workspace={ws}
          selected={ws.id === selectedId}
          onSelect={() => onSelect(ws.id)}
        />
      ))}
    </nav>
  );
}
