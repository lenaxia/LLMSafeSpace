// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	pkgerrors "github.com/lenaxia/llmsafespaces/pkg/errors"
	"github.com/lenaxia/llmsafespaces/pkg/secrets"
)

// DEKUnlocker is the caller-shaped subset of KeyService used by the
// soft-unlock endpoint. *secrets.KeyService satisfies it.
//
// Returns a non-nil error only when the unlock could not be completed
// (wrong password, DB issue, no user keys). The durable jwt_sessions
// write inside UnlockDEKWithSigningKey is best-effort and surfaces via
// logs, not the return value — login-style behavior.
type DEKUnlocker interface {
	UnlockDEKWithSigningKey(ctx context.Context, userID string, password []byte, sessionID string, ttl time.Duration, activeSigningKey []byte) error
}

// UnlockDEKHandler handles POST /api/v1/auth/unlock-dek (Epic 56).
//
// The "soft" in soft-unlock means: no JWT invalidation, no full re-login.
// The user re-enters their password to repopulate the per-session DEK
// when the durable rehydrate path fails for one of the residual cases
// the design enumerates (pre-feature backfill, US-50.4 DEK rotation,
// row corruption). On success the durable jwt_sessions row is rewritten
// under the user's MATCHED signing key — not the active key — so that
// a subsequent rehydrate after Valkey restart still works under the
// same JWT.
type UnlockDEKHandler struct {
	keys DEKUnlocker
}

// NewUnlockDEKHandler creates a handler with the given key-service.
func NewUnlockDEKHandler(keys DEKUnlocker) *UnlockDEKHandler {
	return &UnlockDEKHandler{keys: keys}
}

// Unlock handles POST /auth/unlock-dek.
func (h *UnlockDEKHandler) Unlock(c *gin.Context) {
	userID, sessionID := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	// API-key callers cannot soft-unlock-to-backfill: there is no JWT to
	// wrap the durable row under (no matched signing key). Surface a
	// 400 with a clear hint pointing at the api_keys.WrappedDEK design
	// (api keys store WrappedDEK + KekSalt directly in the api_keys row
	// when decrypt_access=true). The frontend should never reach this
	// endpoint from an API-key auth context, but a misbehaving client
	// otherwise drops through to a generic 500 — better to be explicit.
	matchedSigningKey := extractMatchedSigningKey(c)
	if matchedSigningKey == nil || isAPIKeySessionID(sessionID) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "soft-unlock requires a JWT session",
			"hint":  "API-key sessions store the wrapped DEK on the api_keys row itself (decrypt_access=true); no soft-unlock is needed.",
		})
		return
	}

	// Body parse with a strict size cap so a malicious client can't
	// blow up the request goroutine by posting megabytes.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password required"})
		return
	}

	// Re-derive the DEK and re-cache + re-wrap durable row under the
	// MATCHED key. The TTL is the JWT's remaining lifetime so the
	// cache + durable row expire when the JWT does.
	ttl := remainingTokenTTL(c)
	if ttl <= 0 {
		// Token effectively expired between AuthMiddleware accepting it
		// and us reading exp. Reject so the client re-logs.
		c.JSON(http.StatusUnauthorized, gin.H{"error": "session expired; please log in again"})
		return
	}

	err := h.keys.UnlockDEKWithSigningKey(c.Request.Context(), userID, []byte(req.Password), sessionID, ttl, matchedSigningKey)
	if err != nil {
		// Wrong password is the dominant failure. We map AEAD/bcrypt
		// failures to 401 with a generic message so a leak of err.Error()
		// in a future logger doesn't expose internal detail. The JWT
		// itself remains valid — the user just couldn't re-derive their
		// DEK, so secret reads continue to fail until they retry.
		if errors.Is(err, secrets.ErrInvalidPassword) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid password"})
			return
		}
		// Pre-Epic-10 users with no user_keys row reach this path with
		// a nil-error result from UnlockDEK (existing legacy behavior).
		// Other errors are server-side — surface as 500 without details.
		var se *pkgerrors.StatusError
		if errors.As(err, &se) {
			c.JSON(se.Status, gin.H{"error": se.Message})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unlock failed"})
		return
	}

	c.Status(http.StatusNoContent)
}

// isAPIKeySessionID reports whether a sessionID belongs to an API-key
// authenticated request (set by AuthMiddleware as "apikey:<hash>").
func isAPIKeySessionID(sessionID string) bool {
	return len(sessionID) > 7 && sessionID[:7] == "apikey:"
}

// remainingTokenTTL extracts the JWT's remaining lifetime from the gin
// context. AuthMiddleware doesn't expose the exp claim today, so we
// fall back to a conservative 1h ceiling — enough for the cache to
// outlive a typical request burst without exceeding any reasonable
// JWT lifetime. The durable row inherits this ttl as expires_at, so
// the janitor prunes it on the same schedule as the JWT itself.
//
// A future refinement is to have AuthMiddleware stash exp on the
// context (gated on jti != "") so the handler can use the exact
// remaining lifetime. For now the 1h ceiling is acceptable because:
//
//	(1) The Redis cache uses ttl = min(exp - now, 1h) anyway in
//	    ValidateTokenWithClientIP, so the durable TTL mirrors the
//	    cache TTL contract callers already see.
//	(2) On the next Valkey restart, the durable rehydrate will hit
//	    or miss based on whether the row's expires_at is still in
//	    the future — a 1h TTL means a soft-unlock close to the JWT's
//	    natural expiry produces a 1h durable row that may outlive
//	    the JWT. Harmless: the rehydrate path requires the JWT to
//	    validate before it even reaches GetDEK.
func remainingTokenTTL(c *gin.Context) time.Duration {
	// Conservative default. See doc above for why.
	return time.Hour
}
