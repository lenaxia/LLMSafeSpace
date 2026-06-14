import { api, getRaw } from "./client";
import type { Message, SendMessageRequest } from "./types";

interface OpenCodeMessage {
  info?: {
    role?: string;
    id?: string;
    time?: { created?: number; completed?: number };
    modelID?: string;
    providerID?: string;
  };
  id?: string;
  role?: string;
  parts?: Array<{
    type: string;
    text?: string;
  }>;
}

export function transformHistory(raw: OpenCodeMessage[]): Message[] {
  return raw
    .filter((m) => {
      const role = m.info?.role ?? m.role;
      return role === "user" || role === "assistant";
    })
    .map((m) => ({
      id: m.info?.id ?? m.id ?? `msg-${Math.random()}`,
      role: (m.info?.role ?? m.role) as "user" | "assistant",
      parts: (m.parts ?? []).filter((p) => {
        if (p.type === "tool") return true;
        if (!p.text) return false;
        return p.type === "text" || p.type === "thinking" || p.type === "reasoning";
      }).map((p) => {
        if (p.type === "tool") {
          const part = p as Record<string, unknown>;
          const state = part.state as Record<string, unknown> | undefined;
          const toolName = (part.tool as string) || "";
          const title = (state?.title as string) || "";
          const toolState = (state?.status as string) || "";
          return {
            type: "tool_use" as const,
            text: title ? `${toolName}: ${title}` : toolName,
            toolState,
            input: state?.input,
            toolOutput: (state?.output as string) || undefined,
          };
        }
        return p;
      }),
      createdAt: m.info?.time?.created
        ? new Date(m.info.time.created).toISOString()
        : undefined,
      modelID: m.info?.modelID ?? undefined,
    }))
    .filter((m) => m.parts.length > 0);
}

export interface HistoryPage {
  messages: Message[];
  nextCursor?: string;
}

const PAGE_LIMIT = 50;

export const messagesApi = {
  getHistory: async (workspaceId: string, sessionId: string): Promise<Message[]> => {
    const raw = await api.get<OpenCodeMessage[]>(
      `/workspaces/${workspaceId}/sessions/${sessionId}/message`,
    );
    return transformHistory(raw);
  },
  getHistoryPage: async (
    workspaceId: string,
    sessionId: string,
    opts?: { before?: string },
  ): Promise<HistoryPage> => {
    const params = new URLSearchParams();
    params.set("limit", String(PAGE_LIMIT));
    if (opts?.before) params.set("before", opts.before);
    const { data, headers } = await getRaw<OpenCodeMessage[]>(
      `/workspaces/${workspaceId}/sessions/${sessionId}/message?${params.toString()}`,
    );
    return {
      messages: transformHistory(data),
      nextCursor: headers.get("X-Next-Cursor") ?? undefined,
    };
  },
  sendAsync: (workspaceId: string, sessionId: string, req: SendMessageRequest) =>
    api.post<void>(`/workspaces/${workspaceId}/sessions/${sessionId}/prompt`, req),
  queueMessage: (workspaceId: string, sessionId: string, text: string) =>
    api.post<{ messageID: string }>(`/workspaces/${workspaceId}/sessions/${sessionId}/queue`, { text }),
  getQueue: async (workspaceId: string, sessionId: string) => {
    const res = await api.get<{ messages: Array<{
      id: string; text: string; session_id: string; workspace_id: string; enqueued_at: string; retry_count: number;
    }> }>(`/workspaces/${workspaceId}/sessions/${sessionId}/queue`);
    return res;
  },
  deleteQueueMessage: (workspaceId: string, sessionId: string, messageId: string) =>
    api.delete<void>(`/workspaces/${workspaceId}/sessions/${sessionId}/queue/${messageId}`),
};
