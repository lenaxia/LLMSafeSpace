import { api } from "./client";

export interface PlatformPrompt {
  prompt: string;
}

export interface OrgPrompt {
  prompt: string;
  allowUserPrompt: boolean;
}

export const promptsApi = {
  getPlatform: () => api.get<PlatformPrompt>("/admin/prompt"),
  setPlatform: (prompt: string) => api.put<{ status: string }>("/admin/prompt", { prompt }),
  getOrg: (orgId: string) => api.get<OrgPrompt>(`/orgs/${orgId}/prompt`),
  setOrg: (orgId: string, body: { prompt?: string; allowUserPrompt?: boolean }) =>
    api.put<{ status: string }>(`/orgs/${orgId}/prompt`, body),
};
