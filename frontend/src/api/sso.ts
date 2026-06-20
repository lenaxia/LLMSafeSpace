import { api } from "./client";
import { getEnv } from "../env";

export type OrgRole = "admin" | "member";

export interface OrgSSOConfig {
  orgId: string;
  discoveryUrl: string;
  clientId: string;
  hasSecret: boolean;
  claimedDomains: string[];
  verifiedDomains: string[];
  verificationToken: string;
  autoProvision: boolean;
  groupRoleMapping: Record<string, OrgRole>;
  updatedAt: string;
}

export interface UpsertSSOConfigRequest {
  discoveryUrl: string;
  clientId: string;
  /** Plaintext IdP client secret. Omit to leave the stored secret unchanged. */
  clientSecret?: string;
  claimedDomains: string[];
  autoProvision?: boolean;
  groupRoleMapping: Record<string, OrgRole>;
}

export interface SSODomain {
  domain: string;
  orgSlug: string;
  orgName: string;
}

/**
 * SSO start/callback routes are browser redirects (not JSON), so they are
 * constructed as plain URLs the caller navigates to with window.location.
 */
export const ssoRedirectURL = (orgSlug: string): string => {
  const { apiBaseUrl } = getEnv();
  return `${apiBaseUrl}/auth/sso/${encodeURIComponent(orgSlug)}/start`;
};

export const ssoApi = {
  getConfig: (orgId: string) => api.get<OrgSSOConfig>(`/orgs/${orgId}/sso`),
  upsert: (orgId: string, req: UpsertSSOConfigRequest) =>
    api.put<OrgSSOConfig>(`/orgs/${orgId}/sso`, req),
  remove: (orgId: string) => api.delete<void>(`/orgs/${orgId}/sso`),
  verifyDomain: (orgId: string, domain: string) =>
    api.post<{ domain: string; verified: boolean }>(`/orgs/${orgId}/sso/domains/${encodeURIComponent(domain)}/verify`),
  rotateToken: (orgId: string) =>
    api.post<{ verificationToken: string }>(`/orgs/${orgId}/sso/verification-token/rotate`),
  /** Public: returns every verified domain for login-page discovery. */
  domains: () => api.get<{ domains: SSODomain[] }>(`/auth/sso/domains`),
};
