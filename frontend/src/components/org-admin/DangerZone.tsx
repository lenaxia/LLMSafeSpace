import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { orgsApi } from "../../api/orgs";
import { ApiClientError } from "../../api/client";

interface DangerZoneProps {
  orgId: string;
  orgName: string;
}

export function DangerZone({ orgId, orgName }: DangerZoneProps) {
  const [confirmText, setConfirmText] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const handleDelete = async () => {
    setLoading(true);
    setError("");
    try {
      await orgsApi.delete(orgId);
      await queryClient.invalidateQueries({ queryKey: ["user-orgs"] });
      navigate("/chat");
    } catch (e) {
      if (e instanceof ApiClientError) {
        setError(e.message || "Failed to delete organisation");
      } else {
        setError(e instanceof Error ? e.message : "Failed to delete organisation");
      }
    } finally {
      setLoading(false);
    }
  };

  const canDelete = confirmText === orgName && !loading;

  return (
    <div className="rounded border border-red-500/50 p-4 space-y-3">
      <h3 className="text-sm font-medium text-red-500">Danger Zone</h3>
      <p className="text-xs text-muted-foreground">
        Deleting this organisation will soft-delete it. All workspaces remain
        org-attributed and become frozen — no one can access them. This cannot
        be undone.
      </p>
      {error && <p className="text-xs text-red-500">{error}</p>}
      <div className="space-y-2">
        <label className="text-xs text-muted-foreground">
          Type the organisation name{" "}
          <span className="font-mono font-medium">{orgName}</span>{" "}
          to confirm:
        </label>
        <input
          type="text"
          value={confirmText}
          onChange={(e) => setConfirmText(e.target.value)}
          placeholder={orgName}
          className="h-8 w-full rounded border border-border bg-background px-2 text-sm focus:outline-none focus:ring-1 focus:ring-red-500"
        />
      </div>
      <button
        onClick={handleDelete}
        disabled={!canDelete}
        className="rounded bg-red-600 px-4 py-1.5 text-sm font-medium text-white hover:bg-red-700 disabled:cursor-not-allowed disabled:opacity-50"
      >
        {loading ? "Deleting..." : "Delete organisation"}
      </button>
    </div>
  );
}
