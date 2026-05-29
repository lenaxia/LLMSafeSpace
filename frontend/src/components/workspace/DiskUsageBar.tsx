interface Props {
  usedBytes?: number;
  totalBytes?: number;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

export function DiskUsageBar({ usedBytes, totalBytes }: Props) {
  if (!totalBytes || !usedBytes) return null;

  const percent = Math.min(100, Math.round((usedBytes / totalBytes) * 100));
  const isHigh = percent > 85;

  return (
    <div className="flex items-center gap-2 px-4 py-1 text-xs text-muted-foreground">
      <span>Disk</span>
      <div className="flex-1 h-1.5 rounded-full bg-muted overflow-hidden">
        <div
          className={`h-full rounded-full transition-all ${isHigh ? "bg-orange-500" : "bg-primary/60"}`}
          style={{ width: `${percent}%` }}
        />
      </div>
      <span>{formatBytes(usedBytes)} / {formatBytes(totalBytes)}</span>
    </div>
  );
}
