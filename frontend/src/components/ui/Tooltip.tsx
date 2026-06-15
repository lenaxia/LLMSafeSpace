/**
 * Tooltip — position-aware, accessible tooltip using @radix-ui/react-tooltip.
 *
 * Radix Tooltip uses Floating UI internally and automatically flips placement
 * when the preferred side would overflow the viewport.  The previous custom
 * implementations (DiskUsageBar, SecretsTab) used hard-coded `bottom-full`
 * CSS which caused the popover to clip off the top of the screen when the
 * trigger was near the top of the viewport.
 *
 * Usage:
 *
 *   <Tooltip content="Helpful text">
 *     <button>hover me</button>
 *   </Tooltip>
 *
 *   // Explicit placement (Radix flips automatically if there's no room):
 *   <Tooltip content="Helpful text" side="right">
 *     <span>hover me</span>
 *   </Tooltip>
 *
 *   // Wide content:
 *   <Tooltip content={<>Long explanation<br/>second line</>} maxWidth="sm">
 *     <span>hover me</span>
 *   </Tooltip>
 *
 *   // Disable (e.g. while loading):
 *   <Tooltip content="Save changes" disabled={isSaving}>
 *     <button>Save</button>
 *   </Tooltip>
 */

import * as RadixTooltip from "@radix-ui/react-tooltip";
import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

// Provider should be rendered once near the root of the app.
// Re-exporting here so callers don't need to import Radix directly.
export const TooltipProvider = RadixTooltip.Provider;

const maxWidthClasses = {
  xs: "max-w-[12rem]",  // 192px — short labels
  sm: "max-w-[16rem]",  // 256px — default, one or two sentences
  md: "max-w-[24rem]",  // 384px — longer explanations
  lg: "max-w-[32rem]",  // 512px — rich multi-line content
} as const;

type MaxWidth = keyof typeof maxWidthClasses;
type Side = "top" | "right" | "bottom" | "left";
type Align = "start" | "center" | "end";

interface TooltipProps {
  /** The tooltip content.  Can be a string or any ReactNode. */
  content: ReactNode;
  /** The element that triggers the tooltip. */
  children: ReactNode;
  /** Preferred side.  Radix flips automatically when there's no room. Default: "top". */
  side?: Side;
  /** Alignment relative to the trigger.  Default: "start". */
  align?: Align;
  /** Gap between trigger and tooltip in px.  Default: 6. */
  sideOffset?: number;
  /** Maximum width of the tooltip bubble.  Default: "sm" (256px). */
  maxWidth?: MaxWidth;
  /** When true the tooltip is not rendered (useful during loading states). */
  disabled?: boolean;
  /** Delay before showing in ms.  Default: 300 (Radix default). */
  delayDuration?: number;
}

/**
 * Position-aware tooltip.  Wraps Radix UI Tooltip which uses Floating UI
 * internally to keep the popover inside the viewport.
 */
export function Tooltip({
  content,
  children,
  side = "top",
  align = "start",
  sideOffset = 6,
  maxWidth = "sm",
  disabled = false,
  delayDuration = 300,
}: TooltipProps) {
  if (disabled) {
    // Render children bare when disabled — no tooltip wrapper overhead.
    return <>{children}</>;
  }

  return (
    <RadixTooltip.Root delayDuration={delayDuration}>
      <RadixTooltip.Trigger asChild>{children}</RadixTooltip.Trigger>
      <RadixTooltip.Portal>
        <RadixTooltip.Content
          side={side}
          align={align}
          sideOffset={sideOffset}
          className={cn(
            // Base styles — match the existing popover chrome used elsewhere
            "z-50 rounded-md border border-border bg-popover px-3 py-2",
            "text-xs text-popover-foreground shadow-md",
            // Viewport-safe width
            "w-max",
            maxWidthClasses[maxWidth],
            // Animate in/out via Tailwind + Radix data attributes
            "animate-in fade-in-0 zoom-in-95",
            "data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95",
            // Slide direction matches the side Radix chose (after flip)
            "data-[side=bottom]:slide-in-from-top-2",
            "data-[side=top]:slide-in-from-bottom-2",
            "data-[side=left]:slide-in-from-right-2",
            "data-[side=right]:slide-in-from-left-2",
          )}
        >
          {content}
          <RadixTooltip.Arrow className="fill-border" width={10} height={5} />
        </RadixTooltip.Content>
      </RadixTooltip.Portal>
    </RadixTooltip.Root>
  );
}
