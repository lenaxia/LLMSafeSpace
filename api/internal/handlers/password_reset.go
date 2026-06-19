// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier:AGPL-3.0-or-later

package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	emailsvc "github.com/lenaxia/llmsafespaces/api/internal/services/email"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

const (
	passwordResetTokenBytes = 32
	passwordResetTokenTTL   = 15 * time.Minute
	passwordResetMinLen     = 8
)

// EmailToken is now in pkg/types (avoids import cycle between handlers and database).

// passwordResetStore is the data-access surface for email tokens.
type passwordResetStore interface {
	CreateEmailToken(ctx context.Context, t *types.EmailToken) error
	GetEmailTokenByHash(ctx context.Context, hash string) (*types.EmailToken, error)
	ConsumeEmailToken(ctx context.Context, id string) error
}

// passwordResetUserLookup resolves users by email and ID.
type passwordResetUserLookup interface {
	GetUserByEmail(ctx context.Context, email string) (*types.User, error)
	GetUser(ctx context.Context, userID string) (*types.User, error)
}

// passwordResetKeyInitializer reinitialises the DEK with a new password.
// On password reset via email, the old DEK is unrecoverable (no old password,
// no recovery key available), so InitialiseUserKeys creates a fresh DEK.
type passwordResetKeyInitializer interface {
	InitializeUserKeys(ctx context.Context, userID string, password []byte) (recoveryKeyHex string, err error)
}

// passwordResetPwUpdater updates the bcrypt hash.
type passwordResetPwUpdater interface {
	UpdatePasswordHash(ctx context.Context, userID string, newPassword []byte) error
}

// passwordResetSessionRevoker revokes all outstanding JWTs for a user.
type passwordResetSessionRevoker interface {
	RevokeAllUserSessions(userID string) error
}

// PasswordResetHandler handles the password-reset-via-email flow.
type PasswordResetHandler struct {
	store    passwordResetStore
	users    passwordResetUserLookup
	keyInit  passwordResetKeyInitializer
	pwUpdate passwordResetPwUpdater
	revoker  passwordResetSessionRevoker
	email    *emailsvc.Service
	log      passwordResetLogger
}

type passwordResetLogger interface {
	Warn(msg string, args ...any)
	Error(msg string, err error, args ...any)
}

// NewPasswordResetHandler constructs the handler. email may carry a nil
// provider (noop mode); in that case SendPasswordReset returns
// ErrNotConfigured and the request endpoint logs a warning but still
// returns 202 (the token is still created; the email is just not sent).
func NewPasswordResetHandler(
	store passwordResetStore,
	users passwordResetUserLookup,
	keyInit passwordResetKeyInitializer,
	pwUpdate passwordResetPwUpdater,
	revoker passwordResetSessionRevoker,
	email *emailsvc.Service,
	log passwordResetLogger,
) *PasswordResetHandler {
	return &PasswordResetHandler{
		store: store, users: users, keyInit: keyInit,
		pwUpdate: pwUpdate, revoker: revoker, email: email, log: log,
	}
}

// Request handles POST /api/v1/auth/password-reset/request.
//
// Always returns 202 (no email enumeration). Only sends a reset email if:
//   - the user exists
//   - the user's email is verified (don't send to unverified mailboxes)
func (h *PasswordResetHandler) Request(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a valid email is required"})
		return
	}

	ctx := c.Request.Context()
	emailAddr := strings.ToLower(strings.TrimSpace(req.Email))

	user, err := h.users.GetUserByEmail(ctx, emailAddr)
	if err != nil {
		if h.log != nil {
			h.log.Error("password-reset: user lookup error", err, "email", emailAddr)
		}
		c.Status(http.StatusAccepted)
		return
	}
	if user == nil {
		c.Status(http.StatusAccepted)
		return
	}

	verified := user.EmailVerified
	if !verified {
		c.Status(http.StatusAccepted)
		return
	}

	token, hash, err := generateEmailToken()
	if err != nil {
		if h.log != nil {
			h.log.Error("password-reset: token generation failed", err)
		}
		c.Status(http.StatusAccepted)
		return
	}

	if err := h.store.CreateEmailToken(ctx, &types.EmailToken{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Kind:      "password_reset",
		TokenHash: hash,
		ExpiresAt: time.Now().Add(passwordResetTokenTTL),
	}); err != nil {
		if h.log != nil {
			h.log.Error("password-reset: token store failed", err)
		}
		c.Status(http.StatusAccepted)
		return
	}

	if h.email != nil {
		if err := h.email.SendPasswordReset(ctx, emailAddr, token); err != nil && h.log != nil {
			h.log.Error("password-reset: send email failed", err, "to", emailAddr)
		}
	}

	c.Status(http.StatusAccepted)
}

// Confirm handles POST /api/v1/auth/password-reset/confirm.
//
// Public (the token IS the credential). Verifies the token hash, checks
// expiry + consumption, then executes the 5-step reset:
//  1. Consume token (single-use)
//  2. Reinitialise DEK (old DEK unrecoverable without old password/recovery key)
//  3. Update bcrypt hash
//  4. Revoke all outstanding sessions
//  5. Send "password changed" notification email
//
// Returns the new recovery key so the user can save it.
func (h *PasswordResetHandler) Confirm(c *gin.Context) {
	var req struct {
		Token       string `json:"token" binding:"required"`
		NewPassword string `json:"newPassword" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token and newPassword (min 8 chars) are required"})
		return
	}

	ctx := c.Request.Context()
	hash := hashToken(req.Token)

	tok, err := h.store.GetEmailTokenByHash(ctx, hash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify token"})
		return
	}
	if tok == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "token not found"})
		return
	}
	if tok.ConsumedAt != nil {
		c.JSON(http.StatusGone, gin.H{"error": "token already used"})
		return
	}
	if time.Now().After(tok.ExpiresAt) {
		c.JSON(http.StatusGone, gin.H{"error": "token expired"})
		return
	}

	// Consume first — single-use. If subsequent steps fail, the token is
	// still consumed (the user must request a new one). This is the correct
	// tradeoff: allowing retry on a consumed token would break single-use.
	if err := h.store.ConsumeEmailToken(ctx, tok.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to consume token"})
		return
	}

	// Step 1: Reinitialise DEK. The old DEK is unrecoverable (no old password
	// or recovery key available via email reset). This creates a fresh DEK
	// wrapped with the new password and generates a new recovery key.
	recoveryKey, err := h.keyInit.InitializeUserKeys(ctx, tok.UserID, []byte(req.NewPassword))
	if err != nil {
		if h.log != nil {
			h.log.Error("password-reset: DEK reinit failed", err, "user_id", tok.UserID)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "password reset failed"})
		return
	}

	// Step 2: Update bcrypt hash.
	if err := h.pwUpdate.UpdatePasswordHash(ctx, tok.UserID, []byte(req.NewPassword)); err != nil {
		if h.log != nil {
			h.log.Error("password-reset: bcrypt update failed", err, "user_id", tok.UserID)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "password reset failed"})
		return
	}

	// Step 3: Revoke all outstanding sessions (OWASP-mandated). Best-effort.
	if h.revoker != nil {
		if err := h.revoker.RevokeAllUserSessions(tok.UserID); err != nil && h.log != nil {
			h.log.Warn("password-reset: session revocation failed (non-fatal)", "user_id", tok.UserID)
		}
	}

	// Step 4: Send "password changed" notification (OWASP-mandated).
	// Best-effort: a send failure does not undo the reset.
	if h.email != nil {
		if user, err := h.users.GetUser(ctx, tok.UserID); err == nil && user != nil {
			if sendErr := h.email.SendPasswordChanged(ctx, user.Email); sendErr != nil && h.log != nil {
				h.log.Error("password-reset: notification send failed", sendErr, "user_id", tok.UserID)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"recoveryKey": recoveryKey})
}

func generateEmailToken() (token string, hash string, err error) {
	raw := make([]byte, passwordResetTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	hash = hashToken(token) // hashToken is defined in invitations.go (same package)
	return token, hash, nil
}
