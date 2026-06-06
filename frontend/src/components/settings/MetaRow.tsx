// Shared metadata label/value row used across credential settings tabs.
export function MetaRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex min-w-0 gap-2">
      <span className="shrink-0 text-muted-foreground">{label}:</span>
      <span className={`truncate ${mono ? "font-mono text-[10px]" : ""}`}>{value || "—"}</span>
    </div>
  );
}
