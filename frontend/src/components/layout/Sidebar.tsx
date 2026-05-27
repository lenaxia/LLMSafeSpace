import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { workspacesApi } from "../../api/workspaces";
import { useAuth } from "../../providers/AuthProvider";
import { RenameWorkspaceDialog } from "../workspace/RenameWorkspaceDialog";
import { RenameSessionDialog } from "../session/RenameSessionDialog";
import { KebabMenu } from "../ui/KebabMenu";
import type { KebabMenuItem } from "../ui/KebabMenu";
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
import type { WorkspaceListItem } from "../../api/types";
import { sessionDisplayTitle, generateWorkspaceName } from "../../lib/names";
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
  const [expandedWs, setExpandedWs] = useState<Set<string>>(() =>
    workspaceId ? new Set([workspaceId]) : new Set(),
  );
  const [renamingWs, setRenamingWs] = useState<string | null>(null);
  const [renamingSession, setRenamingSession] = useState<{ wsId: string; sessionId: string; title: string } | null>(null);

  const { data: workspaces } = useQuery({
    queryKey: ["workspaces"],
    queryFn: () => workspacesApi.list(),
  });

  useEffect(() => {
    if (workspaces?.items) {
      setExpandedWs((prev) => {
        const next = new Set(prev);
        workspaces.items.forEach((w) => next.add(w.id));
        return next;
      });
    }
  }, [workspaces?.items]);

  const createMutation = useMutation({
    mutationFn: async (params: { name: string }) => {
      const ws = await workspacesApi.create(params);
      return ws;
    },
    onSuccess: (ws) => {
      queryClient.invalidateQueries({ queryKey: ["workspaces"] });
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

  const renameWsMutation = useMutation({
    mutationFn: ({ wsId, name }: { wsId: string; name: string }) =>
      workspacesApi.renameWorkspace(wsId, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      setRenamingWs(null);
    },
  });

  const deleteWsMutation = useMutation({
    mutationFn: (wsId: string) => workspacesApi.deleteWorkspace(wsId),
    onSuccess: (_data, wsId) => {
      queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      if (workspaceId === wsId) {
        navigate("/chat");
      }
    },
  });

  const renameSessionMutation = useMutation({
    mutationFn: ({ wsId, sessionId, title }: { wsId: string; sessionId: string; title: string }) =>
      workspacesApi.renameSession(wsId, sessionId, title),
    onSuccess: (_data, vars) => {
      queryClient.invalidateQueries({ queryKey: ["sessions", vars.wsId] });
      setRenamingSession(null);
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
  };

  const handleSessionClick = (wsId: string, sid: string) => {
    navigate(`/chat/${wsId}/${sid}`);
    onNavigate?.();
  };

  return (
    <aside className="flex h-full flex-col border-r border-border bg-card resize-x overflow-hidden min-w-48 max-w-96" style={{ width: "16rem" }} aria-label="Navigation">
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        <h1 className="text-sm font-semibold">Safe Space</h1>
        <button
          onClick={() => createMutation.mutate({ name: generateWorkspaceName() })}
          disabled={createMutation.isPending}
          className="rounded p-1 hover:bg-accent disabled:opacity-50"
          aria-label="New workspace"
        >
          {createMutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Plus className="h-4 w-4" />}
        </button>
      </div>

      <div className="flex-1 overflow-y-auto overflow-x-hidden scrollbar-thin scrollbar-track-transparent scrollbar-thumb-muted-foreground/20 hover:scrollbar-thumb-muted-foreground/40">
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
              isRenaming={renamingWs === ws.id}
              onRenameClick={() => setRenamingWs(ws.id)}
              onRenameCancel={() => setRenamingWs(null)}
              onRenameConfirm={(name) => renameWsMutation.mutate({ wsId: ws.id, name })}
              onDelete={() => {
                if (window.confirm(`Delete workspace "${ws.name}"?`)) {
                  deleteWsMutation.mutate(ws.id);
                }
              }}
              onRenameSession={(sessionId, title) => setRenamingSession({ wsId: ws.id, sessionId, title })}
              onDeleteSession={(sessionId) => {
                if (window.confirm("Delete this session?")) {
                  queryClient.invalidateQueries({ queryKey: ["sessions", ws.id] });
                  workspacesApi.renameSession(ws.id, sessionId, "").then(() => {
                    queryClient.invalidateQueries({ queryKey: ["sessions", ws.id] });
                  });
                }
              }}
              renamingSession={renamingSession?.wsId === ws.id ? renamingSession : null}
              onRenameSessionCancel={() => setRenamingSession(null)}
              onRenameSessionConfirm={(sessionId, title) =>
                renameSessionMutation.mutate({ wsId: ws.id, sessionId, title })
              }
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
  isRenaming: boolean;
  onRenameClick: () => void;
  onRenameCancel: () => void;
  onRenameConfirm: (name: string) => void;
  onDelete: () => void;
  onRenameSession: (sessionId: string, title: string) => void;
  onDeleteSession: (sessionId: string) => void;
  renamingSession: { wsId: string; sessionId: string; title: string } | null;
  onRenameSessionCancel: () => void;
  onRenameSessionConfirm: (sessionId: string, title: string) => void;
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
  isRenaming,
  onRenameClick,
  onRenameCancel,
  onRenameConfirm,
  onDelete,
  onRenameSession,
  onDeleteSession,
  renamingSession,
  onRenameSessionCancel,
  onRenameSessionConfirm,
}: WorkspaceGroupProps) {
  const isSelected = workspace.id === selectedWorkspaceId;
  const isSuspended = workspace.phase === "Suspended";
  const isResuming = workspace.phase === "Resuming";
  const isActive = workspace.phase === "Active";

  const kebabItems: KebabMenuItem[] = [
    { label: "Rename", onClick: onRenameClick },
    { label: "Delete", onClick: onDelete, destructive: true },
  ];

  return (
    <div>
      {isRenaming ? (
        <div className="border-b border-border">
          <RenameWorkspaceDialog
            currentName={workspace.name}
            onRename={onRenameConfirm}
            onCancel={onRenameCancel}
          />
        </div>
      ) : (
        <div className={cn(
          "group flex items-center rounded-md transition-colors hover:bg-accent/50",
          isSelected && "bg-accent",
        )}>
          <button
            onClick={onToggle}
            className={cn(
              "flex flex-1 items-center gap-1.5 rounded-md px-3 py-2 text-left text-sm transition-colors",
              isSelected ? "text-accent-foreground" : "",
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
          {isActive && (
            <button
              onClick={onNewSession}
              disabled={creatingSession}
              className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-50 opacity-0 group-hover:opacity-100 transition-opacity"
              aria-label="New chat"
              title="New chat"
            >
              {creatingSession ? <Loader2 className="h-3 w-3 animate-spin" /> : <Plus className="h-3 w-3" />}
            </button>
          )}
          <div className="mr-1">
            <KebabMenu items={kebabItems} align="left" />
          </div>
        </div>
      )}

      {expanded && (
        <WorkspaceSessionList
          workspaceId={workspace.id}
          selectedSessionId={selectedSessionId}
          onSelectSession={onSelectSession}
          onNewSession={onNewSession}
          creatingSession={creatingSession}
          isSuspended={isSuspended || isResuming}
          onRenameSession={onRenameSession}
          onDeleteSession={onDeleteSession}
          renamingSession={renamingSession}
          onRenameSessionCancel={onRenameSessionCancel}
          onRenameSessionConfirm={onRenameSessionConfirm}
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
  onRenameSession: (sessionId: string, title: string) => void;
  onDeleteSession: (sessionId: string) => void;
  renamingSession: { wsId: string; sessionId: string; title: string } | null;
  onRenameSessionCancel: () => void;
  onRenameSessionConfirm: (sessionId: string, title: string) => void;
}

function WorkspaceSessionList({
  workspaceId,
  selectedSessionId,
  onSelectSession,
  onNewSession: _onNewSession,
  creatingSession: _creatingSession,
  isSuspended,
  onRenameSession,
  onDeleteSession,
  renamingSession,
  onRenameSessionCancel,
  onRenameSessionConfirm,
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
      {isLoading && (
        <div className="px-2 py-1 text-xs text-muted-foreground">Loading...</div>
      )}

      {!isLoading && (!sessions || sessions.length === 0) && (
        <div className="px-2 py-1 text-xs text-muted-foreground">No sessions yet</div>
      )}

      {sessions && sessions.length > 0 && (
        <div className="flex flex-col gap-0.5">
          {sessions.map((s) => {
            const title = sessionDisplayTitle(s.title, s.lastMessageAt);
            const isRenaming = renamingSession?.sessionId === s.id;

            if (isRenaming) {
              return (
                <div key={s.id} className="border rounded-md border-border ml-2">
                  <RenameSessionDialog
                    currentTitle={s.title ?? ""}
                    onRename={(newTitle) => onRenameSessionConfirm(s.id, newTitle)}
                    onCancel={onRenameSessionCancel}
                  />
                </div>
              );
            }

            const kebabItems: KebabMenuItem[] = [
              {
                label: "Copy link",
                onClick: () => navigator.clipboard.writeText(`${window.location.origin}/chat/${workspaceId}/${s.id}`),
              },
              {
                label: "Rename",
                onClick: () => onRenameSession(s.id, s.title ?? ""),
              },
              {
                label: "Delete",
                onClick: () => onDeleteSession(s.id),
                destructive: true,
              },
            ];

            return (
              <div
                key={s.id}
                className={cn(
                  "group flex items-center rounded-md transition-colors hover:bg-accent/50",
                  s.id === selectedSessionId && "bg-accent",
                )}
              >
                <button
                  onClick={() => onSelectSession(s.id)}
                  className={cn(
                    "flex flex-1 items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors overflow-hidden",
                    s.id === selectedSessionId
                      ? "text-accent-foreground"
                      : "text-muted-foreground",
                  )}
                >
                  <MessageSquare className="h-3.5 w-3.5 flex-shrink-0" />
                  <span className="flex-1 truncate">{title}</span>
                  {s.lastMessageAt && (
                    <span className="flex-shrink-0 text-xs text-muted-foreground/60">{formatRelativeTime(s.lastMessageAt)}</span>
                  )}
                  {s.status === "active" && (
                    <span className="h-1.5 w-1.5 rounded-full bg-blue-500 flex-shrink-0" />
                  )}
                </button>
                <div className="mr-1 flex-shrink-0">
                  <KebabMenu items={kebabItems} align="left" />
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
