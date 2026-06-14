import { useCallback, useEffect, useState } from "react";
import { useOutletContext } from "react-router-dom";
import { orgsApi, type OrgResponse } from "../../api/orgs";
import type { WorkspaceListItem } from "../../api/types";
import { Badge } from "../ui/Badge";

interface WorkspacesContext {
  org: OrgResponse;
  isAdmin: boolean;
}

export function OrgWorkspacesTab() {
  const { org } = useOutletContext<WorkspacesContext>();
  const [workspaces, setWorkspaces] = useState<WorkspaceListItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const resp = await orgsApi.listWorkspaces(org.id);
      setWorkspaces(resp.items || []);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load workspaces");
    } finally {
      setLoading(false);
    }
  }, [org.id]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold">Workspaces</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          All workspaces in this organization.
        </p>
      </div>

      {loading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {error && <p className="text-sm text-red-500">{error}</p>}

      {!loading && workspaces.length === 0 && (
        <p className="text-sm text-muted-foreground">
          No workspaces in this organization yet.
        </p>
      )}

      {workspaces.length > 0 && (
        <div className="rounded border border-border">
          <table className="w-full text-sm">
            <thead className="border-b border-border bg-muted/50">
              <tr>
                <th className="px-4 py-2 text-left font-medium">Name</th>
                <th className="px-4 py-2 text-left font-medium">Runtime</th>
                <th className="px-4 py-2 text-left font-medium">Phase</th>
                <th className="px-4 py-2 text-left font-medium">Created</th>
              </tr>
            </thead>
            <tbody>
              {workspaces.map((ws) => (
                <tr
                  key={ws.id}
                  className="border-b border-border last:border-0"
                >
                  <td className="px-4 py-2 font-medium">{ws.name}</td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {ws.runtime}
                  </td>
                  <td className="px-4 py-2">
                    {ws.phase ? (
                      <Badge
                        variant={
                          ws.phase === "Active"
                            ? "success"
                            : ws.phase === "Failed"
                              ? "destructive"
                              : "default"
                        }
                      >
                        {ws.phase}
                      </Badge>
                    ) : (
                      <span className="text-xs text-muted-foreground">—</span>
                    )}
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {new Date(ws.createdAt).toLocaleDateString()}
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
