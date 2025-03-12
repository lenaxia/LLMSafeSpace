package runtime

import (
	"net/http"
	"strconv"

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
	runtimeGroup := router.Group("/runtimes")
	runtimeGroup.Use(auth.AuthMiddleware(h.Services.GetAuth()))
	
	runtimeGroup.GET("", h.ListRuntimes)
	runtimeGroup.GET("/:id", h.GetRuntime)
}

func (h *Handler) ListRuntimes(c *gin.Context) {
	// Parse pagination parameters
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	
	// Validate pagination parameters
	if limit < 1 || limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	
	// Get runtimes from Kubernetes
	runtimeList, err := h.Services.GetKubernetesClient().LlmsafespaceV1().RuntimeEnvironments("").List(c.Request.Context(), metav1.ListOptions{
		Limit: int64(limit),
	})
	
	if err != nil {
		h.Logger.Error("Failed to list runtimes", err)
		h.HandleError(c, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	
	// Convert to API response format
	runtimes := make([]map[string]interface{}, 0, len(runtimeList.Items))
	for _, runtime := range runtimeList.Items {
		runtimes = append(runtimes, map[string]interface{}{
			"id":       runtime.Name,
			"name":     runtime.Name,
			"language": runtime.Spec.Language,
			"version":  runtime.Spec.Version,
			"image":    runtime.Spec.Image,
			"tags":     runtime.Spec.Tags,
			"preInstalledPackages": runtime.Spec.PreInstalledPackages,
			"packageManager":       runtime.Spec.PackageManager,
			"securityFeatures":     runtime.Spec.SecurityFeatures,
			"available":            runtime.Status.Available,
		})
	}
	
	c.JSON(http.StatusOK, gin.H{
		"runtimes": runtimes,
		"pagination": gin.H{
			"limit":  limit,
			"offset": offset,
			"total":  len(runtimes),
		},
	})
}

func (h *Handler) GetRuntime(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		h.HandleError(c, http.StatusBadRequest, "missing_id", "Runtime ID is required")
		return
	}
	
	// Get runtime from Kubernetes
	runtime, err := h.Services.GetKubernetesClient().LlmsafespaceV1().RuntimeEnvironments("").Get(c.Request.Context(), id, metav1.GetOptions{})
	if err != nil {
		h.Logger.Error("Failed to get runtime", err, "id", id)
		h.HandleError(c, http.StatusInternalServerError, "retrieval_failed", err.Error())
		return
	}
	
	// Convert to API response format
	response := map[string]interface{}{
		"id":       runtime.Name,
		"name":     runtime.Name,
		"language": runtime.Spec.Language,
		"version":  runtime.Spec.Version,
		"image":    runtime.Spec.Image,
		"tags":     runtime.Spec.Tags,
		"preInstalledPackages": runtime.Spec.PreInstalledPackages,
		"packageManager":       runtime.Spec.PackageManager,
		"securityFeatures":     runtime.Spec.SecurityFeatures,
		"available":            runtime.Status.Available,
	}
	
	if runtime.Spec.ResourceRequirements != nil {
		response["resourceRequirements"] = map[string]interface{}{
			"minCpu":           runtime.Spec.ResourceRequirements.MinCPU,
			"minMemory":        runtime.Spec.ResourceRequirements.MinMemory,
			"recommendedCpu":   runtime.Spec.ResourceRequirements.RecommendedCPU,
			"recommendedMemory": runtime.Spec.ResourceRequirements.RecommendedMemory,
		}
	}
	
	c.JSON(http.StatusOK, response)
}
