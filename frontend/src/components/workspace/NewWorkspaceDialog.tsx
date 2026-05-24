import { Button } from "../ui/Button";

interface Props {
  onCreate: (params: { name: string }) => void;
  onCancel: () => void;
  loading?: boolean;
}

function generateName(): string {
  const adjectives = ["swift", "bright", "calm", "bold", "keen", "warm", "cool", "sharp"];
  const nouns = ["spark", "wave", "leaf", "stone", "cloud", "river", "flame", "wind"];
  const adj = adjectives[Math.floor(Math.random() * adjectives.length)]!;
  const noun = nouns[Math.floor(Math.random() * nouns.length)]!;
  const num = Math.floor(Math.random() * 100);
  return `${adj}-${noun}-${num}`;
}

export function NewWorkspaceDialog({ onCreate, onCancel, loading }: Props) {
  return (
    <div className="flex flex-col gap-3 p-4">
      <h3 className="text-sm font-semibold">New Workspace</h3>
      <p className="text-xs text-muted-foreground">A workspace will be created and ready to chat.</p>
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onCancel}>Cancel</Button>
        <Button size="sm" disabled={loading} onClick={() => onCreate({ name: generateName() })}>
          {loading ? "Creating..." : "Create"}
        </Button>
      </div>
    </div>
  );
}
