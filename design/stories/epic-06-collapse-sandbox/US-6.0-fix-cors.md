# US-6.0: Fix CORS

**Epic:** 6 — Collapse Sandbox into Workspace
**Status:** Planning
**Dependencies:** None (independent, ship now)

## Problem

Every POST from `https://safespace.thekao.cloud` is blocked by CORS. `SecurityMiddleware` logs `CORS origin not allowed` for every authenticated request, yet the request still succeeds (200/201) because CORS headers are missing, not blocking — the browser can't read the response.

## Root Cause

`api/internal/middleware/security.go:59` — `DefaultSecurityConfig()` sets `AllowedOrigins: []string{}` (empty slice). The frontend at `safespace.thekao.cloud` is never allowed.

There are two middleware layers:
1. `SecurityMiddleware` (`security.go:76`) — checks CORS with `cfg.AllowedOrigins` (empty)
2. `CORSMiddleware` (`cors.go:53`) — separate middleware with `DefaultCORSConfig` that defaults to `*`

The security middleware blocks before CORS middleware runs.

## Fix

Add `safespace.thekao.cloud` to `SecurityConfig.AllowedOrigins`. Configure via environment variable or Helm values.

## Files Modified

| File | Change |
|------|--------|
| `api/internal/middleware/security.go` | Add allowed origins from config/env |
| `charts/llmsafespace/templates/api-deployment.yaml` | Add `SECURITY_ALLOWED_ORIGINS` env var |
| `charts/llmsafespace/values.yaml` | Add `api.security.allowedOrigins` |

## Acceptance Criteria

1. POST from `safespace.thekao.cloud` returns `Access-Control-Allow-Origin` header
2. No `CORS origin not allowed` warnings in API logs
3. Frontend login, workspace creation, and session creation all work from browser
