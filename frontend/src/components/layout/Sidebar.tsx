import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { workspacesApi } from "../../api/workspaces";
import { sessionsApi } from "../../api/sessions";
import { useAuth } from "../../providers/AuthProvider";
import { WorkspaceList } from "../workspace/WorkspaceList";
import { WorkspaceSessionList } from "../workspace/WorkspaceSessionList";
import { NewWorkspaceDialog } from "../workspace/NewWorkspaceDialog";
import { Settings, LogOut, Plus } from "lucide-react";

export function Sidebar() {
  const { logout, user } = useAuth();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { workspaceId, sessionId } = useParams();
  const [showNewWorkspace, setShowNewWorkspace] = useState(false);

  const { data: workspaces } = useQuery({
    queryKey: ["workspaces"],
    queryFn: () => workspacesApi.list(),
  });

  const createMutation = useMutation({
    mutationFn: (params: { name: string }) => workspacesApi.create(params),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      setShowNewWorkspace(false);
      navigate(`/chat/${data.id}`);
    },
  });

  const createSessionMutation = useMutation({
    mutationFn: (sandboxId: string) => sessionsApi.create(sandboxId, "New chat"),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["sessions", workspaceId] });
      if (workspaceId) navigate(`/chat/${workspaceId}/${data.id}`);
    },
  });

  const handleNewSession = async () => {
    if (!workspaceId) return;
    try {
      const sandboxes = await workspacesApi.getSandboxes(workspaceId);
      const running = sandboxes.find((sb) => sb.phase === "Running");
      if (running) {
        createSessionMutation.mutate(running.id);
      }
    } catch { /* workspace may not be active */ }
  };

  return (
    <aside className="flex h-full w-64 flex-col border-r border-border bg-card" aria-label="Navigation">
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        <h1 className="text-sm font-semibold">Safe Space</h1>
        <button
          onClick={() => setShowNewWorkspace(true)}
          className="rounded p-1 hover:bg-accent"
          aria-label="New workspace"
        >
          <Plus className="h-4 w-4" />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto">
        {showNewWorkspace && (
          <div className="border-b border-border">
            <NewWorkspaceDialog
              onCreate={(params) => createMutation.mutate(params)}
              onCancel={() => setShowNewWorkspace(false)}
              loading={createMutation.isPending}
            />
          </div>
        )}

        <WorkspaceList
          workspaces={workspaces?.items ?? []}
          selectedId={workspaceId}
          onSelect={(id) => navigate(`/chat/${id}`)}
        />

        {workspaceId && (
          <div className="border-t border-border px-2 py-2">
            <div className="flex items-center justify-between px-3 pb-1">
              <p className="text-xs font-medium text-muted-foreground">Sessions</p>
              <button
                onClick={handleNewSession}
                className="rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                aria-label="New session"
                title="New chat"
              >
                <Plus className="h-3 w-3" />
              </button>
            </div>
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
