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

// Go handler validates "all" | "user" | "org" — "workspace" is not a valid type.
export interface AutoApplyRule {
  credentialId: string;
  targetType: "all" | "user" | "org";
  targetId?: string;
  withinPriority: number;
}

export interface CreateAutoApplyRequest {
  targetType: "all" | "user" | "org";
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
  deleteAutoApply: (id: string, targetType: string, targetId?: string) =>
    // The backend route always requires both :targetType and :targetId path segments
    // (DELETE /:id/auto-apply/:targetType/:targetId). For "all" rules the handler
    // ignores the targetId value, so we send "_" as a sentinel.
    api.delete<void>(
      `/admin/provider-credentials/${id}/auto-apply/${targetType}/${targetId ?? "_"}`
    ),
};

// ---------------------------------------------------------------------------
// User provider credentials (Epic 30 US-30.8)
// Routes: /api/v1/provider-credentials
// The backend returns AdminCredentialResponse for user creds too, so include
// baseURL and modelAllowlist even though users can't set them from the UI yet.
// ---------------------------------------------------------------------------

export interface UserProviderCredential {
  id: string;
  name: string;
  provider: string;
  baseURL?: string;
  modelAllowlist?: string[];
  createdAt: string;
  updatedAt: string;
}

export interface CreateUserCredentialRequest {
  name: string;
  provider: string;
  apiKey: string;
  baseURL?: string;
  modelAllowlist?: string[];
}

export const userProviderCredentialsApi = {
  list: () => api.get<UserProviderCredential[]>("/provider-credentials"),
  get: (id: string) => api.get<UserProviderCredential>(`/provider-credentials/${id}`),
  create: (req: CreateUserCredentialRequest) =>
    api.post<UserProviderCredential>("/provider-credentials", req),
  delete: (id: string) => api.delete<void>(`/provider-credentials/${id}`),
  listBindings: (id: string) =>
    api.get<{ workspaceIds: string[] }>(`/provider-credentials/${id}/bindings`),
  bindToWorkspace: (id: string, workspaceId: string) =>
    api.post<{ bound: boolean }>(`/provider-credentials/${id}/bind/${workspaceId}`, {}),
  unbindFromWorkspace: (id: string, workspaceId: string) =>
    api.delete<void>(`/provider-credentials/${id}/bind/${workspaceId}`),
};
