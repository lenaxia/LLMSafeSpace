// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Shared type definitions for provider credentials across the admin, user, and
// org API clients. The three clients keep their own method implementations
// (different base paths and auth contexts) but import these types so a field
// added to the credential shape only needs updating in one place.
//
// Note: this is distinct from credentials.ts, which holds the older
// CredentialSet admin-API types.

// ProviderCredential is the base API response shape for any provider credential.
// Never includes apiKey. The owner scope (orgId for org credentials) and a
// possible bindWarning are carried by the per-client response types below.
export interface ProviderCredential {
  id: string;
  name: string;
  provider: string;
  baseURL?: string;
  modelAllowlist?: string[];
  modelContextLimits?: Record<string, number>;
  createdAt: string;
  updatedAt: string;
}

// CreateCredentialRequest is the shared create body for admin and user
// credentials (identical request shape). The org client uses the same fields
// plus its own validation; it reuses this type directly.
export interface CreateCredentialRequest {
  name: string;
  provider: string;
  apiKey: string;
  baseURL?: string;
  modelAllowlist?: string[];
  modelContextLimits?: Record<string, number>;
}

// UpdateCredentialRequest is the shared partial-update body. Admin and user
// both use this shape; the org client's update omits provider (org credentials
// lock the provider) so it reuses this type as-is (provider is optional).
export interface UpdateCredentialRequest {
  name?: string;
  apiKey?: string;
  baseURL?: string;
  modelAllowlist?: string[];
  modelContextLimits?: Record<string, number>;
}

// CreateCredentialResponse is the create response carrying an optional
// bindWarning (set when auto-bind to existing workspaces failed — non-fatal,
// the credential is still stored). Admin/user credentials reuse
// ProviderCredential directly; org credentials extend it with orgId.
export interface CreateCredentialResponse extends ProviderCredential {
  bindWarning?: string;
}

// ProbeModelEntry is one entry from GET /:id/models.
export interface ProbeModelEntry {
  id: string;
  contextLimit: number; // 0 = unknown / not yet configured
}

// ProbeModelsResponse is the response from GET /:id/models for any owner type.
export interface ProbeModelsResponse {
  models: ProbeModelEntry[];
  baseURL?: string;
  warning?: string;
}
