package sandbox

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/handlers/auth"
	"github.com/lenaxia/llmsafespace/api/internal/handlers/common"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
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

// @Summary Create a new sandbox
// @Description Creates a new sandbox environment for code execution
// @Tags Sandboxes
// @Accept json
// @Produce json
// @Param sandbox body types.CreateSandboxRequest true "Sandbox creation request"
// @Success 201 {object} types.Sandbox "Created sandbox"
// @Failure 400 {object} errors.APIError "Invalid request"
// @Failure 401 {object} errors.APIError "Unauthorized"
// @Failure 429 {object} errors.APIError "Rate limit exceeded"
// @Failure 500 {object} errors.APIError "Internal server error"
// @Security ApiKeyAuth
// @Router /sandboxes [post]
func (h *Handler) CreateSandbox(c *gin.Context) {
	var req types.CreateSandboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.BadRequest(c, "Invalid request body")
		return
	}
	
	// Validate request
	if err := validation.ValidateCreateSandboxRequest(req); err != nil {
		middleware.HandleAPIError(c, err)
		return
	}
	
	// Set user ID and namespace
	req.UserID = h.GetUserID(c)
	req.Namespace = "default" // Get from config or user context
	
	// Get request logger
	log := h.GetRequestLogger(c)
	
	// Create sandbox
	sandbox, err := h.Services.GetSandbox().CreateSandbox(c.Request.Context(), req)
	if err != nil {
		log.Error("Failed to create sandbox", err, 
			"user_id", req.UserID, 
			"runtime", req.Runtime)
		
		middleware.HandleAPIError(c, errors.NewInternalError("Failed to create sandbox", err))
		return
	}
	
	log.Info("Sandbox created successfully",
		"sandbox_id", sandbox.ID,
		"runtime", req.Runtime,
		"user_id", req.UserID)
	
	h.Created(c, sandbox)
}

// @Summary Get sandbox details
// @Description Retrieves details about a specific sandbox
// @Tags Sandboxes
// @Produce json
// @Param id path string true "Sandbox ID"
// @Success 200 {object} types.Sandbox "Sandbox details"
// @Failure 400 {object} errors.APIError "Invalid request"
// @Failure 401 {object} errors.APIError "Unauthorized"
// @Failure 403 {object} errors.APIError "Forbidden"
// @Failure 404 {object} errors.APIError "Sandbox not found"
// @Failure 500 {object} errors.APIError "Internal server error"
// @Security ApiKeyAuth
// @Router /sandboxes/{id} [get]
func (h *Handler) GetSandbox(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.BadRequest(c, "Sandbox ID is required")
		return
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "read") {
		h.Forbidden(c, "You don't have access to this sandbox")
		return
	}
	
	// Get request logger
	log := h.GetRequestLogger(c)
	
	// Get sandbox
	sandbox, err := h.Services.GetSandbox().GetSandbox(c.Request.Context(), sandboxID)
	if err != nil {
		if errors.IsSandboxNotFoundError(err) {
			h.NotFound(c, "sandbox", sandboxID)
		} else {
			log.Error("Failed to get sandbox", err, "sandbox_id", sandboxID)
			middleware.HandleAPIError(c, errors.NewInternalError("Failed to get sandbox", err))
		}
		return
	}
	
	log.Info("Sandbox retrieved successfully", "sandbox_id", sandboxID)
	
	h.Success(c, http.StatusOK, sandbox)
}

// @Summary List sandboxes
// @Description Lists all sandboxes for the authenticated user
// @Tags Sandboxes
// @Produce json
// @Param limit query int false "Maximum number of sandboxes to return" default(10) minimum(1) maximum(100)
// @Param offset query int false "Number of sandboxes to skip" default(0) minimum(0)
// @Success 200 {object} map[string]interface{} "List of sandboxes"
// @Failure 401 {object} errors.APIError "Unauthorized"
// @Failure 500 {object} errors.APIError "Internal server error"
// @Security ApiKeyAuth
// @Router /sandboxes [get]
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
	
	// Get request logger
	log := h.GetRequestLogger(c)
	
	// List sandboxes
	sandboxes, err := h.Services.GetSandbox().ListSandboxes(c.Request.Context(), userID, limit, offset)
	if err != nil {
		log.Error("Failed to list sandboxes", err, "user_id", userID)
		middleware.HandleAPIError(c, errors.NewInternalError("Failed to list sandboxes", err))
		return
	}
	
	log.Info("Sandboxes listed successfully", 
		"user_id", userID, 
		"count", len(sandboxes),
		"limit", limit,
		"offset", offset)
	
	h.Success(c, http.StatusOK, gin.H{
		"sandboxes": sandboxes,
		"pagination": gin.H{
			"limit":  limit,
			"offset": offset,
			"total":  len(sandboxes), // This should be the total count, not just the returned count
		},
	})
}

// @Summary Terminate a sandbox
// @Description Terminates a specific sandbox
// @Tags Sandboxes
// @Produce json
// @Param id path string true "Sandbox ID"
// @Success 200 {object} map[string]string "Success message"
// @Failure 400 {object} errors.APIError "Invalid request"
// @Failure 401 {object} errors.APIError "Unauthorized"
// @Failure 403 {object} errors.APIError "Forbidden"
// @Failure 404 {object} errors.APIError "Sandbox not found"
// @Failure 500 {object} errors.APIError "Internal server error"
// @Security ApiKeyAuth
// @Router /sandboxes/{id} [delete]
func (h *Handler) TerminateSandbox(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.BadRequest(c, "Sandbox ID is required")
		return
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "delete") {
		h.Forbidden(c, "You don't have access to this sandbox")
		return
	}
	
	// Get request logger
	log := h.GetRequestLogger(c)
	
	// Terminate sandbox
	err := h.Services.GetSandbox().TerminateSandbox(c.Request.Context(), sandboxID)
	if err != nil {
		if errors.IsSandboxNotFoundError(err) {
			h.NotFound(c, "sandbox", sandboxID)
		} else {
			log.Error("Failed to terminate sandbox", err, "sandbox_id", sandboxID)
			middleware.HandleAPIError(c, errors.NewInternalError("Failed to terminate sandbox", err))
		}
		return
	}
	
	log.Info("Sandbox terminated successfully", "sandbox_id", sandboxID)
	
	h.Success(c, http.StatusOK, gin.H{
		"message": "Sandbox terminated successfully",
	})
}

// @Summary Get sandbox status
// @Description Retrieves the current status of a specific sandbox
// @Tags Sandboxes
// @Produce json
// @Param id path string true "Sandbox ID"
// @Success 200 {object} types.SandboxStatus "Sandbox status"
// @Failure 400 {object} errors.APIError "Invalid request"
// @Failure 401 {object} errors.APIError "Unauthorized"
// @Failure 403 {object} errors.APIError "Forbidden"
// @Failure 404 {object} errors.APIError "Sandbox not found"
// @Failure 500 {object} errors.APIError "Internal server error"
// @Security ApiKeyAuth
// @Router /sandboxes/{id}/status [get]
func (h *Handler) GetSandboxStatus(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.BadRequest(c, "Sandbox ID is required")
		return
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "read") {
		h.Forbidden(c, "You don't have access to this sandbox")
		return
	}
	
	// Get request logger
	log := h.GetRequestLogger(c)
	
	// Get sandbox status
	status, err := h.Services.GetSandbox().GetSandboxStatus(c.Request.Context(), sandboxID)
	if err != nil {
		if errors.IsSandboxNotFoundError(err) {
			h.NotFound(c, "sandbox", sandboxID)
		} else {
			log.Error("Failed to get sandbox status", err, "sandbox_id", sandboxID)
			middleware.HandleAPIError(c, errors.NewInternalError("Failed to get sandbox status", err))
		}
		return
	}
	
	log.Info("Sandbox status retrieved successfully", "sandbox_id", sandboxID)
	
	h.Success(c, http.StatusOK, status)
}

// @Summary Execute code or command
// @Description Executes code or a command in a specific sandbox
// @Tags Sandboxes
// @Accept json
// @Produce json
// @Param id path string true "Sandbox ID"
// @Param execution body types.ExecuteRequest true "Execution request"
// @Success 200 {object} types.ExecutionResult "Execution result"
// @Failure 400 {object} errors.APIError "Invalid request"
// @Failure 401 {object} errors.APIError "Unauthorized"
// @Failure 403 {object} errors.APIError "Forbidden"
// @Failure 404 {object} errors.APIError "Sandbox not found"
// @Failure 500 {object} errors.APIError "Internal server error"
// @Security ApiKeyAuth
// @Router /sandboxes/{id}/execute [post]
func (h *Handler) Execute(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.BadRequest(c, "Sandbox ID is required")
		return
	}
	
	var req types.ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.BadRequest(c, "Invalid request body")
		return
	}
	
	// Validate request
	if err := validation.ValidateExecuteRequest(req); err != nil {
		middleware.HandleAPIError(c, err)
		return
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "execute") {
		h.Forbidden(c, "You don't have access to this sandbox")
		return
	}
	
	// Set sandbox ID
	req.SandboxID = sandboxID
	
	// Get request logger
	log := h.GetRequestLogger(c)
	
	// Execute code or command
	result, err := h.Services.GetSandbox().Execute(c.Request.Context(), req)
	if err != nil {
		log.Error("Failed to execute in sandbox", err, 
			"sandbox_id", sandboxID, 
			"type", req.Type)
		middleware.HandleAPIError(c, errors.NewInternalError("Failed to execute in sandbox", err))
		return
	}
	
	log.Info("Execution completed successfully", 
		"sandbox_id", sandboxID, 
		"type", req.Type,
		"exit_code", result.ExitCode)
	
	h.Success(c, http.StatusOK, result)
}

// @Summary List files in sandbox
// @Description Lists files in a specific directory in the sandbox
// @Tags Sandboxes
// @Produce json
// @Param id path string true "Sandbox ID"
// @Param path query string false "Directory path" default("/workspace")
// @Success 200 {object} map[string]interface{} "List of files"
// @Failure 400 {object} errors.APIError "Invalid request"
// @Failure 401 {object} errors.APIError "Unauthorized"
// @Failure 403 {object} errors.APIError "Forbidden"
// @Failure 404 {object} errors.APIError "Sandbox not found"
// @Failure 500 {object} errors.APIError "Internal server error"
// @Security ApiKeyAuth
// @Router /sandboxes/{id}/files [get]
func (h *Handler) ListFiles(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.BadRequest(c, "Sandbox ID is required")
		return
	}
	
	path := c.DefaultQuery("path", "/workspace")
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "read") {
		h.Forbidden(c, "You don't have access to this sandbox")
		return
	}
	
	// Get request logger
	log := h.GetRequestLogger(c)
	
	// List files
	files, err := h.Services.GetSandbox().ListFiles(c.Request.Context(), sandboxID, path)
	if err != nil {
		if errors.IsSandboxNotFoundError(err) {
			h.NotFound(c, "sandbox", sandboxID)
		} else {
			log.Error("Failed to list files", err, 
				"sandbox_id", sandboxID, 
				"path", path)
			middleware.HandleAPIError(c, errors.NewInternalError("Failed to list files", err))
		}
		return
	}
	
	log.Info("Files listed successfully", 
		"sandbox_id", sandboxID, 
		"path", path,
		"file_count", len(files))
	
	h.Success(c, http.StatusOK, gin.H{
		"files": files,
		"path":  path,
	})
}

// @Summary Download a file
// @Description Downloads a file from the sandbox
// @Tags Sandboxes
// @Produce octet-stream
// @Param id path string true "Sandbox ID"
// @Param path path string true "File path"
// @Success 200 {file} file "File content"
// @Failure 400 {object} errors.APIError "Invalid request"
// @Failure 401 {object} errors.APIError "Unauthorized"
// @Failure 403 {object} errors.APIError "Forbidden"
// @Failure 404 {object} errors.APIError "Sandbox or file not found"
// @Failure 500 {object} errors.APIError "Internal server error"
// @Security ApiKeyAuth
// @Router /sandboxes/{id}/files/{path} [get]
func (h *Handler) DownloadFile(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.BadRequest(c, "Sandbox ID is required")
		return
	}
	
	path := c.Param("path")
	if path == "" || path == "/" {
		h.BadRequest(c, "File path is required")
		return
	}
	
	// Remove leading slash from path parameter
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "read") {
		h.Forbidden(c, "You don't have access to this sandbox")
		return
	}
	
	// Get request logger
	log := h.GetRequestLogger(c)
	
	// Download file
	content, err := h.Services.GetSandbox().DownloadFile(c.Request.Context(), sandboxID, path)
	if err != nil {
		if errors.IsSandboxNotFoundError(err) {
			h.NotFound(c, "sandbox", sandboxID)
		} else {
			log.Error("Failed to download file", err, 
				"sandbox_id", sandboxID, 
				"path", path)
			middleware.HandleAPIError(c, errors.NewInternalError("Failed to download file", err))
		}
		return
	}
	
	log.Info("File downloaded successfully", 
		"sandbox_id", sandboxID, 
		"path", path,
		"size", len(content))
	
	// Set content type based on file extension
	contentType := http.DetectContentType(content)
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", "attachment; filename="+path)
	
	c.Data(http.StatusOK, contentType, content)
}

// @Summary Upload a file
// @Description Uploads a file to the sandbox
// @Tags Sandboxes
// @Accept octet-stream
// @Produce json
// @Param id path string true "Sandbox ID"
// @Param path path string true "File path"
// @Param file body []byte true "File content"
// @Success 200 {object} types.FileInfo "File information"
// @Failure 400 {object} errors.APIError "Invalid request"
// @Failure 401 {object} errors.APIError "Unauthorized"
// @Failure 403 {object} errors.APIError "Forbidden"
// @Failure 404 {object} errors.APIError "Sandbox not found"
// @Failure 500 {object} errors.APIError "Internal server error"
// @Security ApiKeyAuth
// @Router /sandboxes/{id}/files/{path} [put]
func (h *Handler) UploadFile(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.BadRequest(c, "Sandbox ID is required")
		return
	}
	
	path := c.Param("path")
	if path == "" || path == "/" {
		h.BadRequest(c, "File path is required")
		return
	}
	
	// Remove leading slash from path parameter
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "write") {
		h.Forbidden(c, "You don't have access to this sandbox")
		return
	}
	
	// Get request logger
	log := h.GetRequestLogger(c)
	
	// Read file content
	content, err := c.GetRawData()
	if err != nil {
		h.BadRequest(c, "Failed to read file content")
		return
	}
	
	// Upload file
	fileInfo, err := h.Services.GetSandbox().UploadFile(c.Request.Context(), sandboxID, path, content)
	if err != nil {
		if errors.IsSandboxNotFoundError(err) {
			h.NotFound(c, "sandbox", sandboxID)
		} else {
			log.Error("Failed to upload file", err, 
				"sandbox_id", sandboxID, 
				"path", path)
			middleware.HandleAPIError(c, errors.NewInternalError("Failed to upload file", err))
		}
		return
	}
	
	log.Info("File uploaded successfully", 
		"sandbox_id", sandboxID, 
		"path", path,
		"size", len(content))
	
	h.Success(c, http.StatusOK, fileInfo)
}

// @Summary Delete a file
// @Description Deletes a file from the sandbox
// @Tags Sandboxes
// @Produce json
// @Param id path string true "Sandbox ID"
// @Param path path string true "File path"
// @Success 200 {object} map[string]string "Success message"
// @Failure 400 {object} errors.APIError "Invalid request"
// @Failure 401 {object} errors.APIError "Unauthorized"
// @Failure 403 {object} errors.APIError "Forbidden"
// @Failure 404 {object} errors.APIError "Sandbox or file not found"
// @Failure 500 {object} errors.APIError "Internal server error"
// @Security ApiKeyAuth
// @Router /sandboxes/{id}/files/{path} [delete]
func (h *Handler) DeleteFile(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.BadRequest(c, "Sandbox ID is required")
		return
	}
	
	path := c.Param("path")
	if path == "" || path == "/" {
		h.BadRequest(c, "File path is required")
		return
	}
	
	// Remove leading slash from path parameter
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "write") {
		h.Forbidden(c, "You don't have access to this sandbox")
		return
	}
	
	// Get request logger
	log := h.GetRequestLogger(c)
	
	// Delete file
	err := h.Services.GetSandbox().DeleteFile(c.Request.Context(), sandboxID, path)
	if err != nil {
		if errors.IsSandboxNotFoundError(err) {
			h.NotFound(c, "sandbox", sandboxID)
		} else {
			log.Error("Failed to delete file", err, 
				"sandbox_id", sandboxID, 
				"path", path)
			middleware.HandleAPIError(c, errors.NewInternalError("Failed to delete file", err))
		}
		return
	}
	
	log.Info("File deleted successfully", 
		"sandbox_id", sandboxID, 
		"path", path)
	
	h.Success(c, http.StatusOK, gin.H{
		"message": "File deleted successfully",
	})
}

// @Summary Install packages
// @Description Installs packages in the sandbox
// @Tags Sandboxes
// @Accept json
// @Produce json
// @Param id path string true "Sandbox ID"
// @Param packages body types.InstallPackagesRequest true "Package installation request"
// @Success 200 {object} types.ExecutionResult "Installation result"
// @Failure 400 {object} errors.APIError "Invalid request"
// @Failure 401 {object} errors.APIError "Unauthorized"
// @Failure 403 {object} errors.APIError "Forbidden"
// @Failure 404 {object} errors.APIError "Sandbox not found"
// @Failure 500 {object} errors.APIError "Internal server error"
// @Security ApiKeyAuth
// @Router /sandboxes/{id}/packages [post]
func (h *Handler) InstallPackages(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.BadRequest(c, "Sandbox ID is required")
		return
	}
	
	var req types.InstallPackagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.BadRequest(c, "Invalid request body")
		return
	}
	
	// Validate request
	if err := validation.ValidateInstallPackagesRequest(req); err != nil {
		middleware.HandleAPIError(c, err)
		return
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "execute") {
		h.Forbidden(c, "You don't have access to this sandbox")
		return
	}
	
	// Set sandbox ID
	req.SandboxID = sandboxID
	
	// Get request logger
	log := h.GetRequestLogger(c)
	
	// Install packages
	result, err := h.Services.GetSandbox().InstallPackages(c.Request.Context(), req)
	if err != nil {
		if errors.IsSandboxNotFoundError(err) {
			h.NotFound(c, "sandbox", sandboxID)
		} else {
			log.Error("Failed to install packages", err, 
				"sandbox_id", sandboxID, 
				"packages", req.Packages)
			middleware.HandleAPIError(c, errors.NewInternalError("Failed to install packages", err))
		}
		return
	}
	
	log.Info("Packages installed successfully", 
		"sandbox_id", sandboxID, 
		"packages", req.Packages,
		"exit_code", result.ExitCode)
	
	h.Success(c, http.StatusOK, result)
}

// @Summary Connect to WebSocket
// @Description Establishes a WebSocket connection for real-time communication with the sandbox
// @Tags Sandboxes
// @Param id path string true "Sandbox ID"
// @Success 101 {string} string "Switching Protocols"
// @Failure 400 {object} errors.APIError "Invalid request"
// @Failure 401 {object} errors.APIError "Unauthorized"
// @Failure 403 {object} errors.APIError "Forbidden"
// @Failure 404 {object} errors.APIError "Sandbox not found"
// @Failure 500 {object} errors.APIError "Internal server error"
// @Security ApiKeyAuth
// @Router /sandboxes/{id}/stream [get]
func (h *Handler) WebSocketHandler(c *gin.Context) {
	sandboxID := c.Param("id")
	if sandboxID == "" {
		h.BadRequest(c, "Sandbox ID is required")
		return
	}
	
	// Check if user has access to this sandbox
	userID := h.GetUserID(c)
	if !h.Services.GetAuth().CheckResourceAccess(userID, "sandbox", sandboxID, "connect") {
		h.Forbidden(c, "You don't have access to this sandbox")
		return
	}
	
	// Get request logger
	log := h.GetRequestLogger(c)
	
	// Check origin
	origin := c.GetHeader("Origin")
	if origin == "" {
		log.Warn("WebSocket connection attempt without Origin header",
			"sandbox_id", sandboxID,
			"user_id", userID,
			"remote_addr", c.ClientIP())
	}
	
	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Error("Failed to upgrade to WebSocket", err, 
			"sandbox_id", sandboxID,
			"user_id", userID)
		return
	}
	
	// Create session
	session, err := h.Services.GetSandbox().CreateSession(userID, sandboxID, conn)
	if err != nil {
		log.Error("Failed to create WebSocket session", err, 
			"sandbox_id", sandboxID, 
			"user_id", userID)
		conn.Close()
		return
	}
	
	// Log connection
	log.Info("WebSocket connection established", 
		"sandbox_id", sandboxID, 
		"user_id", userID, 
		"session_id", session.ID)
	
	// Close session when done
	defer h.Services.GetSandbox().CloseSession(session.ID)
	
	// Handle session
	h.Services.GetSandbox().HandleSession(session)
}
