import { useEffect, useState } from "react";
import { useOutletContext } from "react-router-dom";
import { orgsApi, type OrgResponse, type AuditEntry } from "../../api/orgs";
import { Badge } from "../ui/Badge";

interface AuditContext {
  org: OrgResponse;
  isAdmin: boolean;
}

export function OrgAuditTab() {
  const { org } = useOutletContext<AuditContext>();
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    setLoading(true);
    setError("");
    orgsApi
      .listAudit(org.id)
      .then((data) => setEntries(data.items || []))
      .catch((e) =>
        setError(e instanceof Error ? e.message : "Failed to load audit log"),
      )
      .finally(() => setLoading(false));
  }, [org.id]);

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold">Audit Log</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Organization activity history.
        </p>
      </div>

      {loading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {error && <p className="text-sm text-red-500">{error}</p>}

      {!loading && entries.length === 0 && (
        <p className="text-sm text-muted-foreground">
          No audit entries recorded yet.
        </p>
      )}

      {entries.length > 0 && (
        <div className="rounded border border-border">
          <table className="w-full text-sm">
            <thead className="border-b border-border bg-muted/50">
              <tr>
                <th className="px-4 py-2 text-left font-medium">Action</th>
                <th className="px-4 py-2 text-left font-medium">Actor</th>
                <th className="px-4 py-2 text-left font-medium">Target</th>
                <th className="px-4 py-2 text-left font-medium">Time</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => (
                <tr
                  key={e.id}
                  className="border-b border-border last:border-0"
                >
                  <td className="px-4 py-2">
                    <Badge variant="muted">{e.action}</Badge>
                  </td>
                  <td className="px-4 py-2 font-mono text-xs text-muted-foreground">
                    {e.actorId.slice(0, 8)}…
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {e.targetId || "—"}
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {new Date(e.createdAt).toLocaleString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
