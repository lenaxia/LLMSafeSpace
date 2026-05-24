import { api, streamRequest } from "./client";
import type { Message, SendMessageRequest } from "./types";

export const messagesApi = {
  getHistory: (sandboxId: string, sessionId: string) =>
    api.get<Message[]>(`/sandboxes/${sandboxId}/sessions/${sessionId}/message`),
  send: (sandboxId: string, sessionId: string, req: SendMessageRequest) =>
    streamRequest(`/sandboxes/${sandboxId}/sessions/${sessionId}/message`, req),
};
