package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

// SecretsHandler handles HTTP requests for the secrets API.
type SecretsHandler struct {
	svc *secrets.SecretService
}

// NewSecretsHandler creates a new SecretsHandler.
func NewSecretsHandler(svc *secrets.SecretService) *SecretsHandler {
	return &SecretsHandler{svc: svc}
}

// CreateSecret handles POST /api/v1/secrets
func (h *SecretsHandler) CreateSecret(c *gin.Context) {
	userID, sessionID := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	var req secrets.CreateSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	resp, err := h.svc.CreateSecret(c.Request.Context(), userID, sessionID, req)
	if err != nil {
		handleSecretError(c, err)
		return
	}

	c.JSON(http.StatusCreated, resp)
}

// ListSecrets handles GET /api/v1/secrets
func (h *SecretsHandler) ListSecrets(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	list, err := h.svc.ListSecrets(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list secrets"})
		return
	}
	if list == nil {
		list = []*secrets.SecretResponse{}
	}

	c.JSON(http.StatusOK, gin.H{"secrets": list})
}

// GetSecret handles GET /api/v1/secrets/:id
func (h *SecretsHandler) GetSecret(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	secretID := c.Param("id")
	resp, err := h.svc.GetSecret(c.Request.Context(), userID, secretID)
	if err != nil {
		handleSecretError(c, err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

// UpdateSecret handles PUT /api/v1/secrets/:id
func (h *SecretsHandler) UpdateSecret(c *gin.Context) {
	userID, sessionID := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	secretID := c.Param("id")
	var req secrets.UpdateSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if err := h.svc.UpdateSecret(c.Request.Context(), userID, sessionID, secretID, req); err != nil {
		handleSecretError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// DeleteSecret handles DELETE /api/v1/secrets/:id
func (h *SecretsHandler) DeleteSecret(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	secretID := c.Param("id")
	if err := h.svc.DeleteSecret(c.Request.Context(), userID, secretID); err != nil {
		handleSecretError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// SetBindings handles PUT /api/v1/workspaces/:id/bindings
func (h *SecretsHandler) SetBindings(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	workspaceID := c.Param("id")
	var req secrets.SetBindingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if err := h.svc.SetBindings(c.Request.Context(), userID, workspaceID, req.SecretIDs); err != nil {
		handleSecretError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// GetBindings handles GET /api/v1/workspaces/:id/bindings
func (h *SecretsHandler) GetBindings(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	workspaceID := c.Param("id")
	resp, err := h.svc.GetBindings(c.Request.Context(), userID, workspaceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get bindings"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// SetWorkspaceEnv handles PUT /api/v1/workspaces/:id/env
// Creates or updates env-secret type secrets bound to this workspace.
func (h *SecretsHandler) SetWorkspaceEnv(c *gin.Context) {
	userID, sessionID := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	workspaceID := c.Param("id")
	var req struct {
		Vars map[string]string `json:"vars" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "vars map required"})
		return
	}

	for varName, value := range req.Vars {
		secretName := fmt.Sprintf("%s-env-%s", workspaceID, varName)
		metadata, _ := json.Marshal(map[string]string{"var_name": varName})

		// Try to update existing, create if not found
		existing, _ := h.svc.GetSecretByName(c.Request.Context(), userID, secretName)
		if existing != nil {
			h.svc.UpdateSecret(c.Request.Context(), userID, sessionID, existing.ID, secrets.UpdateSecretRequest{Value: value})
		} else {
			created, err := h.svc.CreateSecret(c.Request.Context(), userID, sessionID, secrets.CreateSecretRequest{
				Name: secretName, Type: secrets.SecretTypeEnvSecret, Value: value,
				Metadata: metadata,
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to set env var: " + varName})
				return
			}
			// Auto-bind to workspace
			currentBindings, _ := h.svc.GetBindings(c.Request.Context(), userID, workspaceID)
			ids := []string{created.ID}
			for _, b := range currentBindings.Bindings {
				ids = append(ids, b.SecretID)
			}
			h.svc.SetBindings(c.Request.Context(), userID, workspaceID, ids)
		}
	}

	c.Status(http.StatusNoContent)
}

// GetWorkspaceEnv handles GET /api/v1/workspaces/:id/env
// Returns env var names (never values) bound to this workspace.
func (h *SecretsHandler) GetWorkspaceEnv(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	workspaceID := c.Param("id")
	resp, err := h.svc.GetBindings(c.Request.Context(), userID, workspaceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get env"})
		return
	}

	vars := []string{}
	for _, b := range resp.Bindings {
		if b.Type == secrets.SecretTypeEnvSecret {
			vars = append(vars, b.Name)
		}
	}
	c.JSON(http.StatusOK, gin.H{"vars": vars})
}

// DeleteWorkspaceEnv handles DELETE /api/v1/workspaces/:id/env/:name
func (h *SecretsHandler) DeleteWorkspaceEnv(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	workspaceID := c.Param("id")
	varName := c.Param("name")
	secretName := fmt.Sprintf("%s-env-%s", workspaceID, varName)

	existing, _ := h.svc.GetSecretByName(c.Request.Context(), userID, secretName)
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "env var not found"})
		return
	}

	if err := h.svc.DeleteSecret(c.Request.Context(), userID, existing.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete env var"})
		return
	}

	c.Status(http.StatusNoContent)
}

// GetAuditLog handles GET /api/v1/secrets/audit
func (h *SecretsHandler) GetAuditLog(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	query := secrets.AuditQuery{
		Action:      c.Query("action"),
		SecretID:    c.Query("secretId"),
		WorkspaceID: c.Query("workspaceId"),
		Limit:       100,
	}

	entries, err := h.svc.QueryAudit(c.Request.Context(), userID, query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query audit log"})
		return
	}
	if entries == nil {
		entries = []*secrets.AuditEntry{}
	}

	c.JSON(http.StatusOK, gin.H{"entries": entries})
}

// KeyRotator is the interface needed by the rotation handler.
type KeyRotator interface {
	RotateKeyWithPassword(ctx context.Context, userID string, password []byte, sessionID string, ttl time.Duration) (int, error)
	ChangePassword(ctx context.Context, userID string, oldPassword, newPassword []byte) error
	ResetWithRecoveryKey(ctx context.Context, userID string, recoveryKeyHex string, newPassword []byte) (string, error)
}

// PasswordHashUpdater updates the user's bcrypt hash in the database.
type PasswordHashUpdater interface {
	UpdatePasswordHash(ctx context.Context, userID string, newPassword []byte) error
}

// RotateKeyHandler handles account key management endpoints.
type RotateKeyHandler struct {
	keySvc     KeyRotator
	pwUpdater  PasswordHashUpdater
}

// NewRotateKeyHandler creates a new RotateKeyHandler.
func NewRotateKeyHandler(keySvc KeyRotator) *RotateKeyHandler {
	return &RotateKeyHandler{keySvc: keySvc}
}

// SetPasswordUpdater sets the optional password hash updater.
func (h *RotateKeyHandler) SetPasswordUpdater(u PasswordHashUpdater) {
	h.pwUpdater = u
}

// RotateKey handles POST /api/v1/account/rotate-key
func (h *RotateKeyHandler) RotateKey(c *gin.Context) {
	userID, sessionID := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password required for key rotation"})
		return
	}

	newVersion, err := h.keySvc.RotateKeyWithPassword(c.Request.Context(), userID, []byte(req.Password), sessionID, 24*time.Hour)
	if err != nil {
		if contains(err.Error(), "invalid password") {
			c.JSON(http.StatusForbidden, gin.H{"error": "invalid password"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "key rotation failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"keyVersion": newVersion})
}

// ChangePassword handles POST /api/v1/account/change-password
func (h *RotateKeyHandler) ChangePassword(c *gin.Context) {
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	var req struct {
		OldPassword string `json:"oldPassword" binding:"required"`
		NewPassword string `json:"newPassword" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "oldPassword and newPassword (min 8 chars) required"})
		return
	}

	if err := h.keySvc.ChangePassword(c.Request.Context(), userID, []byte(req.OldPassword), []byte(req.NewPassword)); err != nil {
		if contains(err.Error(), "unwrap DEK") || contains(err.Error(), "invalid") {
			c.JSON(http.StatusForbidden, gin.H{"error": "invalid current password"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "password change failed"})
		return
	}

	// Also update the bcrypt hash in the user database
	if h.pwUpdater != nil {
		if err := h.pwUpdater.UpdatePasswordHash(c.Request.Context(), userID, []byte(req.NewPassword)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "password change failed"})
			return
		}
	}

	c.Status(http.StatusNoContent)
}

// RecoverAccount handles POST /api/v1/account/recover
func (h *RotateKeyHandler) RecoverAccount(c *gin.Context) {
	// This is a public-ish endpoint (user forgot password) but still needs some identity.
	// In practice, this would be called after email verification. For now, require userID in body.
	var req struct {
		UserID      string `json:"userId" binding:"required"`
		RecoveryKey string `json:"recoveryKey" binding:"required"`
		NewPassword string `json:"newPassword" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userId, recoveryKey, and newPassword required"})
		return
	}

	newRecoveryKey, err := h.keySvc.ResetWithRecoveryKey(c.Request.Context(), req.UserID, req.RecoveryKey, []byte(req.NewPassword))
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid recovery key"})
		return
	}

	// Also update the bcrypt hash so the user can login with the new password
	if h.pwUpdater != nil {
		if err := h.pwUpdater.UpdatePasswordHash(c.Request.Context(), req.UserID, []byte(req.NewPassword)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "recovery failed"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"recoveryKey": newRecoveryKey})
}

// extractAuth gets userID and sessionID (jti) from the Gin context.
func extractAuth(c *gin.Context) (userID, sessionID string) {
	uid, exists := c.Get("userID")
	if !exists {
		return "", ""
	}
	userID = uid.(string)

	// sessionID is the JWT's jti claim, set by auth middleware
	sid, exists := c.Get("sessionID")
	if exists {
		sessionID = sid.(string)
	}
	return userID, sessionID
}

// handleSecretError maps domain errors to HTTP responses.
func handleSecretError(c *gin.Context, err error) {
	msg := err.Error()
	switch {
	case contains(msg, "not found"):
		c.JSON(http.StatusNotFound, gin.H{"error": "secret not found"})
	case contains(msg, "duplicate"):
		c.JSON(http.StatusConflict, gin.H{"error": "secret with this name already exists"})
	case contains(msg, "unavailable"):
		c.JSON(http.StatusForbidden, gin.H{"error": "encryption key not available; re-authenticate"})
	case contains(msg, "invalid secret type"), contains(msg, "requires metadata"), contains(msg, "requires"):
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
