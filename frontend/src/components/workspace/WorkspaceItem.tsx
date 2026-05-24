import { Circle } from "lucide-react";
import type { WorkspaceListItem } from "../../api/types";

interface Props {
  workspace: WorkspaceListItem;
  selected: boolean;
  onSelect: () => void;
}

export function WorkspaceItem({ workspace, selected, onSelect }: Props) {
  const isActive = workspace.phase === "Active";

  return (
    <button
      onClick={onSelect}
      className={`flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm transition-colors ${
        selected ? "bg-accent text-accent-foreground" : "hover:bg-accent/50"
      }`}
      aria-current={selected ? "page" : undefined}
    >
      <Circle
        className={`h-2 w-2 flex-shrink-0 ${isActive ? "fill-green-500 text-green-500" : "fill-muted-foreground/40 text-muted-foreground/40"}`}
      />
      <span className="truncate">{workspace.name}</span>
      {!isActive && workspace.phase && (
        <span className="ml-auto text-xs text-muted-foreground">
          {workspace.phase}
        </span>
      )}
    </button>
  );
}
