import { useState, useRef, useEffect, useCallback } from "react";
import { createPortal } from "react-dom";
import { Copy, MoreHorizontal } from "lucide-react";
import { cn } from "../../lib/utils";

export interface KebabMenuItem {
  label: string;
  onClick: () => void;
  destructive?: boolean;
  disabled?: boolean;
}

interface Props {
  items: KebabMenuItem[];
  align?: "left" | "right";
  footer?: string[];
}

export function KebabMenu({ items, align = "right", footer }: Props) {
  const [open, setOpen] = useState(false);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ top: 0, left: 0 });

  const updatePos = useCallback(() => {
    if (!buttonRef.current) return;
    const rect = buttonRef.current.getBoundingClientRect();
    setPos({
      top: rect.bottom + 4,
      left: align === "right" ? rect.right - 160 : rect.left,
    });
  }, [align]);

  useEffect(() => {
    if (!open) return;
    updatePos();
    const handleClick = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node) &&
          buttonRef.current && !buttonRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, [open, updatePos]);

  return (
    <>
      <button
        ref={buttonRef}
        onClick={(e) => { e.stopPropagation(); setOpen(!open); }}
        className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground transition-opacity"
        aria-label="Actions"
        aria-haspopup="true"
        aria-expanded={open}
      >
        <MoreHorizontal className="h-4 w-4" />
      </button>
      {open && createPortal(
        <div
          ref={menuRef}
          className="fixed z-[9999] w-40 rounded-md border border-border bg-popover py-1 shadow-md"
          style={{ top: pos.top, left: pos.left }}
          role="menu"
        >
          {items.filter(i => !i.destructive).map((item, i) => (
            <button
              key={i}
              role="menuitem"
              disabled={item.disabled}
              onClick={(e) => {
                e.stopPropagation();
                if (!item.disabled) {
                  item.onClick();
                  setOpen(false);
                }
              }}
              className={cn(
                "flex w-full items-center px-3 py-1.5 text-left text-xs transition-colors",
                "text-foreground hover:bg-accent",
                item.disabled && "opacity-50 cursor-not-allowed",
              )}
            >
              {item.label}
            </button>
          ))}
          {footer && footer.length > 0 && (
            <div className="border-t border-border mx-2 my-1 pt-1">
              <div className="flex items-center justify-between px-3">
                <div>
                  {footer.map((line, i) => (
                    <div key={i} className="py-0.5 text-[10px] text-muted-foreground select-text">
                      {line}
                    </div>
                  ))}
                </div>
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    navigator.clipboard.writeText(footer.join("\n"));
                  }}
                  className="ml-2 rounded p-0.5 text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                  aria-label="Copy version info"
                  title="Copy version info"
                >
                  <Copy className="h-3 w-3" />
                </button>
              </div>
            </div>
          )}
          {items.filter(i => i.destructive).length > 0 && (
            <div className="border-t border-border mx-2 my-1" />
          )}
          {items.filter(i => i.destructive).map((item, i) => (
            <button
              key={`d-${i}`}
              role="menuitem"
              disabled={item.disabled}
              onClick={(e) => {
                e.stopPropagation();
                if (!item.disabled) {
                  item.onClick();
                  setOpen(false);
                }
              }}
              className={cn(
                "flex w-full items-center px-3 py-1.5 text-left text-xs transition-colors",
                "text-destructive hover:bg-red-500/10 dark:hover:bg-red-500/20",
                item.disabled && "opacity-50 cursor-not-allowed",
              )}
            >
              {item.label}
            </button>
          ))}
        </div>,
        document.body,
      )}
    </>
  );
}
