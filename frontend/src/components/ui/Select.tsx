import * as RadixSelect from "@radix-ui/react-select";
import { ChevronDown } from "lucide-react";

interface SelectProps {
  value: string;
  onValueChange: (value: string) => void;
  options: string[];
  disabled?: boolean;
  id?: string;
}

export function Select({ value, onValueChange, options, disabled, id }: SelectProps) {
  return (
    <RadixSelect.Root value={value} onValueChange={onValueChange} disabled={disabled}>
      <RadixSelect.Trigger
        id={id}
        className="inline-flex items-center gap-1 rounded-md border border-border bg-background px-3 py-1.5 text-sm disabled:opacity-50"
      >
        <RadixSelect.Value />
        <RadixSelect.Icon>
          <ChevronDown className="h-3.5 w-3.5 opacity-50" />
        </RadixSelect.Icon>
      </RadixSelect.Trigger>
      <RadixSelect.Portal>
        <RadixSelect.Content className="rounded-md border border-border bg-popover p-1 shadow-md">
          <RadixSelect.Viewport>
            {options.map((opt) => (
              <RadixSelect.Item
                key={opt}
                value={opt}
                className="cursor-pointer rounded px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-accent"
              >
                <RadixSelect.ItemText>{opt}</RadixSelect.ItemText>
              </RadixSelect.Item>
            ))}
          </RadixSelect.Viewport>
        </RadixSelect.Content>
      </RadixSelect.Portal>
    </RadixSelect.Root>
  );
}
