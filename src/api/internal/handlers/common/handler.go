package common

import (
	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

type BaseHandler struct {
	Logger   *logger.Logger
	Services interfaces.Services
}

func NewBaseHandler(services interfaces.Services, logger *logger.Logger) *BaseHandler {
	return &BaseHandler{
		Logger:   logger,
		Services: services,
	}
}

func (h *BaseHandler) HandleError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}

func (h *BaseHandler) GetUserID(c *gin.Context) string {
	return h.Services.GetAuth().GetUserID(c)
}
