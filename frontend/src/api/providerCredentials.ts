import { api } from "./client";

// ---------------------------------------------------------------------------
// Admin provider credentials (Epic 30 US-30.7)
// Routes: /api/v1/admin/provider-credentials
// ---------------------------------------------------------------------------

export interface AdminProviderCredential {
  id: string;
  name: string;
  provider: string;
  baseURL?: string;
  modelAllowlist: string[];
  createdAt: string;
  updatedAt: string;
}

export interface CreateAdminCredentialRequest {
  name: string;
  provider: string;
  apiKey: string;
  baseURL?: string;
  modelAllowlist?: string[];
}

export interface UpdateAdminCredentialRequest {
  name?: string;
  apiKey?: string;
  baseURL?: string;
  modelAllowlist?: string[];
}

export interface AutoApplyRule {
  id: string;
  credentialId: string;
  targetType: "all" | "user" | "workspace";
  targetId: string;
  priority: number;
}

export interface CreateAutoApplyRequest {
  targetType: "all" | "user" | "workspace";
  targetId?: string;
  priority?: number;
}

export const adminProviderCredentialsApi = {
  list: () => api.get<AdminProviderCredential[]>("/admin/provider-credentials"),
  get: (id: string) => api.get<AdminProviderCredential>(`/admin/provider-credentials/${id}`),
  create: (req: CreateAdminCredentialRequest) =>
    api.post<AdminProviderCredential>("/admin/provider-credentials", req),
  update: (id: string, req: UpdateAdminCredentialRequest) =>
    api.put<AdminProviderCredential>(`/admin/provider-credentials/${id}`, req),
  delete: (id: string) => api.delete<void>(`/admin/provider-credentials/${id}`),
  listAutoApply: (id: string) =>
    api.get<AutoApplyRule[]>(`/admin/provider-credentials/${id}/auto-apply`),
  createAutoApply: (id: string, req: CreateAutoApplyRequest) =>
    api.post<AutoApplyRule>(`/admin/provider-credentials/${id}/auto-apply`, req),
  deleteAutoApply: (id: string, targetType: string, targetId: string) =>
    api.delete<void>(`/admin/provider-credentials/${id}/auto-apply/${targetType}/${targetId}`),
};

// ---------------------------------------------------------------------------
// User provider credentials (Epic 30 US-30.8)
// Routes: /api/v1/provider-credentials
// ---------------------------------------------------------------------------

export interface UserProviderCredential {
  id: string;
  name: string;
  provider: string;
  createdAt: string;
  updatedAt: string;
}

export interface CreateUserCredentialRequest {
  name: string;
  provider: string;
  apiKey: string;
  baseURL?: string;
}

export const userProviderCredentialsApi = {
  list: () => api.get<UserProviderCredential[]>("/provider-credentials"),
  get: (id: string) => api.get<UserProviderCredential>(`/provider-credentials/${id}`),
  create: (req: CreateUserCredentialRequest) =>
    api.post<UserProviderCredential>("/provider-credentials", req),
  delete: (id: string) => api.delete<void>(`/provider-credentials/${id}`),
  bindToWorkspace: (id: string, workspaceId: string) =>
    api.post<void>(`/provider-credentials/${id}/bind/${workspaceId}`, {}),
  unbindFromWorkspace: (id: string, workspaceId: string) =>
    api.delete<void>(`/provider-credentials/${id}/bind/${workspaceId}`),
};
