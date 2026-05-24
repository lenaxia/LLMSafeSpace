import { api } from "./client";

export const sessionsApi = {
  create: (sandboxId: string, title?: string) =>
    api.post<{ id: string }>(`/sandboxes/${sandboxId}/sessions`, title ? { title } : {}),
};
