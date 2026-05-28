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

export interface RotateKeyResult {
  rotated: number;
  alreadyCurrent: number;
  errors: number;
}

export const credentialsApi = {
  list: () => api.get<CredentialSet[]>("/admin/credentials"),
  create: (req: CreateCredentialSetRequest) => api.post<CredentialSet>("/admin/credentials", req),
  delete: (id: string) => api.delete<void>(`/admin/credentials/${id}`),
  setDefault: (id: string) => api.put<void>(`/admin/credentials/${id}/default`),
  rotateKey: () => api.post<RotateKeyResult>("/admin/credentials/rotate-key"),
};
