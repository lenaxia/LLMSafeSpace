import * as Switch from "@radix-ui/react-switch";
import { cn } from "../../lib/utils";

interface ToggleProps {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  disabled?: boolean;
  id?: string;
}

export function Toggle({ checked, onCheckedChange, disabled, id }: ToggleProps) {
  return (
    <Switch.Root
      id={id}
      checked={checked}
      onCheckedChange={onCheckedChange}
      disabled={disabled}
      className={cn(
        "relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2",
        checked ? "bg-blue-600" : "bg-muted",
        disabled && "opacity-50 cursor-not-allowed",
      )}
    >
      <Switch.Thumb
        className={cn(
          "pointer-events-none block h-5 w-5 rounded-full bg-white shadow-md transition-transform",
          checked ? "translate-x-[20px]" : "translate-x-0",
        )}
      />
    </Switch.Root>
  );
}
