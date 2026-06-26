import { Loader2 } from "lucide-react";
import { cn } from "../../lib/utils";

interface BusyIndicatorProps {
  className?: string;
  size?: "sm" | "md";
}

const sizeMap = { sm: "h-3 w-3", md: "h-3.5 w-3.5" };

export function BusyIndicator({ className, size = "md" }: BusyIndicatorProps) {
  return (
    <Loader2
      className={cn("animate-spin text-blue-500 flex-shrink-0", sizeMap[size], className)}
      aria-label="Agent working"
      data-busy
    />
  );
}
