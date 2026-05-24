import { api } from "./client";
import type {
  ActivateWorkspaceResponse,
  ActiveSessionsResponse,
  WorkspaceListItem,
  SessionListItem,
  WorkspaceListResponse,
  WorkspaceStatus,
} from "./types";

export interface EnsureSessionResponse {
  workspaceId: string;
  workspacePhase: string;
  sessionId: string;
  resumed: boolean;
}

export const workspacesApi = {
  list: () => api.get<WorkspaceListResponse>("/workspaces"),
  create: (params: { name: string; runtime?: string }) =>
    api.post<{ id: string; name: string; workspaceId?: string }>("/workspaces", {
      name: params.name,
      runtime: params.runtime || "base",
      storageSize: "5Gi",
    }),
  createWorkspace: (workspaceId: string, runtime = "base") =>
    api.post<{ id: string }>("/workspaces", { runtime, workspaceRef: workspaceId }),
  ensureSession: (workspaceId: string) =>
    api.post<EnsureSessionResponse>(`/workspaces/${workspaceId}/sessions/new`),
  getStatus: (id: string) => api.get<WorkspaceStatus>(`/workspaces/${id}/status`),
  activate: (id: string) => api.post<ActivateWorkspaceResponse>(`/workspaces/${id}/activate`),
  suspend: (id: string) => api.post<void>(`/workspaces/${id}/suspend`),
  getSessions: (id: string) => api.get<SessionListItem[]>(`/workspaces/${id}/sessions`),
  getActiveSessions: (id: string) => api.get<ActiveSessionsResponse>(`/workspaces/${id}/sessions/active`),
  getWorkspaceSessions: (id: string) => api.get<WorkspaceListItem[]>(`/workspaces/${id}/sessions`),
  renameSession: (workspaceId: string, sessionId: string, title: string) =>
    api.put<void>(`/workspaces/${workspaceId}/sessions/${sessionId}/title`, { title }),
};
