package warmpool

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	
	"github.com/lenaxia/llmsafespace/api/internal/handlers/auth"
	"github.com/lenaxia/llmsafespace/api/internal/handlers/common"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/types"
	"github.com/lenaxia/llmsafespace/api/internal/validation"
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
	warmpoolGroup := router.Group("/warmpools")
	warmpoolGroup.Use(auth.AuthMiddleware(h.Services.GetAuth()))
	
	warmpoolGroup.GET("", h.ListWarmPools)
	warmpoolGroup.POST("", h.CreateWarmPool)
	warmpoolGroup.GET("/:name", h.GetWarmPool)
	warmpoolGroup.PATCH("/:name", h.UpdateWarmPool)
	warmpoolGroup.DELETE("/:name", h.DeleteWarmPool)
	warmpoolGroup.GET("/:name/status", h.GetWarmPoolStatus)
	warmpoolGroup.GET("/status", h.GetGlobalWarmPoolStatus)
}

func (h *Handler) CreateWarmPool(c *gin.Context) {
	var req types.CreateWarmPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.HandleError(c, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}
	
	// Validate request
	if err := validation.ValidateCreateWarmPoolRequest(req); err != nil {
		h.HandleError(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	
	// Set user ID and namespace
	req.UserID = h.GetUserID(c)
	req.Namespace = "default" // Get from config or user context
	
	// Create warm pool
	warmPool, err := h.Services.GetWarmPool().CreateWarmPool(c.Request.Context(), req)
	if err != nil {
		h.Logger.Error("Failed to create warm pool", err, 
			"user_id", req.UserID, 
			"name", req.Name, 
			"runtime", req.Runtime)
		h.HandleError(c, http.StatusInternalServerError, "creation_failed", err.Error())
		return
	}
	
	c.JSON(http.StatusCreated, warmPool)
}

func (h *Handler) GetWarmPool(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_name", "Warm pool name is required")
		return
	}
	
	namespace := c.DefaultQuery("namespace", "default")
	
	// Check if user has access to this warm pool
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "warmpool", name, "read") {
		h.HandleError(c, http.StatusForbidden, "access_denied", "You don't have access to this warm pool")
		return
	}
	
	// Get warm pool
	warmPool, err := h.Services.GetWarmPool().GetWarmPool(c.Request.Context(), name, namespace)
	if err != nil {
		h.Logger.Error("Failed to get warm pool", err, 
			"name", name, 
			"namespace", namespace)
		h.HandleError(c, http.StatusInternalServerError, "retrieval_failed", err.Error())
		return
	}
	
	c.JSON(http.StatusOK, warmPool)
}

func (h *Handler) ListWarmPools(c *gin.Context) {
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
		h.Logger.Error("Failed to list warm pools", err, "user_id", userID)
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

func (h *Handler) UpdateWarmPool(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_name", "Warm pool name is required")
		return
	}
	
	var req types.UpdateWarmPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.HandleError(c, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}
	
	// Set name, user ID, and namespace
	req.Name = name
	req.UserID = h.GetUserID(c)
	req.Namespace = c.DefaultQuery("namespace", "default")
	
	// Check if user has access to this warm pool
	if !h.Services.GetAuth().CheckResourceAccess(req.UserID, "warmpool", name, "update") {
		h.HandleError(c, http.StatusForbidden, "access_denied", "You don't have access to this warm pool")
		return
	}
	
	// Update warm pool
	warmPool, err := h.Services.GetWarmPool().UpdateWarmPool(c.Request.Context(), req)
	if err != nil {
		h.Logger.Error("Failed to update warm pool", err, 
			"name", name, 
			"namespace", req.Namespace)
		h.HandleError(c, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	
	c.JSON(http.StatusOK, warmPool)
}

func (h *Handler) DeleteWarmPool(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_name", "Warm pool name is required")
		return
	}
	
	namespace := c.DefaultQuery("namespace", "default")
	
	// Check if user has access to this warm pool
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "warmpool", name, "delete") {
		h.HandleError(c, http.StatusForbidden, "access_denied", "You don't have access to this warm pool")
		return
	}
	
	// Delete warm pool
	err := h.Services.GetWarmPool().DeleteWarmPool(c.Request.Context(), name, namespace)
	if err != nil {
		h.Logger.Error("Failed to delete warm pool", err, 
			"name", name, 
			"namespace", namespace)
		h.HandleError(c, http.StatusInternalServerError, "deletion_failed", err.Error())
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"message": "Warm pool deleted successfully",
	})
}

func (h *Handler) GetWarmPoolStatus(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_name", "Warm pool name is required")
		return
	}
	
	namespace := c.DefaultQuery("namespace", "default")
	
	// Check if user has access to this warm pool
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "warmpool", name, "read") {
		h.HandleError(c, http.StatusForbidden, "access_denied", "You don't have access to this warm pool")
		return
	}
	
	// Get warm pool status
	status, err := h.Services.GetWarmPool().GetWarmPoolStatus(c.Request.Context(), name, namespace)
	if err != nil {
		h.Logger.Error("Failed to get warm pool status", err, 
			"name", name, 
			"namespace", namespace)
		h.HandleError(c, http.StatusInternalServerError, "status_retrieval_failed", err.Error())
		return
	}
	
	c.JSON(http.StatusOK, status)
}

func (h *Handler) GetGlobalWarmPoolStatus(c *gin.Context) {
	// Get global warm pool status
	status, err := h.Services.GetWarmPool().GetGlobalWarmPoolStatus(c.Request.Context())
	if err != nil {
		h.Logger.Error("Failed to get global warm pool status", err)
		h.HandleError(c, http.StatusInternalServerError, "status_retrieval_failed", err.Error())
		return
	}
	
	c.JSON(http.StatusOK, status)
}
