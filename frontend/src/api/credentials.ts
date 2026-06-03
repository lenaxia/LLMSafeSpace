import { api } from "./client";

export interface CredentialSet {
  id: string;
  name: string;
  isDefault: boolean;
  providers: string[];
  modelAllowlist: string[];
  assignedTo: "all" | string[];
  keyVersion: number;
  createdAt: string;
  updatedAt: string;
}

export interface CreateCredentialSetRequest {
  name: string;
  providers: Record<string, { apiKey: string; baseUrl?: string }>;
  modelAllowlist?: string[];
  assignedTo?: "all" | string[];
  isDefault?: boolean;
}

// Partial update — only included fields are changed. providers replaces
// the full provider config (all existing keys are dropped); to add a
// single key without removing others, fetch first then merge client-side.
export interface UpdateCredentialSetRequest {
  name?: string;
  providers?: Record<string, { apiKey: string; baseUrl?: string }>;
  modelAllowlist?: string[];
  assignedTo?: "all" | string[];
  isDefault?: boolean;
}

export interface RotateKeyResult {
  rotated: number;
  alreadyCurrent: number;
  errors: number;
}

export const credentialsApi = {
  list: () => api.get<CredentialSet[]>("/admin/credentials"),
  get: (id: string) => api.get<CredentialSet>(`/admin/credentials/${id}`),
  create: (req: CreateCredentialSetRequest) => api.post<CredentialSet>("/admin/credentials", req),
  update: (id: string, req: UpdateCredentialSetRequest) =>
    api.put<{ status: string }>(`/admin/credentials/${id}`, req),
  delete: (id: string) => api.delete<void>(`/admin/credentials/${id}`),
  setDefault: (id: string) => api.put<void>(`/admin/credentials/${id}/default`),
  rotateKey: () => api.post<RotateKeyResult>("/admin/credentials/rotate-key"),
};
