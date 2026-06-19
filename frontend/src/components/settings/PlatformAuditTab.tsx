import { useCallback, useEffect, useState } from "react";
import {
  adminAuditApi,
  orgsApi,
  type AuditEntry,
  type AuditListFilters,
  type OrgResponse,
} from "../../api/orgs";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";

const DOMAIN_OPTIONS = ["", "billing", "secrets", "admin", "org"] as const;

export function PlatformAuditTab() {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [orgs, setOrgs] = useState<OrgResponse[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const [orgId, setOrgId] = useState("");
  const [actorId, setActorId] = useState("");
  const [domain, setDomain] = useState("");
  const [applied, setApplied] = useState<AuditListFilters>({});

  const orgNameById = new Map<string, OrgResponse>();
  for (const o of orgs) orgNameById.set(o.id, o);

  const fetchEntries = useCallback(async (filters: AuditListFilters) => {
    setLoading(true);
    setError("");
    try {
      const data = await adminAuditApi.list(filters);
      setEntries(data.items || []);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load audit log");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchEntries({});
    orgsApi
      .list()
      .then((data) => setOrgs(data || []))
      .catch(() => {
        // Org-name resolution is best-effort; the table falls back to orgId.
      });
  }, [fetchEntries]);

  const applyFilters = () => {
    const filters: AuditListFilters = {};
    if (orgId.trim()) filters.orgId = orgId.trim();
    if (actorId.trim()) filters.actorId = actorId.trim();
    if (domain) filters.domain = domain;
    setApplied(filters);
    fetchEntries(filters);
  };

  const resetFilters = () => {
    setOrgId("");
    setActorId("");
    setDomain("");
    setApplied({});
    fetchEntries({});
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold">Platform Audit Log</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Cross-organisation activity history (platform admin only).
        </p>
      </div>

      <div className="rounded border border-border bg-background p-4 space-y-3">
        <p className="text-sm font-medium">Filters</p>
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
          <select
            value={domain}
            onChange={(e) => setDomain(e.target.value)}
            className="h-8 w-full rounded border border-border bg-background px-2 text-sm focus:outline-none focus:ring-1 focus:ring-ring"
          >
            {DOMAIN_OPTIONS.map((d) => (
              <option key={d} value={d}>
                {d === "" ? "Any domain" : d}
              </option>
            ))}
          </select>
          <input
            type="text"
            value={orgId}
            onChange={(e) => setOrgId(e.target.value)}
            placeholder="Org ID"
            className="h-8 w-full rounded border border-border bg-background px-2 text-sm focus:outline-none focus:ring-1 focus:ring-ring"
          />
          <input
            type="text"
            value={actorId}
            onChange={(e) => setActorId(e.target.value)}
            placeholder="Actor ID"
            className="h-8 w-full rounded border border-border bg-background px-2 text-sm focus:outline-none focus:ring-1 focus:ring-ring"
          />
        </div>
        <div className="flex gap-2">
          <Button size="sm" onClick={applyFilters}>
            Apply
          </Button>
          <Button size="sm" variant="ghost" onClick={resetFilters}>
            Reset
          </Button>
        </div>
      </div>

      {loading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {error && <p className="text-sm text-red-500">{error}</p>}

      {!loading && entries.length === 0 && (
        <p className="text-sm text-muted-foreground">
          No audit entries match the current filters.
        </p>
      )}

      {entries.length > 0 && (
        <div className="rounded border border-border">
          <table className="w-full text-sm">
            <thead className="border-b border-border bg-muted/50">
              <tr>
                <th className="px-4 py-2 text-left font-medium">Action</th>
                <th className="px-4 py-2 text-left font-medium">Domain</th>
                <th className="px-4 py-2 text-left font-medium">Org</th>
                <th className="px-4 py-2 text-left font-medium">Actor</th>
                <th className="px-4 py-2 text-left font-medium">Target</th>
                <th className="px-4 py-2 text-left font-medium">Time</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => {
                const org = e.orgId ? orgNameById.get(e.orgId) : undefined;
                return (
                  <tr
                    key={e.id}
                    className="border-b border-border last:border-0"
                  >
                    <td className="px-4 py-2">
                      <Badge variant="muted">{e.action}</Badge>
                    </td>
                    <td className="px-4 py-2 text-muted-foreground">
                      {e.domain}
                    </td>
                    <td className="px-4 py-2 text-muted-foreground">
                      {org ? (
                        <span title={e.orgId}>{org.name}</span>
                      ) : e.orgId ? (
                        <span className="font-mono text-xs" title={e.orgId}>
                          {e.orgId.slice(0, 8)}…
                        </span>
                      ) : (
                        "—"
                      )}
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
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {!loading && entries.length > 0 && (
        <p className="text-xs text-muted-foreground">
          {Object.keys(applied).length > 0
            ? "Filters applied. Adjust and re-apply to refine."
            : "Showing recent activity across all organisations."}
        </p>
      )}
    </div>
  );
}
