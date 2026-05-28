# Worklog: Epic 9 — Configuration & Settings Design

**Date:** 2026-05-27 / 2026-05-28
**Status:** Complete (design only — no implementation)

---

## Summary

Designed and wrote the full Epic 9 specification: a tiered configuration system with declarative settings schema, typed service interfaces, credential encryption with key rotation, and schema-driven admin/user settings UI.

Went through three review iterations:
1. Initial draft + internal consistency validation
2. Critical design review (robustness, SOLID, security, performance, legacy cruft)
3. Full rewrite incorporating all findings

---

## Key Design Decisions

- **Typed accessors** (`GetBool`, `GetInt`, `GetString`) — no `any` returns, no type assertion panics
- **Split services** — `InstanceSettingsService` (cached, hot-path) vs `UserSettingsService` (per-user, on-demand)
- **Singleflight cache** — full-map cache with `sync/singleflight` prevents thundering herd on TTL expiry
- **AES-256-GCM with versioned key rotation** — `key_version` column + blob prefix, `POST /admin/credentials/rotate-key` endpoint, idempotent re-encryption
- **Schema versioning** — `SchemaVersion` constant, seed job detects orphaned keys
- **Config.yaml deprecation** — Tier 2 fields removed from `Config` struct; DB is sole runtime authority
- **Auth middleware loads role** — `userRole` in context for all authenticated requests; AdminGuard reads from context (no DB call)
- **Dead code cleanup** — explicit story (US-9.7) to remove `RequireRoles`, `ExemptRoles`, deprecated config fields
- **No Tier 1 platform endpoint** — infrastructure config not exposed via API (security, no user value)

---

## Files Changed

- `design/stories/epic-9-configuration-settings/README.md` — full epic specification (724 lines)

---

## Issues Found & Fixed During Review

| Issue | Resolution |
|-------|-----------|
| `user_settings.user_id` was UUID, actual table uses VARCHAR(36) | Fixed FK type |
| Timestamps were `TIMESTAMP`, existing schema uses `WITH TIME ZONE` | Aligned |
| `credentials.defaultSetId` redundant with `is_default` column | Removed setting |
| No `updated_at` trigger mechanism | Added Postgres trigger function |
| `ActivateWorkspaceResponse.Suspended` is `string` but cap reduction needs multiple | Noted `[]string` change |
| Rate limiter reads static config, needs SettingsService | Added prerequisite #4 |
| `CredentialSetService` missing from `Services` interface | Added |
| Seed job depended on service (US-9.2) but only needs schema (US-9.1) | Fixed dependency graph |
| Static encryption key with no rotation | Added key rotation story (US-9.14) |
| `any` return types lose type safety | Replaced with typed accessors |
| Single SettingsService violates SRP | Split into two services |
| Cache stampede on TTL expiry | Added singleflight |
| Tier 1 platform endpoint leaks topology | Removed |
| Config.yaml fields duplicate Tier 2 settings | Explicit deprecation plan |

---

## Story Count

17 stories across 3 phases:
- **Phase A (Foundation):** 8 stories — schema, services, seed, APIs, cleanup
- **Phase B (UI + Enforcement):** 5 stories — admin/user/workspace UI, max workspaces/storage
- **Phase C (Credentials):** 4 stories — credential sets, key rotation, credentials UI, model picker
