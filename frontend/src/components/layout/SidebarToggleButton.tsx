import { Menu, X } from "lucide-react";

interface SidebarToggleButtonProps {
  open: boolean;
  onClick: () => void;
}

export function SidebarToggleButton({ open, onClick }: SidebarToggleButtonProps) {
  return (
    <button
      onClick={onClick}
      className="rounded p-2 hover:bg-accent"
      aria-label={open ? "Close menu" : "Open menu"}
      aria-expanded={open}
    >
      {open ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}
    </button>
  );
}
