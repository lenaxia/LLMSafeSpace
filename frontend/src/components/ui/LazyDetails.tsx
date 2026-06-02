import { useState } from "react";
import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

interface Props {
  summary: ReactNode;
  children: ReactNode;
  className?: string;
  contentClassName?: string;
}

/**
 * A <details> element that defers mounting its children until first opened.
 * Use for heavy content (diff viewers, charts, large code blocks) that
 * shouldn't inject styles or compute layout while collapsed.
 */
export function LazyDetails({ summary, children, className, contentClassName }: Props) {
  const [opened, setOpened] = useState(false);
  return (
    <details
      className={cn("group", className)}
      onToggle={(e) => {
        if ((e.target as HTMLDetailsElement).open) setOpened(true);
      }}
    >
      {summary}
      {opened && <div className={contentClassName}>{children}</div>}
    </details>
  );
}
