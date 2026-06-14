# Worklog: US-38.4 + US-38.5 — Hash API Keys in Redis, Fix noHTML Validator

**Date:** 2026-06-13
**Session:** Implement US-38.4 (hash API keys in Redis cache keys) and US-38.5 (fix noHTML validator logic error)
**Status:** Complete

---

## Objective

Fix two CRITICAL security vulnerabilities: API keys stored unhashed in Redis cache keys, and the noHTML validator allowing strings with unclosed angle brackets.

---

## Work Completed

### US-38.4: Hash API Keys in Redis Cache Keys
- Replaced raw API key with SHA-256 hash in Redis cache keys at auth.go:134 and auth.go:446
- Updated ~23 mock assertions in auth_test.go and auth_apikey_dek_test.go
- Consistent with existing JWT token hashing at auth.go:368

### US-38.5: Fix noHTML Validator Logic Error
- Changed || to && in validateNoHTML at validation.go:351
- Created validation_nohtml_test.go with 18 tests (6 bug tests, 12 pass tests)

---

## Key Decisions

- Used existing pkgutil.HashString (SHA-256) for consistency with JWT caching pattern
- All 18 noHTML tests use package middleware (internal test) to access unexported validateNoHTML

---

## Blockers

None.

---

## Tests Run

- `go test ./api/internal/middleware/ -run TestNoHTML -v` — 18/18 PASS
- `go test ./api/internal/services/auth/ -v` — PASS

---

## Next Steps

None — both fixes are complete and self-contained.

---

## Files Modified

- api/internal/services/auth/auth.go (2 cache key sites)
- api/internal/services/auth/auth_test.go (~12 mock assertions + import)
- api/internal/services/auth/auth_apikey_dek_test.go (~11 mock assertions)
- api/internal/middleware/validation.go (1 character fix)
- api/internal/middleware/validation_nohtml_test.go (new, 18 tests)
