import { useState, useEffect } from "react";
import { Button } from "../ui/Button";
import { generateWorkspaceName } from "../../lib/names";
import { orgsApi, type OrgResponse } from "../../api/orgs";

interface Props {
  onCreate: (params: { name: string; orgId?: string }) => void;
  onCancel: () => void;
  loading?: boolean;
}

export function NewWorkspaceDialog({ onCreate, onCancel, loading }: Props) {
  const [orgs, setOrgs] = useState<OrgResponse[]>([]);
  const [selectedOrg, setSelectedOrg] = useState<string>("");

  useEffect(() => {
    orgsApi.list().then((data) => setOrgs(data || [])).catch(() => {});
  }, []);

  return (
    <div className="flex flex-col gap-3 p-4">
      <h3 className="text-sm font-semibold">New Workspace</h3>
      <p className="text-xs text-muted-foreground">A workspace will be created and ready to chat.</p>
      {orgs.length > 0 && (
        <select
          value={selectedOrg}
          onChange={(e) => setSelectedOrg(e.target.value)}
          className="h-8 rounded border border-border bg-background px-2 text-sm focus:outline-none focus:ring-1 focus:ring-ring"
        >
          <option value="">Personal workspace</option>
          {orgs.map((org) => (
            <option key={org.id} value={org.id}>
              {org.name}
            </option>
          ))}
        </select>
      )}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onCancel}>Cancel</Button>
        <Button
          size="sm"
          disabled={loading}
          onClick={() => onCreate({ name: generateWorkspaceName(), orgId: selectedOrg || undefined })}
        >
          {loading ? "Creating..." : "Create"}
        </Button>
      </div>
    </div>
  );
}
