import { Button } from "../ui/Button";
import { generateWorkspaceName } from "../../lib/names";

interface Props {
  onCreate: (params: { name: string }) => void;
  onCancel: () => void;
  loading?: boolean;
}

export function NewWorkspaceDialog({ onCreate, onCancel, loading }: Props) {
  return (
    <div className="flex flex-col gap-3 p-4">
      <h3 className="text-sm font-semibold">New Workspace</h3>
      <p className="text-xs text-muted-foreground">A workspace will be created and ready to chat.</p>
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onCancel}>Cancel</Button>
        <Button size="sm" disabled={loading} onClick={() => onCreate({ name: generateWorkspaceName() })}>
          {loading ? "Creating..." : "Create"}
        </Button>
      </div>
    </div>
  );
}
