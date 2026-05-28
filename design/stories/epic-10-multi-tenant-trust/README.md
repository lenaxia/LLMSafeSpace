# Epic 10: Multi-Tenant Trust & Secret Management

**Status:** Planning
**Depends On:** Epic 6 (Collapse Sandbox into Workspace), Epic 8 (Credential Health)
**Motivation:** LLMSafeSpace must support Bring Your Own Credentials (BYOC) for a public multi-tenant deployment. Users need to store SSH keys, Git tokens, LLM API keys, secret files, and environment variables — securely, without the platform ever seeing plaintext, and without cross-tenant exposure.

---

## Problem Statement

### Current State

1. **Credentials stored as plaintext K8s Secrets.** `workspace-creds-{id}` contains raw provider config. Any cluster-admin, compromised node, or etcd breach exposes every user's credentials.

2. **Single credential type.** The `SetCredentialsRequest` only supports `provider` + `config` (LLM provider JSON). No support for SSH keys, Git PATs, arbitrary secret files, or environment variables.

3. **Workspace-scoped only.** Credentials are per-workspace. A user with 10 workspaces must re-enter the same Anthropic key 10 times. No sharing, no binding.

4. **No tenant isolation.** All workspaces run in a single namespace. The controller has cluster-wide Secret read access. A compromised controller exposes all tenants.

5. **No audit trail.** No record of who accessed which secret, when, or why.

### Threat Model

| Attacker | Access | Current Exposure | After This Epic |
|----------|--------|-----------------|-----------------|
| Cluster admin (kubectl) | All namespaces, all Secrets | All user credentials (plaintext) | Ciphertext only (useless without user's password) |
| Compromised API server | Memory + DB access | All credentials in active sessions | DEKs for active sessions only; inactive users safe |
| Compromised database | Postgres dump | N/A (creds in K8s today) | Ciphertext + wrapped DEKs (useless without passwords) |
| Compromised node (kubelet) | Secrets mounted to pods on that node | Plaintext for pods on that node | Same (unavoidable — pod needs plaintext to function) |
| Other tenant | Their own workspace pod | Nothing (network policy) | Nothing (virtual namespace + network policy) |

### Design Principles

1. **Zero-knowledge at rest.** The platform never stores plaintext secrets. Encrypted blobs in Postgres, decrypted only in pod memory (tmpfs).
2. **Login is the unlock event.** No separate passphrase. Password authentication derives the key encryption key (KEK) which unwraps the data encryption key (DEK). DEK cached in session memory only.
3. **User-level secrets, workspace-level bindings.** Secrets are owned by users, attached to workspaces. Rotate once, all workspaces get the update.
4. **Graceful rotation.** Password change = O(1) re-wrap. DEK compromise = lazy re-encryption on next read. No big-bang migrations.
5. **Design for Vault, build for Postgres.** The secret provider interface supports future Vault/HSM backends without rewrite.

---

## Architecture

### Cryptographic Model (Key Wrapping)

```
Account Creation:
  1. Generate random 256-bit DEK (Data Encryption Key)
  2. Derive KEK = HKDF-SHA256(password, user_salt, "llmsafespace-kek")
  3. wrapped_dek = AES-256-GCM(KEK, DEK)
  4. Generate recovery_key (random 128-bit, displayed once)
  5. Derive recovery_KEK = HKDF-SHA256(recovery_key, recovery_salt, "llmsafespace-recovery")
  6. wrapped_dek_recovery = AES-256-GCM(recovery_KEK, DEK)
  7. Store: wrapped_dek, wrapped_dek_recovery, user_salt, recovery_salt

Login:
  1. Verify password (argon2id)
  2. Derive KEK from password
  3. Unwrap DEK
  4. Cache DEK in session store (Redis/memory), keyed by session_id, TTL = JWT lifetime
  5. Issue JWT containing session_id

Secret Operations (with valid JWT):
  1. Extract session_id from JWT
  2. Retrieve DEK from session cache
  3. Encrypt/decrypt user secrets with DEK

Password Change:
  1. Derive old_KEK from old_password → unwrap DEK
  2. Derive new_KEK from new_password + new_salt
  3. Re-wrap: new_wrapped_dek = AES-256-GCM(new_KEK, DEK)
  4. DEK unchanged. All secrets unchanged. O(1).

Password Reset (with recovery key):
  1. Derive recovery_KEK from recovery_key + recovery_salt
  2. Unwrap DEK from wrapped_dek_recovery
  3. Derive new_KEK from new_password + new_salt
  4. Re-wrap DEK. Generate new recovery key. O(1).

Password Reset (without recovery key):
  1. All secrets are irrecoverable. Wipe user_secrets rows.
  2. User re-enters credentials from scratch.
```

### Data Model

```sql
-- User key material (never contains plaintext secrets)
CREATE TABLE user_keys (
    user_id        TEXT PRIMARY KEY,
    key_version    INTEGER NOT NULL DEFAULT 1,
    wrapped_dek    BYTEA NOT NULL,          -- AES-GCM(KEK, DEK)
    wrapped_dek_recovery BYTEA,             -- AES-GCM(recovery_KEK, DEK), nullable if user opts out
    salt           BYTEA NOT NULL,          -- for HKDF(password, salt)
    recovery_salt  BYTEA,                   -- for HKDF(recovery_key, salt)
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    rotated_at     TIMESTAMPTZ
);

-- Encrypted user secrets
CREATE TABLE user_secrets (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        TEXT NOT NULL REFERENCES user_keys(user_id),
    name           TEXT NOT NULL,           -- user-chosen name, e.g. "my-anthropic-key"
    type           TEXT NOT NULL,           -- llm-provider | ssh-key | git-credential | secret-file | env-secret
    ciphertext     BYTEA NOT NULL,          -- AES-GCM(DEK, plaintext)
    key_version    INTEGER NOT NULL,        -- which DEK version encrypted this
    metadata       JSONB NOT NULL DEFAULT '{}', -- non-sensitive: mount_path, git remote URL, env var name, etc.
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, name)
);

-- Which secrets are attached to which workspaces
CREATE TABLE user_secret_bindings (
    secret_id      UUID NOT NULL REFERENCES user_secrets(id) ON DELETE CASCADE,
    workspace_id   TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (secret_id, workspace_id)
);

-- Workspace-level env overrides (also encrypted)
CREATE TABLE workspace_env_secrets (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        TEXT NOT NULL REFERENCES user_keys(user_id),
    workspace_id   TEXT NOT NULL,
    name           TEXT NOT NULL,           -- env var name
    ciphertext     BYTEA NOT NULL,
    key_version    INTEGER NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(workspace_id, name)
);

-- Audit log (append-only)
CREATE TABLE secret_audit_log (
    id             BIGSERIAL PRIMARY KEY,
    user_id        TEXT NOT NULL,
    action         TEXT NOT NULL,           -- create | read | update | delete | bind | unbind | rotate
    secret_id      UUID,
    workspace_id   TEXT,
    metadata       JSONB DEFAULT '{}',      -- additional context (secret name, type, etc.)
    timestamp      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_audit_user_time ON secret_audit_log(user_id, timestamp DESC);
```

### Secret Types & Pod Injection

| Type | Metadata Fields | Injection Path | Permissions |
|------|----------------|----------------|-------------|
| `llm-provider` | `provider` (openai, anthropic, deepseek, etc.) | `/sandbox-cfg/credentials` | 0600 |
| `ssh-key` | `key_type` (ed25519, rsa), `host` (github.com, etc.) | `~/.ssh/id_{type}` + `~/.ssh/config` entry | 0600 |
| `git-credential` | `host` (github.com, gitea.example.com), `protocol` (https) | `~/.git-credentials` + git config | 0600 |
| `secret-file` | `mount_path` (user-specified, e.g. `/workspace/.secrets/cert.pem`) | Specified path | 0600 |
| `env-secret` | `var_name` (e.g. `DATABASE_URL`) | `/sandbox-cfg/env` (sourced by entrypoint) | 0600 |

### Pod Startup Flow

```
1. User calls POST /workspaces/:id/activate (JWT in header)
2. API extracts session_id from JWT → retrieves DEK from session cache
3. API queries user_secret_bindings for this workspace
4. API decrypts each bound secret with DEK
5. API also decrypts workspace-level env overrides
6. API creates ephemeral K8s Secret:
     Name: workspace-unlock-{workspace_id}-{random}
     OwnerReference: Pod (deleted when pod dies)
     Data:
       secrets.json: [{ type, name, metadata, plaintext }, ...]
7. Controller starts pod with init container mounting the ephemeral Secret
8. Init container materializes secrets to correct paths on tmpfs:
     - SSH keys → ~/.ssh/ with correct permissions + ssh-agent
     - Git creds → ~/.git-credentials + git config credential helper
     - LLM config → /sandbox-cfg/credentials (existing path)
     - Env vars → /sandbox-cfg/env
     - Secret files → user-specified paths
9. Init container zeros the mounted Secret data from its memory
10. Controller deletes the ephemeral K8s Secret after pod reaches Running
11. Plaintext exists only in pod tmpfs. Pod deletion = secrets gone.
```

### Tenant Isolation (Virtual Namespaces)

```
Physical cluster:
  └── namespace: llmsafespace-system (controller, API server)
  └── namespace: llmsafespace-workloads (all workspace pods)
        ├── Virtual namespace: tenant-{user_id_1}
        │     ├── workspace pod A
        │     ├── workspace pod B
        │     └── NetworkPolicy: deny all cross-tenant
        ├── Virtual namespace: tenant-{user_id_2}
        │     ├── workspace pod C
        │     └── NetworkPolicy: deny all cross-tenant
        └── ...

Controller RBAC:
  - ClusterRole for CRD watches (Workspace, RuntimeEnvironment)
  - Namespaced Role per virtual namespace for pod/secret/pvc operations
  - Label selector on informer: llmsafespace.dev/tenant={user_id}
```

### API Surface

```
User Secrets:
  POST   /api/v1/secrets                    — create a new secret
  GET    /api/v1/secrets                    — list secrets (names + types + metadata, never values)
  GET    /api/v1/secrets/:id                — get secret metadata (never value)
  PUT    /api/v1/secrets/:id                — update secret value
  DELETE /api/v1/secrets/:id                — delete secret

Secret Bindings:
  PUT    /api/v1/workspaces/:id/bindings    — set which secrets are bound to this workspace
  GET    /api/v1/workspaces/:id/bindings    — list bound secret names/types

Workspace Env Overrides:
  PUT    /api/v1/workspaces/:id/env         — set workspace-level env vars
  GET    /api/v1/workspaces/:id/env         — list env var names (never values)
  DELETE /api/v1/workspaces/:id/env/:name   — remove a workspace env var

Key Management:
  POST   /api/v1/account/rotate-key         — force DEK rotation (re-encrypts all secrets)
  GET    /api/v1/account/recovery-key       — regenerate recovery key (requires password confirmation)

Legacy (backwards-compatible, maps to new system):
  PUT    /api/v1/workspaces/:id/credentials — creates/updates an llm-provider secret + binds it
  DELETE /api/v1/workspaces/:id/credentials — unbinds + deletes the llm-provider secret

Audit:
  GET    /api/v1/secrets/audit              — query audit log for current user
```

---

## User Stories

### US-10.1: Key Wrapping & User Key Lifecycle

**Goal:** Implement the cryptographic foundation — DEK generation, KEK derivation, key wrapping, session-based DEK caching.

**Scope:**
- `user_keys` table + migration
- Key generation at account creation (or first secret creation for existing users)
- KEK derivation (HKDF-SHA256) during login
- DEK unwrap + cache in session store (Redis key with TTL)
- DEK eviction on logout / session expiry
- Recovery key generation + display at setup
- Password change: re-wrap DEK with new KEK
- Password reset with recovery key: unwrap + re-wrap
- Password reset without recovery key: wipe secrets

**Acceptance Criteria:**
- DEK never written to disk or database in plaintext
- DEK only exists in memory during active sessions
- Password change does not re-encrypt any secrets
- Recovery key can unwrap DEK and re-wrap with new password
- Unit tests for all crypto operations (encrypt, decrypt, wrap, unwrap, derive)
- Integration test: login → cache DEK → logout → DEK evicted

---

### US-10.2: User Secret Store (CRUD)

**Goal:** Implement encrypted secret storage in Postgres with typed secrets.

**Scope:**
- `user_secrets` table + migration
- API endpoints: POST/GET/PUT/DELETE `/api/v1/secrets`
- Secret types: `llm-provider`, `ssh-key`, `git-credential`, `secret-file`, `env-secret`
- Encryption: plaintext → AES-256-GCM(DEK, plaintext) → store ciphertext
- Metadata validation per type (e.g., `ssh-key` requires `key_type`; `secret-file` requires `mount_path`)
- GET never returns plaintext — only name, type, metadata, timestamps
- `key_version` stored with each secret for future rotation

**Acceptance Criteria:**
- Secrets encrypted before write, decrypted only when needed for pod injection
- Type-specific metadata validation (invalid metadata rejected with 400)
- Duplicate name per user rejected (409)
- Secret CRUD requires valid JWT (DEK available in session)
- No plaintext in logs, error messages, or API responses
- Integration tests for full CRUD lifecycle per secret type

---

### US-10.3: Workspace Secret Bindings

**Goal:** Allow users to attach/detach secrets to workspaces. Support workspace-level env overrides.

**Scope:**
- `user_secret_bindings` table + migration
- `workspace_env_secrets` table + migration
- API endpoints: PUT/GET `/api/v1/workspaces/:id/bindings`
- API endpoints: PUT/GET/DELETE `/api/v1/workspaces/:id/env`
- Binding validation: secret must belong to the requesting user
- Workspace env overrides: encrypted with same DEK, workspace-scoped
- Precedence: workspace env > user-level env-secret bindings

**Acceptance Criteria:**
- User can bind multiple secrets to one workspace
- User can bind one secret to multiple workspaces
- Deleting a secret cascades to remove all its bindings
- Workspace-level env vars override user-level env-secret with same var name
- Cannot bind another user's secret (403)
- Integration test: bind secret → verify it appears in workspace's bound list

---

### US-10.4: Pod Secret Injection (Init Container Rewrite)

**Goal:** Replace the current `credential-setup` init container with a general-purpose secret materializer that handles all secret types.

**Scope:**
- API creates ephemeral K8s Secret with decrypted `secrets.json` on workspace activate
- Ephemeral Secret has ownerReference to Pod (GC'd on pod deletion)
- Init container reads `secrets.json`, materializes to correct paths:
  - `llm-provider` → `/sandbox-cfg/credentials`
  - `ssh-key` → `~/.ssh/id_{type}` (0600) + `~/.ssh/config` host entry + start ssh-agent
  - `git-credential` → `~/.git-credentials` + `git config credential.helper store`
  - `secret-file` → user-specified `mount_path` (0600)
  - `env-secret` → `/sandbox-cfg/env` (KEY=VALUE format, sourced by entrypoint)
- All materialization on tmpfs (never on PVC)
- Controller deletes ephemeral Secret after pod reaches Running phase
- Entrypoint sources `/sandbox-cfg/env` before starting agent

**Acceptance Criteria:**
- SSH key injection: `ssh -T git@github.com` works from within pod
- Git credential injection: `git clone https://github.com/private/repo` works without prompt
- LLM provider injection: agent connects to provider on startup (existing behavior preserved)
- Secret file injection: file exists at specified path with 0600 permissions
- Env injection: `echo $VAR_NAME` returns expected value
- Ephemeral Secret deleted within 30s of pod reaching Running
- No plaintext secrets in K8s etcd after pod is Running
- Integration test: full flow from activate → pod running → secrets accessible → pod deleted → secrets gone

---

### US-10.5: Audit Logging

**Goal:** Record every secret operation in an append-only audit log.

**Scope:**
- `secret_audit_log` table + migration
- Middleware that logs: create, read (decrypt for pod injection), update, delete, bind, unbind, rotate
- Each log entry: user_id, action, secret_id, workspace_id, timestamp, metadata
- API endpoint: GET `/api/v1/secrets/audit` (paginated, filterable by action/secret/workspace/date range)
- Async write (channel + background goroutine) — audit logging must not add latency to hot path

**Acceptance Criteria:**
- Every secret operation produces exactly one audit entry
- Audit log is append-only (no UPDATE or DELETE on the table)
- API returns audit entries for current user only (no cross-tenant leakage)
- Audit entries include secret name and type but never plaintext values
- Performance: audit write adds <1ms p99 to request latency
- Integration test: perform CRUD operations → verify audit entries exist with correct metadata

---

### US-10.6: Virtual Namespace Tenant Isolation

**Goal:** Isolate tenant workloads using virtual namespaces. Prevent cross-tenant network access and resource visibility.

**Scope:**
- Evaluate and select virtual namespace implementation (vcluster vs Loft virtual namespaces)
- Tenant provisioning: create virtual namespace on first workspace creation for a user
- NetworkPolicy: default-deny ingress + egress between virtual namespaces
- Controller label selector: `llmsafespace.dev/tenant={user_id}` on all workspace pods
- RBAC: controller's pod/secret operations scoped to tenant label
- Resource quotas per virtual namespace (CPU, memory, storage, pod count)
- Tenant cleanup: delete virtual namespace when user account is deleted (after grace period)

**Acceptance Criteria:**
- Pod in tenant A cannot reach pod in tenant B (network test)
- Controller cannot list/get secrets without matching tenant label
- Resource quota prevents one tenant from starving others
- Virtual namespace creation adds <2s to first workspace creation
- Integration test: create workspaces for two users → verify network isolation → verify secret isolation

---

### US-10.7: S3 Shared Folder

**Goal:** Provide each user with a persistent S3-backed folder mounted across all their workspaces for sharing prompts, scripts, binaries, and config files.

**Scope:**
- S3 bucket with per-user prefix: `s3://{bucket}/{user_id}/`
- Mountpoint for Amazon S3 CSI driver (or s3fs-fuse for non-AWS deployments)
- Mount at `/shared` in workspace pods (read-only by default)
- User setting to enable write access from within workspaces
- Size quota enforcement via S3 lifecycle policy or monitoring + alert
- API endpoints for upload/download (alternative to in-pod writes):
  - `PUT /api/v1/shared/{path}` — upload file
  - `GET /api/v1/shared/{path}` — download file
  - `GET /api/v1/shared/` — list files
  - `DELETE /api/v1/shared/{path}` — delete file
- Shared folder lifecycle independent of any workspace (persists across all workspace deletions)

**Acceptance Criteria:**
- File uploaded via API visible in all user's workspace pods at `/shared/`
- File written in workspace A (if write enabled) visible in workspace B
- Other users cannot access the folder (S3 IAM policy or prefix isolation)
- Size quota enforced (upload rejected when quota exceeded)
- Shared folder survives workspace deletion
- Integration test: upload file → start workspace → verify file at `/shared/` → delete workspace → start new workspace → file still there

---

### US-10.8: Lazy DEK Rotation

**Goal:** Support DEK rotation without downtime or batch migration. Secrets re-encrypted on next read.

**Scope:**
- `key_version` tracking on `user_keys` and `user_secrets`
- Rotation trigger: `POST /api/v1/account/rotate-key`
  - Generate new DEK₂
  - Wrap with current KEK
  - Increment key_version
  - Store old wrapped_dek as `wrapped_dek_prev` (needed to decrypt old-version secrets)
- On secret read (pod injection): if `secret.key_version < user.key_version`, decrypt with old DEK, re-encrypt with new DEK, update row
- Deadline enforcement: admin can set "rotate by" date; after deadline, old DEK destroyed, un-migrated secrets become irrecoverable (with warning)
- Forced rotation on compromise: admin API to force all users to re-key on next login

**Acceptance Criteria:**
- Rotation is O(1) — only re-wraps DEK, does not touch secrets
- Secrets lazily re-encrypted on next access (transparent to user)
- Old-version secrets still decryptable until deadline
- After deadline + old DEK destruction, old-version secrets return error
- Admin can trigger platform-wide forced rotation
- Integration test: create secrets → rotate → access secrets → verify re-encrypted with new version

---

### US-10.9: Legacy Credential API Compatibility

**Goal:** Maintain backwards compatibility with existing `PUT /workspaces/:id/credentials` endpoint.

**Scope:**
- Existing endpoint maps to: create `llm-provider` secret (name: `{workspace_id}-provider`) + bind to workspace
- `DELETE /workspaces/:id/credentials` maps to: unbind + delete the auto-created secret
- Existing tests continue to pass without modification
- Deprecation notice in API response headers (`Deprecation: true`, `Sunset: <date>`)

**Acceptance Criteria:**
- Existing client code works unchanged
- Credentials set via legacy API are stored encrypted (same as new API)
- Credentials set via legacy API visible in new `/api/v1/secrets` list
- Deprecation header present in responses

---

## Dependency Graph

```
US-10.1 (Key Wrapping) ─────────────────────────────────────────────┐
    │                                                                │
    ▼                                                                │
US-10.2 (Secret Store CRUD) ──┐                                     │
    │                          │                                     │
    ▼                          ▼                                     │
US-10.3 (Bindings)        US-10.5 (Audit Logging)                   │
    │                                                                │
    ▼                                                                │
US-10.4 (Pod Injection) ─────────────────────────────────────────────┤
    │                                                                │
    ▼                                                                │
US-10.9 (Legacy Compat)                                              │
                                                                     │
US-10.6 (Virtual Namespaces) ── independent, can parallel ───────────┤
                                                                     │
US-10.7 (S3 Shared Folder) ── independent, can parallel ─────────────┤
                                                                     │
US-10.8 (Lazy Rotation) ── requires US-10.1 + US-10.2 ──────────────┘
```

**Critical path:** US-10.1 → US-10.2 → US-10.3 → US-10.4 → US-10.9

**Parallelizable:** US-10.5, US-10.6, US-10.7 can proceed independently after US-10.1.

---

## Vault Extension Point (Design Only — No Implementation)

The secret provider interface that US-10.1 and US-10.2 implement:

```go
// pkg/secrets/provider.go

type SecretProvider interface {
    // Encrypt encrypts plaintext with the user's current DEK.
    Encrypt(ctx context.Context, userID string, plaintext []byte) (ciphertext []byte, keyVersion int, err error)

    // Decrypt decrypts ciphertext using the appropriate DEK version.
    Decrypt(ctx context.Context, userID string, ciphertext []byte, keyVersion int) (plaintext []byte, err error)

    // RotateKey generates a new DEK for the user. Old DEK retained for lazy migration.
    RotateKey(ctx context.Context, userID string) (newKeyVersion int, err error)

    // DEKAvailable returns true if the user's DEK is currently cached (active session).
    DEKAvailable(ctx context.Context, userID string) bool
}

// V1 implementation: PostgresSecretProvider (HKDF + AES-GCM + session cache)
// Future: VaultSecretProvider (Vault transit engine for encrypt/decrypt)
// Future: HSMSecretProvider (PKCS#11 for hardware-backed keys)
```

This interface is the extension point. Adding Vault later means implementing `VaultSecretProvider` — no changes to the API layer, secret store, bindings, or pod injection.

---

## Organization/Team Extension Point

Epic 10 is user-scoped. Organizations layer on top without restructuring:

**Design constraint for this epic:** The secret provider interface and database schema must use `owner_id` + `owner_type` (user | org) rather than hardcoding `user_id` as the only ownership dimension. This allows Epic 11 (Organizations) to add org-level secrets without rewriting the encryption or injection layers.

**What Epic 11 adds (not built here):**
- `organizations` + `org_memberships` tables
- Org DEK (separate from user DEK), wrapped with every org admin's KEK
- `org_secrets` table (same schema as `user_secrets`, keyed by `org_id`)
- Workspace bindings can reference both user secrets and org secrets
- Pod injection merges both: user secrets + org secrets bound to that workspace
- Org-level S3 shared folder (team prefix, mounted alongside user prefix)
- Org-level resource quotas

**What does NOT change when orgs are added:**
- User secrets remain user-scoped and user-encrypted
- Virtual namespace isolation remains per-user (org members still get separate namespaces)
- Pod startup flow is identical (just more secrets to materialize)
- Audit logging schema works as-is (already has `user_id` + `workspace_id`; add `org_id` column later)

**Implementation note:** In `user_secrets` and the `SecretProvider` interface, use:
```go
type SecretOwner struct {
    ID   string    // user ID or org ID
    Type OwnerType // "user" or "org"
}

type OwnerType string
const (
    OwnerTypeUser OwnerType = "user"
    OwnerTypeOrg  OwnerType = "org"
)
```

This keeps the door open without building anything prematurely.

---

## Non-Requirements (Explicitly Out of Scope)

- **Vault integration** — design the interface, don't build it
- **TEE / hardware enclaves** — v3+
- **Per-secret expiry / auto-rotation** — v2 (nice to have, not blocking)
- **Secret sharing between users** — not planned (each user manages their own); org-level sharing deferred to Epic 11
- **Client-side encryption in browser** — server-side encryption is sufficient; the trust boundary is "platform at rest," not "platform in memory during active session"
- **HSM-backed key storage** — enterprise tier, future
