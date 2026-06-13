import { api } from "./client";
import type { WorkspaceListItem } from "./types";

export interface Organization {
  id: string;
  name: string;
  slug: string;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
}

export interface OrgResponse {
  id: string;
  name: string;
  slug: string;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
  userRole: "admin" | "member";
  userPendingKeyWrap: boolean;
  memberCount: number;
}

export interface OrgMember {
  orgId: string;
  userId: string;
  username: string;
  email: string;
  role: "admin" | "member";
  pendingKeyWrap: boolean;
  createdAt: string;
}

export interface OrgCredential {
  id: string;
  orgId: string;
  name: string;
  provider: string;
  modelAllowlist: string[];
  createdAt: string;
  updatedAt: string;
}

export interface CreateOrgRequest {
  name: string;
  slug: string;
  password: string;
}

export interface AddMemberRequest {
  userId: string;
  role: "admin" | "member";
}

export interface AcceptKeyRequest {
  password: string;
}

export interface CreateOrgCredentialRequest {
  name: string;
  provider: string;
  apiKey: string;
  baseURL?: string;
  modelAllowlist?: string[];
}

export interface UpdateOrgCredentialRequest {
  name?: string;
  apiKey?: string;
  modelAllowlist?: string[];
}

export const orgsApi = {
  list: () => api.get<OrgResponse[]>("/orgs"),
  create: (req: CreateOrgRequest) => api.post<Organization>("/orgs", req),
  get: (id: string) => api.get<Organization>(`/orgs/${id}`),
  update: (id: string, req: { name?: string; slug?: string }) =>
    api.put<Organization>(`/orgs/${id}`, req),
  delete: (id: string) => api.delete<void>(`/orgs/${id}`),

  listMembers: (id: string) => api.get<OrgMember[]>(`/orgs/${id}/members`),
  addMember: (id: string, req: AddMemberRequest) =>
    api.post<OrgMember>(`/orgs/${id}/members`, req),
  removeMember: (id: string, userId: string) =>
    api.delete<void>(`/orgs/${id}/members/${userId}`),
  acceptKey: (id: string, req: AcceptKeyRequest) =>
    api.post<{ message: string }>(`/orgs/${id}/accept-key`, req),
  rotateKey: (id: string, req: { password: string }) =>
    api.post<{ message: string }>(`/orgs/${id}/rotate-key`, req),

  listCredentials: (id: string) =>
    api.get<OrgCredential[]>(`/orgs/${id}/credentials`),
  createCredential: (id: string, req: CreateOrgCredentialRequest) =>
    api.post<OrgCredential>(`/orgs/${id}/credentials`, req),
  updateCredential: (id: string, credId: string, req: UpdateOrgCredentialRequest) =>
    api.put<OrgCredential>(`/orgs/${id}/credentials/${credId}`, req),
  deleteCredential: (id: string, credId: string) =>
    api.delete<void>(`/orgs/${id}/credentials/${credId}`),

  listWorkspaces: (id: string) =>
    api.get<{ items: WorkspaceListItem[] }>(`/orgs/${id}/workspaces`),
};
