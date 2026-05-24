import { api } from "./client";
import type {
  ActivateWorkspaceResponse,
  ActiveSessionsResponse,
  SandboxListItem,
  SessionListItem,
  WorkspaceListResponse,
  WorkspaceStatus,
} from "./types";

export const workspacesApi = {
  list: () => api.get<WorkspaceListResponse>("/workspaces"),
  create: (params: { name: string; runtime?: string }) =>
    api.post<{ id: string; name: string }>("/workspaces", {
      name: params.name,
      runtime: params.runtime || "python:3.11",
      storageSize: "5Gi",
    }),
  getStatus: (id: string) => api.get<WorkspaceStatus>(`/workspaces/${id}/status`),
  activate: (id: string) => api.post<ActivateWorkspaceResponse>(`/workspaces/${id}/activate`),
  suspend: (id: string) => api.post<void>(`/workspaces/${id}/suspend`),
  getSessions: (id: string) => api.get<SessionListItem[]>(`/workspaces/${id}/sessions`),
  getActiveSessions: (id: string) => api.get<ActiveSessionsResponse>(`/workspaces/${id}/sessions/active`),
  getSandboxes: (id: string) => api.get<SandboxListItem[]>(`/workspaces/${id}/sandboxes`),
  renameSession: (workspaceId: string, sessionId: string, title: string) =>
    api.put<void>(`/workspaces/${workspaceId}/sessions/${sessionId}/title`, { title }),
};
