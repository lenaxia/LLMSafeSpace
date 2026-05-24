import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { workspacesApi } from "../../api/workspaces";
import { useAuth } from "../../providers/AuthProvider";
import { WorkspaceList } from "../workspace/WorkspaceList";
import { WorkspaceSessionList } from "../workspace/WorkspaceSessionList";
import { Settings, LogOut } from "lucide-react";

export function Sidebar() {
  const { logout, user } = useAuth();
  const navigate = useNavigate();
  const { workspaceId, sessionId } = useParams();

  const { data: workspaces } = useQuery({
    queryKey: ["workspaces"],
    queryFn: () => workspacesApi.list(),
  });

  return (
    <aside className="flex w-64 flex-col border-r border-border bg-card" aria-label="Navigation">
      <div className="flex items-center gap-2 border-b border-border px-4 py-3">
        <h1 className="text-sm font-semibold">Safe Space</h1>
      </div>

      <div className="flex-1 overflow-y-auto">
        <WorkspaceList
          workspaces={workspaces?.items ?? []}
          selectedId={workspaceId}
          onSelect={(id) => navigate(`/chat/${id}`)}
        />

        {workspaceId && (
          <div className="border-t border-border px-2 py-2">
            <p className="px-3 pb-1 text-xs font-medium text-muted-foreground">Sessions</p>
            <WorkspaceSessionList
              workspaceId={workspaceId}
              selectedSessionId={sessionId}
              onSelectSession={(sid) => navigate(`/chat/${workspaceId}/${sid}`)}
            />
          </div>
        )}
      </div>

      <div className="border-t border-border p-2">
        <div className="flex items-center justify-between">
          <span className="truncate px-2 text-xs text-muted-foreground">
            {user?.username}
          </span>
          <div className="flex gap-1">
            <button
              onClick={() => navigate("/settings")}
              className="rounded p-1.5 hover:bg-accent"
              aria-label="Settings"
            >
              <Settings className="h-4 w-4" />
            </button>
            <button
              onClick={logout}
              className="rounded p-1.5 hover:bg-accent"
              aria-label="Log out"
            >
              <LogOut className="h-4 w-4" />
            </button>
          </div>
        </div>
      </div>
    </aside>
  );
}
