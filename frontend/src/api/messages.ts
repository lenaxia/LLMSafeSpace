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
        if (p.type === "tool") return true;
        if (!p.text) return false;
        return p.type === "text" || p.type === "thinking" || p.type === "reasoning";
      }).map((p) => {
        if (p.type === "tool") {
          // Map opencode tool part fields to our MessagePart shape
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
