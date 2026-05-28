package handlers

import (
	"net/http"

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
