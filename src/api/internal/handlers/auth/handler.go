package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/handlers/common"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

type Handler struct {
	*common.BaseHandler
}

func NewHandler(services interfaces.Services, logger *logger.Logger) *Handler {
	return &Handler{
		BaseHandler: common.NewBaseHandler(services, logger),
	}
}

func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	authGroup := router.Group("/auth")
	
	authGroup.POST("/login", h.Login)
	authGroup.POST("/logout", h.Logout)
	authGroup.POST("/token", h.CreateToken)
	authGroup.DELETE("/token", h.RevokeToken)
	
	// API Key management
	apiKeyGroup := router.Group("/apikeys")
	apiKeyGroup.Use(AuthMiddleware(h.Services.GetAuth()))
	
	apiKeyGroup.GET("", h.ListAPIKeys)
	apiKeyGroup.POST("", h.CreateAPIKey)
	apiKeyGroup.DELETE("/:id", h.RevokeAPIKey)
}

func (h *Handler) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	
	if err := c.ShouldBindJSON(&req); err != nil {
		h.HandleError(c, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}
	
	// Authentication logic would go here
	// For now, just return a mock token
	token, err := h.Services.GetAuth().GenerateToken(req.Username)
	if err != nil {
		h.HandleError(c, http.StatusInternalServerError, "token_generation_failed", err.Error())
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id": req.Username,
		},
	})
}

func (h *Handler) Logout(c *gin.Context) {
	token := c.GetHeader("Authorization")
	if token == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_token", "No token provided")
		return
	}
	
	// Remove "Bearer " prefix if present
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}
	
	err := h.Services.GetAuth().RevokeToken(token)
	if err != nil {
		h.HandleError(c, http.StatusInternalServerError, "token_revocation_failed", err.Error())
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"message": "Successfully logged out",
	})
}

func (h *Handler) CreateToken(c *gin.Context) {
	var req struct {
		UserID string `json:"userId" binding:"required"`
	}
	
	if err := c.ShouldBindJSON(&req); err != nil {
		h.HandleError(c, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}
	
	token, err := h.Services.GetAuth().GenerateToken(req.UserID)
	if err != nil {
		h.HandleError(c, http.StatusInternalServerError, "token_generation_failed", err.Error())
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"token": token,
	})
}

func (h *Handler) RevokeToken(c *gin.Context) {
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	
	if err := c.ShouldBindJSON(&req); err != nil {
		h.HandleError(c, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}
	
	err := h.Services.GetAuth().RevokeToken(req.Token)
	if err != nil {
		h.HandleError(c, http.StatusInternalServerError, "token_revocation_failed", err.Error())
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"message": "Token successfully revoked",
	})
}

func (h *Handler) ListAPIKeys(c *gin.Context) {
	// This would typically query the database for API keys
	// For now, just return a mock response
	c.JSON(http.StatusOK, gin.H{
		"apiKeys": []gin.H{
			{
				"id": "key1",
				"name": "Development Key",
				"createdAt": "2023-01-01T00:00:00Z",
			},
		},
	})
}

func (h *Handler) CreateAPIKey(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	
	if err := c.ShouldBindJSON(&req); err != nil {
		h.HandleError(c, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}
	
	// This would typically create an API key in the database
	// For now, just return a mock response
	c.JSON(http.StatusCreated, gin.H{
		"id": "new-key-id",
		"key": "llmsafespace_api_key_123456",
		"name": req.Name,
		"createdAt": "2023-07-01T00:00:00Z",
	})
}

func (h *Handler) RevokeAPIKey(c *gin.Context) {
	keyID := c.Param("id")
	if keyID == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_key_id", "API key ID is required")
		return
	}
	
	// This would typically revoke the API key in the database
	// For now, just return a success response
	c.JSON(http.StatusOK, gin.H{
		"message": "API key successfully revoked",
	})
}
