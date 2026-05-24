import { api, streamRequest } from "./client";
import type { Message, SendMessageRequest } from "./types";

export const messagesApi = {
  getHistory: (workspaceId: string, sessionId: string) =>
    api.get<Message[]>(`/workspaces/${workspaceId}/sessions/${sessionId}/message`),
  send: (workspaceId: string, sessionId: string, req: SendMessageRequest) =>
    streamRequest(`/workspaces/${workspaceId}/sessions/${sessionId}/message`, req),
};
