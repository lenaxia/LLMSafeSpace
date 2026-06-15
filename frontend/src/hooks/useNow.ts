import { useEffect, useState } from "react";

/**
 * Returns the current time as a Unix ms value, refreshed every `intervalMs`.
 * Use this wherever relative timestamps ("Xm ago") need to stay current.
 *
 * Components that call useNow each maintain their own interval, but React
 * batches all setState calls that fire within the same event loop turn, so
 * simultaneous ticks across sibling components collapse into one reconcile pass.
 *
 * Default interval is 60 s — matches the minute-level granularity of
 * formatRelativeTime, so no visible update is ever skipped.
 */
export function useNow(intervalMs = 60_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}
