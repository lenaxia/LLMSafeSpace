package metrics

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	
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
	metricsGroup := router.Group("/metrics")
	
	// Public metrics endpoint for Prometheus scraping
	metricsGroup.GET("", gin.WrapH(promhttp.Handler()))
	
	// Protected detailed metrics endpoints
	protectedGroup := metricsGroup.Group("/detailed")
	protectedGroup.Use(auth.AuthMiddleware(h.Services.GetAuth()))
	
	protectedGroup.GET("/sandboxes", h.GetSandboxMetrics)
	protectedGroup.GET("/warmpools", h.GetWarmPoolMetrics)
	protectedGroup.GET("/executions", h.GetExecutionMetrics)
	protectedGroup.GET("/resources", h.GetResourceMetrics)
}

func (h *Handler) GetSandboxMetrics(c *gin.Context) {
	// Get user ID for filtering
	userID := h.GetUserID(c)
	
	// Get time range from query parameters
	timeRange := c.DefaultQuery("timeRange", "1h")
	
	// Get sandbox metrics
	metrics := map[string]interface{}{
		"total_created": 0,
		"total_terminated": 0,
		"active_count": 0,
		"warm_pod_usage": map[string]interface{}{
			"total_hits": 0,
			"total_misses": 0,
			"hit_ratio": 0.0,
		},
		"runtimes": map[string]int{},
		"creation_times": map[string]float64{},
	}
	
	// In a real implementation, these would be fetched from the metrics service
	// For now, return sample data
	
	h.Success(c, http.StatusOK, metrics)
}

func (h *Handler) GetWarmPoolMetrics(c *gin.Context) {
	metrics := map[string]interface{}{
		"pools": []map[string]interface{}{
			{
				"name": "python-pool",
				"runtime": "python:3.10",
				"available_pods": 5,
				"assigned_pods": 2,
				"utilization": 0.4,
				"hit_ratio": 0.85,
				"scale_events": 12,
			},
		},
		"global_stats": map[string]interface{}{
			"total_pods": 7,
			"total_assigned": 2,
			"average_utilization": 0.4,
			"average_hit_ratio": 0.85,
		},
	}
	
	h.Success(c, http.StatusOK, metrics)
}

func (h *Handler) GetExecutionMetrics(c *gin.Context) {
	metrics := map[string]interface{}{
		"total_executions": 1000,
		"successful_executions": 950,
		"failed_executions": 50,
		"average_duration": 1.5,
		"by_type": map[string]interface{}{
			"code": map[string]interface{}{
				"total": 800,
				"success_rate": 0.95,
				"average_duration": 1.2,
			},
			"command": map[string]interface{}{
				"total": 200,
				"success_rate": 0.98,
				"average_duration": 0.8,
			},
		},
		"by_runtime": map[string]interface{}{
			"python:3.10": map[string]interface{}{
				"total": 600,
				"success_rate": 0.96,
				"average_duration": 1.3,
			},
		},
	}
	
	h.Success(c, http.StatusOK, metrics)
}

func (h *Handler) GetResourceMetrics(c *gin.Context) {
	metrics := map[string]interface{}{
		"current_usage": map[string]interface{}{
			"total_cpu": 15.5,
			"total_memory": "25Gi",
			"total_storage": "100Gi",
		},
		"by_runtime": map[string]interface{}{
			"python:3.10": map[string]interface{}{
				"cpu": 8.5,
				"memory": "15Gi",
				"storage": "60Gi",
			},
		},
		"by_user": map[string]interface{}{
			"user-123": map[string]interface{}{
				"cpu": 2.5,
				"memory": "5Gi",
				"storage": "20Gi",
			},
		},
	}
	
	h.Success(c, http.StatusOK, metrics)
}
