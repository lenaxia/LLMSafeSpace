import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { workspacesApi } from "../../api/workspaces";
import { useAuth } from "../../providers/AuthProvider";
import { NewWorkspaceDialog } from "../workspace/NewWorkspaceDialog";
import {
  Settings,
  LogOut,
  Plus,
  Circle,
  MessageSquare,
  ChevronRight,
  ChevronDown,
  Play,
  Loader2,
} from "lucide-react";
import type { SessionListItem, WorkspaceListItem } from "../../api/types";
import { formatRelativeTime } from "../../lib/time";
import { cn } from "../../lib/utils";

interface Props {
  onNavigate?: () => void;
}

export function Sidebar({ onNavigate }: Props) {
  const { logout, user } = useAuth();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { workspaceId, sessionId } = useParams();
  const [showNewWorkspace, setShowNewWorkspace] = useState(false);
  const [expandedWs, setExpandedWs] = useState<Set<string>>(() =>
    workspaceId ? new Set([workspaceId]) : new Set(),
  );

  const { data: workspaces } = useQuery({
    queryKey: ["workspaces"],
    queryFn: () => workspacesApi.list(),
  });

  const createMutation = useMutation({
    mutationFn: async (params: { name: string }) => {
      const ws = await workspacesApi.create(params);
      return ws;
    },
    onSuccess: (ws) => {
      queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      setShowNewWorkspace(false);
      setExpandedWs((prev) => new Set(prev).add(ws.id));
      navigate(`/chat/${ws.id}`);
      onNavigate?.();
    },
  });

  const activateMutation = useMutation({
    mutationFn: (wsId: string) => workspacesApi.activate(wsId),
    onSuccess: (_data, wsId) => {
      queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      queryClient.invalidateQueries({ queryKey: ["workspace-status"] });
      queryClient.invalidateQueries({ queryKey: ["sessions", wsId] });
    },
  });

  const newSessionMutation = useMutation({
    mutationFn: (wsId: string) => workspacesApi.ensureSession(wsId),
    onSuccess: (data, wsId) => {
      queryClient.invalidateQueries({ queryKey: ["sessions", wsId] });
      navigate(`/chat/${wsId}/${data.sessionId}`);
      onNavigate?.();
    },
  });

  const handleWorkspaceClick = (ws: WorkspaceListItem) => {
    const isExpanded = expandedWs.has(ws.id);

    if (ws.phase === "Suspended") {
      activateMutation.mutate(ws.id);
      setExpandedWs((prev) => new Set(prev).add(ws.id));
      return;
    }

    setExpandedWs((prev) => {
      const next = new Set(prev);
      if (isExpanded) {
        next.delete(ws.id);
      } else {
        next.add(ws.id);
      }
      return next;
    });

    if (!isExpanded) {
      queryClient
        .fetchQuery({
          queryKey: ["sessions", ws.id],
          queryFn: () => workspacesApi.getSessions(ws.id),
        })
        .then((sessions: SessionListItem[]) => {
          if (sessionId && ws.id === workspaceId) return;
          const first = sessions.length > 0 ? sessions[0] : undefined;
          if (first) {
            navigate(`/chat/${ws.id}/${first.id}`);
          } else {
            navigate(`/chat/${ws.id}`);
          }
          onNavigate?.();
        });
    }
  };

  const handleSessionClick = (wsId: string, sid: string) => {
    navigate(`/chat/${wsId}/${sid}`);
    onNavigate?.();
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

        <nav className="flex flex-col gap-0.5 p-2" aria-label="Workspaces">
          {(workspaces?.items ?? []).map((ws) => (
            <WorkspaceGroup
              key={ws.id}
              workspace={ws}
              expanded={expandedWs.has(ws.id)}
              selectedWorkspaceId={workspaceId}
              selectedSessionId={sessionId}
              activating={activateMutation.isPending && activateMutation.variables === ws.id}
              creatingSession={newSessionMutation.isPending && newSessionMutation.variables === ws.id}
              onToggle={() => handleWorkspaceClick(ws)}
              onSelectSession={(sid) => handleSessionClick(ws.id, sid)}
              onNewSession={() => newSessionMutation.mutate(ws.id)}
            />
          ))}
          {((workspaces?.items ?? []).length === 0) && (
            <div className="px-4 py-8 text-center text-sm text-muted-foreground">
              No workspaces yet
            </div>
          )}
        </nav>
      </div>

      <div className="border-t border-border p-2">
        <div className="flex items-center justify-between">
          <span className="truncate px-2 text-xs text-muted-foreground">{user?.username}</span>
          <div className="flex gap-1">
            <button onClick={() => { navigate("/settings"); onNavigate?.(); }} className="rounded p-1.5 hover:bg-accent" aria-label="Settings">
              <Settings className="h-4 w-4" />
            </button>
            <button onClick={logout} className="rounded p-1.5 hover:bg-accent" aria-label="Log out">
              <LogOut className="h-4 w-4" />
            </button>
          </div>
        </div>
      </div>
    </aside>
  );
}

interface WorkspaceGroupProps {
  workspace: WorkspaceListItem;
  expanded: boolean;
  selectedWorkspaceId?: string;
  selectedSessionId?: string;
  activating: boolean;
  creatingSession: boolean;
  onToggle: () => void;
  onSelectSession: (sessionId: string) => void;
  onNewSession: () => void;
}

function WorkspaceGroup({
  workspace,
  expanded,
  selectedWorkspaceId,
  selectedSessionId,
  activating,
  creatingSession,
  onToggle,
  onSelectSession,
  onNewSession,
}: WorkspaceGroupProps) {
  const isSelected = workspace.id === selectedWorkspaceId;
  const isSuspended = workspace.phase === "Suspended";
  const isResuming = workspace.phase === "Resuming";
  const isActive = workspace.phase === "Active";

  return (
    <div>
      <button
        onClick={onToggle}
        className={cn(
          "flex w-full items-center gap-1.5 rounded-md px-3 py-2 text-left text-sm transition-colors",
          isSelected ? "bg-accent text-accent-foreground" : "hover:bg-accent/50",
        )}
      >
        {expanded ? (
          <ChevronDown className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
        ) : (
          <ChevronRight className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
        )}
        <Circle
          className={cn(
            "h-2 w-2 flex-shrink-0",
            isActive
              ? "fill-green-500 text-green-500"
              : isSuspended
                ? "fill-yellow-500 text-yellow-500"
                : "fill-muted-foreground/40 text-muted-foreground/40",
          )}
        />
        <span className="flex-1 truncate">{workspace.name}</span>
        {activating && <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />}
        {isSuspended && !activating && (
          <Play className="h-3 w-3 flex-shrink-0 text-yellow-500" />
        )}
        {!isActive && !isSuspended && !activating && workspace.phase && (
          <span className="text-xs text-muted-foreground">{workspace.phase}</span>
        )}
      </button>

      {expanded && (
        <WorkspaceSessionList
          workspaceId={workspace.id}
          selectedSessionId={selectedSessionId}
          onSelectSession={onSelectSession}
          onNewSession={onNewSession}
          creatingSession={creatingSession}
          isSuspended={isSuspended || isResuming}
        />
      )}
    </div>
  );
}

interface SessionListProps {
  workspaceId: string;
  selectedSessionId?: string;
  onSelectSession: (sessionId: string) => void;
  onNewSession: () => void;
  creatingSession: boolean;
  isSuspended: boolean;
}

function WorkspaceSessionList({
  workspaceId,
  selectedSessionId,
  onSelectSession,
  onNewSession,
  creatingSession,
  isSuspended,
}: SessionListProps) {
  const { data: sessions, isLoading } = useQuery({
    queryKey: ["sessions", workspaceId],
    queryFn: () => workspacesApi.getSessions(workspaceId),
    enabled: !!workspaceId,
  });

  if (isSuspended) {
    return (
      <div className="ml-7 px-2 py-1 text-xs text-muted-foreground">
        {isLoading ? "Checking..." : "Resuming workspace..."}
      </div>
    );
  }

  return (
    <div className="ml-5 pl-2 border-l border-border">
      <div className="flex items-center justify-between px-2 py-1">
        <span className="text-[10px] font-medium text-muted-foreground uppercase tracking-wide">Sessions</span>
        {!isSuspended && (
          <button
            onClick={onNewSession}
            disabled={creatingSession}
            className="rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-50"
            aria-label="New chat"
            title="New chat"
          >
            {creatingSession ? (
              <Loader2 className="h-3 w-3 animate-spin" />
            ) : (
              <Plus className="h-3 w-3" />
            )}
          </button>
        )}
      </div>

      {isLoading && (
        <div className="px-2 py-1 text-xs text-muted-foreground">Loading...</div>
      )}

      {!isLoading && (!sessions || sessions.length === 0) && (
        <div className="px-2 py-1 text-xs text-muted-foreground">No sessions yet</div>
      )}

      {sessions && sessions.length > 0 && (
        <div className="flex flex-col gap-0.5">
          {sessions.map((s) => {
            const title =
              s.title ||
              `Chat ${s.lastMessageAt ? formatRelativeTime(s.lastMessageAt) : ""}`;
            return (
              <button
                key={s.id}
                onClick={() => onSelectSession(s.id)}
                className={cn(
                  "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-colors",
                  s.id === selectedSessionId
                    ? "bg-accent text-accent-foreground"
                    : "hover:bg-accent/50 text-muted-foreground",
                )}
              >
                <MessageSquare className="h-3 w-3 flex-shrink-0" />
                <span className="flex-1 truncate">{title}</span>
                {s.status === "active" && (
                  <span className="h-1.5 w-1.5 rounded-full bg-blue-500" />
                )}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
