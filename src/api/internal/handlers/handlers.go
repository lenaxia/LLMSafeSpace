package handlers

import (
	"github.com/gin-gonic/gin"
	
	"github.com/lenaxia/llmsafespace/api/internal/handlers/auth"
	"github.com/lenaxia/llmsafespace/api/internal/handlers/profile"
	"github.com/lenaxia/llmsafespace/api/internal/handlers/runtime"
	"github.com/lenaxia/llmsafespace/api/internal/handlers/sandbox"
	"github.com/lenaxia/llmsafespace/api/internal/handlers/swagger"
	"github.com/lenaxia/llmsafespace/api/internal/handlers/user"
	"github.com/lenaxia/llmsafespace/api/internal/handlers/warmpool"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// Handlers contains all API handlers
type Handlers struct {
	logger   *logger.Logger
	services interfaces.Services
	
	auth     *auth.Handler
	sandbox  *sandbox.Handler
	warmPool *warmpool.Handler
	runtime  *runtime.Handler
	profile  *profile.Handler
	user     *user.Handler
}

// New creates a new Handlers instance
func New(log *logger.Logger, svc interfaces.Services) *Handlers {
	return &Handlers{
		logger:   log,
		services: svc,
		auth:     auth.NewHandler(svc, log),
		sandbox:  sandbox.NewHandler(svc, log),
		warmPool: warmpool.NewHandler(svc, log),
		runtime:  runtime.NewHandler(svc, log),
		profile:  profile.NewHandler(svc, log),
		user:     user.NewHandler(svc, log),
	}
}

// RegisterRoutes registers all API routes
func (h *Handlers) RegisterRoutes(router *gin.Engine) {
	// API version group
	v1 := router.Group("/api/v1")
	
	// Register routes for each handler
	h.auth.RegisterRoutes(v1)
	h.sandbox.RegisterRoutes(v1)
	h.warmPool.RegisterRoutes(v1)
	h.runtime.RegisterRoutes(v1)
	h.profile.RegisterRoutes(v1)
	h.user.RegisterRoutes(v1)
	
	// Register Swagger UI routes
	swagger.RegisterRoutes(router)
	
	// Health check endpoint
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
}
