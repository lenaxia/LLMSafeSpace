/**
 * Format a timestamp as a human-readable relative string.
 * Pass `now` explicitly (from useNow()) so the output is pure and testable.
 */
export function formatRelativeTime(iso: string, now: number): string {
  const diff = now - new Date(iso).getTime();
  const mins = Math.floor(diff / 60_000);
  if (mins < 1) return "now";
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  return `${days}d`;
}
