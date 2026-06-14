# Worklog: Credential Model Config UI — Context Window Sizes

**Date:** 2026-06-14
**Session:** Add model allowlist + context window configuration UI to credential settings panels.
**Status:** Complete

---

## Objective

Users had no way to configure `ContextLimit` per model when setting up LLM provider credentials. Without it, custom provider models (e.g. thekao cloud / glm-5.x) always showed "Unknown" as the context usage denominator because `ModelContextLimit()` returned 0. The `ContextLimit` field existed in `LLMModelConfig` (added in worklog 0272) but had no user-facing input path.

---

## What Was Built

### DB (migration 000031)

`ALTER TABLE provider_credentials ADD COLUMN model_context_limits JSONB NOT NULL DEFAULT '{}'`

Stores `{"model_id": context_window_tokens}`. Additive, backward compatible.

### Go

- `AdminCredentialRow`, `UserCredentialRow`, `OrgCredentialMetadata`, `CredentialBinding` — all gain `ModelContextLimits map[string]int`, plumbed through all DB queries
- `injection.go` — when synthesising `LLMModelConfig` entries from the allowlist, applies `ModelContextLimits[id]` as `ContextLimit` (only when the model's blob entry has `ContextLimit = 0`, so relay-model limits are never overwritten)
- `credential_probe.go` — three new endpoints:
  - `GET /api/v1/admin/provider-credentials/:id/models` — decrypts credential, calls provider `/v1/models`, returns model list merged with saved limits
  - `GET /api/v1/provider-credentials/:id/models` — same for user creds
  - `POST /api/v1/probe-models` — credential-free probe (auth required), used by user create form before credential ID exists

### Frontend

- `AdminProviderCredentialsTab`: two-phase create — enter creds, click "Create & fetch models", credential is created, models auto-probed, table rendered with checkbox + context window input per model, saved via PUT
- `UserProviderCredentialsTab`: single-phase — enter creds, click "Fetch models" inline, model table expands, all saved in one POST

---

## Tests

- `TestAdminProviderCredentials_Create_ModelContextLimits` — round-trip
- `TestAdminProviderCredentials_Update_ModelContextLimits` — PUT updates limits
- `TestAdminProviderCredentials_ProbeModels_NoBaseURL` — graceful warning
- `TestAdminProviderCredentials_ProbeModels_NotFound` — 404
- `TestAdminProviderCredentials_ProbeModels_WithBaseURL_CallsProvider` — unreachable → warning, not 500
- `TestAdminProviderCredentials_ProbeModels_WithBaseURL_Success` — 3 models returned with saved limits pre-populated
- `TestCredentialPrecedence_ModelContextLimits_InjectedIntoLLMModelConfig` — limits flow into `LLMModelConfig`
- `TestCredentialPrecedence_ModelContextLimits_DoesNotOverrideExisting` — relay limits not clobbered

---

## Files Modified

- `api/migrations/000031_model_context_limits.{up,down}.sql` + chart mirror
- `pkg/secrets/credential_store.go`, `pg_credential_store.go`, `org_credential_store.go`, `injection.go`
- `api/internal/handlers/credential_probe.go` (new)
- `api/internal/handlers/admin_provider_credentials.go` + `_test.go`
- `api/internal/handlers/user_provider_credentials.go`, `org_credentials.go`
- `api/internal/server/router.go`
- `frontend/src/api/providerCredentials.ts`
- `frontend/src/components/settings/AdminProviderCredentialsTab.{tsx,test.tsx}`
- `frontend/src/components/settings/UserProviderCredentialsTab.tsx`
