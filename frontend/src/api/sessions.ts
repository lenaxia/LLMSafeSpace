import { api } from "./client";

interface CreateSessionResponse {
  sessionId: string;
  workspaceId: string;
  workspacePhase: string;
  resumed: boolean;
}

export const sessionsApi = {
  create: (workspaceId: string, title?: string) =>
    api.post<CreateSessionResponse>(`/workspaces/${workspaceId}/sessions/new`, title ? { title } : {}),
};
