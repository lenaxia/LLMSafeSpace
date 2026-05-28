package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/pkg/credentials"
)

// CredentialServiceInterface defines the methods the handler needs from the credential service.
type CredentialServiceInterface interface {
	Create(ctx context.Context, req credentials.CreateCredentialSetRequest) (*credentials.CredentialSet, error)
	Get(ctx context.Context, id string) (*credentials.CredentialSet, error)
	List(ctx context.Context) ([]*credentials.CredentialSet, error)
	Update(ctx context.Context, id string, req credentials.UpdateCredentialSetRequest) error
	Delete(ctx context.Context, id string) error
	SetDefault(ctx context.Context, id string) error
	GetDefault(ctx context.Context) (*credentials.CredentialSet, error)
	RotateEncryptionKey(ctx context.Context) (*credentials.RotateKeyResult, error)
	ListForUser(ctx context.Context, userID string) ([]*credentials.CredentialSet, error)
}

// CredentialsHandler handles credential set CRUD API requests.
type CredentialsHandler struct {
	svc CredentialServiceInterface
}

// NewCredentialsHandler creates a new credentials handler.
func NewCredentialsHandler(svc CredentialServiceInterface) *CredentialsHandler {
	return &CredentialsHandler{svc: svc}
}

// CreateCredentialSet handles POST /admin/credentials.
func (h *CredentialsHandler) CreateCredentialSet(c *gin.Context) {
	var req credentials.CreateCredentialSetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.Name == "" || req.Providers == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and providers are required"})
		return
	}

	cs, err := h.svc.Create(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, cs)
}

// GetCredentialSet handles GET /admin/credentials/:id.
func (h *CredentialsHandler) GetCredentialSet(c *gin.Context) {
	cs, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cs)
}

// ListCredentialSets handles GET /admin/credentials.
func (h *CredentialsHandler) ListCredentialSets(c *gin.Context) {
	list, err := h.svc.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if list == nil {
		list = []*credentials.CredentialSet{}
	}
	c.JSON(http.StatusOK, list)
}

// UpdateCredentialSet handles PUT /admin/credentials/:id.
func (h *CredentialsHandler) UpdateCredentialSet(c *gin.Context) {
	var req credentials.UpdateCredentialSetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	err := h.svc.Update(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

// DeleteCredentialSet handles DELETE /admin/credentials/:id.
func (h *CredentialsHandler) DeleteCredentialSet(c *gin.Context) {
	err := h.svc.Delete(c.Request.Context(), c.Param("id"))
	if err != nil {
		if strings.Contains(err.Error(), "referenced") {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// SetDefaultCredentialSet handles PUT /admin/credentials/:id/default.
func (h *CredentialsHandler) SetDefaultCredentialSet(c *gin.Context) {
	err := h.svc.SetDefault(c.Request.Context(), c.Param("id"))
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "default set"})
}

// RotateCredentialKey handles POST /admin/credentials/rotate-key.
func (h *CredentialsHandler) RotateCredentialKey(c *gin.Context) {
	result, err := h.svc.RotateEncryptionKey(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}
