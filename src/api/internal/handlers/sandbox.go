package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/auth"
	"github.com/lenaxia/llmsafespace/api/internal/services/sandbox"
)

// SandboxHandler handles sandbox-related API endpoints
type SandboxHandler struct {
	logger        *logger.Logger
	sandboxSvc    SandboxService
	authSvc       AuthService
	upgrader      websocket.Upgrader
}

// NewSandboxHandler creates a new SandboxHandler
func NewSandboxHandler(log *logger.Logger, sandboxSvc SandboxService, authSvc AuthService) *SandboxHandler {
	return &SandboxHandler{
		logger:     log,
		sandboxSvc: sandboxSvc,
		authSvc:    authSvc,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				// In production, implement proper origin checking
				return true
			},
		},
	}
}

// RegisterRoutes registers all sandbox routes
func (h *SandboxHandler) RegisterRoutes(router *gin.RouterGroup) {
	sandboxGroup := router.Group("/sandboxes")
	sandboxGroup.Use(h.authSvc.AuthMiddleware())
	
	sandboxGroup.GET("", h.ListSandboxes)
	sandboxGroup.POST("", h.CreateSandbox)
	sandboxGroup.GET("/:id", h.GetSandbox)
	sandboxGroup.DELETE("/:id", h.TerminateSandbox)
	sandboxGroup.GET("/:id/status", h.GetSandboxStatus)
	sandboxGroup.POST("/:id/execute", h.ExecuteCode)
	sandboxGroup.GET("/:id/files", h.ListFiles)
	sandboxGroup.GET("/:id/files/*path", h.DownloadFile)
	sandboxGroup.PUT("/:id/files/*path", h.UploadFile)
	sandboxGroup.DELETE("/:id/files/*path", h.DeleteFile)
	sandboxGroup.POST("/:id/packages", h.InstallPackages)
}

// ListSandboxes lists all sandboxes for the authenticated user
func (h *SandboxHandler) ListSandboxes(c *gin.Context) {
	userID := h.authSvc.GetUserID(c)
	
	// Get query parameters
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	
	// Get sandboxes
	sandboxes, err := h.sandboxSvc.ListSandboxes(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to list sandboxes", err, "user_id", userID)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to list sandboxes",
		})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"sandboxes": sandboxes,
	})
}

// CreateSandbox creates a new sandbox
func (h *SandboxHandler) CreateSandbox(c *gin.Context) {
	userID := h.authSvc.GetUserID(c)
	
	// Parse request body
	var req sandbox.CreateSandboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request body",
		})
		return
	}
	
	// Set user ID
	req.UserID = userID
	
	// Create sandbox
	sb, err := h.sandboxSvc.CreateSandbox(c.Request.Context(), req)
	if err != nil {
		h.logger.Error("Failed to create sandbox", err, 
			"user_id", userID, 
			"runtime", req.Runtime,
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to create sandbox",
		})
		return
	}
	
	c.JSON(http.StatusOK, sb)
}

// GetSandbox gets a sandbox by ID
func (h *SandboxHandler) GetSandbox(c *gin.Context) {
	userID := h.authSvc.GetUserID(c)
	sandboxID := c.Param("id")
	
	// Check if user has access to this sandbox
	if !h.authSvc.CheckResourceAccess(userID, "sandbox", sandboxID, "read") {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "You don't have access to this sandbox",
		})
		return
	}
	
	// Get sandbox
	sb, err := h.sandboxSvc.GetSandbox(c.Request.Context(), sandboxID)
	if err != nil {
		h.logger.Error("Failed to get sandbox", err, 
			"user_id", userID, 
			"sandbox_id", sandboxID,
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to get sandbox",
		})
		return
	}
	
	c.JSON(http.StatusOK, sb)
}

// TerminateSandbox terminates a sandbox
func (h *SandboxHandler) TerminateSandbox(c *gin.Context) {
	userID := h.authSvc.GetUserID(c)
	sandboxID := c.Param("id")
	
	// Check if user has access to this sandbox
	if !h.authSvc.CheckResourceAccess(userID, "sandbox", sandboxID, "terminate") {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "You don't have access to this sandbox",
		})
		return
	}
	
	// Terminate sandbox
	err := h.sandboxSvc.TerminateSandbox(c.Request.Context(), sandboxID)
	if err != nil {
		h.logger.Error("Failed to terminate sandbox", err, 
			"user_id", userID, 
			"sandbox_id", sandboxID,
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to terminate sandbox",
		})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"message": "Sandbox terminated successfully",
	})
}

// GetSandboxStatus gets the status of a sandbox
func (h *SandboxHandler) GetSandboxStatus(c *gin.Context) {
	userID := h.authSvc.GetUserID(c)
	sandboxID := c.Param("id")
	
	// Check if user has access to this sandbox
	if !h.authSvc.CheckResourceAccess(userID, "sandbox", sandboxID, "read") {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "You don't have access to this sandbox",
		})
		return
	}
	
	// Get sandbox status
	status, err := h.sandboxSvc.GetSandboxStatus(c.Request.Context(), sandboxID)
	if err != nil {
		h.logger.Error("Failed to get sandbox status", err, 
			"user_id", userID, 
			"sandbox_id", sandboxID,
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to get sandbox status",
		})
		return
	}
	
	c.JSON(http.StatusOK, status)
}

// ExecuteCode executes code in a sandbox
func (h *SandboxHandler) ExecuteCode(c *gin.Context) {
	userID := h.authSvc.GetUserID(c)
	sandboxID := c.Param("id")
	
	// Check if user has access to this sandbox
	if !h.authSvc.CheckResourceAccess(userID, "sandbox", sandboxID, "execute") {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "You don't have access to this sandbox",
		})
		return
	}
	
	// Parse request body
	var req sandbox.ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request body",
		})
		return
	}
	
	// Set sandbox ID
	req.SandboxID = sandboxID
	
	// Execute code
	result, err := h.sandboxSvc.Execute(c.Request.Context(), req)
	if err != nil {
		h.logger.Error("Failed to execute code", err, 
			"user_id", userID, 
			"sandbox_id", sandboxID,
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to execute code",
		})
		return
	}
	
	c.JSON(http.StatusOK, result)
}

// ListFiles lists files in a sandbox
func (h *SandboxHandler) ListFiles(c *gin.Context) {
	userID := h.authSvc.GetUserID(c)
	sandboxID := c.Param("id")
	
	// Check if user has access to this sandbox
	if !h.authSvc.CheckResourceAccess(userID, "sandbox", sandboxID, "read") {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "You don't have access to this sandbox",
		})
		return
	}
	
	// Get path
	path := c.DefaultQuery("path", "/workspace")
	
	// List files
	files, err := h.sandboxSvc.ListFiles(c.Request.Context(), sandboxID, path)
	if err != nil {
		h.logger.Error("Failed to list files", err, 
			"user_id", userID, 
			"sandbox_id", sandboxID,
			"path", path,
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to list files",
		})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"files": files,
	})
}

// DownloadFile downloads a file from a sandbox
func (h *SandboxHandler) DownloadFile(c *gin.Context) {
	userID := h.authSvc.GetUserID(c)
	sandboxID := c.Param("id")
	
	// Check if user has access to this sandbox
	if !h.authSvc.CheckResourceAccess(userID, "sandbox", sandboxID, "read") {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "You don't have access to this sandbox",
		})
		return
	}
	
	// Get path
	path := c.Param("path")
	
	// Download file
	content, err := h.sandboxSvc.DownloadFile(c.Request.Context(), sandboxID, path)
	if err != nil {
		h.logger.Error("Failed to download file", err, 
			"user_id", userID, 
			"sandbox_id", sandboxID,
			"path", path,
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to download file",
		})
		return
	}
	
	// Set content type
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", "attachment; filename="+path)
	
	// Write content
	c.Writer.Write(content)
}

// UploadFile uploads a file to a sandbox
func (h *SandboxHandler) UploadFile(c *gin.Context) {
	userID := h.authSvc.GetUserID(c)
	sandboxID := c.Param("id")
	
	// Check if user has access to this sandbox
	if !h.authSvc.CheckResourceAccess(userID, "sandbox", sandboxID, "write") {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "You don't have access to this sandbox",
		})
		return
	}
	
	// Get path
	path := c.Param("path")
	
	// Read file content
	content, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Failed to read file content",
		})
		return
	}
	
	// Upload file
	fileInfo, err := h.sandboxSvc.UploadFile(c.Request.Context(), sandboxID, path, content)
	if err != nil {
		h.logger.Error("Failed to upload file", err, 
			"user_id", userID, 
			"sandbox_id", sandboxID,
			"path", path,
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to upload file",
		})
		return
	}
	
	c.JSON(http.StatusOK, fileInfo)
}

// DeleteFile deletes a file from a sandbox
func (h *SandboxHandler) DeleteFile(c *gin.Context) {
	userID := h.authSvc.GetUserID(c)
	sandboxID := c.Param("id")
	
	// Check if user has access to this sandbox
	if !h.authSvc.CheckResourceAccess(userID, "sandbox", sandboxID, "write") {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "You don't have access to this sandbox",
		})
		return
	}
	
	// Get path
	path := c.Param("path")
	
	// Delete file
	err := h.sandboxSvc.DeleteFile(c.Request.Context(), sandboxID, path)
	if err != nil {
		h.logger.Error("Failed to delete file", err, 
			"user_id", userID, 
			"sandbox_id", sandboxID,
			"path", path,
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to delete file",
		})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"message": "File deleted successfully",
	})
}

// InstallPackages installs packages in a sandbox
func (h *SandboxHandler) InstallPackages(c *gin.Context) {
	userID := h.authSvc.GetUserID(c)
	sandboxID := c.Param("id")
	
	// Check if user has access to this sandbox
	if !h.authSvc.CheckResourceAccess(userID, "sandbox", sandboxID, "write") {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "You don't have access to this sandbox",
		})
		return
	}
	
	// Parse request body
	var req sandbox.InstallPackagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request body",
		})
		return
	}
	
	// Set sandbox ID
	req.SandboxID = sandboxID
	
	// Install packages
	result, err := h.sandboxSvc.InstallPackages(c.Request.Context(), req)
	if err != nil {
		h.logger.Error("Failed to install packages", err, 
			"user_id", userID, 
			"sandbox_id", sandboxID,
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to install packages",
		})
		return
	}
	
	c.JSON(http.StatusOK, result)
}

// HandleWebSocket handles WebSocket connections for streaming execution outputs
func (h *SandboxHandler) HandleWebSocket(c *gin.Context) {
	userID := h.authSvc.GetUserID(c)
	sandboxID := c.Param("id")
	
	// Check if user has access to this sandbox
	if !h.authSvc.CheckResourceAccess(userID, "sandbox", sandboxID, "execute") {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "You don't have access to this sandbox",
		})
		return
	}
	
	// Upgrade connection to WebSocket
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Error("Failed to upgrade connection", err)
		return
	}
	defer conn.Close()
	
	// Create session
	session, err := h.sandboxSvc.CreateSession(userID, sandboxID, conn)
	if err != nil {
		h.logger.Error("Failed to create session", err)
		return
	}
	defer h.sandboxSvc.CloseSession(session.ID)
	
	// Handle WebSocket session
	h.sandboxSvc.HandleSession(session)
}
