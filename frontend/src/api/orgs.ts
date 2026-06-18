import { api } from "./client";
import type { ProviderCredential } from "./providerCredentialTypes";
import type { WorkspaceListItem } from "./types";

export type OrgStatus = "pending_activation" | "active" | "suspended";
export type OrgPlan = "free" | "team" | "business" | "enterprise";
export type OrgSubscriptionStatus =
  | "inactive"
  | "active"
  | "trialing"
  | "past_due"
  | "canceled"
  | "unpaid";

export interface Organization {
  id: string;
  name: string;
  slug: string;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
  status: OrgStatus;
  planId: OrgPlan;
  subscriptionStatus: OrgSubscriptionStatus;
}

export interface OrgResponse extends Organization {
  userRole: "admin" | "member";
  memberCount: number;
}

export interface CreateOrgResponse extends OrgResponse {}

export interface OrgMember {
  orgId: string;
  userId: string;
  username: string;
  email: string;
  role: "admin" | "member";
  createdAt: string;
}

export interface OrgCredential extends ProviderCredential {
  orgId: string;
  modelAllowlist: string[];
  modelContextLimits: Record<string, number>;
  /** Present when the credential was created but org-workspace auto-bind failed. */
  bindWarning?: string;
}

export interface OrgInvitation {
  id: string;
  orgId: string;
  email: string;
  role: "admin" | "member";
  invitedBy: string;
  expiresAt: string;
  acceptedAt?: string;
  declinedAt?: string;
  bounceType?: string;
  bouncedAt?: string;
  createdAt: string;
}

export interface AuditEntry {
  id: number;
  actorId: string;
  domain: string;
  action: string;
  targetId?: string;
  orgId?: string;
  metadata?: Record<string, unknown>;
  createdAt: string;
}

export interface InvitationDetail {
  orgName: string;
  orgSlug: string;
  inviterName: string;
  role: "admin" | "member";
  expiresAt: string;
}

export interface CreateOrgRequest {
  name: string;
  slug: string;
  /** Owner's email; resolved to a user ID server-side (design 0031 D1). */
  ownerEmail: string;
  planId?: OrgPlan;
}

export interface CreateInvitationsRequest {
  emails: string[];
  role: "admin" | "member";
}

export const orgsApi = {
  list: () => api.get<OrgResponse[]>("/orgs"),
  create: (req: CreateOrgRequest) =>
    api.post<CreateOrgResponse>("/orgs", req),
  get: (id: string) => api.get<OrgResponse>(`/orgs/${id}`),
  update: (id: string, req: { name?: string; slug?: string }) =>
    api.put<Organization>(`/orgs/${id}`, req),
  delete: (id: string) => api.delete<void>(`/orgs/${id}`),

  listMembers: (id: string) => api.get<OrgMember[]>(`/orgs/${id}/members`),
  addMember: (id: string, req: { userId: string; role: "admin" | "member" }) =>
    api.post<OrgMember>(`/orgs/${id}/members`, req),
  removeMember: (id: string, userId: string) =>
    api.delete<void>(`/orgs/${id}/members/${userId}`),
  changeMemberRole: (id: string, userId: string, role: "admin" | "member") =>
    api.put<{ message: string }>(`/orgs/${id}/members/${userId}`, { role }),

  listInvitations: (id: string) =>
    api.get<OrgInvitation[]>(`/orgs/${id}/invitations`),
  createInvitations: (id: string, req: CreateInvitationsRequest) =>
    api.post<OrgInvitation[]>(`/orgs/${id}/invitations`, req),
  revokeInvitation: (id: string, invId: string) =>
    api.delete<void>(`/orgs/${id}/invitations/${invId}`),
  resendInvitation: (id: string, invId: string) =>
    api.post<OrgInvitation>(`/orgs/${id}/invitations/${invId}/resend`),

  getInvitationByToken: (token: string) =>
    api.get<InvitationDetail>(`/invitations/${token}`),
  acceptInvitation: (token: string) =>
    api.post<{ membership: OrgMember }>(`/invitations/${token}/accept`),
  declineInvitation: (token: string) =>
    api.post<{ status: string }>(`/invitations/${token}/decline`),

  listCredentials: (id: string) =>
    api.get<OrgCredential[]>(`/orgs/${id}/credentials`),
  createCredential: (
    id: string,
    req: {
      name: string;
      provider: string;
      apiKey: string;
      baseURL?: string;
      modelAllowlist?: string[];
      modelContextLimits?: Record<string, number>;
    },
  ) => api.post<OrgCredential>(`/orgs/${id}/credentials`, req),
  updateCredential: (
    id: string,
    credId: string,
    req: {
      name?: string;
      apiKey?: string;
      baseURL?: string;
      modelAllowlist?: string[];
      modelContextLimits?: Record<string, number>;
    },
  ) => api.put<OrgCredential>(`/orgs/${id}/credentials/${credId}`, req),
  deleteCredential: (id: string, credId: string) =>
    api.delete<void>(`/orgs/${id}/credentials/${credId}`),
  probeCredentialModels: (id: string, credId: string) =>
    api.get<{ models: { id: string; contextLimit: number }[]; baseURL?: string; warning?: string }>(
      `/orgs/${id}/credentials/${credId}/models`,
    ),

  listWorkspaces: (id: string) =>
    api.get<{ items: WorkspaceListItem[] }>(`/orgs/${id}/workspaces`),

  checkout: (id: string, planId: string) =>
    api.post<{ url: string }>(`/orgs/${id}/billing/checkout`, { planId }),
  portal: (id: string) =>
    api.post<{ url: string }>(`/orgs/${id}/billing/portal`),
  listAudit: (id: string) =>
    api.get<{ items: AuditEntry[] }>(`/orgs/${id}/audit`),
};
