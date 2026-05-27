import { api } from "./client";
import type { Message, SendMessageRequest } from "./types";

interface OpenCodeMessage {
  info?: {
    role?: string;
    id?: string;
  };
  id?: string;
  role?: string;
  parts?: Array<{
    type: string;
    text?: string;
  }>;
}

function transformHistory(raw: OpenCodeMessage[]): Message[] {
  return raw
    .filter((m) => {
      const role = m.info?.role ?? m.role;
      return role === "user" || role === "assistant";
    })
    .map((m) => ({
      id: m.info?.id ?? m.id ?? `msg-${Math.random()}`,
      role: (m.info?.role ?? m.role) as "user" | "assistant",
      parts: (m.parts ?? []).filter((p) => {
        if (!p.text) return false;
        return p.type === "text" || p.type === "thinking" || p.type === "reasoning";
      }),
    }))
    .filter((m) => m.parts.length > 0);
}

export const messagesApi = {
  getHistory: async (workspaceId: string, sessionId: string): Promise<Message[]> => {
    const raw = await api.get<OpenCodeMessage[]>(
      `/workspaces/${workspaceId}/sessions/${sessionId}/message`,
    );
    return transformHistory(raw);
  },
  sendAsync: (workspaceId: string, sessionId: string, req: SendMessageRequest) =>
    api.post<void>(`/workspaces/${workspaceId}/sessions/${sessionId}/prompt`, req),
};
