# RT-1.7 — Secret Storage Map

**Phase:** 1 (Reconnaissance)
**Method:** Static analysis. Every claim cites `file:line`. Cross-references RT-1.3 (RBAC) and the redaction layer at `pkg/redact/redact.go`.
**Sources read:** all `api/migrations/*.sql`, `pkg/secrets/*.go`, `pkg/credentials/*.go`, `pkg/agentd/secrets/secrets.go`, `pkg/redact/redact.go`, `api/internal/services/auth/auth.go`, `api/internal/services/cache/cache.go`, `api/internal/services/database/database.go`, `api/internal/handlers/secrets.go`, `api/internal/handlers/proxy.go`, `cmd/workspace-agentd/main.go`, `cmd/workspace-agentd/secrets.go`, `controller/internal/workspace/controller.go`, all `charts/llmsafespace/templates/*.yaml`.

---

## 1. User authentication secrets

### 1.1 User password (login factor)

| Property | Value | Citation |
|---|---|---|
| Semantic | User-chosen login secret. Also derives the per-user KEK that wraps the secrets DEK. | `pkg/secrets/key_service.go:113-138` |
| At rest | `users.password_hash VARCHAR(255)` — bcrypt with cost 12 | `api/migrations/000001_initial_schema.up.sql:5`; `api/internal/services/auth/auth.go:398, 426-429` |
| In transit | HTTP body — `{ "password": "…" }` on `/auth/login`, `/auth/register`, `/account/change-password`, `/account/recover`, `/secrets/{id}/reveal`, `/account/rotate-key`. JSON over the API ingress. | `api/internal/services/auth/auth.go:487`; `api/internal/handlers/secrets.go:148-149, 484-486, 516-519, 549-553` |
| Encryption layer | bcrypt only at rest. **In transit relies entirely on TLS at the ingress** — no application-layer wrapping. | `auth.go:426`, `auth.go:487` |
| Who can read | Plaintext: only the user (and any process able to read the JSON request body before TLS terminates — i.e. anything with `pods/exec` into the API pod, or anything that controls the ingress). Hash: anyone with `secrets` get on the `users` row → DB. RT-1.3 F1.3.6 establishes API SA can `pods/exec` the API pod itself when `workspaceNamespace == releaseNamespace` (default). | RT-1.3:73, 121-123 |
| Lifetime | No TTL. Survives until user deletion (CASCADE) or password change. No rotation policy. | `api/migrations/000001_initial_schema.up.sql:14`; no `password_changed_at`/`password_age` column |

**Cross-cutting:** the password is also the single factor that gates the per-user encryption key (KEK derived via HKDF — see §3 below). A leaked password compromises every user_secret.

### 1.2 JWT (session token)

| Property | Value | Citation |
|---|---|---|
| Semantic | Bearer session token. Identity assertion (`sub` claim) plus session ID (`jti`, used as DEK cache key). | `api/internal/services/auth/auth.go:262-274`; `pkg/secrets/key_service.go:113` |
| At rest | Not stored on the server. **The `jti` is reused as the Redis DEK cache key** (`dek:<jti>`). Also reused as token revocation marker (`token:<jti>`). | `pkg/secrets/redis_cache.go:32`; `auth.go:332-336` |
| In transit | `Authorization: Bearer <jwt>` HTTP header **or** `lsp_session` cookie. | `auth.go:603-608` |
| Encryption | HS256 signature over `LLMSAFESPACE_AUTH_JWTSECRET` (see §3.1). Body is **base64, not encrypted** — `sub`, `jti`, `exp`, `iat` are readable to any holder. | `auth.go:262, 269-273` |
| Who can read | Everyone in the request path: client, ingress, API pod. `apikey:`/`token:` Redis keys are accessible to anyone with Redis credentials. | `cache.go:29-34, 79-95` |
| Lifetime | `cfg.Auth.TokenDuration` — no default in `values.yaml` for the chart; auth tests set 24h (`auth_test.go:33`). Validation cache: min(remaining lifetime, 1h) (`auth.go:351-354`). Revocation cache: remaining lifetime, written to BOTH `token:<sha-md5(token)>` and `token:<jti>` (`auth.go:209-223`). | `auth.go:262-274, 351-354` |

**Note on hash function:** `hashToken` uses MD5 (`auth.go:27-30`). MD5 is collision-broken; for cache keying it's an availability concern (collision → wrong cache hit). RT-1.5/RT-1.6 should also flag this.

### 1.3 API key (long-lived bearer)

| Property | Value | Citation |
|---|---|---|
| Semantic | Long-lived bearer alternative to JWT. Prefix-discriminated from JWT via `cfg.Auth.APIKeyPrefix`. | `auth.go:280-282, 561` |
| At rest | **`api_keys.key VARCHAR(255) NOT NULL UNIQUE` — stored CLEARTEXT.** Lookup is `WHERE k.key = $1`. No bcrypt, no HMAC, no even SHA-256. | `api/migrations/000001_initial_schema.up.sql:12-20`; `database.go:244-271` |
| In transit | `Authorization: Bearer <key>` (same header as JWT) | `utilities.IsAPIKey(...)` at `auth.go:280` |
| Encryption | None. | — |
| Who can read | Anything with SELECT on `api_keys` → DB credentials → see §3.2. **Also stored as Redis cache key** `apikey:<full-bearer-token>` for 15 min (`auth.go:117, 374-389`) — anyone reading the Redis keyspace recovers live bearer tokens. RT-1.3 F1.3.3 establishes API SA can read every Secret in the workspace ns including the chart credentials Secret holding postgres-password and redis-password. | `auth.go:95-98, 117, 388-389` |
| Lifetime | `expires_at` column exists (migration 000001:19) but **never set** by `CreateAPIKey` (`auth.go:556-578` constructs `APIKey` with no `ExpiresAt` field set). No rotation. Deletion only on explicit `DeleteAPIKey`. CASCADE on user delete. | `auth.go:563-571`; migration 000001:14 (`ON DELETE CASCADE`) |

### 1.4 Recovery key (per-user account recovery)

| Property | Value | Citation |
|---|---|---|
| Semantic | 16-byte random key shown to user once at registration; can independently unwrap the DEK if the user forgets the password. | `pkg/secrets/key_service.go:74-91, 203-271` |
| At rest | **Not stored.** Only `user_keys.wrapped_dek_recovery` (the DEK wrapped with a KEK derived from the recovery key) and `user_keys.recovery_salt` are stored. The recovery key itself was emitted hex-encoded to the user once. | `api/migrations/000007_user_keys.up.sql:7, 9`; `key_service.go:108` |
| In transit | Returned from `/auth/register` once; submitted on `/account/recover`. | `secrets.go:573, 549-553` |
| Encryption | The DEK wrapped with `recoveryKEK = HKDF(recovery_key, recovery_salt, "llmsafespace-recovery")`. AES-256-GCM. | `key_service.go:84-92`; `crypto.go:14-18` |
| Who can read | Plaintext: only the user (server has no copy). Recovery wrap-record: anyone with DB SELECT. | — |
| Lifetime | Until rotated by `ResetWithRecoveryKey` (which generates a new one and returns it once, `key_service.go:248-270`). **No expiry, no rotation, no record of disclosure.** A user who pasted the recovery key into a chat message has no way to invalidate it without performing a recovery. | — |

---

## 2. User-stored credentials (the secrets *users put into* the platform)

### 2.1 user_secrets table — the canonical user-credential store

| Property | Value | Citation |
|---|---|---|
| Semantic | LLM API keys, SSH private keys, git OAuth tokens, env vars, mountable secret files. Per-user, encrypted with the user's DEK. | `pkg/secrets/types.go` (search `SecretType*` constants); `pkg/agentd/secrets/secrets.go:413-427` |
| At rest | `user_secrets.ciphertext BYTEA` (AES-256-GCM, nonce prepended). `user_secrets.key_version SMALLINT`. `metadata JSONB` (cleartext — includes mount_path, var_name, host). | `api/migrations/000008_user_secrets.up.sql:7-9`; `pkg/secrets/secret_service.go:36-65`; `pkg/secrets/crypto.go:106-120` |
| In transit (user → API) | HTTP body on `POST /api/v1/secrets`, `PUT /api/v1/secrets/:id`. JSON `{ "value": "<plaintext>", … }`. | `api/internal/handlers/secrets.go:44-57, 98-118` |
| In transit (API → sandbox pod) | Decrypted server-side (DEK from Redis cache), bundled into JSON, **POSTed cleartext over plain HTTP** to `http://<podIP>:4097/v1/reload-secrets` (no TLS, no auth). | `api/internal/handlers/secrets.go:291-308`; `pkg/secrets/injection.go:21-62`; `cmd/workspace-agentd/secrets.go:159-213` |
| Materialization onto pod | `controller/internal/workspace/controller.go:902-929` writes a separate ephemeral K8s `Secret` named `workspace-secrets-<workspace-id>` with key `secrets.json`; controller mounts it read-only into a credential-setup init container; init container copies to `/sandbox-cfg/secrets.json`; agentd `materialize` subcommand reads it and writes per-secret files (mode 0600) under `/home/sandbox/.secrets/`, `/home/sandbox/.ssh/`, `/home/sandbox/.git-credentials`, plus an env file at `/tmp/secrets-env`. | `controller.go:706-740, 902-929`; `pkg/agentd/secrets/secrets.go:363-396, 460-557` |
| Encryption layer | Per-user DEK (256-bit, random, generated at registration) wrapped by KEK = HKDF-SHA256(password, salt, "llmsafespace-kek"). | `pkg/secrets/crypto.go:26-34`; `key_service.go:52-109` |
| Unwrapped by | Login flow at `auth.go:504-519` calls `keyService.UnlockDEK` which decrypts the wrapped DEK and stores it in Redis under `dek:<jti>`. The Redis-stored DEK can itself be wrapped with a server-derived "master" key (`pkg/secrets/redis_cache.go:31-44`) — only if `LLMSAFESPACE_MASTER_SECRET` is set; otherwise stored as hex (`redis_cache.go:40-42`). | `redis_cache.go:23-44`; `secrets_adapters.go:245-272` |
| Who can read | Plaintext: only a request handler that has the live `sessionID` and Redis connectivity. The DEK is in-memory in the API process for the duration of the decrypt call. **The DEK is also accessible to anyone who can read the `dek:*` keyspace in Redis** — i.e., anyone with the redis password (chart Secret), or anyone with `pods/exec` into the API pod (RT-1.3 F1.3.6), or anyone with the master key (chart env var). | `redis_cache.go:46-67` |
| Lifetime | DB row: until user deletes secret, or user CASCADE-deletes (`migrations/000008_user_secrets.up.sql:4`). DEK in Redis: TTL = `cfg.Auth.TokenDuration` (`auth.go:515`). Ephemeral K8s Secret: created on workspace activation (`workspace_service.go:780`), deleted on workspace terminate (`controller.go:191, 256, 450-459`). On-disk files in pod: gone when pod is deleted (`emptyDir`-style mounts). |

### 2.2 user_secret_bindings — orphans on workspace delete

| Property | Value | Citation |
|---|---|---|
| Schema | `secret_id UUID REFERENCES user_secrets(id) ON DELETE CASCADE`, `workspace_id VARCHAR(36) NOT NULL` — **`workspace_id` has NO foreign key constraint and NO ON DELETE policy**. | `api/migrations/000008_user_secrets.up.sql:18-23` |
| Implication | Deleting a workspace from the `workspaces` table leaves stale `user_secret_bindings` rows pointing to a non-existent `workspace_id`. They are not actively decrypted (no pod), but they do appear in `GetBindings`-style queries until the user manually unbinds. | `pkg/secrets/store.go` (search `GetBindings`); `workspace_service.go` (no binding cleanup on delete) |

### 2.3 secret_audit_log — never expires

| Property | Value | Citation |
|---|---|---|
| Schema | `user_id`, `secret_id`, `workspace_id`, `metadata JSONB`, `timestamp`. **No FK constraints; nothing CASCADEs.** | `api/migrations/000008_user_secrets.up.sql:28-36` |
| Retention | None implemented. Only test cleanup deletes rows (`pg_integration_test.go:40, 318`). Production has no retention job, no TTL, no rotation. |
| PII risk | `metadata` includes secret name (e.g., `"openai-key"`) and may include workspace IDs even after the workspace is deleted. Indefinite retention. |

### 2.4 credential_sets (instance-level LLM credentials)

| Property | Value | Citation |
|---|---|---|
| Semantic | Operator-managed pool of LLM provider credentials shared across users. | `api/migrations/000006_settings.up.sql:19-32`; `pkg/credentials/service.go:63-95` |
| At rest | `providers_encrypted BYTEA` AES-256-GCM with version-prefix byte and AAD = credential set name. | `pkg/credentials/crypto.go:49-82` |
| In transit | HTTP body. Decrypted only when consumed (e.g., by workspace activation passing them to the agent). |
| Encryption key | `LLMSAFESPACE_CREDENTIAL_ENCRYPTION_KEY` env var (hex). **Dev fallback: random key generated at startup** (`api/internal/app/app.go:301-316`). |
| Who can read | Plaintext: anyone with the env var. The env var is set on the API pod from… nothing. The chart does not provision it (no `LLMSAFESPACE_CREDENTIAL_ENCRYPTION_KEY` in `api-deployment.yaml`). |
| Lifetime | No rotation mechanism. `KeyVersion` column exists but no key-version migration / rewrap path is wired up beyond `ListByKeyVersionBelow` for inspection (`pkg/credentials/service.go:20`). |

**Critical issue:** if the API pod restarts without `LLMSAFESPACE_CREDENTIAL_ENCRYPTION_KEY` set, a fresh random key is generated and **every credential_set in the database becomes undecryptable forever** with no warning to the operator. `app.go:312` makes this silent.

---

## 3. System secrets

### 3.1 JWT signing key

| Property | Value | Citation |
|---|---|---|
| Semantic | HS256 secret. Whoever holds it can mint any user's JWT. | `auth.go:140, 269` |
| At rest | K8s Secret `<release>-credentials`, key `jwt-secret`. Created by Helm chart with `randAlphaNum 64` if not specified, persisted via `helm.sh/resource-policy: keep`. | `charts/llmsafespace/templates/secret.yaml:21-25`; `charts/llmsafespace/values.yaml:251` |
| In transit | Mounted as env var `LLMSAFESPACE_AUTH_JWTSECRET` on API pod via `secretKeyRef`. | `charts/llmsafespace/templates/api-deployment.yaml:58-62` |
| Encryption | etcd at-rest encryption only (cluster-dependent; chart does not require it). |
| Who can read | Anyone with `secrets get` in `<release-namespace>`. RT-1.3 F1.3.3 establishes the API SA itself has `secrets full CRUD + watch` in workspace ns (which == release ns by default), making the JWT secret reachable from any pod that mounts the API SA — including any pod the API SA can `exec` into (F1.3.6: API pod, controller pod, migration jobs, MCP pods). |
| Lifetime | **Never rotated.** Comment in chart explicitly says: "rotating jwt-secret would invalidate all active sessions" — `helm.sh/resource-policy: keep` prevents accidental rotation on `helm uninstall`. No rotation runbook, no dual-key support (`auth.go:298-304` accepts only one key). |

### 3.2 Postgres password

| Property | Value | Citation |
|---|---|---|
| At rest | Same K8s Secret, key `postgres-password`. Default chart value: `"changeme"` (`values.yaml:249`). |
| In transit | Env var `LLMSAFESPACE_DATABASE_PASSWORD` on API pod (`api-deployment.yaml:47-51`). API → Postgres connection: protocol depends on `postgresql.sslMode` which defaults to **`disable`** (`values.yaml:259`). |
| Who can read | Same blast radius as JWT secret. |
| Lifetime | Never rotated. |

### 3.3 Redis password

| Property | Value | Citation |
|---|---|---|
| At rest | Same Secret, key `redis-password`. **Default empty** (`values.yaml:250`). |
| In transit | Env var `LLMSAFESPACE_REDIS_PASSWORD` (api-deployment.yaml:52-57, marked `optional: true`). Plain TCP to Redis (no TLS in chart). |
| Who can read | Same as JWT secret. |
| Lifetime | Never rotated. |

### 3.4 Webhook TLS cert (controller validating webhook)

| Property | Value | Citation |
|---|---|---|
| Semantic | TLS cert for the controller's admission webhook server. RSA-2048, 1-year validity, 30-day renewal window. | `charts/llmsafespace/templates/webhook-cert.yaml:38-45` |
| At rest | K8s Secret `<release>-webhook-cert` (cert-manager issued). | `webhook-cert.yaml:39` |
| In transit | Mounted on controller pod at `/tmp/k8s-webhook-server/serving-certs`, mode 0444. | `controller-deployment.yaml:71-74, 80-84` |
| Encryption | etcd at-rest only. |
| Who can read | Controller SA. Anyone with `secrets get` in release ns. RT-1.3 F1.3.1 establishes controller has cluster-wide `secrets full CRUD` so any leaked controller token can also exfiltrate this. |
| Lifetime | Issued by cert-manager Issuer/ClusterIssuer (self-signed by default, `webhook-cert.yaml:18, 26`). Rotation policy `Always`. |

### 3.5 Encryption keys held in env vars on API pod

| Env var | Purpose | Where set | Chart-provisioned? |
|---|---|---|---|
| `LLMSAFESPACE_MASTER_SECRET` | Derives DEK-cache wrapping key (HKDF "dek-cache"). If unset, DEKs are stored as hex in Redis. | API process startup; `secrets_adapters.go:254` | **No.** Chart's `api-deployment.yaml:44-66` does not include this env var. |
| `LLMSAFESPACE_DEK_MASTER_KEY` | Legacy alias for above. | `secrets_adapters.go:257` | No. |
| `LLMSAFESPACE_CREDENTIAL_ENCRYPTION_KEY` | KEK for `credential_sets.providers_encrypted`. | `app.go:302`. Random fallback if unset (silent data loss on restart). | **No.** Not in `api-deployment.yaml`. |

These three keys are required for the encryption guarantees of §2.1 (DEK at rest in Redis) and §2.4 (credential_sets at rest in DB) to hold. **The chart does not provision any of them.** A default install has zero defense-in-depth: DEKs sit in Redis as hex; credential_sets are unrecoverable on restart.

---

## 4. Workspace-time secrets

### 4.1 Sandbox pod password (opencode BasicAuth)

| Property | Value | Citation |
|---|---|---|
| Semantic | 32-character random string used as the `OPENCODE_SERVER_PASSWORD` for HTTP Basic Auth on the sandbox's opencode server (`opencode:<password>`). | `controller/internal/workspace/controller.go:479`; `cmd/workspace-agentd/main.go:38, 350-359`; `runtimes/base/tools/entrypoints/entrypoint-opencode.sh:20-21` |
| At rest | K8s Secret `workspace-pw-<workspace-name>` in workspace ns, key `password`. **Cleartext bytes; not hashed, not encrypted at rest beyond etcd.** | `controller.go:473-491`; `controller/internal/workspace/constants.go:40` |
| In transit (controller → pod) | Mounted into credential-setup init container at `/mnt/secrets/password/password`, copied to `/sandbox-cfg/password`. | `controller.go:706-721` |
| In transit (API → opencode for proxying) | API reads the K8s Secret (`proxy.go:590-614`), caches in-memory, then sends `Authorization: Basic` over **plain HTTP** (`http://<podIP>:4096/...`). | `proxy.go:404-405, 425, 814, 1007`; `session_tracker.go:169, 174`; `proxy_input.go:124` |
| Encryption | None in transit. etcd at rest only. |
| Who can read | Anyone with `secrets get` in workspace ns: the API SA (RT-1.3:70), the controller SA, anyone with `pods/exec` into pods that can read the SA token. The agentd reads it at startup (`main.go:346`). |
| Lifetime | Generated once on PVC bind (`controller.go:138-145`). **Never rotated.** Deleted on workspace terminate (`controller.go:369-375`). No mechanism to re-roll the password if a leak is suspected. |

### 4.2 Session DEK (Redis cache of unwrapped DEK)

| Property | Value | Citation |
|---|---|---|
| Semantic | Per-session unwrapped DEK that decrypts user_secrets. | `pkg/secrets/key_service.go:113-138` |
| At rest | Redis key `dek:<jti>`. Optionally wrapped with `dekMasterKey()` (32 bytes, HKDF-derived from `LLMSAFESPACE_MASTER_SECRET`); otherwise hex-encoded plaintext. | `pkg/secrets/redis_cache.go:12, 31-44` |
| In transit | Set/Get on Redis connection (plain TCP per chart, no TLS). |
| Encryption | Only if master secret is set (see §3.5). The chart does not set it. |
| Who can read | Redis credentials → DEK; or master secret + Redis → DEK. The session DEK directly unwraps every secret the user owns, so this is functionally equivalent to a per-user secret-store master key. |
| Lifetime | TTL = `cfg.Auth.TokenDuration` (default unknown — chart does not set; tests use 24h). Evicted on logout (`auth_revocation_test.go` for test coverage; `key_service.go:141`). |

### 4.3 agentd-side password file

| Property | Value | Citation |
|---|---|---|
| At rest | `/sandbox-cfg/password` inside sandbox pod, mode set by emptyDir default. Read once at agentd startup, held in `OpenCodeClient.password` field (cleartext in process memory). | `pkg/agentd/types.go:8`; `cmd/workspace-agentd/main.go:346-359` |
| In transit | None outside pod. Used to set the basic-auth header on agentd's loopback calls to opencode (`localhost:4096`). | `main.go:38, 203` |
| Lifetime | Process lifetime. Recreated on pod restart (controller ensurePasswordSecret may re-fetch existing or never-rotate). |

### 4.4 agentd auth tokens — **THERE ARE NONE**

The agentd HTTP server on `0.0.0.0:4097` (`pkg/agentd/types.go:16-17`) accepts `/v1/healthz`, `/v1/readyz`, `/v1/statusz`, `/v1/reload-secrets` **without any authentication**. `cmd/workspace-agentd/secrets.go:159-213` decodes the request body and calls `Materialize` directly. The only mitigation is the workspace NetworkPolicy (`charts/llmsafespace/templates/workspace-network-policy.yaml:30-41`) which restricts ingress to pods matching `networkPolicy.apiPodLabelSelector` in the release namespace.

**Failure modes covered by this single defense:**
- NetworkPolicy CNI not installed → no enforcement → any pod in the cluster can POST cleartext secrets to `/v1/reload-secrets` and overwrite the sandbox's secrets.
- Operator misconfigures `apiPodLabelSelector` → wrong pods can reach the agentd.
- Anyone with `pods/exec` into the API pod (RT-1.3 F1.3.6) can issue the request from inside the cluster's allowed selector.

### 4.5 Ephemeral user-secrets K8s Secret

| Property | Value | Citation |
|---|---|---|
| Semantic | Bundle of decrypted user secrets, written to K8s as a `Secret` so the credential-setup init container can mount it. Single key `secrets.json`. | `api/internal/services/workspace/workspace_service.go:902-929` |
| At rest | K8s Secret `workspace-secrets-<workspace-id>` in workspace ns. **Cleartext JSON of every bound secret's plaintext.** etcd encryption only. |
| In transit | Created via API server (the workspace_service.go path uses the API SA's K8s client). |
| Encryption | None at the application layer. |
| Who can read | Same blast radius as §4.1 — `secrets get` in workspace ns reaches it. The labels `llmsafespace.dev/ephemeral=true` and `llmsafespace.dev/workspace=<id>` make it trivially discoverable. |
| Lifetime | Created on workspace activation (`workspace_service.go:780`), deleted on workspace terminate or on pod startup (`controller.go:191, 256`). **However, if a workspace activation completes successfully and the pod boots, the controller does NOT immediately delete this Secret** — `controller.go:191, 256` only delete it from `handleSuspended` and `handleTerminating` paths, not after init container success. The Secret therefore lingers on disk until the workspace is suspended or deleted. |

---

## 5. Build-time secrets

Searched for image-baked secrets in:
- `runtimes/base/Dockerfile*` — no `ARG` or `ENV` for credentials, only runtime version pinning.
- `controller/Dockerfile`, `api/Dockerfile`, `cmd/workspace-agentd/Dockerfile` — no credential ARGs.
- Helm chart `values.yaml` — `imagePullSecrets` is configurable but not pre-baked into images.

No build-time secrets are baked into images. **Status: clean.**

---

## 6. Phase-1 findings

### F1.7.1 (CRITICAL) — Decrypted user secrets transmitted over plain HTTP

`api/internal/handlers/secrets.go:291` — the API POSTs decrypted user secrets to `http://<podIP>:4097/v1/reload-secrets` without TLS. The body contains every bound secret's plaintext (LLM API keys, SSH private keys, OAuth tokens). Any sniffer between the API pod and the sandbox pod (CNI plugin, sidecar, mirrored interface) sees them. Also no authentication on the receiving end (`cmd/workspace-agentd/secrets.go:159-213` accepts arbitrary POSTs); only workspace NetworkPolicy gates this.

Promote to Phase 6 test plan: **RT-6.27 — POST forged secrets bundle to `/v1/reload-secrets` from a pod that satisfies the NetworkPolicy selector** (e.g., another API replica) and observe override. Cross-references RT-1.4 §4 (network topology) which already flagged plaintext intra-cluster traffic.

### F1.7.2 (CRITICAL) — API key stored cleartext in DB and full bearer used as Redis key

`api/migrations/000001_initial_schema.up.sql:12-20` defines `api_keys.key VARCHAR(255) UNIQUE`; `database.go:244-271` queries `WHERE k.key = $1`. Live bearer tokens recoverable from a single SQL `SELECT key FROM api_keys`. RT-1.3 F1.3.3 establishes API SA reads the chart Secret holding `postgres-password` → exfil all API keys. Additionally `auth.go:117, 388` stores `apikey:<full-bearer-token>` in Redis for 15 minutes; anyone reading the Redis keyspace prefix recovers live tokens.

Should be: hash with HMAC-SHA-256 at rest using a server key; index on prefix; lookup by prefix then constant-time-compare hash.

### F1.7.3 (HIGH) — Sandbox pod password is never rotated, kept cleartext at rest

`controller.go:473-491` generates the password once and stores it cleartext in a K8s Secret. `proxy.go:590-614` caches in-memory in the API. **No rotation mechanism exists.** A leak of any of: K8s API credentials with workspace-ns Secret access, the API pod's memory, the sandbox pod's `/sandbox-cfg/password` file, or any wireshark of port 4096 (since BasicAuth header rides plaintext HTTP — `proxy.go:425, 814, 1007`) — gives the attacker authenticated access to opencode for the workspace lifetime.

### F1.7.4 (HIGH) — Credential-set encryption key has random dev fallback that silently breaks data on restart

`api/internal/app/app.go:301-316`: if `LLMSAFESPACE_CREDENTIAL_ENCRYPTION_KEY` is unset, a random key is generated. The chart's `api-deployment.yaml:44-66` **does not set this env var**. Any default install that uses credential_sets:
1. First boot: random key A → encrypts.
2. API pod restart: random key B → previous ciphertext undecryptable.
3. No alarm; the operator sees decrypt errors at request time.

Same issue applies to `LLMSAFESPACE_MASTER_SECRET` (DEK cache wrapping). Chart does not set; without it, DEKs sit hex-encoded in Redis.

### F1.7.5 (HIGH) — JWT secret is never rotated by design

`charts/llmsafespace/templates/secret.yaml:9-20` documents that `jwt-secret` MUST persist across upgrades to avoid invalidating sessions. There is no dual-key validation in `auth.go:298-304` (single `s.jwtSecret`). The result: JWT key compromise has no remediation short of a maintenance window where every user must re-authenticate. Same applies to `postgres-password` and `redis-password` — `helm.sh/resource-policy: keep` prevents Helm from rotating any of these.

### F1.7.6 (MEDIUM) — Ephemeral user-secrets K8s Secret lingers after pod boot

`workspace_service.go:902-929` creates `workspace-secrets-<id>` containing all decrypted secrets. `controller.go:450-459` only deletes it on `handleSuspended` and `handleTerminating`. While the workspace pod is running, this cleartext bundle remains on the K8s API and in etcd. RT-1.3:70 establishes API SA can read every Secret in workspace ns; F1.3.6 establishes lateral movement through `pods/exec`. The secret should be deleted after init containers complete.

### F1.7.7 (MEDIUM) — `user_secret_bindings.workspace_id` has no FK; orphans on workspace delete

`api/migrations/000008_user_secrets.up.sql:18-23`: `workspace_id` is `VARCHAR(36) NOT NULL` with no `REFERENCES workspaces(id)` and no cleanup. Deleting a workspace from the API leaves binding rows pointing to a non-existent workspace. Not exploitable directly, but contradicts the documented zero-knowledge property: a forensic operator querying `user_secret_bindings` after workspace deletion still sees which secret was bound to which (now-deleted) workspace.

### F1.7.8 (MEDIUM) — `secret_audit_log` has unbounded retention and no PII expiry

`api/migrations/000008_user_secrets.up.sql:28-36`: no FK, no retention. The log keeps `user_id`, `secret_id`, `workspace_id`, `metadata.name` indefinitely, including for users and workspaces that have been deleted. The `metadata` field is JSONB and can include human-readable secret names (e.g., `"my-personal-github-pat"`). No GDPR/data-deletion path.

### F1.7.9 (MEDIUM) — Recovery key has no rotation, no record of disclosure

`pkg/secrets/key_service.go:108`: emitted hex once at registration, never tracked again. A user who lost or shared the recovery key has no first-class way to rotate it without performing a recovery. There is no `recovery_key_disclosed_at` column, no opt-out beyond skipping `WrappedDEKRecovery` at registration (which the API does not expose).

### F1.7.10 (LOW) — MD5 used for token cache key

`api/internal/services/auth/auth.go:27-30`: `hashToken` uses MD5. Collision-broken; cache poisoning is the realistic risk (token A and token B colliding → A accepted as B). Replace with SHA-256.

### F1.7.11 (LOW) — `/v1/reload-secrets` log message includes count but no provenance

`cmd/workspace-agentd/secrets.go:186-190`: logs `materialized=N skipped=N failed=N` without the source IP or any client identifier. If the NetworkPolicy is bypassed (F1.7.1), the agentd cannot answer "which pod pushed these secrets". Add request source logging (subject to redaction at `pkg/redact/redact.go:28-45`).

### F1.7.12 (INFO) — Redaction layer exists but does not cover all sinks

`pkg/redact/redact.go:28-45` handles the most common secret shapes. It is wired to the request-logging middleware (`api/internal/middleware/logging.go:54`) and request-error middleware (`error_handler.go:40`). It is **not** wired into:
- agentd's stderr output of `reportResult` (`cmd/workspace-agentd/secrets.go:142-154`) — though this only emits secret names + Outcome, not values.
- the `recoveryKey` field in the `/auth/register` response (returned cleartext intentionally).
- DB error wrapping — `fmt.Errorf("encrypt secret: %w", err)` in `secret_service.go:39` may surface internal state through error chains.

Not a finding per se; recorded so Phase 4 (defensive review) covers it.

---

## 7. Cross-reference matrix

| Secret type | RT-1.3 RBAC reach | RT-1.4 network reach | Encryption at rest | Encryption in transit | Rotation |
|---|---|---|---|---|---|
| User password | API SA via `pods/exec` (F1.3.6) | TLS at ingress only | bcrypt | TLS-only | User-initiated |
| JWT secret | API SA, controller SA via `secrets get` (F1.3.3, F1.3.1) | env var in pod | etcd-only | env var (no transit) | **Never** |
| API key | API SA + DB creds | env var, Redis | **None** | TLS-only | **Never** |
| Recovery key | None on server (zero-knowledge) | TLS at ingress only | Not stored | TLS-only | Manual via recovery flow |
| user_secrets ciphertext | DB creds | DB connection (no TLS by default) | AES-256-GCM | TLS-only | Via key rotation flow |
| user_secrets DEK (Redis) | Redis creds + master secret | Redis connection (no TLS by default) | Optional AES-256-GCM | none | Per-session |
| credential_sets | DB + cred encryption key | DB connection | AES-256-GCM (when key set) | TLS-only | Not implemented |
| Postgres password | secrets get on release ns (F1.3.3) | env var | etcd-only | env var | **Never** |
| Redis password | same | env var | etcd-only | env var | **Never** |
| Webhook TLS | controller SA cluster-wide (F1.3.1); operator creds | Mounted file | etcd-only | n/a | cert-manager rotates yearly |
| Sandbox pod password | API SA, controller SA in workspace ns | Plain HTTP between API and pod | **None** | **Plain HTTP BasicAuth** | **Never** |
| Ephemeral user-secrets Secret | API SA, controller SA in workspace ns | n/a | etcd-only | n/a | Per-activation; lingers post-boot |
| `/v1/reload-secrets` payload | NetworkPolicy-gated only | **Plain HTTP, no auth** | n/a | **Plaintext** | n/a |

---

## 8. Phase-1 priority for Phase 6

The findings above split into three buckets:

**Block release (must fix before any production posture):**
- F1.7.1 — plain-HTTP plaintext secrets between API and sandbox
- F1.7.2 — cleartext API key storage
- F1.7.4 — silent random-key fallback for credential_sets

**Bake into the threat-model and Phase-6 pentest plan:**
- F1.7.3 — sandbox pod password rotation
- F1.7.5 — JWT/Postgres/Redis password rotation
- F1.7.6 — ephemeral Secret lingering

**Hygiene track (Phase 4 defensive review):**
- F1.7.7, F1.7.8, F1.7.9, F1.7.10, F1.7.11, F1.7.12
