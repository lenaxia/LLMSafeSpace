package user

import (
	"net/http"

	"github.com/gin-gonic/gin"
	
	"github.com/lenaxia/llmsafespace/api/internal/handlers/auth"
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
	userGroup := router.Group("/user")
	userGroup.Use(auth.AuthMiddleware(h.Services.GetAuth()))
	
	userGroup.GET("", h.GetCurrentUser)
	userGroup.GET("/sandboxes", h.ListUserSandboxes)
	userGroup.GET("/warmpools", h.ListUserWarmPools)
}

func (h *Handler) GetCurrentUser(c *gin.Context) {
	userID := h.GetUserID(c)
	
	// Get user from database
	user, err := h.Services.GetDatabase().GetUserByID(c.Request.Context(), userID)
	if err != nil {
		h.Logger.Error("Failed to get user", err, "user_id", userID)
		h.HandleError(c, http.StatusInternalServerError, "user_retrieval_failed", err.Error())
		return
	}
	
	// Remove sensitive information
	if user["password"] != nil {
		delete(user, "password")
	}
	
	c.JSON(http.StatusOK, user)
}

func (h *Handler) ListUserSandboxes(c *gin.Context) {
	userID := h.GetUserID(c)
	
	// Parse pagination parameters
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	
	// Validate pagination parameters
	if limit < 1 || limit > 100 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}
	
	// List sandboxes
	sandboxes, err := h.Services.GetSandbox().ListSandboxes(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.Logger.Error("Failed to list user sandboxes", err, "user_id", userID)
		h.HandleError(c, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"sandboxes": sandboxes,
		"pagination": gin.H{
			"limit":  limit,
			"offset": offset,
			"total":  len(sandboxes), // This should be the total count, not just the returned count
		},
	})
}

func (h *Handler) ListUserWarmPools(c *gin.Context) {
	userID := h.GetUserID(c)
	
	// Parse pagination parameters
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	
	// Validate pagination parameters
	if limit < 1 || limit > 100 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}
	
	// List warm pools
	warmPools, err := h.Services.GetWarmPool().ListWarmPools(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.Logger.Error("Failed to list user warm pools", err, "user_id", userID)
		h.HandleError(c, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"warmPools": warmPools,
		"pagination": gin.H{
			"limit":  limit,
			"offset": offset,
			"total":  len(warmPools), // This should be the total count, not just the returned count
		},
	})
}
