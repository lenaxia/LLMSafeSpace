package sandbox

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	
	"github.com/lenaxia/llmsafespace/api/internal/handlers/auth"
	"github.com/lenaxia/llmsafespace/api/internal/handlers/common"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/types"
	"github.com/lenaxia/llmsafespace/api/internal/validation"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// In production, implement proper origin checking
		return true
	},
}

type Handler struct {
	*common.BaseHandler
}

func NewHandler(services interfaces.Services, logger *logger.Logger) *Handler {
	return &Handler{
		BaseHandler: common.NewBaseHandler(services, logger),
	}
}

func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	sandboxGroup := router.Group("/sandboxes")
	sandboxGroup.Use(auth.AuthMiddleware(h.Services.GetAuth()))
	
	sandboxGroup.GET("", h.ListSandboxes)
	sandboxGroup.POST("", h.CreateSandbox)
	sandboxGroup.GET("/:id", h.GetSandbox)
	sandboxGroup.DELETE("/:id", h.TerminateSandbox)
	sandboxGroup.GET("/:id/status", h.GetSandboxStatus)
	sandboxGroup.POST("/:id/execute", h.Execute)
	sandboxGroup.GET("/:id/files", h.ListFiles)
	sandboxGroup.GET("/:id/files/*path", h.DownloadFile)
	sandboxGroup.PUT("/:id/files/*path", h.UploadFile)
	sandboxGroup.DELETE("/:id/files/*path", h.DeleteFile)
	sandboxGroup.POST("/:id/packages", h.InstallPackages)
	sandboxGroup.GET("/:id/stream", h.WebSocketHandler)
}

func (h *Handler) CreateSandbox(c *gin.Context) {
	var req types.CreateSandboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.HandleError(c, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}
	
	// Validate request
	if err := validation.ValidateCreateSandboxRequest(req); err != nil {
		h.HandleError(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	
	// Set user ID and namespace
	req.UserID = h.GetUserID(c)
	req.Namespace = "default" // Get from config or user context
	
	// Create sandbox
	sandbox, err := h.Services.GetSandbox().CreateSandbox(c.Request.Context(), req)
	if err != nil {
		h.Logger.Error("Failed to create sandbox", err, 
			"user_id", req.UserID, 
			"runtime", req.Runtime)
		h.HandleError(c, http.StatusInternalServerError, "creation_failed", err.Error())
		return
	}
	
	c.JSON(http.StatusCreated, sandbox)
}

func (h *Handler) GetSandbox(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_id", "Sandbox ID is required")
		return
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "read") {
		h.HandleError(c, http.StatusForbidden, "access_denied", "You don't have access to this sandbox")
		return
	}
	
	// Get sandbox
	sandbox, err := h.Services.GetSandbox().GetSandbox(c.Request.Context(), sandboxID)
	if err != nil {
		if _, ok := err.(*types.SandboxNotFoundError); ok {
			h.HandleError(c, http.StatusNotFound, "not_found", err.Error())
		} else {
			h.Logger.Error("Failed to get sandbox", err, "sandbox_id", sandboxID)
			h.HandleError(c, http.StatusInternalServerError, "retrieval_failed", err.Error())
		}
		return
	}
	
	c.JSON(http.StatusOK, sandbox)
}

func (h *Handler) ListSandboxes(c *gin.Context) {
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
		h.Logger.Error("Failed to list sandboxes", err, "user_id", userID)
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

func (h *Handler) TerminateSandbox(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_id", "Sandbox ID is required")
		return
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "delete") {
		h.HandleError(c, http.StatusForbidden, "access_denied", "You don't have access to this sandbox")
		return
	}
	
	// Terminate sandbox
	err := h.Services.GetSandbox().TerminateSandbox(c.Request.Context(), sandboxID)
	if err != nil {
		if _, ok := err.(*types.SandboxNotFoundError); ok {
			h.HandleError(c, http.StatusNotFound, "not_found", err.Error())
		} else {
			h.Logger.Error("Failed to terminate sandbox", err, "sandbox_id", sandboxID)
			h.HandleError(c, http.StatusInternalServerError, "termination_failed", err.Error())
		}
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"message": "Sandbox terminated successfully",
	})
}

func (h *Handler) GetSandboxStatus(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_id", "Sandbox ID is required")
		return
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "read") {
		h.HandleError(c, http.StatusForbidden, "access_denied", "You don't have access to this sandbox")
		return
	}
	
	// Get sandbox status
	status, err := h.Services.GetSandbox().GetSandboxStatus(c.Request.Context(), sandboxID)
	if err != nil {
		if _, ok := err.(*types.SandboxNotFoundError); ok {
			h.HandleError(c, http.StatusNotFound, "not_found", err.Error())
		} else {
			h.Logger.Error("Failed to get sandbox status", err, "sandbox_id", sandboxID)
			h.HandleError(c, http.StatusInternalServerError, "status_retrieval_failed", err.Error())
		}
		return
	}
	
	c.JSON(http.StatusOK, status)
}

func (h *Handler) Execute(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_id", "Sandbox ID is required")
		return
	}
	
	var req types.ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.HandleError(c, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}
	
	// Validate request
	if req.Type != "code" && req.Type != "command" {
		h.HandleError(c, http.StatusBadRequest, "invalid_type", "Type must be 'code' or 'command'")
		return
	}
	if req.Content == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_content", "Content is required")
		return
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "execute") {
		h.HandleError(c, http.StatusForbidden, "access_denied", "You don't have access to this sandbox")
		return
	}
	
	// Set sandbox ID
	req.SandboxID = sandboxID
	
	// Execute code or command
	result, err := h.Services.GetSandbox().Execute(c.Request.Context(), req)
	if err != nil {
		h.Logger.Error("Failed to execute in sandbox", err, 
			"sandbox_id", sandboxID, 
			"type", req.Type)
		h.HandleError(c, http.StatusInternalServerError, "execution_failed", err.Error())
		return
	}
	
	c.JSON(http.StatusOK, result)
}

func (h *Handler) ListFiles(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_id", "Sandbox ID is required")
		return
	}
	
	path := c.DefaultQuery("path", "/workspace")
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "read") {
		h.HandleError(c, http.StatusForbidden, "access_denied", "You don't have access to this sandbox")
		return
	}
	
	// List files
	files, err := h.Services.GetSandbox().ListFiles(c.Request.Context(), sandboxID, path)
	if err != nil {
		if _, ok := err.(*types.SandboxNotFoundError); ok {
			h.HandleError(c, http.StatusNotFound, "not_found", err.Error())
		} else {
			h.Logger.Error("Failed to list files", err, 
				"sandbox_id", sandboxID, 
				"path", path)
			h.HandleError(c, http.StatusInternalServerError, "list_files_failed", err.Error())
		}
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"files": files,
		"path":  path,
	})
}

func (h *Handler) DownloadFile(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_id", "Sandbox ID is required")
		return
	}
	
	path := c.Param("path")
	if path == "" || path == "/" {
		h.HandleError(c, http.StatusBadRequest, "missing_path", "File path is required")
		return
	}
	
	// Remove leading slash from path parameter
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "read") {
		h.HandleError(c, http.StatusForbidden, "access_denied", "You don't have access to this sandbox")
		return
	}
	
	// Download file
	content, err := h.Services.GetSandbox().DownloadFile(c.Request.Context(), sandboxID, path)
	if err != nil {
		if _, ok := err.(*types.SandboxNotFoundError); ok {
			h.HandleError(c, http.StatusNotFound, "not_found", err.Error())
		} else {
			h.Logger.Error("Failed to download file", err, 
				"sandbox_id", sandboxID, 
				"path", path)
			h.HandleError(c, http.StatusInternalServerError, "download_failed", err.Error())
		}
		return
	}
	
	// Set content type based on file extension
	contentType := http.DetectContentType(content)
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", "attachment; filename="+path)
	
	c.Data(http.StatusOK, contentType, content)
}

func (h *Handler) UploadFile(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_id", "Sandbox ID is required")
		return
	}
	
	path := c.Param("path")
	if path == "" || path == "/" {
		h.HandleError(c, http.StatusBadRequest, "missing_path", "File path is required")
		return
	}
	
	// Remove leading slash from path parameter
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "write") {
		h.HandleError(c, http.StatusForbidden, "access_denied", "You don't have access to this sandbox")
		return
	}
	
	// Read file content
	content, err := c.GetRawData()
	if err != nil {
		h.HandleError(c, http.StatusBadRequest, "invalid_content", "Failed to read file content")
		return
	}
	
	// Upload file
	fileInfo, err := h.Services.GetSandbox().UploadFile(c.Request.Context(), sandboxID, path, content)
	if err != nil {
		if _, ok := err.(*types.SandboxNotFoundError); ok {
			h.HandleError(c, http.StatusNotFound, "not_found", err.Error())
		} else {
			h.Logger.Error("Failed to upload file", err, 
				"sandbox_id", sandboxID, 
				"path", path)
			h.HandleError(c, http.StatusInternalServerError, "upload_failed", err.Error())
		}
		return
	}
	
	c.JSON(http.StatusOK, fileInfo)
}

func (h *Handler) DeleteFile(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_id", "Sandbox ID is required")
		return
	}
	
	path := c.Param("path")
	if path == "" || path == "/" {
		h.HandleError(c, http.StatusBadRequest, "missing_path", "File path is required")
		return
	}
	
	// Remove leading slash from path parameter
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "write") {
		h.HandleError(c, http.StatusForbidden, "access_denied", "You don't have access to this sandbox")
		return
	}
	
	// Delete file
	err := h.Services.GetSandbox().DeleteFile(c.Request.Context(), sandboxID, path)
	if err != nil {
		if _, ok := err.(*types.SandboxNotFoundError); ok {
			h.HandleError(c, http.StatusNotFound, "not_found", err.Error())
		} else {
			h.Logger.Error("Failed to delete file", err, 
				"sandbox_id", sandboxID, 
				"path", path)
			h.HandleError(c, http.StatusInternalServerError, "delete_failed", err.Error())
		}
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"message": "File deleted successfully",
	})
}

func (h *Handler) InstallPackages(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_id", "Sandbox ID is required")
		return
	}
	
	var req types.InstallPackagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.HandleError(c, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}
	
	// Validate request
	if len(req.Packages) == 0 {
		h.HandleError(c, http.StatusBadRequest, "missing_packages", "At least one package is required")
		return
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "execute") {
		h.HandleError(c, http.StatusForbidden, "access_denied", "You don't have access to this sandbox")
		return
	}
	
	// Set sandbox ID
	req.SandboxID = sandboxID
	
	// Install packages
	result, err := h.Services.GetSandbox().InstallPackages(c.Request.Context(), req)
	if err != nil {
		if _, ok := err.(*types.SandboxNotFoundError); ok {
			h.HandleError(c, http.StatusNotFound, "not_found", err.Error())
		} else {
			h.Logger.Error("Failed to install packages", err, 
				"sandbox_id", sandboxID, 
				"packages", req.Packages)
			h.HandleError(c, http.StatusInternalServerError, "installation_failed", err.Error())
		}
		return
	}
	
	c.JSON(http.StatusOK, result)
}

func (h *Handler) WebSocketHandler(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_id", "Sandbox ID is required")
		return
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "connect") {
		h.HandleError(c, http.StatusForbidden, "access_denied", "You don't have access to this sandbox")
		return
	}
	
	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.Logger.Error("Failed to upgrade to WebSocket", err, "sandbox_id", sandboxID)
		return
	}
	
	// Create session
	session, err := h.Services.GetSandbox().CreateSession(userID, sandboxID, conn)
	if err != nil {
		h.Logger.Error("Failed to create WebSocket session", err, 
			"sandbox_id", sandboxID, 
			"user_id", userID)
		conn.Close()
		return
	}
	
	// Log connection
	h.Logger.Info("WebSocket connection established", 
		"sandbox_id", sandboxID, 
		"user_id", userID, 
		"session_id", session.ID)
	
	// Close session when done
	defer h.Services.GetSandbox().CloseSession(session.ID)
	
	// Handle session
	h.Services.GetSandbox().HandleSession(session)
}
