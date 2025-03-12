package common

import (
	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
)

// BaseHandler provides common functionality for all handlers
type BaseHandler struct {
	Logger   *logger.Logger
	Services interfaces.Services
}

// NewBaseHandler creates a new base handler
func NewBaseHandler(services interfaces.Services, logger *logger.Logger) *BaseHandler {
	return &BaseHandler{
		Logger:   logger,
		Services: services,
	}
}

// HandleError handles an error and returns an appropriate response
func (h *BaseHandler) HandleError(c *gin.Context, status int, code, message string) {
	apiErr := &errors.APIError{
		Type:    errors.ErrorType(code),
		Code:    code,
		Message: message,
	}
	middleware.HandleAPIError(c, apiErr)
}

// GetUserID gets the user ID from the context
func (h *BaseHandler) GetUserID(c *gin.Context) string {
	return h.Services.GetAuth().GetUserID(c)
}

// ValidateRequest validates a request body against a model
func (h *BaseHandler) ValidateRequest(c *gin.Context, model interface{}) error {
	return middleware.ValidateRequest(c, model)
}

// GetRequestLogger gets a logger with request context
func (h *BaseHandler) GetRequestLogger(c *gin.Context) *logger.Logger {
	if requestLogger, exists := c.Get("logger"); exists {
		return requestLogger.(*logger.Logger)
	}
	return h.Logger
}

// Success sends a success response
func (h *BaseHandler) Success(c *gin.Context, status int, data interface{}) {
	c.JSON(status, data)
}

// Created sends a created response
func (h *BaseHandler) Created(c *gin.Context, data interface{}) {
	c.JSON(201, data)
}

// NoContent sends a no content response
func (h *BaseHandler) NoContent(c *gin.Context) {
	c.Status(204)
}

// BadRequest sends a bad request response
func (h *BaseHandler) BadRequest(c *gin.Context, message string) {
	apiErr := errors.NewBadRequestError(message, nil)
	middleware.HandleAPIError(c, apiErr)
}

// Unauthorized sends an unauthorized response
func (h *BaseHandler) Unauthorized(c *gin.Context, message string) {
	apiErr := errors.NewAuthError(message, nil)
	middleware.HandleAPIError(c, apiErr)
}

// Forbidden sends a forbidden response
func (h *BaseHandler) Forbidden(c *gin.Context, message string) {
	apiErr := errors.NewForbiddenError(message, nil)
	middleware.HandleAPIError(c, apiErr)
}

// NotFound sends a not found response
func (h *BaseHandler) NotFound(c *gin.Context, resourceType, resourceID string) {
	apiErr := errors.NewNotFoundError(resourceType, resourceID, nil)
	middleware.HandleAPIError(c, apiErr)
}

// InternalError sends an internal error response
func (h *BaseHandler) InternalError(c *gin.Context, message string, err error) {
	apiErr := errors.NewInternalError(message, err)
	middleware.HandleAPIError(c, apiErr)
}
