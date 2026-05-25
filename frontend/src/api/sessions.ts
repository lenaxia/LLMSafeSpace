import { api } from "./client";

export const sessionsApi = {
  create: (workspaceId: string, title?: string) =>
    api.post<{ id: string }>(`/workspaces/${workspaceId}/sessions/new`, title ? { title } : {}),
};
