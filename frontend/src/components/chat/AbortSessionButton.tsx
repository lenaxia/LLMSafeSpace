import { Square } from "lucide-react";
import { Button } from "../ui/Button";

interface Props {
  onAbort: () => void;
  disabled?: boolean;
}

export function AbortSessionButton({ onAbort, disabled }: Props) {
  return (
    <Button
      variant="destructive"
      size="sm"
      onClick={onAbort}
      disabled={disabled}
      aria-label="Stop generating"
    >
      <Square className="mr-1 h-3 w-3" />
      Stop
    </Button>
  );
}
