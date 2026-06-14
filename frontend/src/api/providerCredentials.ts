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
  modelContextLimits: Record<string, number>;
  createdAt: string;
  updatedAt: string;
}

export interface CreateAdminCredentialRequest {
  name: string;
  provider: string;
  apiKey: string;
  baseURL?: string;
  modelAllowlist?: string[];
  modelContextLimits?: Record<string, number>;
}

export interface UpdateAdminCredentialRequest {
  name?: string;
  apiKey?: string;
  baseURL?: string;
  modelAllowlist?: string[];
  modelContextLimits?: Record<string, number>;
}

// ProbeModelEntry is one entry from GET /:id/models.
export interface ProbeModelEntry {
  id: string;
  contextLimit: number; // 0 = unknown / not yet configured
}

// ProbeModelsResponse is the response from GET /provider-credentials/:id/models
// and GET /admin/provider-credentials/:id/models.
export interface ProbeModelsResponse {
  models: ProbeModelEntry[];
  baseURL?: string;
  warning?: string;
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
  probeModels: (id: string) =>
    api.get<ProbeModelsResponse>(`/admin/provider-credentials/${id}/models`),
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
  modelContextLimits?: Record<string, number>;
  createdAt: string;
  updatedAt: string;
}

export interface CreateUserCredentialRequest {
  name: string;
  provider: string;
  apiKey: string;
  baseURL?: string;
  modelAllowlist?: string[];
  modelContextLimits?: Record<string, number>;
}

// CredentialBindingInfo is returned by GET /provider-credentials/:id/bindings.
// sourceType distinguishes user-initiated (explicit) from seeded (auto) bindings.
// Auto-bound workspaces cannot be unbound via the UI — the backend returns 409 Conflict.
export interface CredentialBindingInfo {
  workspaceId: string;
  sourceType: "explicit" | "auto";
}

export interface ListBindingsResponse {
  workspaceIds: string[];
  bindings: CredentialBindingInfo[];
}

// CreateUserCredentialResponse: 201 on success, 207 when credential was created
// but auto-bind to existing workspaces failed (bindWarning present).
export interface CreateUserCredentialResponse extends UserProviderCredential {
  bindWarning?: string;
  credential?: UserProviderCredential; // present on 207 — cred nested under this key
}

export const userProviderCredentialsApi = {
  list: () => api.get<UserProviderCredential[]>("/provider-credentials"),
  get: (id: string) => api.get<UserProviderCredential>(`/provider-credentials/${id}`),
  create: (req: CreateUserCredentialRequest) =>
    api.post<CreateUserCredentialResponse>("/provider-credentials", req),
  delete: (id: string) => api.delete<void>(`/provider-credentials/${id}`),
  probeModels: (id: string) =>
    api.get<ProbeModelsResponse>(`/provider-credentials/${id}/models`),
  // Probe models without a saved credential — pass apiKey + baseURL directly.
  // Used in the create form to show the model list before saving.
  probeModelsAnon: (apiKey: string, baseURL: string) =>
    api.post<ProbeModelsResponse>("/probe-models", { apiKey, baseURL }),
  listBindings: (id: string) =>
    api.get<ListBindingsResponse>(`/provider-credentials/${id}/bindings`),
  bindToWorkspace: (id: string, workspaceId: string) =>
    api.post<{ bound: boolean }>(`/provider-credentials/${id}/bind/${workspaceId}`, {}),
  unbindFromWorkspace: (id: string, workspaceId: string) =>
    api.delete<void>(`/provider-credentials/${id}/bind/${workspaceId}`),
};
