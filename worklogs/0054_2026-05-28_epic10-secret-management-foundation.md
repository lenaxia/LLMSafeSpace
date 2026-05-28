# Worklog: Epic 10 — Zero-Knowledge Secret Management Foundation

**Date:** 2026-05-28
**Session:** Implement US-10.1 (Key Wrapping), US-10.2 (Secret Store), US-10.3 (Bindings), US-10.5 (Audit) core
**Status:** In Progress

---

## Objective

Implement the cryptographic foundation and secret management core for Epic 10 (Multi-Tenant Trust & Secret Management). This covers the critical path: key wrapping, encrypted secret CRUD, workspace bindings, and audit logging.

---

## Work Completed

### Epic 10 Validation
- Read and validated the full Epic 10 README against the V2 evolution document and existing codebase
- Identified conflict: Epic says "argon2id" for password verification but codebase uses bcrypt. Decision: keep bcrypt (changing would break existing users), use HKDF-SHA256 for KEK derivation independently
- Validated that `jti` in existing JWT can serve as session ID for DEK caching (no new claim needed)
- Confirmed `golang.org/x/crypto` v0.32.0 includes HKDF
- Confirmed owner_id/owner_type design constraint for future org support

### US-10.1: Key Wrapping & User Key Lifecycle
- `pkg/secrets/crypto.go`: HKDF-SHA256 KEK derivation, AES-256-GCM encrypt/decrypt, DEK generation, key wrapping/unwrapping
- `pkg/secrets/key_service.go`: InitializeUserKeys, UnlockDEK, EvictDEK, GetDEK, ChangePassword, ResetWithRecoveryKey, HasKeys
- `pkg/secrets/provider.go`: SecretProvider interface with SecretOwner (ID + OwnerType) for future org support
- 22 crypto tests + 14 key service tests = 36 tests passing

### US-10.2: User Secret Store (CRUD)
- `pkg/secrets/types.go`: 5 secret types (llm-provider, ssh-key, git-credential, secret-file, env-secret)
- `pkg/secrets/secret_service.go`: CreateSecret, GetSecret, ListSecrets, UpdateSecret, DeleteSecret, DecryptSecretValue
- `pkg/secrets/store.go`: SecretStore interface
- Type-specific metadata validation (ssh-key requires key_type, secret-file requires mount_path, env-secret requires var_name)
- 20 secret service tests passing

### US-10.3: Workspace Secret Bindings
- SetBindings, GetBindings in SecretService
- Cascade delete (deleting a secret removes all its bindings)
- Binding validation (secret must belong to requesting user)

### US-10.5: Audit Logging
- Append-only audit log with create/read/update/delete/bind actions
- AsyncAuditLogger for non-blocking writes on hot path
- Query API with filters (action, secretId, workspaceId, date range)

### Persistence Layer
- `pkg/secrets/pg_key_store.go`: PostgreSQL KeyStore implementation
- `pkg/secrets/pg_secret_store.go`: PostgreSQL SecretStore implementation with transactions for bindings
- `pkg/secrets/redis_cache.go`: Redis DEK cache with TTL
- 4 Redis cache tests using miniredis

### HTTP Handler Layer
- `api/internal/handlers/secrets.go`: Full CRUD + bindings + audit endpoints
- 10 integration tests covering all endpoints
- Error mapping (404, 409, 400, 403, 500)

### Auth Integration
- `api/internal/utilities/jti.go`: Extract jti from JWT for session ID
- Auth middleware now sets `sessionID` in Gin context
- 7 JTI extraction tests

### Database Migrations
- `000006_user_keys.up.sql`: user_keys table
- `000007_user_secrets.up.sql`: user_secrets, user_secret_bindings, secret_audit_log tables

---

## Key Decisions

1. **Use `jti` as session ID for DEK caching** — avoids adding new JWT claims, maintains backwards compatibility
2. **Keep bcrypt for password verification** — changing to argon2id would break existing users; KEK derivation uses HKDF independently
3. **owner_id + owner_type in SecretProvider interface** — future-proofs for org-level secrets without premature implementation
4. **Async audit logging** — buffered channel + background goroutine to avoid adding latency to hot path
5. **In-memory mocks for unit tests, miniredis for Redis tests** — fast, deterministic, no external dependencies

---

## Blockers

None.

---

## Tests Run

```bash
go test -timeout 30s -race ./pkg/secrets/...          # 60 tests PASS
go test -timeout 30s -race ./api/internal/handlers/... # 10 handler tests PASS
go test -timeout 30s -race ./api/internal/utilities/... # all PASS
go test -timeout 120s -short ./...                     # 31 packages, 0 failures
```

---

## Next Steps

1. **Register secrets routes in the main router** (`api/internal/server/router.go`) — wire the SecretsHandler into the authenticated route group
2. **Integrate KeyService with login flow** — call `UnlockDEK` during `Login()` and `InitializeUserKeys` during `Register()`
3. **US-10.4: Pod Secret Injection** — rewrite init container to materialize all secret types from `secrets.json`
4. **US-10.8: Lazy DEK Rotation** — implement `RotateKey` with key_version tracking and lazy re-encryption
5. **US-10.9: Legacy Credential API Compatibility** — map existing `PUT /workspaces/:id/credentials` to new secret system
6. **US-10.6: Virtual Namespace Tenant Isolation** — evaluate vcluster vs label-based isolation
7. **US-10.7: S3 Shared Folder** — CSI driver integration

---

## Files Modified

- `pkg/secrets/provider.go` (new)
- `pkg/secrets/crypto.go` (new)
- `pkg/secrets/crypto_test.go` (new)
- `pkg/secrets/key_service.go` (new)
- `pkg/secrets/key_service_test.go` (new)
- `pkg/secrets/types.go` (new)
- `pkg/secrets/store.go` (new)
- `pkg/secrets/secret_service.go` (new)
- `pkg/secrets/secret_service_test.go` (new)
- `pkg/secrets/pg_key_store.go` (new)
- `pkg/secrets/pg_secret_store.go` (new)
- `pkg/secrets/redis_cache.go` (new)
- `pkg/secrets/redis_cache_test.go` (new)
- `api/migrations/000006_user_keys.up.sql` (new)
- `api/migrations/000006_user_keys.down.sql` (new)
- `api/migrations/000007_user_secrets.up.sql` (new)
- `api/migrations/000007_user_secrets.down.sql` (new)
- `api/internal/handlers/secrets.go` (new)
- `api/internal/handlers/secrets_test.go` (new)
- `api/internal/handlers/secrets_test_helpers_test.go` (new)
- `api/internal/utilities/jti.go` (new)
- `api/internal/utilities/jti_test.go` (new)
- `api/internal/middleware/auth.go` (modified — added sessionID extraction)
