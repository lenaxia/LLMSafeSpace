// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Typed API client for /api/v1/auth/api-keys.
//
// Backend response shapes (from pkg/types/types.go + router.go):
//
//   POST /auth/api-keys  → APIKey (full struct, key populated exactly once)
//   GET  /auth/api-keys  → APIKey[] (key field is empty string for all entries)
//   DELETE /auth/api-keys/:id → 204 No Content
//
// Note: the backend returns the full APIKey struct on create, NOT a wrapper
// { key, apiKey } shape. The frontend CreateApiKeyResponse type in types.ts
// abstracts this — we normalise here.

import { api } from "./client";

// Re-export shared types from types.ts for consumers of this module.
export type { ApiKey, CreateApiKeyRequest, CreateApiKeyResponse } from "./types";
import type { ApiKey, CreateApiKeyRequest, CreateApiKeyResponse } from "./types";

// The raw wire response for create — backend sends the full APIKey struct with
// `key` populated. We normalise to CreateApiKeyResponse in the client below.
interface RawCreateResponse {
  id: string;
  name: string;
  prefix: string;
  createdAt: string;
  lastUsedAt?: string;
  key: string;
  active: boolean;
  decryptAccess: boolean;
  dekSynced: boolean;
  allowedCidrs?: string[];
  expiresAt?: string;
  legacy?: boolean;
}

export const apiKeysApi = {
  /**
   * Create a new API key. The raw secret is returned once in the response
   * and never retrievable again.
   */
  async create(req: CreateApiKeyRequest): Promise<CreateApiKeyResponse> {
    const raw = await api.post<RawCreateResponse>("/auth/api-keys", req);
    return {
      key: raw.key,
      apiKey: {
        id: raw.id,
        name: raw.name,
        prefix: raw.prefix,
        createdAt: raw.createdAt,
        lastUsedAt: raw.lastUsedAt,
      },
    };
  },

  /** List all API keys for the authenticated user. The key value is never returned. */
  list(): Promise<ApiKey[]> {
    return api.get<ApiKey[]>("/auth/api-keys");
  },

  /** Delete an API key by ID. Returns undefined on success (204). */
  delete(id: string): Promise<void> {
    return api.delete<void>(`/auth/api-keys/${id}`);
  },
};
